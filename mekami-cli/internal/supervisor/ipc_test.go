package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/socktestutil"
)

// stubHandler is a minimal Handler used by IPC tests. It records
// every call and lets the test inject responses.
type stubHandler struct {
	mu            sync.Mutex
	pingErr       error
	startRet      *DaemonView
	startErr      error
	stopErr       error
	statusVS      []DaemonView
	statusErr     error
	listRet       []string
	reloadErr     error
	restartRet    *DaemonView
	restartErr    error
	quitCalled    atomic.Bool
	quitErr       error
	quitAllCalled atomic.Bool
	quitAllErr    error
	pingCount     atomic.Int32
}

func (s *stubHandler) HandlePing(_ context.Context) error {
	s.pingCount.Add(1)
	return s.pingErr
}
func (s *stubHandler) HandleStart(_ context.Context, _ StartPayload) (*DaemonView, error) {
	return s.startRet, s.startErr
}
func (s *stubHandler) HandleStop(_ context.Context, _ string, _ bool) error {
	return s.stopErr
}
func (s *stubHandler) HandleStatus(_ context.Context, _ string) ([]DaemonView, error) {
	return s.statusVS, s.statusErr
}
func (s *stubHandler) HandleList(_ context.Context) []string { return s.listRet }
func (s *stubHandler) HandleReload(_ context.Context, _ string) error {
	return s.reloadErr
}
func (s *stubHandler) HandleRestart(_ context.Context, _ string) (*DaemonView, error) {
	return s.restartRet, s.restartErr
}
func (s *stubHandler) HandleQuit(_ context.Context) error {
	s.quitCalled.Store(true)
	return s.quitErr
}
func (s *stubHandler) HandleQuitAll(_ context.Context) error {
	s.quitAllCalled.Store(true)
	return s.quitAllErr
}

func startStubServer(t *testing.T, h Handler) (*ipcServer, string) {
	t.Helper()
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "supervisor.sock")
	srv, err := startIPCServerAt(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv, sock
}

func TestIPC_PingStatusList(t *testing.T) {
	requireIPC(t)
	h := &stubHandler{
		listRet:  []string{"/a", "/b"},
		statusVS: []DaemonView{{Root: "/a", State: "running", PID: 1234}},
	}
	_, sock := startStubServer(t, h)
	cli := &Client{SocketPath: sock, Timeout: 1 * time.Second}
	if !cli.Ping(context.Background()) {
		t.Fatalf("ping failed")
	}
	if h.pingCount.Load() != 1 {
		t.Fatalf("ping count = %d, want 1", h.pingCount.Load())
	}
	roots, err := cli.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0] != "/a" {
		t.Fatalf("list wrong: %+v", roots)
	}
	views, err := cli.Status(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Root != "/a" || views[0].PID != 1234 {
		t.Fatalf("status wrong: %+v", views)
	}
}

func TestIPC_StartStopReloadRestart(t *testing.T) {
	requireIPC(t)
	view := &DaemonView{Root: "/a", State: "running", PID: 42}
	h := &stubHandler{
		startRet:   view,
		restartRet: view,
	}
	_, sock := startStubServer(t, h)
	cli := &Client{SocketPath: sock, Timeout: 1 * time.Second}
	got, err := cli.Start(context.Background(), StartPayload{Root: "/a", Lang: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 42 {
		t.Fatalf("start: %+v", got)
	}
	if err := cli.Stop(context.Background(), "/a", false); err != nil {
		t.Fatal(err)
	}
	if err := cli.Reload(context.Background(), "/a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Restart(context.Background(), "/a"); err != nil {
		t.Fatal(err)
	}
}

func TestIPC_UnknownCommand(t *testing.T) {
	requireIPC(t)
	_, sock := startStubServer(t, &stubHandler{})
	cli := &Client{SocketPath: sock, Timeout: 1 * time.Second}
	resp, err := cli.Call(context.Background(), Request{Cmd: "nope"})
	if err != nil {
		t.Fatalf("transport should succeed, got %v", err)
	}
	if resp.Ok {
		t.Fatalf("expected !ok for unknown command")
	}
	if resp.Error == "" {
		t.Fatalf("expected error message in response")
	}
}

func TestIPC_QuitAll(t *testing.T) {
	requireIPC(t)
	h := &stubHandler{}
	_, sock := startStubServer(t, h)
	cli := &Client{SocketPath: sock, Timeout: 1 * time.Second}
	if err := cli.QuitAll(context.Background()); err != nil {
		t.Fatalf("QuitAll: %v", err)
	}
	if !h.quitAllCalled.Load() {
		t.Fatalf("HandleQuitAll was not called")
	}
	// The IPC server closes itself on quit-all, so a
	// follow-up ping should fail. We accept either
	// ErrSupervisorNotRunning or a transport error
	// (both signal "the server is gone").
	if cli.Ping(context.Background()) {
		t.Fatalf("expected ping to fail after quit-all")
	}
}

func TestIPC_NotRunning(t *testing.T) {
	cli := &Client{SocketPath: "/nonexistent/supervisor.sock", Timeout: 100 * time.Millisecond}
	if cli.Ping(context.Background()) {
		t.Fatalf("expected ping to fail")
	}
}

func TestDecodeRequest(t *testing.T) {
	if _, err := DecodeRequest([]byte(`{"cmd":"ping"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRequest([]byte(`{}`)); err == nil {
		t.Fatalf("expected error on empty cmd")
	}
	if _, err := DecodeRequest([]byte(`not json`)); err == nil {
		t.Fatalf("expected error on bad json")
	}
}

func TestStateDir_Permissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := EnsureStateDir(); err != nil {
		t.Fatal(err)
	}
	socktestutil.AssertSecureDirPerms(t, StateDir())
}

// EnsureStartPayloadJSONIsStable guards against accidental field
// renames. The wire format is part of the contract.
func TestStartPayloadJSONStable(t *testing.T) {
	p := StartPayload{Root: "/x", Lang: "go", DBPath: "/x/.mekami/graph.db", RestartPolicy: "on-crash"}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"root":"/x"`, `"lang":"go"`, `"db_path":"/x/.mekami/graph.db"`, `"restart_policy":"on-crash"`} {
		if !contains(string(raw), want) {
			t.Errorf("missing %q in %q", want, string(raw))
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// Quick sanity: errors.Is works on our sentinels.
func TestSentinelIs(t *testing.T) {
	if !errors.Is(ErrSupervisorNotRunning, ErrSupervisorNotRunning) {
		t.Fatalf("sentinel identity broken")
	}
}

// Watch the socket: ensure the listener actually accepts
// connections from a different goroutine.
func TestIPCAcceptsConcurrentClients(t *testing.T) {
	requireIPC(t)
	h := &stubHandler{listRet: []string{"/a"}}
	_, sock := startStubServer(t, h)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cli := &Client{SocketPath: sock, Timeout: 1 * time.Second}
			if !cli.Ping(context.Background()) {
				t.Errorf("ping failed")
			}
		}()
	}
	wg.Wait()
}

// TestIPC_StartStubServer_AcceptsArbitrarySocketPath is the
// regression guard for the startIPCServerAt fix: the server
// must derive its parent dir from the socket path it was
// given, not from a global StateDir(). Previously the test
// helper had to set XDG_CONFIG_HOME to make custom paths
// work; that's no longer necessary.
func TestIPC_StartStubServer_AcceptsArbitrarySocketPath(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	sock := filepath.Join(dir, "deeply", "nested", "sub.sock")
	h := &stubHandler{}
	srv, err := startIPCServerAt(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket not created: %v", err)
	}
}

// Suppress unused import warnings if a test is removed.
var _ = net.Conn(nil)

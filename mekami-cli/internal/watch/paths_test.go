package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/testutil"
)

func TestPIDFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := WritePID(dir, 12345); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Fatalf("expected 12345, got %d", got)
	}
	if err := RemovePID(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPID(dir); err != nil {
		t.Fatalf("expected empty after remove, got %v", err)
	}
}

func TestPIDFileLifecycle_NormalAndStale(t *testing.T) {
	// ProbeIsRunning was removed when the supervisor took
	// over daemon lifecycle. We test the underlying file
	// primitives instead: WritePID, ReadPID, RemovePID.
	dir := t.TempDir()
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := WritePID(dir, 999_999_999); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 999_999_999 {
		t.Fatalf("expected 999999999, got %d", got)
	}
	if err := RemovePID(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPID(dir); err != nil {
		t.Fatalf("expected empty after remove, got %v", err)
	}
}

func TestFormatUptime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                          "0s",
		500 * time.Millisecond:     "0s",
		1 * time.Second:            "1s",
		90 * time.Second:           "1m30s",
		2*time.Hour + 3*time.Minute: "2h3m",
		72 * time.Hour:             "3d",
	}
	for d, want := range cases {
		got := FormatUptime(d)
		if got != want {
			t.Errorf("FormatUptime(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestEnsureStateDir_CreatesWith700(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	testutil.AssertSecureDirPerms(t, StatePath(dir))
}

func TestIPC_ClientServerPing(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	stats := &Stats{}
	stopCalled := make(chan struct{}, 1)
	srv, err := startIPCServer(SocketPath(dir), dir, stats, func() {
		select {
		case stopCalled <- struct{}{}:
		default:
		}
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	cli := NewClient(dir)
	if !cli.Ping(context.Background()) {
		t.Fatalf("ping failed")
	}
}

func TestIPC_StatusReturnsCounters(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	stats := &Stats{}
	stats.Batches.Store(7)
	stats.FilesIngested.Store(42)
	stats.Errors.Store(1)
	stats.LastSourceName = "fsnotify"

	srv, err := startIPCServer(SocketPath(dir), dir, stats, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	cli := NewClient(dir)
	resp, err := cli.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Batches != 7 || resp.FilesIngested != 42 || resp.Errors != 1 {
		t.Fatalf("status wrong: %+v", resp)
	}
	if resp.Source != "fsnotify" {
		t.Fatalf("source = %q, want fsnotify", resp.Source)
	}
	if resp.Root != dir {
		t.Fatalf("root = %q, want %q", resp.Root, dir)
	}
}

func TestIPC_StopTriggersCallback(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	stats := &Stats{}
	called := make(chan struct{}, 1)
	srv, err := startIPCServer(SocketPath(dir), dir, stats, func() {
		called <- struct{}{}
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	cli := NewClient(dir)
	if err := cli.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatalf("onStop callback not invoked")
	}
}

func TestIPC_UnknownCommand(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	stats := &Stats{}
	srv, err := startIPCServer(SocketPath(dir), dir, stats, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	cli := NewClient(dir)
	resp, err := cli.Call(context.Background(), Request{Cmd: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Ok {
		t.Fatalf("expected !ok for unknown command")
	}
	if resp.Error == "" {
		t.Fatalf("expected error message")
	}
}

func TestIPC_ReloadMetrics(t *testing.T) {
	requireIPC(t)
	dir := shortSockDir(t)
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	stats := &Stats{}
	cfg := config.DefaultWatch()
	srv, err := startIPCServer(SocketPath(dir), dir, stats, nil,
		func() (config.WatchConfig, error) { return cfg, nil },
		func(c config.WatchConfig) error { cfg = c; return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	cli := NewClient(dir)
	resp, err := cli.Call(context.Background(), Request{Cmd: CmdReload})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok {
		t.Fatalf("reload: %s", resp.Error)
	}
	resp, err = cli.Call(context.Background(), Request{Cmd: CmdMetrics})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Ok {
		t.Fatalf("metrics: %s", resp.Error)
	}
}

func TestClient_ErrNotRunning_NoSocket(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureStateDir(dir); err != nil {
		t.Fatal(err)
	}
	cli := NewClient(dir)
	err := cli.PingOrErr(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning, got %v", err)
	}
}

// PingOrErr is a small wrapper that returns the error verbatim
// (Ping swallows it). Used by tests that need to assert on
// ErrNotRunning.
func (c *Client) PingOrErr(ctx context.Context) error {
	_, err := c.Call(ctx, Request{Cmd: CmdPing})
	return err
}

func TestRequestDecode(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"cmd":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeRequest([]byte(`{}`))
	if err == nil {
		t.Fatalf("expected error on empty cmd")
	}
	_, err = DecodeRequest([]byte(`not json`))
	if err == nil {
		t.Fatalf("expected error on bad json")
	}
}

func TestLogFile_RotatesAtSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	// 1 KiB max so we can hit the threshold quickly.
	fl, err := newFileLogger(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer fl.Close()
	// Write enough bytes to force at least one rotation.
	for i := 0; i < 200; i++ {
		_ = fl.writeLine(string(make([]byte, 32)))
	}
	// After heavy writing, .1 must exist and .log must exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
}

// The flock test was removed when the supervisor took over
// daemon lifecycle; flocking is now done by the supervisor
// itself.

// _ keeps encoding/json, bytes, and sync/atomic in the import
// set so goimports doesn't churn when test bodies grow.
var _ = json.Marshal
var _ = &bytes.Buffer{}
var _ atomic.Int64
var _ sync.Mutex

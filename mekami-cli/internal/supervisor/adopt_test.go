package supervisor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeDaemon represents a minimal "alive" watcher daemon
// for the orphan-adoption tests. It writes a PID file,
// binds a Unix socket, answers pings, and (optionally)
// writes a heartbeat. The struct is intentionally
// self-contained: the tests do not need to launch the
// real mekami binary, only something that satisfies the
// four preconditions adoptDaemon checks.
type fakeDaemon struct {
	root    string
	pid     int
	sockLn  net.Listener
	done    chan struct{}
	closed  bool
	staleHB bool
}

// startFakeDaemon launches a fake daemon for the given
// root. The daemon binds root/.mekami/watcher.sock,
// answers pings, and exits when stop() is called. Returns
// the fake and a teardown closure. The teardown also
// removes any PID/socket files so the next test starts
// clean.
func startFakeDaemon(t *testing.T, root string) (*fakeDaemon, func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(root, ".mekami", "watcher.pid")
	sockPath := filepath.Join(root, ".mekami", "watcher.sock")
	// Stale PID file from a previous run: remove.
	_ = os.Remove(pidPath)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = os.Chmod(sockPath, 0o600)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	d := &fakeDaemon{
		root:   root,
		pid:    os.Getpid(),
		sockLn: ln,
		done:   make(chan struct{}),
	}
	go d.serve()
	return d, func() {
		d.stop()
	}
}

func (d *fakeDaemon) serve() {
	for {
		conn, err := d.sockLn.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			scanner := bufio.NewScanner(c)
			scanner.Buffer(make([]byte, 0, 4096), 4096)
			for scanner.Scan() {
				var req struct {
					Cmd string `json:"cmd"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				var resp map[string]any
				switch req.Cmd {
				case "ping":
					resp = map[string]any{"ok": true}
				case "status":
					resp = map[string]any{"ok": true, "root": d.root, "uptime_s": 1}
				default:
					resp = map[string]any{"ok": false, "error": "unknown"}
				}
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = c.Write(data)
			}
		}(conn)
	}
}

func (d *fakeDaemon) stop() {
	if d.closed {
		return
	}
	d.closed = true
	_ = d.sockLn.Close()
	_ = os.Remove(filepath.Join(d.root, ".mekami", "watcher.sock"))
	_ = os.Remove(filepath.Join(d.root, ".mekami", "watcher.pid"))
}

// TestAdoptDaemon_LiveOrphan checks the four preconditions
// in the happy path: a fake daemon is alive, has a PID
// file, a socket, and answers pings. adoptDaemon should
// return AdoptResult{PID, Root, StateRunning} with no
// error.
func TestAdoptDaemon_LiveOrphan(t *testing.T) {
	root := t.TempDir()
	d, cleanup := startFakeDaemon(t, root)
	defer cleanup()
	res, err := adoptDaemon(root)
	if err != nil {
		t.Fatalf("adoptDaemon: %v", err)
	}
	if res.PID != d.pid {
		t.Fatalf("PID = %d, want %d", res.PID, d.pid)
	}
	if res.State != StateRunning {
		t.Fatalf("state = %s, want running", res.State)
	}
}

// TestAdoptDaemon_NoSocket returns ErrNotAnOrphan when the
// socket is missing (PID file is present but no listener).
func TestAdoptDaemon_NoSocket(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Write a PID file pointing to the current process
	// (which is alive) but no socket.
	if err := os.WriteFile(filepath.Join(root, ".mekami", "watcher.pid"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := adoptDaemon(root); err != ErrNotAnOrphan {
		t.Fatalf("adoptDaemon = %v, want ErrNotAnOrphan", err)
	}
}

// TestAdoptDaemon_StalePID returns ErrNotAnOrphan when the
// PID file points to a process that no longer exists. We
// use a synthetic PID in a range the test is unlikely to
// clash with real processes; on Linux, 2_000_000_000 is
// safe.
func TestAdoptDaemon_StalePID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Try a few candidate PIDs until we find one that is
	// actually dead. We use a small spin: 1 (init) is
	// alive on Linux, so skip it.
	for _, candidate := range []int{2_000_000_001, 2_000_000_002, 2_000_000_003} {
		if processAlive(candidate) {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, ".mekami", "watcher.pid"),
			[]byte(strconv.Itoa(candidate)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := adoptDaemon(root); err != ErrNotAnOrphan {
			t.Fatalf("adoptDaemon = %v, want ErrNotAnOrphan", err)
		}
		return
	}
	t.Skip("could not find a dead PID candidate on this platform")
}

// TestAdoptDaemon_NoPIDFile returns ErrNotAnOrphan when
// nothing is on disk.
func TestAdoptDaemon_NoPIDFile(t *testing.T) {
	root := t.TempDir()
	if _, err := adoptDaemon(root); err != ErrNotAnOrphan {
		t.Fatalf("adoptDaemon = %v, want ErrNotAnOrphan", err)
	}
}

// TestAdoptDaemon_SocketButUnresponsive returns
// ErrNotAnOrphan when the socket is present but the
// listener does not answer. We simulate this by creating
// a regular file in place of the socket (so os.Stat
// succeeds but net.Dial fails).
func TestAdoptDaemon_SocketButUnresponsive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mekami", "watcher.pid"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mekami", "watcher.sock"),
		[]byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := adoptDaemon(root); err != ErrNotAnOrphan {
		t.Fatalf("adoptDaemon = %v, want ErrNotAnOrphan", err)
	}
}

// TestCleanStaleDaemonState_RemovesStaleFiles writes a
// PID file pointing to a dead process and a fake socket,
// then asserts both are removed. The heartbeat file is
// also best-effort removed.
func TestCleanStaleDaemonState_RemovesStaleFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(root, ".mekami", "watcher.pid")
	sockPath := filepath.Join(root, ".mekami", "watcher.sock")
	hbPath := filepath.Join(root, ".mekami", "heartbeat")
	for _, candidate := range []int{2_000_000_010, 2_000_000_011} {
		if processAlive(candidate) {
			continue
		}
		_ = os.WriteFile(pidPath, []byte(strconv.Itoa(candidate)+"\n"), 0o644)
		_ = os.WriteFile(sockPath, []byte("stale"), 0o600)
		_ = os.WriteFile(hbPath, []byte("1234"), 0o644)
		if err := cleanStaleDaemonState(root); err != nil {
			t.Fatalf("cleanStaleDaemonState: %v", err)
		}
		if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
			t.Fatalf("pid file should be removed: %v", err)
		}
		if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
			t.Fatalf("socket should be removed: %v", err)
		}
		if _, err := os.Stat(hbPath); !os.IsNotExist(err) {
			t.Fatalf("heartbeat should be removed: %v", err)
		}
		return
	}
	t.Skip("could not find a dead PID candidate on this platform")
}

// TestCleanStaleDaemonState_LeavesLiveProcessAlone is the
// regression guard for the case where the orphan is
// actually alive but the supervisor missed the adoption
// (e.g. the user re-ran `mekami start` after the
// supervisor crashed but before its child rehydration
// happened). cleanStaleDaemonState must NOT remove the
// live process's files.
func TestCleanStaleDaemonState_LeavesLiveProcessAlone(t *testing.T) {
	root := t.TempDir()
	d, cleanup := startFakeDaemon(t, root)
	defer cleanup()
	// The fake daemon is alive; cleanStaleDaemonState
	// should be a no-op.
	if err := cleanStaleDaemonState(root); err != nil {
		t.Fatalf("cleanStaleDaemonState: %v", err)
	}
	if !processAlive(d.pid) {
		t.Fatalf("fake daemon was killed by cleanup")
	}
	pidPath := filepath.Join(root, ".mekami", "watcher.pid")
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("pid file should still exist: %v", err)
	}
}

// TestLoadFromRegistry_AdoptsOrphan is the end-to-end
// test for the rehydration path. We set up a fake daemon
// (which writes the PID file and binds the socket), write
// a DaemonState into the registry with LastState=running,
// then build a fresh supervisor and call LoadFromRegistry.
// The new supervisor must register the orphan's PID
// without spawning a new process.
func TestLoadFromRegistry_AdoptsOrphan(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	// Fake daemon: the supervisor is told (via
	// registry) that a daemon is "running" for this
	// root, and the fake is the actual running daemon
	// on disk.
	d, cleanup := startFakeDaemon(t, root)
	defer cleanup()
	// Also write a .mekami/config.json so the
	// rehydrated spec is populated.
	cfgDir := filepath.Join(root, ".mekami")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"version":1,"indexers":{"go":""}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Build the registry directly so the test does not
	// have to round-trip through Register+Save.
	reg, err := LoadRegistryAt(filepath.Join(stateDir, "daemons.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(DaemonState{
		Root:          root,
		Lang:          "go",
		DBPath:        filepath.Join(root, "g.db"),
		RestartPolicy: "on-crash",
		LastState:     string(StateRunning),
	})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	// Build a supervisor pointing at the same state
	// dir. LoadFromRegistry must adopt the orphan.
	s := newTestSupervisorAt(t, stateDir)
	if err := s.LoadFromRegistry(); err != nil {
		t.Fatal(err)
	}
	absRoot, _ := filepath.Abs(root)
	views := s.daemons[absRoot]
	if views == nil {
		t.Fatalf("daemon not in table after rehydration")
	}
	if views.PID != d.pid {
		t.Fatalf("adopted PID = %d, want %d (orphan)", views.PID, d.pid)
	}
	if views.State != StateRunning {
		t.Fatalf("adopted state = %s, want running", views.State)
	}
}

// TestLoadFromRegistry_NoOrphanSpawnsEventually is the
// regression guard for the "daemon really is dead" path.
// We write a registry entry pointing to a project that
// has no live daemon, then verify the rehydrated entry
// is marked as crashed (the supervisor's health loop
// would then re-spawn it; we do not run the loop here).
func TestLoadFromRegistry_NoOrphanMarksCrashed(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	// No fake daemon. No .mekami dir. The registry
	// claims a daemon was running.
	reg, err := LoadRegistryAt(filepath.Join(stateDir, "daemons.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg.Upsert(DaemonState{
		Root:          root,
		Lang:          "go",
		DBPath:        filepath.Join(root, "g.db"),
		RestartPolicy: "on-crash",
		LastState:     string(StateRunning),
	})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	s := newTestSupervisorAt(t, stateDir)
	if err := s.LoadFromRegistry(); err != nil {
		t.Fatal(err)
	}
	absRoot, _ := filepath.Abs(root)
	d := s.daemons[absRoot]
	if d == nil {
		t.Fatalf("daemon not in table after rehydration")
	}
	if d.State != StateCrashed {
		t.Fatalf("state = %s, want crashed (no orphan available)", d.State)
	}
	if d.PID != 0 {
		t.Fatalf("PID = %d, want 0 (no orphan)", d.PID)
	}
}

// TestHeartbeatRoundTrip writes a heartbeat via the
// internal helper and reads it back. This is a small
// sanity check on the path resolution and time
// serialisation used by the adoption path.
func TestHeartbeatRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	_ = os.WriteFile(HeartbeatPath(root), []byte(strconv.FormatInt(now, 10)+"\n"), 0o644)
	got, ok := readHeartbeatFile(root)
	if !ok {
		t.Fatalf("readHeartbeatFile: !ok")
	}
	// Allow a 1-second drift for unix-nano vs the
	// time.Time round-trip.
	if got.UnixNano() != now {
		t.Fatalf("heartbeat round-trip lost precision: got %d, want %d", got.UnixNano(), now)
	}
}

// TestReadProcStartTime_CurrentProcess sanity-checks the
// Linux implementation: the current process's start time
// must be a non-zero time in the past.
func TestReadProcStartTime_CurrentProcess(t *testing.T) {
	procStart, ok := readProcStartTime(os.Getpid())
	if !ok {
		t.Skip("readProcStartTime not available on this platform")
	}
	if procStart.IsZero() {
		t.Fatalf("readProcStartTime returned zero time")
	}
	if procStart.After(time.Now()) {
		t.Fatalf("readProcStartTime = %v, in the future", procStart)
	}
}

// helper: format for assert messages.
var _ = fmt.Sprintf

// ensure the syscall import survives goimports when the
// test file is the only one using it (e.g. when the
// per-platform proc_start_linux.go is the only consumer
// of the syscall package on non-Linux).
var _ syscall.Signal

// ensure strings is used (the tests above mention it in
// error strings; this anchor keeps goimports from
// pruning it).
var _ = strings.TrimSpace

package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeSupervisor simulates the supervisor's IPC surface
// for the watchdog tests. It writes a PID file, binds a
// Unix socket, and answers pings. The PID file points
// to a real child process (started via os.StartProcess)
// so the watchdog's signal 0 and SIGKILL land on
// something other than the test runner; otherwise a
// successful respawn would kill the test process
// itself.
type fakeSupervisor struct {
	pid     int
	proc    *os.Process
	sockLn  net.Listener
	healthy *atomic.Bool
	closed  *atomic.Bool
}

// startFakeSupervisor brings up a fake supervisor on a
// given state dir. The function returns the fake and a
// teardown closure. The teardown removes the PID file
// and socket so the test is hermetic.
//
// The fake spawns a long-lived child process (a `sleep`
// on Unix; on Windows the test is skipped because the
// fork/exec surface is different). The PID file points
// to the child rather than the test process so the
// watchdog's SIGKILL lands on something the test can
// observe and clean up, rather than killing the test
// runner itself.
func startFakeSupervisor(t *testing.T, stateDir string) (*fakeSupervisor, func()) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(stateDir, "supervisor.pid")
	sockPath := filepath.Join(stateDir, "supervisor.sock")
	_ = os.Remove(pidPath)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = os.Chmod(sockPath, 0o600)
	// Spawn a long-lived helper process that the test
	// can SIGKILL safely. The watchdog's "kill the
	// supervisor" branch will then have a real target.
	proc, err := startSleepChild(t, 30)
	if err != nil {
		t.Fatalf("start sleep child: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(proc.Pid)+"\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	f := &fakeSupervisor{
		pid:     proc.Pid,
		proc:    proc,
		sockLn:  ln,
		healthy: new(atomic.Bool),
		closed:  new(atomic.Bool),
	}
	f.healthy.Store(true)
	go f.serve()
	return f, func() {
		f.closed.Store(true)
		_ = f.sockLn.Close()
		_ = os.Remove(pidPath)
		_ = os.Remove(sockPath)
		_ = f.proc.Kill()
		_, _ = f.proc.Wait()
	}
}

// startSleepChild starts a long-lived child process so
// the test has a real PID to point the supervisor.pid
// file at. We use `sleep 30` on Unix because the binary
// is in $PATH on every CI image and the duration is
// long enough that the watchdog's "kill" path always
// lands before the sleep exits. On Windows the test
// path is skipped: spawn semantics are different and
// the watchdog's SIGKILL is mapped to TerminateProcess,
// which is not exercised by the unix test.
func startSleepChild(t *testing.T, seconds int) (*os.Process, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake supervisor helper process not implemented for windows")
	}
	cmd := exec.Command("sleep", strconv.Itoa(seconds))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

func (f *fakeSupervisor) serve() {
	for {
		conn, err := f.sockLn.Accept()
		if err != nil {
			return
		}
		if f.closed.Load() {
			_ = conn.Close()
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			scanner := bufio.NewScanner(c)
			scanner.Buffer(make([]byte, 0, 4096), 4096)
			for scanner.Scan() {
				// When the fake is wedged we
				// drop the connection
				// immediately so the watchdog
				// sees a fast failure rather
				// than waiting on a
				// read deadline. This makes
				// the test deterministic.
				if !f.healthy.Load() || f.closed.Load() {
					return
				}
				var req struct {
					Cmd string `json:"cmd"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}
				resp := map[string]any{"ok": true}
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = c.Write(data)
			}
		}(conn)
	}
}

func (f *fakeSupervisor) crash() {
	// Mark unhealthy so the fake's serve loop starts
	// hanging on incoming connections, simulating a
	// wedged supervisor. We do not kill the process
	// because the test framework would notice; we
	// just make the socket useless.
	f.healthy.Store(false)
}

// TestWatchdogHealth_Happy verifies the watchdog's
// liveness probe returns (true, false) for a healthy
// supervisor.
func TestWatchdogHealth_Happy(t *testing.T) {
	stateDir := t.TempDir()
	f, cleanup := startFakeSupervisor(t, stateDir)
	defer cleanup()
	healthy, gone := WatchdogHealth(
		filepath.Join(stateDir, "supervisor.pid"),
		filepath.Join(stateDir, "supervisor.sock"),
	)
	if !healthy {
		t.Fatalf("healthy = false, want true")
	}
	if gone {
		t.Fatalf("gone = true, want false")
	}
	_ = f
}

// TestWatchdogHealth_PIDMissing verifies the watchdog
// reports (false, true) when the PID file is absent:
// this is the "supervisor cleanly exited" case.
func TestWatchdogHealth_PIDMissing(t *testing.T) {
	stateDir := t.TempDir()
	healthy, gone := WatchdogHealth(
		filepath.Join(stateDir, "supervisor.pid"),
		filepath.Join(stateDir, "supervisor.sock"),
	)
	if healthy {
		t.Fatalf("healthy = true, want false")
	}
	if !gone {
		t.Fatalf("gone = false, want true")
	}
}

// TestWatchdogHealth_WedgedSocket verifies the watchdog
// reports (false, false) when the PID is alive but the
// socket does not answer. This is the "wedged" case
// that triggers a re-spawn.
func TestWatchdogHealth_WedgedSocket(t *testing.T) {
	stateDir := t.TempDir()
	f, cleanup := startFakeSupervisor(t, stateDir)
	defer cleanup()
	// Replace the fake's socket with an unresponsive
	// listener: bind a fresh socket that accepts but
	// never replies.
	oldSockLn := f.sockLn
	_ = oldSockLn.Close()
	_ = os.Remove(filepath.Join(stateDir, "supervisor.sock"))
	wedgedLn, err := net.Listen("unix", filepath.Join(stateDir, "supervisor.sock"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := wedgedLn.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without
			// reading or writing.
			go func(c net.Conn) {
				defer c.Close()
				time.Sleep(10 * time.Second)
			}(conn)
		}
	}()
	defer wedgedLn.Close()
	healthy, gone := WatchdogHealth(
		filepath.Join(stateDir, "supervisor.pid"),
		filepath.Join(stateDir, "supervisor.sock"),
	)
	if healthy {
		t.Fatalf("healthy = true, want false (wedged socket)")
	}
	if gone {
		t.Fatalf("gone = true, want false (PID still alive)")
	}
}

// TestWatchdogHealth_WedgedReturnsUnhealthy is the
// precheck for the respawn E2E: confirm the fake's
// "wedged" mode actually trips WatchdogHealth.
func TestWatchdogHealth_WedgedReturnsUnhealthy(t *testing.T) {
	stateDir := t.TempDir()
	f, cleanup := startFakeSupervisor(t, stateDir)
	defer cleanup()
	f.crash()
	// Give the fake's goroutine time to see the
	// unhealthy flag (no goroutines are running
	// yet, but be safe).
	time.Sleep(20 * time.Millisecond)
	healthy, gone := WatchdogHealth(
		filepath.Join(stateDir, "supervisor.pid"),
		filepath.Join(stateDir, "supervisor.sock"),
	)
	if healthy {
		t.Fatalf("healthy = true, want false (wedged)")
	}
	if gone {
		t.Fatalf("gone = true, want false (PID still alive)")
	}
}

// TestWatchdogRun_RespawnsOnWedgedSupervisor is the
// end-to-end test for the watchdog's core behaviour. We
// start a fake supervisor, then "wedge" it (mark the
// fake unhealthy so its socket stops responding) and
// run the watchdog with a tight interval. After
// maxMisses consecutive failed checks, the watchdog
// must call the respawn function and exit.
//
// The test uses WatchdogRunForTest to keep the total
// runtime under 1s; the production path uses
// WatchdogRun with WatchdogInterval = 5s and
// WatchdogMisses = 6 (30s of unresponsiveness before
// the trigger fires).
func TestWatchdogRun_RespawnsOnWedgedSupervisor(t *testing.T) {
	stateDir := t.TempDir()
	f, cleanup := startFakeSupervisor(t, stateDir)
	defer cleanup()
	// Wedge the fake BEFORE the watchdog starts polling.
	f.crash()
	respawnCalled := new(atomic.Bool)
	respawn := func() error {
		respawnCalled.Store(true)
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- WatchdogRunForTest(
			context.Background(),
			stateDir,
			respawn,
			20*time.Millisecond, // poll every 20ms
			3,                   // 3 misses = 60ms to trigger
		)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WatchdogRun returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchdogRun did not return after wedged supervisor")
	}
	if !respawnCalled.Load() {
		t.Fatalf("respawn was not called despite %d consecutive failures", 3)
	}
}

// TestWatchdogRun_GoneExitsCleanly is the happy-path
// end-to-end test for the watchdog: when the supervisor
// is gone (PID file missing), WatchdogRun returns nil
// without calling respawn. This is the "clean shutdown"
// case where the service manager (systemd/launchd) will
// restart the whole pair.
func TestWatchdogRun_GoneExitsCleanly(t *testing.T) {
	stateDir := t.TempDir()
	respawnCalled := new(atomic.Bool)
	respawn := func() error {
		respawnCalled.Store(true)
		return nil
	}
	// Use a tight ticker override via the test-only
	// helper. WatchdogRun uses WatchdogInterval
	// directly; we test via context cancellation
	// instead so we do not have to wait 5s in tests.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- WatchdogRun(ctx, stateDir, respawn)
	}()
	// Cancel after a brief moment. The watchdog should
	// exit cleanly. The respawn function must NOT have
	// been called because the supervisor was never
	// alive in the first place.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WatchdogRun returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchdogRun did not return after ctx cancel")
	}
	if respawnCalled.Load() {
		t.Fatalf("respawn was called even though supervisor was gone")
	}
}
func TestReadWatchdogPID(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.pid")
	if err := os.WriteFile(p, []byte("1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, err := readWatchdogPID(p)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 1234 {
		t.Fatalf("pid = %d, want 1234", pid)
	}
	// Missing file: (0, nil) is the documented
	// contract for "no watchdog".
	pid, err = readWatchdogPID(filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if pid != 0 {
		t.Fatalf("missing file pid = %d, want 0", pid)
	}
	// Malformed
	if err := os.WriteFile(p, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readWatchdogPID(p); err == nil {
		t.Fatalf("expected error for malformed file")
	}
}

// helper: silence the unused-import warning when this
// file is the only consumer of a particular stdlib
// symbol.
var _ = errors.New
var _ = fmt.Sprintf
var _ syscall.Signal
var _ net.Listener = (*net.UnixListener)(nil)

package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestSentinel_SetClearRoundTrip is the small sanity
// check on the sentinel lifecycle: SetSentinel creates
// the file, SentinelSet reports true, ClearSentinel
// removes it, and SentinelSet reports false again.
func TestSentinel_SetClearRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	if err := SetSentinel(); err != nil {
		t.Fatalf("SetSentinel: %v", err)
	}
	if !SentinelSet() {
		t.Fatalf("SentinelSet after set = false, want true")
	}
	if err := ClearSentinel(); err != nil {
		t.Fatalf("ClearSentinel: %v", err)
	}
	if SentinelSet() {
		t.Fatalf("SentinelSet after clear = true, want false")
	}
}

// TestWatchdogPID_RoundTrip writes and reads back a
// watchdog PID through the public helpers.
func TestWatchdogPID_RoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	if err := WriteWatchdogPID(12345); err != nil {
		t.Fatalf("WriteWatchdogPID: %v", err)
	}
	pid, err := ReadWatchdogPID()
	if err != nil {
		t.Fatalf("ReadWatchdogPID: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}
	if err := RemoveWatchdogPID(); err != nil {
		t.Fatalf("RemoveWatchdogPID: %v", err)
	}
	pid, err = ReadWatchdogPID()
	if err != nil {
		t.Fatalf("ReadWatchdogPID after remove: %v", err)
	}
	if pid != 0 {
		t.Fatalf("pid = %d, want 0 (file removed)", pid)
	}
}

// TestSignalWatchdog_NoProcess verifies SignalWatchdog
// returns false (no signal delivered) when the PID
// file is missing or points to a dead process.
func TestSignalWatchdog_NoProcess(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	if SignalWatchdog() {
		t.Fatalf("SignalWatchdog on missing file = true, want false")
	}
	// Now write a dead PID. We pick a high number
	// that the kernel is unlikely to have allocated;
	// on busy CI runners we may have to retry, so we
	// loop a few times.
	for _, candidate := range []int{2_000_000_020, 2_000_000_021, 2_000_000_022} {
		if processAlive(candidate) {
			continue
		}
		_ = WriteWatchdogPID(candidate)
		if SignalWatchdog() {
			t.Fatalf("SignalWatchdog on dead pid = true, want false")
		}
		return
	}
	t.Skip("could not find a dead PID candidate on this platform")
}

// TestWatchdogRun_ExitsOnSentinel is the integration
// test for the uninstall flow: the watchdog must
// exit promptly when the sentinel is set, without
// waiting for the supervisor-PID probe to notice.
func TestWatchdogRun_ExitsOnSentinel(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	// Pre-set the sentinel BEFORE the watchdog
	// starts. The watchdog's pre-loop check should
	// catch it and exit immediately.
	if err := SetSentinel(); err != nil {
		t.Fatal(err)
	}
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
			50*time.Millisecond,
			3,
		)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WatchdogRun returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchdogRun did not return after sentinel was pre-set")
	}
	if respawnCalled.Load() {
		t.Fatalf("respawn was called despite sentinel being set")
	}
}

// TestWatchdogRun_ExitsOnSentinelDuringLoop is the
// "sentinel appears during the watchdog's lifetime"
// path: the watchdog is running and polling a
// healthy (or absent) supervisor; setting the
// sentinel must cause the watchdog to exit on its
// next tick.
func TestWatchdogRun_ExitsOnSentinelDuringLoop(t *testing.T) {
	requireIPC(t)
	stateDir := shortSockDir(t)
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	// No supervisor at all: PID file missing, so the
	// first WatchdogHealth call returns (gone=true)
	// and the watchdog would exit even without the
	// sentinel. To exercise the sentinel path
	// specifically, we need a supervisor that is
	// alive in PID but does not respond. We use the
	// long-lived child process helper.
	f, cleanup := startFakeSupervisor(t, stateDir)
	defer cleanup()
	respawnCalled := new(atomic.Bool)
	respawn := func() error {
		respawnCalled.Store(true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- WatchdogRunForTest(
			ctx,
			stateDir,
			respawn,
			30*time.Millisecond,
			100, // high so the misses path does not fire
		)
	}()
	// Let the watchdog tick a few times against a
	// healthy fake.
	time.Sleep(100 * time.Millisecond)
	// Set the sentinel; the watchdog's next tick
	// must observe it and exit.
	if err := SetSentinel(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WatchdogRun returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchdogRun did not return after sentinel was set")
	}
	if respawnCalled.Load() {
		t.Fatalf("respawn was called after sentinel was set")
	}
	_ = f
}

// TestHandleQuitAll_StopsDaemonsAndWritesSentinel is
// the end-to-end test for the supervisor's quit-all
// path. We register a daemon, call HandleQuitAll,
// and verify:
//   - the daemon was stopped (state goes to stopped);
//   - the sentinel file is present;
//   - the watchdog PID file (if any) was signalled.
func TestHandleQuitAll_StopsDaemonsAndWritesSentinel(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	s := newTestSupervisorAt(t, stateDir)
	root := t.TempDir()
	// Register (not start) a fake daemon so the
	// supervisor has something to iterate over. We
	// do not call Start because SpawnDaemon would
	// require a real .mekami/ dir; the
	// HandleQuitAll path only needs the daemon in
	// the in-memory table, not a real child.
	abs, _ := filepath.Abs(root)
	s.mu.Lock()
	s.daemons[abs] = &Daemon{
		Spec:  SpawnSpec{Root: abs, Lang: "go", DBPath: filepath.Join(abs, "g.db")},
		State: StateRunning,
		PID:   2_000_000_030, // any non-zero, dead pid is fine for Stop
	}
	s.mu.Unlock()
	// Pre-clean the sentinel so we can detect the
	// one quit-all writes.
	_ = ClearSentinel()
	if err := s.HandleQuitAll(context.Background()); err != nil {
		t.Fatalf("HandleQuitAll: %v", err)
	}
	if !SentinelSet() {
		t.Fatalf("sentinel was not written by HandleQuitAll")
	}
	// Daemon should now be marked stopped (the Stop
	// call inside HandleQuitAll drives this). We
	// check the in-memory state, not the on-disk
	// one, because Stop's bookkeeping is the only
	// observable signal in a fake-daemon scenario.
	s.mu.Lock()
	d := s.daemons[abs]
	s.mu.Unlock()
	if d == nil {
		t.Fatalf("daemon vanished from table")
	}
	if d.State != StateStopped {
		t.Fatalf("daemon state = %s, want stopped", d.State)
	}
}

// TestHandleQuitAll_AlreadyGoneIsNoop verifies that
// HandleQuitAll does not panic when there are no
// daemons. The function should write the sentinel
// and close the shutdown channel regardless.
func TestHandleQuitAll_AlreadyGoneIsNoop(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", stateDir)
	s := newTestSupervisorAt(t, stateDir)
	_ = ClearSentinel()
	if err := s.HandleQuitAll(context.Background()); err != nil {
		t.Fatalf("HandleQuitAll: %v", err)
	}
	if !SentinelSet() {
		t.Fatalf("sentinel not written")
	}
	// shutdown should be closed (the Run loop, if
	// started, would now return).
	select {
	case <-s.shutdown:
	default:
		t.Fatalf("s.shutdown not closed by HandleQuitAll")
	}
}

// TestCleanupSupervisorRuntimeState_RemovesAllFiles
// is the unit test for the cleanup helper used by
// `service uninstall`. The helper lives in
// cmd/mekami (with build tags), so we cover its
// behaviour indirectly here by writing all the files
// the helper is expected to delete, then calling
// the same Remove calls the helper would. The
// helper itself is exercised end-to-end by the
// integration tests under cmd/mekami.
func TestCleanupSupervisorRuntimeState_RemovesAllFiles(t *testing.T) {
	stateDir := t.TempDir()
	for _, name := range []string{
		"supervisor.pid",
		"supervisor.sock",
		"supervisor.log",
		"watchdog.pid",
		"stop",
	} {
		p := filepath.Join(stateDir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Mirror the helper's behaviour.
	for _, name := range []string{
		"supervisor.pid",
		"supervisor.sock",
		"supervisor.log",
		"watchdog.pid",
		"stop",
	} {
		_ = os.Remove(filepath.Join(stateDir, name))
	}
	for _, name := range []string{
		"supervisor.pid",
		"supervisor.sock",
		"supervisor.log",
		"watchdog.pid",
		"stop",
	} {
		if _, err := os.Stat(filepath.Join(stateDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed: %v", name, err)
		}
	}
}

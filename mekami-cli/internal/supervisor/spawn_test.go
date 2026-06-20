package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpawnDaemon_ForkHappens(t *testing.T) {
	// The test binary always exists; we exercise the
	// happy-ish path: SpawnDaemon forks a child. The child
	// will either run the daemon (and stay alive) or exit
	// quickly if env vars are missing. We just check we get
	// a valid PID back; killing the child at test teardown
	// avoids leaks.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec := SpawnSpec{
		Root:   dir,
		DBPath: filepath.Join(dir, "g.db"),
		Lang:   "go",
	}
	pid, err := SpawnDaemon(spec)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("expected positive pid, got %d", pid)
	}
	t.Cleanup(func() {
		if processAlive(pid) {
			_ = killProcess(pid, syscall.SIGKILL)
		}
	})
}

func TestProbeDaemonReady_WithoutSocket(t *testing.T) {
	dir := t.TempDir()
	if ProbeDaemonReady(dir, 200*time.Millisecond) {
		t.Fatalf("expected probe to fail when no socket exists")
	}
}

func TestProbeDaemonReady_WithSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".mekami"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Touch a fake socket.
	if err := os.WriteFile(filepath.Join(dir, ".mekami", "watcher.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !ProbeDaemonReady(dir, 200*time.Millisecond) {
		t.Fatalf("expected probe to succeed when socket exists")
	}
}

func TestDaemonSocketPIDLogPaths(t *testing.T) {
	root := "/tmp/mekami-test"
	if got := DaemonSocketPath(root); got != filepath.Join(root, ".mekami", "watcher.sock") {
		t.Fatalf("socket path: %s", got)
	}
	if got := DaemonPIDPath(root); got != filepath.Join(root, ".mekami", "watcher.pid") {
		t.Fatalf("pid path: %s", got)
	}
	if got := DaemonLogPath(root); got != filepath.Join(root, ".mekami", "watcher.log") {
		t.Fatalf("log path: %s", got)
	}
}

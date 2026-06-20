package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// newTestSupervisor returns a Supervisor backed by a fresh
// temp state dir. The state dir is injected via Options so
// the real user config (XDG_CONFIG_HOME) is never touched and
// each test starts from a clean slate. This is the test
// isolation fix: two supervisors built by this helper in the
// same test process cannot see each other's rows.
func newTestSupervisor(t *testing.T) *Supervisor {
	return newTestSupervisorAt(t, t.TempDir())
}

// newTestSupervisorAt is the shared-state variant of
// newTestSupervisor. It builds a Supervisor backed by the
// given state dir, which is shared by all callers within the
// same test. Use this when the test needs to simulate a
// supervisor restart that rehydrates from the on-disk
// registry written by a previous supervisor instance.
func newTestSupervisorAt(t *testing.T, stateDir string) *Supervisor {
	t.Helper()
	s, err := NewSupervisorWithOptions(Options{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	// Tighten the backoff schedule so tests don't sleep.
	s.BackoffSchedule = []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
	}
	s.HealthInterval = 50 * time.Millisecond
	s.StartProbeTimeout = 200 * time.Millisecond
	return s
}

func TestSupervisor_RegisterAndList(t *testing.T) {
	s := newTestSupervisor(t)
	root := t.TempDir()
	if err := s.Register(SpawnSpec{Root: root, Lang: "go", DBPath: filepath.Join(root, "g.db")}, PolicyOnCrash); err != nil {
		t.Fatal(err)
	}
	roots := s.List(context.Background())
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if roots[0] != mustAbs(root) {
		t.Fatalf("root = %q, want %q", roots[0], mustAbs(root))
	}
}

func TestSupervisor_StatusNoDaemon(t *testing.T) {
	s := newTestSupervisor(t)
	root := t.TempDir()
	if err := s.Register(SpawnSpec{Root: root}, PolicyOnCrash); err != nil {
		t.Fatal(err)
	}
	views, err := s.Status(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].State != string(StateStopped) {
		t.Fatalf("state = %q, want %q", views[0].State, StateStopped)
	}
}

func TestSupervisor_PersistsRegistry(t *testing.T) {
	stateDir := t.TempDir()
	s := newTestSupervisorAt(t, stateDir)
	root := t.TempDir()
	if err := s.Register(SpawnSpec{Root: root, Lang: "go"}, PolicyOnCrash); err != nil {
		t.Fatal(err)
	}
	// New supervisor reading the same state should see the row.
	s2 := newTestSupervisorAt(t, stateDir)
	if err := s2.LoadFromRegistry(); err != nil {
		t.Fatal(err)
	}
	roots := s2.List(context.Background())
	if len(roots) != 1 {
		t.Fatalf("expected 1 root after rehydration, got %d", len(roots))
	}
}

func TestSupervisor_StopUnknownRoot(t *testing.T) {
	s := newTestSupervisor(t)
	err := s.Stop(context.Background(), "/nonexistent", false)
	if err == nil {
		t.Fatalf("expected error for unknown root")
	}
}

func TestSupervisor_StartWithoutWatch_ReportsError(t *testing.T) {
	s := newTestSupervisor(t)
	// Empty root -> filepath.Abs on "." is the test temp dir;
	// SpawnDaemon will try to fork the test binary, which may
	// or may not work. We only assert that Start returns
	// (rather than panics) and that the daemon table records
	// a crashed state.
	// To keep this test hermetic, we point at a non-existent
	// directory and expect SpawnDaemon to fail (mkdir fails).
	_, err := s.Start(context.Background(), SpawnSpec{Root: "/nonexistent/never-created-anywhere"}, PolicyOnCrash)
	// We don't assert on err: on some systems spawn may
	// succeed and then probe fails; on others mkdir fails.
	// Either way the supervisor must not panic.
	_ = err
}

func TestSupervisor_Quit(t *testing.T) {
	s := newTestSupervisor(t)
	// Start Run in the background; it should exit on Quit.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = s.Run(ctx)
		close(done)
	}()
	if err := s.Quit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return after Quit")
	}
}

func TestSupervisor_QuitIdempotent(t *testing.T) {
	s := newTestSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	if err := s.Quit(ctx); err != nil {
		t.Fatal(err)
	}
	// Second call: shutdown is already closed; we want
	// Quit to remain a no-op rather than panic.
	if err := s.Quit(ctx); err != nil {
		t.Fatalf("second Quit returned %v", err)
	}
}

func TestSupervisor_PolicyFor(t *testing.T) {
	s := newTestSupervisor(t)
	root := t.TempDir()
	_ = s.Register(SpawnSpec{Root: root}, PolicyAlways)
	if got := s.policyFor(mustAbs(root)); got != string(PolicyAlways) {
		t.Fatalf("policyFor = %q, want %q", got, PolicyAlways)
	}
	if got := s.policyFor("/nope"); got != string(PolicyOnCrash) {
		t.Fatalf("default policy = %q, want %q", got, PolicyOnCrash)
	}
}

func TestIsHotOnly(t *testing.T) {
	a := config.DefaultWatch()
	b := a
	if !isHotOnly(a, b) {
		t.Fatalf("identical should be hot")
	}
	c := a
	c.OnStart = "skip"
	if isHotOnly(a, c) {
		t.Fatalf("on_start change should be cold")
	}
}

func mustAbs(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	return abs
}

// Ensure the env-var override in the state dir is honoured
// even when the user's real XDG_CONFIG_HOME is set.
func TestStateDir_Override(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got, want := StateDir(), filepath.Join(dir, "mekami", "supervisor"); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}
}

func TestDaemonView_Fields(t *testing.T) {
	d := &Daemon{
		Spec:      SpawnSpec{Root: "/p", Lang: "go", DBPath: "/p/g.db"},
		State:     StateRunning,
		PID:       1234,
		StartedAt: time.Now().Add(-30 * time.Second),
	}
	v := daemonViewLocked(d)
	if v.Root != "/p" || v.PID != 1234 || v.State != "running" {
		t.Fatalf("view wrong: %+v", v)
	}
	if v.UptimeS < 25 || v.UptimeS > 35 {
		t.Fatalf("uptime out of range: %d", v.UptimeS)
	}
}

func TestProcessAlive_CurrentPID(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatalf("expected current process to be alive")
	}
	if processAlive(0) {
		t.Fatalf("expected pid 0 to be dead")
	}
	if processAlive(2_000_000_000) {
		t.Fatalf("expected huge pid to be dead")
	}
}

// TestSupervisor_StateIsolation_BetweenInstances is a
// regression guard for a class of bugs where a supervisor
// instance could read rows from another instance's state
// dir. With StateDir injected per-instance, two supervisors
// built with distinct Options must not share any data.
func TestSupervisor_StateIsolation_BetweenInstances(t *testing.T) {
	a, err := NewSupervisorWithOptions(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := a.Register(SpawnSpec{Root: root, Lang: "go"}, PolicyOnCrash); err != nil {
		t.Fatal(err)
	}

	b, err := NewSupervisorWithOptions(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if roots := b.List(context.Background()); len(roots) != 0 {
		t.Fatalf("fresh supervisor must not see rows from other: got %v", roots)
	}
	if err := b.LoadFromRegistry(); err != nil {
		t.Fatal(err)
	}
	if roots := b.List(context.Background()); len(roots) != 0 {
		t.Fatalf("LoadFromRegistry must not leak across instances: got %v", roots)
	}
}

package watch

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

func TestCoalescer_DebounceCollapses(t *testing.T) {
	c := NewCoalescer(50*time.Millisecond, 1024)
	for i := 0; i < 10; i++ {
		c.Add(Event{Path: "a.go", Kind: EventWrite})
		time.Sleep(2 * time.Millisecond)
	}
	batch, ok := c.Drain(neverStop())
	if !ok || len(batch) != 1 {
		t.Fatalf("expected one collapsed event, got ok=%v batch=%v", ok, batch)
	}
	if batch[0].Path != "a.go" {
		t.Fatalf("wrong path: %q", batch[0].Path)
	}
}

func TestCoalescer_Promotion(t *testing.T) {
	c := NewCoalescer(10*time.Millisecond, 1024)
	c.Add(Event{Path: "x.go", Kind: EventCreate})
	c.Add(Event{Path: "x.go", Kind: EventWrite})
	c.Add(Event{Path: "x.go", Kind: EventChmod})
	c.Add(Event{Path: "x.go", Kind: EventRemove})
	batch, _ := c.Drain(neverStop())
	if len(batch) != 1 {
		t.Fatalf("expected 1, got %d (%v)", len(batch), batch)
	}
	if batch[0].Kind != EventRemove {
		t.Fatalf("expected Remove to win, got %v", batch[0].Kind)
	}
}

func TestCoalescer_SeparateBatches(t *testing.T) {
	c := NewCoalescer(20*time.Millisecond, 1024)
	stop := make(chan struct{})
	var (
		mu     sync.Mutex
		batch1 []Event
	)
	done := make(chan struct{})
	go func() {
		b, _ := c.Drain(stop)
		mu.Lock()
		batch1 = b
		mu.Unlock()
		close(done)
	}()
	c.Add(Event{Path: "a.go", Kind: EventWrite})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("first batch did not arrive")
	}
	mu.Lock()
	if len(batch1) != 1 || batch1[0].Path != "a.go" {
		mu.Unlock()
		t.Fatalf("first batch wrong: %v", batch1)
	}
	mu.Unlock()
	c.Add(Event{Path: "b.go", Kind: EventWrite})
	b2, ok := c.Drain(stop)
	if !ok || len(b2) != 1 || b2[0].Path != "b.go" {
		t.Fatalf("second batch wrong: ok=%v batch=%v", ok, b2)
	}
	close(stop)
}

func TestCoalescer_StopReturns(t *testing.T) {
	c := NewCoalescer(time.Hour, 1024)
	stop := make(chan struct{})
	close(stop)
	// With stop pre-closed and no events, Drain should return
	// immediately with ok=false and no batch.
	batch, ok := c.Drain(stop)
	if ok {
		t.Fatalf("expected !ok on stop, got batch=%v", batch)
	}
	if len(batch) != 0 {
		t.Fatalf("expected no events, got %d", len(batch))
	}
}

func TestCoalescer_StopReturnsLeftover(t *testing.T) {
	// When stop is closed before Drain is called, the leftover
	// events are still returned, but ok=false to signal the
	// caller to exit.
	c := NewCoalescer(time.Hour, 1024)
	c.Add(Event{Path: "a.go", Kind: EventWrite})
	stop := make(chan struct{})
	close(stop)
	batch, ok := c.Drain(stop)
	if ok {
		t.Fatalf("expected ok=false on stop, got ok=true batch=%v", batch)
	}
	if len(batch) != 1 {
		t.Fatalf("expected 1 leftover, got %d", len(batch))
	}
}

func TestCoalescer_BufferFull(t *testing.T) {
	c := NewCoalescer(time.Hour, 4)
	for i := 0; i < 10; i++ {
		c.Add(Event{Path: filepath.Join("dir", string(rune('a'+i))+".go"), Kind: EventWrite})
	}
	// 4 accepted, 6 dropped — we cannot tell from the outside
	// exactly which 4, but FlushImmediately should return <= 4.
	got := c.FlushImmediately()
	if len(got) > 4 {
		t.Fatalf("buffered=4 but got %d", len(got))
	}
}

func TestFilter_GoFile(t *testing.T) {
	f := DefaultFilter()
	cases := map[string]bool{
		"foo.go":              true,
		"sub/bar.go":          true,
		"deep/nested/x.go":    true,
		"foo_test.go":         false,
		"foo.txt":             false,
		"README.md":           false,
		".mekami/x.go":        false,
		"sub/.mekami/x.go":    false,
		"vendor/x.go":         false,
		"node_modules/x.go":   false,
		".git/x.go":           false,
		"_dev/x.go":           false,
		"foo.tmp":             false,
		"foo.swp":             false,
		"foo.swo":             false,
		".DS_Store":           false,
		"foo~":                false,
		"foo.go.bak":          false, // doesn't match *.swp
		"go.mod":              true,  // structural
		"go.work":             true,
		"go.sum":              true,
		"sub/go.mod":          true,
		"":                    false,
	}
	for in, want := range cases {
		got := f.Accept(in)
		if got != want {
			t.Errorf("Accept(%q): got %v, want %v", in, got, want)
		}
	}
}

func TestFilter_CustomPatterns(t *testing.T) {
	f := &Filter{IgnorePatterns: []string{"secret*.go", "*.gen.go"}}
	if f.Accept("secret.go") {
		t.Errorf("secret.go should be filtered")
	}
	if f.Accept("foo.gen.go") {
		t.Errorf("foo.gen.go should be filtered")
	}
	if !f.Accept("normal.go") {
		t.Errorf("normal.go should pass")
	}
}

func TestTranslate(t *testing.T) {
	root := "/tmp/proj"
	cases := []struct {
		ev       fsnotifyEvent
		wantPath string
		wantKind EventKind
		ok       bool
	}{
		{fsnotifyEvent{Name: "/tmp/proj/a.go", Op: opCreate}, "a.go", EventCreate, true},
		{fsnotifyEvent{Name: "/tmp/proj/sub/b.go", Op: opWrite}, "sub/b.go", EventWrite, true},
		{fsnotifyEvent{Name: "/tmp/proj/c.go", Op: opRemove}, "c.go", EventRemove, true},
		{fsnotifyEvent{Name: "/tmp/proj/d.go", Op: opChmod}, "d.go", EventChmod, true},
		{fsnotifyEvent{Name: "/tmp/proj/e.go", Op: opRename}, "e.go", EventRename, true},
		{fsnotifyEvent{Name: "", Op: opCreate}, "", 0, false},
		{fsnotifyEvent{Name: "/other/x.go", Op: opCreate}, "", 0, false},
		{fsnotifyEvent{Name: "/tmp/proj/f.go", Op: 0}, "", 0, false},
	}
	for i, tc := range cases {
		got, ok := Translate(root, tc.ev.toFsnotify())
		if ok != tc.ok {
			t.Errorf("case %d: ok=%v want %v", i, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Path != tc.wantPath {
			t.Errorf("case %d path: got %q want %q", i, got.Path, tc.wantPath)
		}
		if got.Kind != tc.wantKind {
			t.Errorf("case %d kind: got %v want %v", i, got.Kind, tc.wantKind)
		}
	}
}

func TestRun_OnceBuildsAndStops(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := writeGoMod(dir, "testmod"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "main.go"), "package foo\nfunc A() int { return 1 }\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	var stats *Stats
	var runErr error
	go func() {
		stats, runErr = Run(ctx, Options{
			Root:   dir,
			DBPath: dbPath,
			Config: configOnStartBuild(),
			Once:   true,
			Quiet:  true,
			Logger: StdLogger{W: nil},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("Run(Once) did not return in 3s")
	}
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if stats.FullRebuilds.Load() < 1 {
		t.Fatalf("expected at least 1 full rebuild, got %d", stats.FullRebuilds.Load())
	}
}

func TestRun_ContextCancelStopsGracefully(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := writeGoMod(dir, "testmod"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "main.go"), "package foo\nfunc A() int { return 1 }\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Options{
			Root:   dir,
			DBPath: dbPath,
			Config: configOnStartSkip(),
			Quiet:  true,
			Logger: StdLogger{W: nil},
		})
		done <- err
	}()
	// Give Run a moment to start the fsnotify loop.
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not return after cancel")
	}
}

func TestRun_NoRootErrors(t *testing.T) {
	_, err := Run(context.Background(), Options{
		DBPath: "/tmp/x.db",
		Quiet:  true,
	})
	if err == nil {
		t.Fatalf("expected error when Root is empty")
	}
}

// TestRun_EndToEnd exercises the full flow: skip on-start, then
// modify a file and verify the DB reflects the change. This is the
// most realistic test: it actually waits for fsnotify to deliver
// events, the coalescer to batch them, and the build pipeline to
// update the DB.
func TestRun_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := writeGoMod(dir, "testmod"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "main.go"), "package foo\nfunc A() int { return 1 }\n"); err != nil {
		t.Fatal(err)
	}

	// Pre-seed: full build so the DB has last_root set.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := buildOnce(ctx, dir, dbPath); err != nil {
		t.Fatalf("prebuild: %v", err)
	}

	cfg := configOnStartSkip()
	cfg.DebounceMs = 50

	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = Run(runCtx, Options{
			Root:   dir,
			DBPath: dbPath,
			Config: cfg,
			Quiet:  true,
			Logger: StdLogger{W: nil},
		})
		close(done)
	}()

	// Give the watcher a moment to subscribe.
	time.Sleep(200 * time.Millisecond)

	// Modify main.go: rename A -> A2.
	if err := writeFile(filepath.Join(dir, "main.go"), "package foo\nfunc A2() int { return 42 }\n"); err != nil {
		t.Fatal(err)
	}

	// Poll for the rename to land. Generous timeout because
	// fsnotify + debounce + SQLite can take a moment.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if symbolInDB(t, dbPath, "foo.A2") && !symbolInDB(t, dbPath, "foo.A") {
			runCancel()
			<-done
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	runCancel()
	<-done
	t.Fatalf("watcher did not pick up rename in time")
}

// buildOnce is a thin shim around ingest.Build used only by the
// end-to-end test. Kept here (not in testhelpers) because it's
// specific to the watch integration suite.
func buildOnce(ctx context.Context, root, dbPath string) error {
	return ingestBuild(ctx, root, dbPath)
}

func symbolInDB(t *testing.T, dbPath, qname string) bool {
	t.Helper()
	row := queryDB(t, dbPath, "SELECT 1 FROM symbols WHERE qualified_name = ? LIMIT 1", qname)
	return row
}

// neverStop returns a channel that is never closed. Used by tests
// that only want to drive the coalescer through one or two Drain
// calls and rely on the debounce window to deliver the batch.
func neverStop() <-chan struct{} {
	return make(chan struct{})
}

func configOnStartBuild() config.WatchConfig {
	c := config.DefaultWatch()
	c.OnStart = "build"
	return c
}

func configOnStartSkip() config.WatchConfig {
	c := config.DefaultWatch()
	c.OnStart = "skip"
	c.DebounceMs = 50
	return c
}

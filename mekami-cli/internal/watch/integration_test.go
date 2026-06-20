//go:build integration

// End-to-end tests for the watch package. These tests load the
// real Go frontend via a blank import of mekami-core-go and run
// ingest.Build against real Go source. They are gated behind the
// `integration` build tag so the default `go test ./...` does
// not require the frontend to be present in the test binary.
//
// To run them locally:
//
//	cp go.work.e2e.example go.work
//	go work sync
//	go test -tags integration ./internal/watch/...
//	rm go.work go.work.sum
//
// Or, if the e2e workspace is not set up, the same tests can be
// run with the default workspace as long as the proxy version of
// mekami-core-go can be resolved:
//
//	go test -tags integration ./internal/watch/...
//
// The tests cover:
//   - TestRun_OnceBuildsAndStops: Run with Once=true ingests and
//     returns.
//   - TestRun_ContextCancelStopsGracefully: Run returns cleanly
//     when its context is cancelled mid-loop.
//   - TestRun_EndToEnd: full fsnotify -> coalescer -> build ->
//     DB propagation.
//   - TestPoller_RunLoopIntegration: same as EndToEnd but with
//     the polling source.
package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/Wolf258/mekami-core-go"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
)

// ingestBuild is a thin shim around ingest.Build for end-to-end
// tests. The test file needs the symbol so the import in the
// test file does not have to be repeated.
func ingestBuild(ctx context.Context, root, dbPath string) error {
	_, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   root,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	return err
}

// buildOnce is a thin shim around ingestBuild used by the
// end-to-end test. It exists so the test body reads naturally.
func buildOnce(ctx context.Context, root, dbPath string) error {
	return ingestBuild(ctx, root, dbPath)
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

func TestPoller_RunLoopIntegration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := writeGoMod(dir, "testmod"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "main.go"), "package foo\nfunc A() int { return 1 }\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ingestBuild(ctx, dir, dbPath); err != nil {
		t.Fatalf("prebuild: %v", err)
	}

	src := NewPollerSource(dir, 50*time.Millisecond, StdLogger{W: nil})
	loopDone := make(chan struct{})
	stats := &Stats{}
	go func() {
		_ = RunLoop(ctx, src, Options{
			Root:   dir,
			DBPath: dbPath,
			Config: pollerFastConfig(),
			Logger: StdLogger{W: nil},
			Quiet:  true,
		}, stats)
		close(loopDone)
	}()
	defer func() {
		cancel()
		<-loopDone
	}()

	time.Sleep(80 * time.Millisecond)

	path := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, "package foo\nfunc B() int { return 42 }\n"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if symbolInDB(t, dbPath, "foo.B") && !symbolInDB(t, dbPath, "foo.A") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("poller did not propagate change in time")
}

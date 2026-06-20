//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// chdirTo swaps the process cwd to dir for the duration of the
// test. Returns a cleanup func that restores the original cwd.
// Tests that exercise a relative Build.Root rely on the cwd so
// WalkDir/Join/Rel paths match what the production CLI does when
// it runs `mekami build` from the project root.
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// TestBuild_RelativeRoot_FullBuild is the regression sentinel for
// the `mekami build --clean` data-loss bug. Before the fix, Build
// accepted a relative Root, walk produced relative rel paths,
// ingestFilesParallel joined them with the still-relative root,
// and the resulting `abs` argument to ParseFile was relative. The
// frontend then ran filepath.Rel(absolute, relative) and stdlib
// errored with "Rel: can't make X relative to Y", which the build
// loop silently turned into a skip. Result: clean DB, exit 0.
//
// After the fix, Build absolutizes Root on entry, so abs is always
// absolute and the Rel succeeds.
func TestBuild_RelativeRoot_FullBuild(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteModuleFiles(t, dir)

	dbPath := filepath.Join(t.TempDir(), "graph.db")
	chdirTo(t, dir)

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   ".",
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	if err != nil {
		t.Fatalf("Build with relative root failed: %v", err)
	}
	if stats.FilesScanned == 0 {
		t.Fatalf("expected FilesScanned > 0, got 0")
	}
	if stats.FilesIngested == 0 {
		t.Fatalf("expected FilesIngested > 0, got 0 (regression: data-loss bug returned)")
	}
	if stats.FilesSkipped != 0 {
		t.Errorf("expected FilesSkipped == 0, got %d (reasons: %v)",
			stats.FilesSkipped, stats.SkippedByReason)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	afterStats, err := queries.Stats(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if afterStats["files"] == 0 {
		t.Errorf("DB has 0 files after build (regression: data-loss bug returned)")
	}
}

// TestBuild_RelativeRoot_AfterClean mirrors the original bug
// report exactly: an init-like build followed by a build --clean
// with a relative root. The DB must not be empty after the second
// build.
func TestBuild_RelativeRoot_AfterClean(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteModuleFiles(t, dir)

	dbPath := filepath.Join(t.TempDir(), "graph.db")
	chdirTo(t, dir)

	// First build (no clean): the absolute root path is stamped
	// into the DB.
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   ".",
		DBPath: dbPath,
		Quiet:  true,
	}); err != nil {
		t.Fatalf("first build failed: %v", err)
	}

	// Second build with --clean and the same relative root.
	// Before the fix this returned exit 0 with FilesIngested=0
	// and the DB left empty.
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   ".",
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	if err != nil {
		t.Fatalf("clean build with relative root failed: %v", err)
	}
	if stats.FilesIngested == 0 {
		t.Fatalf("clean build ingested 0 files (regression: data-loss bug returned)")
	}
	if stats.FilesSkipped != 0 {
		t.Errorf("clean build should not skip any files, got %d (reasons: %v)",
			stats.FilesSkipped, stats.SkippedByReason)
	}
}

// TestBuild_SkippedSummary_DoesNotDumpPerFileLines verifies that
// when files are skipped, the build output is a compact summary
// (one line per distinct reason, plus a count) rather than N
// per-file "skip X: ..." lines. The user's console is the
// human-facing surface; the per-file detail belongs in a log
// file if anywhere.
func TestBuild_SkippedSummary_DoesNotDumpPerFileLines(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "graph.db")

	// Write a go.mod + one parseable file + one file with a
	// parse error (missing go.mod at root but a Go file with a
	// syntactically invalid declaration). The intent is to
	// produce at least one skip, so the summary path runs.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := "package testmod\nfunc A() int { return 1 }\n"
	if err := os.WriteFile(filepath.Join(dir, "good.go"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := "package testmod\nfunc A( { return 1 }\n" // syntax error
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  false, // exercise the summary path
		// Stash the summary into a side buffer instead of
		// stderr so the test can assert on it.
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	_ = stats

	// Direct call to PrintSkippedSummary so we can inspect the
	// output without capturing os.Stderr. This is the same code
	// path Build calls when !opts.Quiet && FilesSkipped > 0.
	buf.Reset()
	ingest.PrintSkippedSummary(&buf, stats, true)
	out := buf.String()

	if stats.FilesSkipped == 0 {
		t.Skip("no files were skipped; cannot exercise summary path")
	}

	// The summary must reference --clean explicitly when the
	// caller passed Clean=true so the operator notices the
	// data-loss risk.
	if !strings.Contains(out, "--clean skipped") {
		t.Errorf("summary must mention --clean when Clean=true, got: %q", out)
	}
	// The summary must be a count + small set of lines, not N
	// per-file lines. A safe upper bound: 5 reason lines + 1
	// header + optional "more" line.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 10 {
		t.Errorf("summary has %d lines; expected a compact summary (<= 10 lines), got:\n%s",
			len(lines), out)
	}
	for _, ln := range lines {
		// The per-file skip line in production had shape
		// "skip  <rel>: <reason>". Assert none of the
		// summary lines have that shape.
		if strings.HasPrefix(ln, "skip ") {
			t.Errorf("summary contains per-file skip line %q (regression: should be a count summary)", ln)
		}
	}
}

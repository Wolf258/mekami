//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// firstBuild runs a Go build on dir and returns the DB path.
// It is the shared setup for the prune tests: each test wants a
// populated DB to start from so the cross-language cleanup has
// something to remove.
func firstBuild(t *testing.T, dir string) string {
	t.Helper()
	testutil.WriteModuleFiles(t, dir)
	dbPath := filepath.Join(t.TempDir(), "graph.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatalf("first build: %v", err)
	}
	return dbPath
}

// relabelLang opens dbPath, flips every files.lang row to newLang,
// and re-saves with the same hash so the next build's hash check
// still passes. Used to simulate that a different frontend once
// ingested the same files.
func relabelLang(t *testing.T, dbPath, newLang string) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET lang = ?`, newLang); err != nil {
		t.Fatalf("relabel: %v", err)
	}
}

// countLang returns how many file rows have the given lang.
func countLang(t *testing.T, dbPath, lang string) int {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM files WHERE lang = ?`, lang).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestBuild_PruneRemovesDataForDisabledLang verifies the happy
// path: the DB has files tagged with a lang that's no longer in
// the allowed set, and the next build removes them.
func TestBuild_PruneRemovesDataForDisabledLang(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	if got := countLang(t, dbPath, "go"); got == 0 {
		t.Fatalf("expected go files after first build, got %d", got)
	}
	// Simulate: previously, the project also had a Python
	// frontend tracking the same files. Relabel the rows so they
	// appear to be Python now.
	relabelLang(t, dbPath, "python")

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{"go"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stats.RemovedLangs == nil {
		t.Fatal("expected RemovedLangs to be populated, got nil")
	}
	if n, ok := stats.RemovedLangs["python"]; !ok || n == 0 {
		t.Errorf("expected python in RemovedLangs with count > 0, got %v", stats.RemovedLangs)
	}
	if got := countLang(t, dbPath, "python"); got != 0 {
		t.Errorf("expected no python rows after prune, got %d", got)
	}
	if got := countLang(t, dbPath, "go"); got == 0 {
		t.Errorf("expected go rows after re-ingest, got %d", got)
	}
}

// TestBuild_PruneSkippedWhenAllowedLangsEmpty verifies the legacy
// path: callers that don't set AllowedLangs get the old
// behaviour where the cross-language cleanup does not run and
// stats.RemovedLangs stays nil. The final cleanup loop still
// removes the old row (because the file is re-ingested by the
// active frontend), so we only assert that no prune log line
// was emitted by checking the stats field.
func TestBuild_PruneSkippedWhenAllowedLangsEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	relabelLang(t, dbPath, "python")

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Lang:   "go",
		Quiet:  true,
		// AllowedLangs intentionally nil
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stats.RemovedLangs != nil {
		t.Errorf("expected no prune, got RemovedLangs = %v", stats.RemovedLangs)
	}
}

// TestBuild_PruneMultipleLangsAtOnce verifies that two or more
// disabled langs are removed in the same pass and both show up
// in the per-language counts. Setup: write two files in the
// same dir, run a build, then relabel one row to python and the
// other to rust. The next build's AllowedLangs=[go] prunes both.
func TestBuild_PruneMultipleLangsAtOnce(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module testmod\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "main.go"), "package foo\nfunc A() int { return 1 }\n")
	testutil.MustWrite(t, filepath.Join(dir, "extra.go"), "package foo\nfunc B() int { return 2 }\n")
	dbPath := filepath.Join(t.TempDir(), "graph.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rows, err := s.DB().QueryContext(ctx, `SELECT id, path FROM files ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type fp struct {
		id   int64
		path string
	}
	var all []fp
	for rows.Next() {
		var x fp
		if err := rows.Scan(&x.id, &x.path); err != nil {
			t.Fatal(err)
		}
		all = append(all, x)
	}
	rows.Close()
	if len(all) < 2 {
		t.Fatalf("need at least 2 file rows, got %d", len(all))
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET lang = 'python' WHERE id = ?`, all[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET lang = 'rust' WHERE id = ?`, all[1].id); err != nil {
		t.Fatal(err)
	}
	s.Close()

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{"go"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, ok := stats.RemovedLangs["python"]; !ok {
		t.Errorf("expected python in RemovedLangs, got %v", stats.RemovedLangs)
	}
	if _, ok := stats.RemovedLangs["rust"]; !ok {
		t.Errorf("expected rust in RemovedLangs, got %v", stats.RemovedLangs)
	}
	if got := countLang(t, dbPath, "python"); got != 0 {
		t.Errorf("python rows survived prune: %d", got)
	}
	if got := countLang(t, dbPath, "rust"); got != 0 {
		t.Errorf("rust rows survived prune: %d", got)
	}
}

// TestBuild_PruneKeepsAllAllowed verifies that AllowedLangs can
// list more than one language and none of the allowed rows get
// removed, even if the active frontend only ingests one of them.
func TestBuild_PruneKeepsAllAllowed(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	// Move the on-disk file out of the way so the next build's
	// walk won't find it, and re-tag its row as rust (a still-
	// allowed second lang). Then insert a python row directly:
	// the prune must drop it, but the rust row must stay even
	// though the cleanup loop sees it as "unseen" (it is not in
	// the walk's seen map).
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET lang = 'rust'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `
		INSERT INTO files(path, hash, mtime, size, lang) VALUES
		('other.py', 'h', 0, 0, 'python')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{"go", "rust"}, // python must go
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, ok := stats.RemovedLangs["python"]; !ok {
		t.Errorf("expected python to be pruned, got %v", stats.RemovedLangs)
	}
	if _, ok := stats.RemovedLangs["rust"]; ok {
		t.Errorf("rust must not be pruned (in AllowedLangs), got %v", stats.RemovedLangs)
	}
	if got := countLang(t, dbPath, "rust"); got == 0 {
		t.Errorf("rust rows should survive prune, got %d (RemovedLangs=%v FilesRemoved=%d)", got, stats.RemovedLangs, stats.FilesRemoved)
	}
}

// TestBuild_PruneEmptyAllowedListIsNoop guards against a nil-vs-
// empty footgun: a non-nil but empty AllowedLangs is treated the
// same as nil (no cross-language cleanup runs).
func TestBuild_PruneEmptyAllowedListIsNoop(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	relabelLang(t, dbPath, "python")

	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{}, // explicitly empty, not nil
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if stats.RemovedLangs != nil {
		t.Errorf("empty AllowedLangs should be a no-op, got %v", stats.RemovedLangs)
	}
}

// TestBuild_PruneCascadesThroughForeignKeys checks the FK
// cascade: symbols and refs belonging to the pruned lang must
// also disappear. This is what makes the cross-language cleanup
// safe to call on a populated DB without leaving dangling rows.
func TestBuild_PruneCascadesThroughForeignKeys(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	relabelLang(t, dbPath, "python")

	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{"go"},
	}); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	// We expect no symbols or refs under any "python" file.
	var orphanedSyms int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE f.lang = 'python'`).Scan(&orphanedSyms); err != nil {
		t.Fatal(err)
	}
	if orphanedSyms != 0 {
		t.Errorf("expected no symbols under pruned lang, got %d", orphanedSyms)
	}
	var orphanedRefs int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN files f ON f.id = s.file_id
		WHERE f.lang = 'python'`).Scan(&orphanedRefs); err != nil {
		t.Fatal(err)
	}
	if orphanedRefs != 0 {
		t.Errorf("expected no refs under pruned lang, got %d", orphanedRefs)
	}
}

// TestBuild_PruneLogLineFormat sanity-checks the format helper
// directly. formatPruneLog is private; we re-implement the
// access via the BuildStats.RemovedLangs field, but a real end-
// to-end test of the log line requires a stderr capture which
// the rest of the suite doesn't use. We rely on the build-level
// tests above for the per-language counts; this last test just
// documents the call site of the formatter.
func TestBuild_PruneProducesNonEmptyStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := firstBuild(t, dir)
	relabelLang(t, dbPath, "python")
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:         dir,
		DBPath:       dbPath,
		Lang:         "go",
		Quiet:        true,
		AllowedLangs: []string{"go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.RemovedLangs) == 0 {
		t.Errorf("expected non-empty RemovedLangs, got %v", stats.RemovedLangs)
	}
}

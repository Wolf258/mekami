//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// symbolExists is a small helper that returns true iff a symbol with
// the given qualified name is currently indexed. Used to assert that
// an incremental build actually applied a rename/delete, instead of
// just inspecting row counts (which can hide a missing cascade).
func symbolExists(t *testing.T, dbPath, qname string) bool {
	t.Helper()
	ctx := context.Background()
	s, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	rows, err := s.DB().QueryContext(ctx,
		`SELECT 1 FROM symbols WHERE qualified_name = ? LIMIT 1`, qname)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	return rows.Next()
}

func TestBuildIncremental_AddNewFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Add a new file, then run BuildIncremental on its path.
	extra := `package foo
func B() int { return A() }
`
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"extra.go"})
	if err != nil {
		t.Fatalf("BuildIncremental: %v", err)
	}
	if stats.Mode != "incremental" {
		t.Fatalf("mode: got %q, want incremental", stats.Mode)
	}
	if stats.FilesIngested != 1 {
		t.Fatalf("FilesIngested: got %d, want 1", stats.FilesIngested)
	}
	if !symbolExists(t, dbPath, "foo.B") {
		t.Fatalf("expected foo.B in DB after incremental add")
	}
}

func TestBuildIncremental_ModifyFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Modify main.go: rename A -> A2, return a different value. The
	// old A should disappear and the new A2 should appear.
	updated := `package foo
func A2() int { return 42 }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"main.go"})
	if err != nil {
		t.Fatalf("BuildIncremental: %v", err)
	}
	if stats.FilesIngested != 1 {
		t.Fatalf("FilesIngested: got %d, want 1", stats.FilesIngested)
	}
	if symbolExists(t, dbPath, "foo.A") {
		t.Fatalf("expected foo.A to be removed after rename")
	}
	if !symbolExists(t, dbPath, "foo.A2") {
		t.Fatalf("expected foo.A2 to exist after rename")
	}
}

func TestBuildIncremental_RemoveFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte(`package foo
func B() int { return A() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !symbolExists(t, dbPath, "foo.B") {
		t.Fatalf("setup: foo.B should exist after full build")
	}

	// Delete extra.go on disk and run BuildIncremental on its path.
	if err := os.Remove(filepath.Join(dir, "extra.go")); err != nil {
		t.Fatal(err)
	}
	stats, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"extra.go"})
	if err != nil {
		t.Fatalf("BuildIncremental: %v", err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("FilesRemoved: got %d, want 1", stats.FilesRemoved)
	}
	if symbolExists(t, dbPath, "foo.B") {
		t.Fatalf("expected foo.B to be gone after remove")
	}
	// Confirm the row in files is also gone (cascade check).
	s, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE path = ?`, "extra.go").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected extra.go row removed, found %d", n)
	}
}

func TestBuildIncremental_StructuralReturnsError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Touch go.mod: incremental should refuse and signal promotion.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testmod\n\ngo 1.22\nrequire example.com/x v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"go.mod"})
	if !errors.Is(err, ingest.ErrStructuralChange) {
		t.Fatalf("expected ErrStructuralChange, got %v", err)
	}
}

func TestBuildIncremental_SkipsUnchangedHash(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Same content (same hash) — the worker should skip without
	// re-parsing. Stats.FilesIngested stays at 0.
	stats, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"main.go"})
	if err != nil {
		t.Fatalf("BuildIncremental: %v", err)
	}
	if stats.FilesIngested != 0 {
		t.Fatalf("unchanged hash should not re-ingest, got FilesIngested=%d", stats.FilesIngested)
	}
}

func TestBuildIncremental_RejectsNonGo(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A README is a real file in the project, but the walker never
	// indexes it. An incremental over its path should be rejected.
	if err := os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# test"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"README.md"})
	if err == nil {
		t.Fatalf("expected error for non-Go path")
	}
	if !strings.Contains(err.Error(), "is not handled by language") {
		t.Fatalf("error should mention non-handled language, got: %v", err)
	}
}

func TestBuildIncremental_RejectsPathOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"../escape.go"})
	if err == nil {
		t.Fatalf("expected traversal to error")
	}
}

func TestBuildIncremental_NoLastRoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	// Don't run Build first — the DB file is created (via store.Open
	// inside BuildIncremental) but has no last_root. We need to
	// ensure the parent dir exists for Open to succeed.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ingest.BuildIncremental(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"main.go"})
	if !errors.Is(err, ingest.ErrNoLastRoot) {
		t.Fatalf("expected ErrNoLastRoot, got %v", err)
	}
}

func TestBuildIncremental_RefsClearedOnRemove(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	testutil.WriteModuleFilesWith(t, dir, `package foo
func A() int { return 1 }
`)
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte(`package foo
func B() int { return A() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Clean: true, Quiet: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Snapshot ref count; it should be at least 1 (B->A).
	s, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := queries.Stats(ctx, s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	if before["refs"] < 1 {
		t.Fatalf("setup: expected refs >= 1, got %d", before["refs"])
	}

	// Remove the caller file; refs should drop.
	if err := os.Remove(filepath.Join(dir, "extra.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.BuildIncremental(ctx, ingest.BuildOptions{
		Root: dir, DBPath: dbPath, Quiet: true,
	}, []string{"extra.go"}); err != nil {
		t.Fatal(err)
	}
	s2, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	after, err := queries.Stats(ctx, s2)
	if err != nil {
		t.Fatal(err)
	}
	if after["refs"] >= before["refs"] {
		t.Fatalf("refs did not decrease: before=%d after=%d", before["refs"], after["refs"])
	}
}

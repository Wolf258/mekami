//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/path"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// TestPathBetweenUnknownSymbol asserts the new validation behaviour:
// asking for a path where one endpoint does not exist in the index
// must return *path.ErrSymbolNotFound with the bad qname embedded,
// not an empty (nil) result.
func TestPathBetweenUnknownSymbol(t *testing.T) {
	src := `package foo
func A() {}
func Z() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()

	// Bad `from`
	_, err := path.Between(ctx, s, "foo.Missing", "foo.Z", 5)
	var nf *path.ErrSymbolNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("expected *path.ErrSymbolNotFound for missing from, got %T: %v", err, err)
	}
	if nf.QName != "foo.Missing" {
		t.Fatalf("expected QName=foo.Missing, got %q", nf.QName)
	}

	// Bad `to`
	_, err = path.Between(ctx, s, "foo.A", "foo.Missing", 5)
	if !errors.As(err, &nf) {
		t.Fatalf("expected *path.ErrSymbolNotFound for missing to, got %T: %v", err, err)
	}
	if nf.QName != "foo.Missing" {
		t.Fatalf("expected QName=foo.Missing, got %q", nf.QName)
	}
}

// TestIndexStatusRoundTrip builds a tiny project and then reads the
// status back: the counts must be non-zero and the build_at timestamp
// must be present.
func TestIndexStatusRoundTrip(t *testing.T) {
	src := `package foo
func A() {}
func B() { A() }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()

	st, err := queries.IndexStatus(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastBuildAt == "" {
		t.Fatal("expected last_build_at to be set after build")
	}
	if _, err := timeParse(st.LastBuildAt); err != nil {
		t.Fatalf("last_build_at is not RFC3339: %v", err)
	}
	if st.Counts["symbols"] < 2 {
		t.Fatalf("expected >=2 symbols, got %d", st.Counts["symbols"])
	}
	if st.Counts["refs"] < 1 {
		t.Fatalf("expected >=1 ref (A->B is the import anchor, B->A is a call), got %d", st.Counts["refs"])
	}
	if st.LastRoot == "" {
		t.Fatal("expected last_root to be set")
	}
}

// TestFilePathCandidatesAmbiguous creates two files with the same
// basename and checks that FilePathCandidates reports both.
func TestFilePathCandidatesAmbiguous(t *testing.T) {
	src := `package p
func A() {}
`
	// buildTestGraph only writes a single file, so for this test we
	// drive Build directly with a multi-file project.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	buildTestStore(t, dir, dbPath)
	s, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	matches, count, err := queries.FilePathCandidates(context.Background(), s, "store.go")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 candidates, got %d: %v", count, matches)
	}
	if len(matches) != 2 || matches[0] != "a/store.go" || matches[1] != "b/store.go" {
		t.Fatalf("unexpected match order: %v", matches)
	}
}

// TestFilePathCandidatesNoMatch asserts an empty result when the
// suffix matches nothing.
func TestFilePathCandidatesNoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package x\nfunc A(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	buildTestStore(t, dir, dbPath)
	s, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, count, err := queries.FilePathCandidates(context.Background(), s, "nope.go")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 candidates, got %d", count)
	}
}

// timeParse wraps time.Parse so the test body can stay short.
func timeParse(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// buildTestStore runs Build against a project root and writes the DB
// at dbPath. It is used by tests that need to seed a multi-file
// project (buildTestGraph only takes a single source blob).
func buildTestStore(t *testing.T, root, dbPath string) {
	t.Helper()
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   root,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}
}

// openStore wraps store.Open for brevity in the tests above.
func openStore(path string) (*store.Store, error) { return store.Open(path) }

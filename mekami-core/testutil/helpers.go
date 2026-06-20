// Package testutil provides shared helpers for the test suites living
// under tests/. Helpers are exported because the tests live in
// `package <foo>_test` (black-box) and need to import them from
// outside the original production package.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
)

// MustMkdir creates each given directory (and any missing parents).
// Test failure on error.
func MustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// MustWrite writes content to path with 0644 perms. Test failure on
// error. Used as a one-liner fixture helper across ingest/build/workspace
// tests.
func MustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WriteModuleFiles writes a minimal "module testmod" go.mod and a
// single-file package "foo" with one func A() in `dir`. It is the
// shared fixture for build/ingest/root-change tests.
func WriteModuleFiles(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package foo
func A() int { return 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WriteModuleFilesWith writes the same minimal go.mod and a main.go
// whose body is `src`. Used by tests that need a different symbol
// layout (e.g. cross-file references, callers/callees chains).
func WriteModuleFilesWith(t *testing.T, dir, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// OpenStoreForTest wraps store.Open for tests in this package that
// need direct DB access (e.g. to inspect meta after Build).
func OpenStoreForTest(dbPath string) (*store.Store, error) {
	return store.Open(dbPath)
}

// QueriesStatsForTest is a one-liner that calls queries.Stats. Kept
// here so test files don't need to import the queries package
// directly; many of them already pull in store via this helper.
func QueriesStatsForTest(ctx context.Context, s *store.Store) (map[string]int64, error) {
	return queries.Stats(ctx, s)
}

//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// buildTestGraph creates a fresh in-memory graph from a single .go
// file in a temp dir. It uses the public Build entry point so the
// test exercises the same path production code does (parseGoFile +
// writeParseResult + walk.Fingerprint + modlayout) end to end. Any
// regression in those primitives is caught by the assertions on
// queries.RefsFrom / queries.RefsTo / path.Between that follow.
func buildTestGraph(t *testing.T, gomod, src, fileName string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, fileName)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}
	storeHandle, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return storeHandle
}

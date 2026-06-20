//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// TestBuild_RenamedFileInsideRoot verifies the rename reconciliation:
//   - Build 1 ingests foo/a.go.
//   - On disk, foo/a.go is renamed to foo/b.go (content identical).
//   - Build 2 must drop the old row, ingest the new path, and leave
//     exactly one files row keyed on foo/b.go.
//
// This pins down the B11 behavior: a renamed file is always ingested
// in the second build (its path changed, so the hash cache cannot
// hit), and the old row is removed by the missing-on-disk sweep.
func TestBuild_RenamedFileInsideRoot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	src := `package foo
func Bar() int { return 1 }
`
	if err := os.MkdirAll(filepath.Join(dir, "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "foo", "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module testmod\n\ngo 1.26.3\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	stats1, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats1.FilesIngested != 1 {
		t.Fatalf("first build: expected 1 ingested, got %d", stats1.FilesIngested)
	}
	if stats1.SymbolsAdded <= 0 {
		t.Fatalf("first build: expected SymbolsAdded > 0, got %d", stats1.SymbolsAdded)
	}
	if stats1.RefsAdded < 0 {
		t.Fatalf("first build: expected RefsAdded >= 0, got %d", stats1.RefsAdded)
	}

	// Rename foo/a.go -> foo/b.go (identical content).
	if err := os.Rename(filepath.Join(dir, "foo", "a.go"), filepath.Join(dir, "foo", "b.go")); err != nil {
		t.Fatal(err)
	}

	stats2, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  false,
		Quiet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats2.FilesIngested != 1 {
		t.Errorf("second build (rename): expected 1 ingested (new path), got %d", stats2.FilesIngested)
	}
	if stats2.FilesScanned != 1 {
		t.Errorf("second build (rename): expected 1 scanned, got %d", stats2.FilesScanned)
	}
	// Pure rename (identical content, different path): the old file is
	// swept and the new one is ingested, so the net delta of symbols
	// and refs must be zero — proving the stats are net-delta, not
	// running total.
	if stats2.SymbolsAdded != 0 {
		t.Errorf("second build (rename): expected SymbolsAdded=0 (net delta), got %d (running total?)", stats2.SymbolsAdded)
	}
	if stats2.RefsAdded != 0 {
		t.Errorf("second build (rename): expected RefsAdded=0 (net delta), got %d (running total?)", stats2.RefsAdded)
	}

	s, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	files, err := queries.AllFiles(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 file row after rename, got %d (%v)", len(files), files)
	}
	if filepath.ToSlash(files[0].Path) != "foo/b.go" {
		t.Errorf("expected remaining file to be foo/b.go, got %q", files[0].Path)
	}
}

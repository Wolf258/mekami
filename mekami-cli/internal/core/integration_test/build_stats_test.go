//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// TestBuild_StatsAreDelta verifies that SymbolsAdded/RefsAdded
// reflect the delta introduced by this build, not the running total
// of the database. The second build with no source changes must
// report zero additions.
func TestBuild_StatsAreDelta(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	src := `package foo
func A() int { return 1 }
func B() int { return A() }
`
	testutil.WriteModuleFilesWith(t, dir, src)

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
	if stats1.SymbolsAdded <= 0 {
		t.Fatalf("first build: expected SymbolsAdded > 0, got %d", stats1.SymbolsAdded)
	}
	if stats1.RefsAdded <= 0 {
		t.Fatalf("first build: expected RefsAdded > 0, got %d", stats1.RefsAdded)
	}

	// Second build with no source changes: the file is re-ingested only
	// if the hash changes. With identical content, hash matches and no
	// symbols/refs are added. Delta must be zero.
	stats2, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  false,
		Quiet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats2.SymbolsAdded != 0 {
		t.Errorf("second build: expected SymbolsAdded=0, got %d (running total? delta semantics broken)", stats2.SymbolsAdded)
	}
	if stats2.RefsAdded != 0 {
		t.Errorf("second build: expected RefsAdded=0, got %d (running total? delta semantics broken)", stats2.RefsAdded)
	}
}

// TestBuild_StatsReflectNewFile verifies that adding a new function
// between builds reports a delta (not a running total). The first
// build populates several symbols; the second adds one new func in
// a separate file. The reported delta must be 1 (just the new
// symbol), not the running total of the database.
func TestBuild_StatsReflectNewFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	first := `package foo
func A() int { return 1 }
func C() int { return 2 }
func D() int { return 3 }
func E() int { return 4 }
func F() int { return 5 }
func G() int { return 6 }
`
	testutil.WriteModuleFilesWith(t, dir, first)

	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}

	// Inspect the running total via the DB directly so we can assert
	// that the second build's reported delta is strictly smaller.
	s, err := testutil.OpenStoreForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	totalBefore, err := testutil.QueriesStatsForTest(ctx, s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	if totalBefore["symbols"] < 5 {
		t.Fatalf("setup: expected at least 5 symbols in DB, got %d", totalBefore["symbols"])
	}

	// Add a brand-new file with one new func and one call. This forces
	// the second build to ingest a fresh file rather than mutate main.go.
	newFile := `package foo
func B() int { return A() }
`
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte(newFile), 0o644); err != nil {
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

	// Delta must be exactly 1 (just the new func B) and strictly less
	// than the running total. If SymbolsAdded reported the running
	// total, it would be at least 5.
	if stats2.SymbolsAdded != 1 {
		t.Errorf("second build: expected SymbolsAdded=1 (delta of new func B), got %d", stats2.SymbolsAdded)
	}
	if stats2.SymbolsAdded >= totalBefore["symbols"] {
		t.Errorf("second build: SymbolsAdded=%d should be < running total %d (delta semantics broken?)",
			stats2.SymbolsAdded, totalBefore["symbols"])
	}
	if stats2.RefsAdded < 1 {
		t.Errorf("second build: expected RefsAdded >= 1 (call from B to A), got %d", stats2.RefsAdded)
	}
}

//go:build integration
// +build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wolf258/mekami-core/diff"
	"github.com/Wolf258/mekami-core/ingest"
	"github.com/Wolf258/mekami-core/store"
)

// TestShowChanges_HashOnly verifies that show_changes
// treats a file with unchanged content (matching hash) but a different
// mtime as NOT modified. Hash is the source of truth for content.
func TestDiffSinceLastBuild_HashOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	src := `package foo
func Bar() int { return 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module testmod\n\ngo 1.26.3\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Build once (clean start); this also sets last_root.
	opts := ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}
	if _, err := ingest.Build(ctx, opts); err != nil {
		t.Fatal(err)
	}

	// Re-open the same db for the diff check.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	absRoot, _ := filepath.Abs(dir)

	// Touch the file: mtime changes, content (and thus hash) is identical.
	mainPath := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(mainPath, future, future); err != nil {
		t.Fatal(err)
	}

	d, err := diff.SinceLastBuild(ctx, s, absRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Modified) != 0 {
		t.Errorf("expected no modified files after a touch with unchanged content, got %v", d.Modified)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("expected no changes at all, got added=%v removed=%v modified=%v",
			d.Added, d.Removed, d.Modified)
	}
}

// TestBuild_HashOnly verifies that Build skips re-ingesting a file whose
// content hash is unchanged, even if mtime differs. Hash is the source
// of truth; mtime is recorded but does not gate re-ingestion.
func TestBuild_HashOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	src := `package foo
func Bar() int { return 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module testmod\n\ngo 1.26.3\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// First build with Clean=true to start from scratch.
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

	// Touch the file: mtime changes, content (and thus hash) is identical.
	mainPath := filepath.Join(dir, "main.go")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(mainPath, future, future); err != nil {
		t.Fatal(err)
	}

	// Second build preserves the DB; should detect the unchanged hash and skip.
	stats2, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  false,
		Quiet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats2.FilesIngested != 0 {
		t.Errorf("second build (touch only): expected 0 ingested, got %d", stats2.FilesIngested)
	}
}

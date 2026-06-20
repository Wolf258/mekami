//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// buildGraphWithVendor creates a temp module where the project
// imports a vendored dependency whose package name differs from the
// last path segment, then runs the public Build entry point so the
// graph reflects the same code path production users hit. The
// returned store is opened and the caller is responsible for
// closing it.
func buildGraphWithVendor(t *testing.T, gomod, mainSrc, depPkg, depPath, depFile string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	// main.go
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// vendor/<depPath>/<depFile> with package name = depPkg
	depDir := filepath.Join(dir, "vendor", depPath)
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	depSrc := "package " + depPkg + "\n\nfunc Renamed() {}\n"
	if err := os.WriteFile(filepath.Join(depDir, depFile), []byte(depSrc), 0o644); err != nil {
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
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Phase 1 (Fase 1 of the indexer rewrite) replaces the heuristic
// that assumed the last path segment equals the package name. The
// regression test below builds a graph over a module whose vendor
// contains a dependency whose `package <name>` decl is
// intentionally different from the final path segment, and asserts
// that the ref's ToQualified is the *real* package name (not the
// path basename).
func TestIngestImportAlias_PathDiffersFromPkgName(t *testing.T) {
	// Import path is "example.com/strcase" (last segment "strcase")
	// but the package decl is "package snake" — exactly the case
	// the historical heuristic got wrong. The historical build
	// would emit a ref to "strcase.Renamed"; the fixed build must
	// emit a ref to "snake.Renamed".
	const depPath = "example.com/strcase"
	const depPkg = "snake"
	s := buildGraphWithVendor(t,
		"module test.local/main\n\ngo 1.26.3\n",
		`package main

import "example.com/strcase"

func Use() { strcase.Renamed() }
`,
		depPkg, depPath, "strcase.go",
	)
	defer s.Close()
	ctx := context.Background()

	// The real ref: snake.Renamed.
	refs, err := queries.RefsTo(ctx, s, "snake.Renamed", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("expected at least one ref to snake.Renamed; got none " +
			"(the indexer is still using the path-basename heuristic)")
	}

	// The bogus ref: strcase.Renamed. Must be empty after the fix.
	bad, err := queries.RefsTo(ctx, s, "strcase.Renamed", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo(bogus): %v", err)
	}
	if len(bad) > 0 {
		t.Errorf("expected zero refs to bogus strcase.Renamed, got %d", len(bad))
	}
}

// TestIngestImportAlias_ExplicitAliasSameAsRenamed covers the case
// where the user wrote an explicit alias that happens to match the
// real package name. Both old and new pipelines produce the same
// ref. The test pins the new pipeline's behaviour so a future
// regression that re-introduces the heuristic for explicit aliases
// is caught.
func TestIngestImportAlias_ExplicitAliasSameAsRenamed(t *testing.T) {
	const depPath = "example.com/strcase"
	const depPkg = "snake"
	s := buildGraphWithVendor(t,
		"module test.local/main\n\ngo 1.26.3\n",
		`package main

import alias "example.com/strcase"

func Use() { alias.Renamed() }
`,
		depPkg, depPath, "strcase.go",
	)
	defer s.Close()
	ctx := context.Background()

	// The explicit alias 'alias' resolves to the real package name
	// 'snake' (resolved from the vendor entry). The ref target is
	// snake.Renamed, not alias.Renamed.
	refs, err := queries.RefsTo(ctx, s, "snake.Renamed", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("expected ref to snake.Renamed via explicit alias")
	}
	bad, _ := queries.RefsTo(ctx, s, "alias.Renamed", "", "", 100)
	if len(bad) > 0 {
		t.Errorf("expected zero refs to literal alias 'alias.Renamed', got %d", len(bad))
	}
}

// TestIngestImportAlias_FallbackOnMissingVendor covers the
// regression guard for the fallback path: when the dependency is
// not locatable in vendor / GOMODCACHE / workspace, the indexer
// must fall back to the last path segment so no ref is dropped.
func TestIngestImportAlias_FallbackOnMissingVendor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module test.local/main\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nimport \"example.com/missing\"\n\nfunc Use() { missing.Renamed() }\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	// No vendor dir; the dep is not in GOMODCACHE either. The
	// indexer must fall back to "missing" as the package name and
	// still emit the ref.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	refs, err := queries.RefsTo(context.Background(), s, "missing.Renamed", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected fallback ref to missing.Renamed")
	}
}

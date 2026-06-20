//go:build integration

package integration_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// TestListImportsReturnsTopLevelSymbols verifies that ListImports returns
// the real top-level symbols (func/type/var/const/method) declared in
// files that import a given package — not the synthetic __imports__
// anchor, not files, and not internal helpers.
func TestListImportsReturnsTopLevelSymbols(t *testing.T) {
	dir := t.TempDir()
	// A "library" module with one exported function.
	libDir := filepath.Join(dir, "lib")
	testutil.MustMkdir(t, libDir)
	testutil.MustWrite(t, filepath.Join(libDir, "go.mod"), "module example.com/lib\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(libDir, "lib.go"), "package lib\nfunc Hello() {}\n")

	// An "app" module that imports the library and has its own symbols.
	appDir := filepath.Join(dir, "app")
	testutil.MustMkdir(t, appDir)
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "go.work"), "go 1.22\n\nuse ./lib\nuse ./app\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"),
		"package app\n"+
			"\n"+
			"import \"example.com/lib\"\n"+
			"\n"+
			"type App struct{}\n"+
			"var AppName = \"x\"\n"+
			"const Version = 1\n"+
			"func Run() { lib.Hello() }\n"+
			"func helper() {}\n"+ // unexported — should be included (top-level + func kind)
			"")

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
	ctx := context.Background()

	syms, err := queries.ListImports(ctx, s, "example.com/lib")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) == 0 {
		t.Fatal("expected at least one symbol importing example.com/lib")
	}

	// Collect names for assertions.
	names := map[string]bool{}
	for _, s := range syms {
		if s.Kind == "file" {
			t.Errorf("ListImports returned a fake 'file' kind entry: %+v", s)
		}
		if s.StartLine == 0 && s.EndLine == 0 {
			t.Errorf("ListImports returned a fake zero-line entry: %+v", s)
		}
		if s.QualifiedName == "__imports__" {
			t.Errorf("ListImports returned the synthetic __imports__ anchor: %+v", s)
		}
		names[s.Name] = true
	}

	for _, want := range []string{"App", "AppName", "Version", "Run"} {
		if !names[want] {
			t.Errorf("expected top-level symbol %q in result, got %v", want, names)
		}
	}
}

// TestListImportsEmptyForUnusedPackage verifies that asking for the
// importers of a package no one imports returns an empty list, not nil
// or an error.
func TestListImportsEmptyForUnusedPackage(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/solo\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "a.go"), "package solo\nfunc A() {}\n")

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

	syms, err := queries.ListImports(context.Background(), s, "example.com/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 0 {
		t.Errorf("expected no symbols importing a non-existent package, got %d", len(syms))
	}
}

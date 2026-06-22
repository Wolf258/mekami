//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/handlers"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

// buildWorkspaceForCycle builds a 2-package Go workspace with the
// given package contents, returning the open store. Used by both
// the circular_imports and dependents tests.
func buildWorkspaceForCycle(t *testing.T, mod, pkgA, pkgB, fileA, fileB string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, pkgA), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, pkgB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pkgA, fileA), []byte(fileA), 0o644); err != nil {
		// fileA is the full path; ignore the inconsistency and
		// just use the basename.
		if err := os.WriteFile(filepath.Join(dir, pkgA, "a.go"), []byte(fileA), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, pkgB, "b.go"), []byte(fileB), 0o644); err != nil {
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
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestImportCycles_ABPair(t *testing.T) {
	mod := "module testmod"
	srcA := "package a\nimport \"testmod/b\"\nfunc A() { b.B() }\n"
	srcB := "package b\nimport \"testmod/a\"\nfunc B() { a.A() }\n"
	s := buildWorkspaceForCycle(t, mod, "a", "b", srcA, srcB)

	cycles, err := queries.ImportCycles(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}
	// Canonical key: smallest package first.
	cycle := cycles[0]
	if len(cycle) != 2 {
		t.Fatalf("expected 2-node cycle, got %v", cycle)
	}
	if cycle[0] != "testmod/a" || cycle[1] != "testmod/b" {
		t.Errorf("expected [testmod/a testmod/b], got %v", cycle)
	}
}

func TestImportCycles_NoCycles(t *testing.T) {
	mod := "module testmod"
	srcA := "package a\nfunc A() {}\n"
	srcB := "package b\nimport \"testmod/a\"\nfunc B() { a.A() }\n"
	s := buildWorkspaceForCycle(t, mod, "a", "b", srcA, srcB)

	cycles, err := queries.ImportCycles(context.Background(), s)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d: %v", len(cycles), cycles)
	}
}

func TestImportCycles_ThreeNodeCycle(t *testing.T) {
	mod := "module testmod"
	srcA := "package a\nimport \"testmod/b\"\nfunc A() { b.B() }\n"
	srcB := "package b\nimport \"testmod/c\"\nfunc B() { c.C() }\n"
	srcC := "package c\nimport \"testmod/a\"\nfunc C() { a.A() }\n"
	// We build the graph manually because the helper only
	// supports two packages; this test has three.
	_ = srcA
	_ = srcB
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ name, src string }{
		{"a", srcA}, {"b", srcB}, {"c", srcC},
	} {
		pdir := filepath.Join(dir, p.name)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, p.name+".go"), []byte(p.src), 0o644); err != nil {
			t.Fatal(err)
		}
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
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cycles, err := queries.ImportCycles(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}
	if len(cycles[0]) != 3 {
		t.Errorf("expected 3-node cycle, got %v", cycles[0])
	}
	// Canonical form: starts with the smallest package_id.
	if cycles[0][0] != "testmod/a" {
		t.Errorf("expected cycle to start with testmod/a, got %v", cycles[0])
	}
}

func TestCircularImportsHandler_NoCycles(t *testing.T) {
	mod := "module testmod"
	srcA := "package a\nfunc A() {}\n"
	srcB := "package b\nimport \"testmod/a\"\nfunc B() { a.A() }\n"
	s := buildWorkspaceForCycle(t, mod, "a", "b", srcA, srcB)

	out, err := handlers.CircularImports(context.Background(), s, naming.ArgMap{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "no circular imports") {
		t.Errorf("expected 'no circular imports detected' in text, got %q", res.Text)
	}
}

func TestCircularImportsHandler_WithCycle(t *testing.T) {
	mod := "module testmod"
	srcA := "package a\nimport \"testmod/b\"\nfunc A() { b.B() }\n"
	srcB := "package b\nimport \"testmod/a\"\nfunc B() { a.A() }\n"
	s := buildWorkspaceForCycle(t, mod, "a", "b", srcA, srcB)

	out, err := handlers.CircularImports(context.Background(), s, naming.ArgMap{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "1 cycle") {
		t.Errorf("expected '1 cycle' in text, got %q", res.Text)
	}
	if !strings.Contains(res.Text, "testmod/a") || !strings.Contains(res.Text, "testmod/b") {
		t.Errorf("text missing packages: %q", res.Text)
	}
}

func TestUnused_OneOrphanExported(t *testing.T) {
	mod := "module testmod"
	src := "package foo\nfunc Used() {}\nfunc Unused() {}\nfunc caller() { Used() }\n"
	_ = mod
	_ = buildWorkspaceForCycle(t, mod, ".", ".", src, "package x\n")
	// Build a single-package graph instead.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
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
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	syms, err := queries.UnusedSymbols(context.Background(), st, false, 100)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// We expect only foo.Unused (foo.Used has a ref from caller,
	// foo.caller is unexported).
	found := false
	for _, s := range syms {
		if s.QualifiedName == "foo.Unused" {
			found = true
		}
		if s.QualifiedName == "foo.Used" {
			t.Errorf("foo.Used should not appear in unused list: %v", syms)
		}
	}
	if !found {
		t.Errorf("foo.Unused missing from result: %v", syms)
	}
}

func TestUnusedHandler_WithEntryPointFilter(t *testing.T) {
	mod := "module testmod"
	src := "package foo\nfunc Used() {}\nfunc Unused() {}\nfunc main() { Used() }\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
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
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	out, err := handlers.Unused(context.Background(), st, naming.ArgMap{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// main is filtered out; only Unused should remain.
	if !strings.Contains(res.Text, "foo.Unused") {
		t.Errorf("expected foo.Unused in result: %q", res.Text)
	}
	if strings.Contains(res.Text, "foo.main") {
		t.Errorf("main should be filtered out: %q", res.Text)
	}
}

func TestUnusedHandler_AllEntryPointsFiltered(t *testing.T) {
	mod := "module testmod"
	src := "package foo\nfunc init() {}\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod+"\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
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
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	out, err := handlers.Unused(context.Background(), st, naming.ArgMap{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// init should be filtered; result is the "no unused" message.
	if !strings.Contains(res.Text, "no unused") {
		t.Errorf("expected 'no unused' message after filter, got %q", res.Text)
	}
}

//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/handlers"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

// buildDependentsGraph builds a 3-file Go module with a small
// call graph: A -> B -> C, where A also calls D directly. Used
// to verify the BFS traversal returns the expected tree shape.
func buildDependentsGraph(t *testing.T) *store.Store {
	t.Helper()
	const src = `package foo

func A() { B(); D() }
func B() { C() }
func C() {}
func D() {}
func main() { A() }
`
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
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestDependents_SymbolCallers_Transitive(t *testing.T) {
	s := buildDependentsGraph(t)
	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target":   "foo.C",
		"level":    "symbol",
		"callers":  "callers",
		"max_depth": 4,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// Direct caller of C is B; B's caller is A; A's caller is main.
	// Depth limit 4 means we should reach main.
	if !strings.Contains(res.Text, "foo.C") {
		t.Errorf("expected foo.C in tree: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.B") {
		t.Errorf("expected foo.B in tree: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.A") {
		t.Errorf("expected foo.A in tree: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.main") {
		t.Errorf("expected foo.main in tree: %q", res.Text)
	}
}

func TestDependents_SymbolCallers_NotTransitive(t *testing.T) {
	s := buildDependentsGraph(t)
	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target":     "foo.C",
		"level":      "symbol",
		"transitive": false,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// transitive=false caps at max_depth=1: only B, not A.
	if !strings.Contains(res.Text, "foo.B") {
		t.Errorf("expected foo.B in tree: %q", res.Text)
	}
	if strings.Contains(res.Text, "foo.A") {
		t.Errorf("foo.A should not be present with transitive=false: %q", res.Text)
	}
}

func TestDependents_SymbolCallees(t *testing.T) {
	s := buildDependentsGraph(t)
	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target":    "foo.A",
		"level":     "symbol",
		"direction": "callees",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// A's direct callees are B and D. B's callee is C. D is a leaf.
	if !strings.Contains(res.Text, "foo.B") {
		t.Errorf("expected foo.B in tree: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.D") {
		t.Errorf("expected foo.D in tree: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.C") {
		t.Errorf("expected foo.C (B's callee) in tree: %q", res.Text)
	}
}

func TestDependents_TargetNotFound(t *testing.T) {
	s := buildDependentsGraph(t)
	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target": "foo.DoesNotExist",
		"level":  "symbol",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "not found in index") {
		t.Errorf("expected 'not found in index' message, got %q", res.Text)
	}
}

func TestDependents_LeafHasNoCallers(t *testing.T) {
	// D is called by A only; let's find a func nobody calls.
	// Actually every func in this fixture is called. Build a
	// one-file graph with a single unreferenced func.
	const src = `package foo
func Orphan() {}
func main() {}
`
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
	out, err := handlers.Dependents(context.Background(), st, naming.ArgMap{
		"target": "foo.Orphan",
		"level":  "symbol",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "foo.Orphan") {
		t.Errorf("expected the target name in the tree, got %q", res.Text)
	}
	// Only the root in the tree; no other dependents.
	// The format prints "(1 node(s) total...".
	if !strings.Contains(res.Text, "1 node(s) total") {
		t.Errorf("expected 1-node total, got %q", res.Text)
	}
}

func TestDependents_InvalidLevel(t *testing.T) {
	s := buildDependentsGraph(t)
	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target": "foo.A",
		"level":  "garbage",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "invalid level") {
		t.Errorf("expected 'invalid level' message, got %q", res.Text)
	}
}

func TestDependents_PackageLevel(t *testing.T) {
	// Build a 2-package workspace: a imports b; verify that
	// dependents of b reports a.
	mod := "module testmod"
	srcA := "package a\nimport \"testmod/b\"\nfunc A() { b.B() }\n"
	srcB := "package b\nfunc B() {}\n"
	s := buildWorkspaceForCycle(t, mod, "a", "b", srcA, srcB)

	out, err := handlers.Dependents(context.Background(), s, naming.ArgMap{
		"target": "testmod/b",
		"level":  "package",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "testmod/a") {
		t.Errorf("expected testmod/a as a dependent of testmod/b: %q", res.Text)
	}
}

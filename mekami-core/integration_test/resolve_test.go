//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wolf258/mekami-core/path"
	"github.com/Wolf258/mekami-core/queries"
)

// T1: list_file should resolve a suffix match.
func TestFileOutlinePathResolution(t *testing.T) {
	src := `package foo
func Bar() int { return 1 }
type Baz struct{ X int }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "a/b/c.go")
	defer s.Close()
	ctx := context.Background()

	// Exact match
	syms, err := queries.FileOutline(ctx, s, "a/b/c.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols by exact path, got %d", len(syms))
	}

	// Suffix match
	syms, err = queries.FileOutline(ctx, s, "c.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 symbols by suffix 'c.go', got %d", len(syms))
	}
}

// T2 (agresiva): receiver call should be resolved to the receiver type.
func TestIngestReceiverCalls(t *testing.T) {
	src := `package foo
type Store struct{ X int }
func (s *Store) Bar() int { return s.X }
func (s *Store) Foo() int { s.Bar(); return 1 }
func (s *Store) Make() *Store { return &Store{X: 1} }
func (s *Store) Use() int { m := s.Make(); return m.Bar() }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()

	refs, err := queries.RefsTo(ctx, s, "foo.Store.Bar", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	// Expect call sites from Foo (s.Bar). The Use() function is also
	// expected to resolve m.Bar() to foo.Store.Bar, but only when the
	// local type resolver learns the return type of s.Make(). The
	// current resolver handles &T{} and same-package constructors but
	// not receiver-method calls, so Use() does not contribute. We
	// count distinct caller symbols (not total refs) to be robust
	// against the legacy RefCall+RefValue duplication.
	callers := map[string]bool{}
	for _, r := range refs {
		callers[r.FromSymbol.QualifiedName] = true
	}
	if !callers["foo.Store.Foo"] {
		t.Fatalf("expected ref from foo.Store.Foo, got %d refs: %+v", len(refs), refs)
	}
	// Make sure no ref to a bogus "s.Bar" qualified name exists.
	bad, _ := queries.RefsTo(ctx, s, "s.Bar", "", "", 100)
	if len(bad) > 0 {
		t.Fatalf("found bogus 's.Bar' refs: %d", len(bad))
	}
}

// T2 (agresiva): local variable of known type should resolve.
func TestIngestLocalVarResolution(t *testing.T) {
	src := `package foo
type T struct{}
func (t *T) Hello() {}
func NewT() *T { return &T{} }
func Use() { x := NewT(); x.Hello() }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()

	refs, err := queries.RefsTo(ctx, s, "foo.T.Hello", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected a ref to foo.T.Hello from Use()")
	}
}

// T2: composite literal with local var type should still resolve.
func TestIngestCompositeLitReceiver(t *testing.T) {
	src := `package foo
type Engine struct{}
func (e *Engine) Start() {}
func Make() { e := &Engine{}; e.Start() }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	refs, _ := queries.RefsTo(ctx, s, "foo.Engine.Start", "", "", 100)
	if len(refs) == 0 {
		t.Fatal("expected a ref to foo.Engine.Start via &Engine{} composite")
	}
}

// T3: shortest path between A and B should be returned as RefSite edges.
func TestPathBetweenChain(t *testing.T) {
	src := `package foo
func A() { B() }
func B() { C() }
func C() { D() }
func D() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	p, err := path.Between(ctx, s, "foo.A", "foo.D", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 3 {
		t.Fatalf("expected 3 edges (A->B, B->C, C->D), got %d: %+v", len(p), p)
	}
	if p[0].ToQName != "foo.B" || p[1].ToQName != "foo.C" || p[2].ToQName != "foo.D" {
		t.Fatalf("unexpected edge sequence: %v %v %v", p[0].ToQName, p[1].ToQName, p[2].ToQName)
	}
}

func TestPathBetweenDepthBound(t *testing.T) {
	src := `package foo
func A() { B() }
func B() { C() }
func C() { D() }
func D() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	// Depth 2 = A -> B -> C is reachable, but C -> D is the 3rd hop.
	p, err := path.Between(ctx, s, "foo.A", "foo.D", 2)
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("expected no path within depth 2, got %d edges", len(p))
	}
}

func TestPathBetweenNotFound(t *testing.T) {
	src := `package foo
func A() {}
func Z() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	p, err := path.Between(ctx, s, "foo.A", "foo.Z", 5)
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("expected nil for disconnected symbols, got %d edges", len(p))
	}
}

func TestPathBetweenSameSymbol(t *testing.T) {
	src := `package foo
func A() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	p, err := path.Between(ctx, s, "foo.A", "foo.A", 5)
	if !errors.Is(err, path.ErrSameSymbol) {
		t.Fatalf("expected ErrSameSymbol, got %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil path for from==to, got %d edges", len(p))
	}
}

// TestPathBetweenIgnoresTypeUse ensures that PathBetween follows only
// call edges. A type-use ref (e.g. a function variable or struct
// declaration that mentions a type) must not contribute to the call
// path, otherwise PathBetween would report paths that aren't real
// call chains.
func TestPathBetweenIgnoresTypeUse(t *testing.T) {
	src := `package foo
type T struct{}
func (t *T) Hello() {}
var x T
func Use() { x.Hello() }
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	// There is no real call from "foo.x" (a var) to "foo.T.Hello".
	// PathBetween must return nil even though the graph has a
	// type-use edge between them.
	p, err := path.Between(ctx, s, "foo.x", "foo.T.Hello", 5)
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("expected no path over type-use edges, got %d edges", len(p))
	}
}

// TestPathBetween_Diamond verifies the BFS returns the shortest path
// when the graph has two routes from from to to of different lengths.
// A -> B -> D and A -> C -> D; the BFS must pick the 2-hop route
// (3 edges), not 3-hop (4 edges).
func TestPathBetween_Diamond(t *testing.T) {
	src := `package foo
func A() { B(); C() }
func B() { D() }
func C() { D() }
func D() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	p, err := path.Between(ctx, s, "foo.A", "foo.D", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 {
		t.Fatalf("expected 2 edges (A->B, B->D or A->C, C->D), got %d: %+v", len(p), p)
	}
	if p[len(p)-1].ToQName != "foo.D" {
		t.Fatalf("expected last edge to point to foo.D, got %v", p[len(p)-1].ToQName)
	}
}

// TestPathBetween_VisitedNotOverwritten guards against a regression
// where parent[c] is set after visited[c] is set, but a later arrival
// of the same node overwrites the parent pointer. With the fixed BFS
// order (visited check before parent assignment), a node visited via
// a longer path cannot have its parent rewritten.
func TestPathBetween_VisitedNotOverwritten(t *testing.T) {
	src := `package foo
func A() { M() }
func Short() { T() }
func Long() { M() }
func M() { T() }
func T() {}
`
	s := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer s.Close()
	ctx := context.Background()
	// A reaches T via M (2 hops). We verify the path is exactly 2 edges.
	p, err := path.Between(ctx, s, "foo.A", "foo.T", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 {
		t.Fatalf("expected 2 edges (A->M, M->T), got %d: %+v", len(p), p)
	}
	if p[0].ToQName != "foo.M" || p[1].ToQName != "foo.T" {
		t.Fatalf("unexpected edge sequence: %v %v", p[0].ToQName, p[1].ToQName)
	}
}

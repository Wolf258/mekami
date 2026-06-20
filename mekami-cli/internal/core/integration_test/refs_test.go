//go:build integration

package integration_test

import (
	"context"
	"sort"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/queries"
)

// TestRefsFrom_NoKind verifies that with kind="", RefsFrom returns
// every distinct to_qualified, regardless of ref kind. The test uses
// a function whose body contains a mix of refs (call + type-use +
// value) so we exercise the body-driven ref collection.
func TestRefsFrom_NoKind(t *testing.T) {
	src := `package foo
type T struct{ X int }
func (t *T) Hello() int { return t.X }
func Use(t *T) { t.Hello(); _ = t.X }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()
	got, err := queries.RefsFrom(ctx, storeHandle, "foo.Use", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatalf("expected refs, got none")
	}
	for _, q := range got {
		if q == "" {
			t.Fatalf("empty qname in result: %v", got)
		}
	}
}

// TestRefsFrom_KindCall verifies that with kind=RefCall, only the
// direct call target is returned (not the type-use / value-use refs
// that the same source function has).
func TestRefsFrom_KindCall(t *testing.T) {
	src := `package foo
type Store struct{ X int }
func (s *Store) Bar() int { return s.X }
func (s *Store) Foo() int { s.Bar(); return 1 }
func Use() { s := &Store{1}; s.Bar() }
func _Make() *Store { return &Store{X: 1} }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()
	got, err := queries.RefsFrom(ctx, storeHandle, "foo.Store.Foo", "", "call", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 call ref (Bar), got %d: %v", len(got), got)
	}
	if got[0] != "foo.Store.Bar" {
		t.Fatalf("expected foo.Store.Bar, got %q", got[0])
	}
}

// TestRefsFrom_KindTypeUse verifies that with kind=RefTypeUse, the
// type-use edges are returned (and not the call edge to Bar).
func TestRefsFrom_KindTypeUse(t *testing.T) {
	src := `package foo
type Store struct{ X int }
func (s *Store) Bar() int { return s.X }
func (s *Store) Foo() int { s.Bar(); return 1 }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()
	got, err := queries.RefsFrom(ctx, storeHandle, "foo.Store.Foo", "", "type-use", 100)
	if err != nil {
		t.Fatal(err)
	}
	// The "call" to Bar must NOT be in this set.
	for _, q := range got {
		if q == "foo.Store.Bar" {
			t.Fatalf("call ref leaked into type-use filter: %v", got)
		}
	}
}

// TestRefsFrom_EmptyKindReturnsAll sanity-checks that an unfiltered
// query returns every distinct qname produced by a function with a
// receiver (which the local resolver can map). We use the same
// Store/Bar pattern as TestIngestReceiverCalls.
func TestRefsFrom_EmptyKindReturnsAll(t *testing.T) {
	src := `package foo
type Store struct{ X int }
func (s *Store) Bar() int { return s.X }
func (s *Store) Foo() int { s.Bar(); return 1 }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()
	all, err := queries.RefsFrom(ctx, storeHandle, "foo.Store.Foo", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(all)
	if len(all) == 0 {
		t.Fatal("expected refs in 'all' case")
	}
	// No duplicate qnames from the unfiltered query.
	seen := map[string]bool{}
	for _, q := range all {
		if seen[q] {
			t.Fatalf("duplicate qname in unfiltered result: %q", q)
		}
		seen[q] = true
	}
}

// TestRefsFrom_PathPrefixStillApplies guards against a regression
// where adding the kind filter accidentally clobbers the path-prefix
// filter.
func TestRefsFrom_PathPrefixStillApplies(t *testing.T) {
	src := `package foo
func A() { B() }
func B() { C() }
func C() {}
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()
	got, err := queries.RefsFrom(ctx, storeHandle, "foo.A", "main.go", "call", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "foo.B" {
		t.Fatalf("expected [foo.B] for prefix main.go, got %v", got)
	}
}

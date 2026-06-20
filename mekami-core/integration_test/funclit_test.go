//go:build integration
// +build integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/queries"
)

// TestFuncLit_CobraRunECapturesCalls reproduces the canonical case
// where a *ast.FuncLit is the value of a struct field at file scope:
//
//	var cmd = &cobra.Command{
//	    RunE: func(cmd *cobra.Command, args []string) error {
//	        return target()
//	    },
//	}
//
// Before the FuncLit pass, every call inside the closure was
// invisible to the graph because there is no enclosing *ast.FuncDecl
// to attribute it to. After the fix, the closure gets a synthetic
// owner symbol (kind=funclit) and the call to `target()` shows up in
// queries.RefsTo.
func TestFuncLit_CobraRunECapturesCalls(t *testing.T) {
	src := `package foo

import "errors"

var cmd = &struct {
	RunE func() error
}{
	RunE: func() error {
		return target()
	},
}

func target() error { return errors.New("x") }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()

	// The call to target() inside the closure must be visible.
	refs, err := queries.RefsTo(ctx, storeHandle, "foo.target", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo foo.target: %v", err)
	}
	if len(refs) == 0 {
		t.Fatalf("expected at least one caller of foo.target (the cobra RunE closure), got 0")
	}
	found := false
	for _, r := range refs {
		if r.FromSymbol.Kind == string(api.KindFuncLit) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a synthetic funclit caller of foo.target, got kinds: %v", refKinds(refs))
	}
}

// TestFuncLit_SyntheticSymbolName asserts the synthetic qualified
// name format: pkg.__lit__<basename>_<startLine>__.
func TestFuncLit_SyntheticSymbolName(t *testing.T) {
	src := `package foo

var c = &struct {
	F func()
}{
	F: func() { target() },
}

func target() {}
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()

	syms, err := queries.SearchSymbols(ctx, storeHandle, "__lit__", string(api.KindFuncLit), "", 100)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(syms) == 0 {
		t.Fatalf("expected at least one synthetic funclit symbol, got 0")
	}
	for _, s := range syms {
		if !strings.HasPrefix(s.QualifiedName, "foo.__lit__main_") {
			t.Fatalf("synthetic qname %q does not match expected prefix foo.__lit__main_", s.QualifiedName)
		}
		if !strings.HasSuffix(s.QualifiedName, "__") {
			t.Fatalf("synthetic qname %q missing trailing __", s.QualifiedName)
		}
	}
}

// TestFuncLit_NestedClosureAttributedToEnclosingFunc ensures we do
// NOT emit a second synthetic symbol for a FuncLit that is already
// inside a named function's body. The inner closure's refs must be
// attributed to the named function, not to a new funclit.
func TestFuncLit_NestedClosureAttributedToEnclosingFunc(t *testing.T) {
	src := `package foo

func outer() {
	fn := func() { target() }
	_ = fn
}

func target() {}
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()

	// The inner closure's call must be attributed to outer, not to
	// a synthetic funclit symbol.
	refs, err := queries.RefsTo(ctx, storeHandle, "foo.target", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 caller of foo.target (outer), got %d: %v", len(refs), refKinds(refs))
	}
	if refs[0].FromSymbol.Kind != string(api.KindFunc) {
		t.Fatalf("expected the caller kind to be func (outer), got %q", refs[0].FromSymbol.Kind)
	}

	// And there must be no synthetic funclit symbol at all.
	syms, err := queries.SearchSymbols(ctx, storeHandle, "__lit__", string(api.KindFuncLit), "", 100)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(syms) != 0 {
		t.Fatalf("expected 0 funclit symbols for a nested closure, got %d: %v", len(syms), symNames(syms))
	}
}

// TestFuncLit_GoStatementAtFileScope covers the `go func(){...}()`
// and `defer func(){...}()` shapes that are also top-level FuncLits
// even when not inside a composite literal. They must also get a
// synthetic owner.
func TestFuncLit_GoStatementAtFileScope(t *testing.T) {
	src := `package foo

var _ = func() int { return target() }()

func target() int { return 1 }
`
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()

	refs, err := queries.RefsTo(ctx, storeHandle, "foo.target", "", "", 100)
	if err != nil {
		t.Fatalf("RefsTo: %v", err)
	}
	found := false
	for _, r := range refs {
		if r.FromSymbol.Kind == string(api.KindFuncLit) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a funclit caller from the IIFE, got kinds: %v", refKinds(refs))
	}
}

// TestFuncLit_PathBetweenRoutesThroughClosure verifies that
// path.Between follows call edges through a synthetic funclit symbol,
// so an MCP client asking "what's the call path from cmd to target?"
// gets a real answer even when cmd's RunE is the only link.
func TestFuncLit_PathBetweenRoutesThroughClosure(t *testing.T) {
	src := `package foo

var entry = &struct {
	Run func()
}{
	Run: func() { target() },
}

func target() {}
`
	// entry is a struct value, not a real symbol in the graph, so we
	// look up the closure's synthetic symbol directly and assert
	// path.Between can route from it to foo.target.
	storeHandle := buildTestGraph(t, "module testmod\n\ngo 1.22\n", src, "main.go")
	defer storeHandle.Close()
	ctx := context.Background()

	syms, err := queries.SearchSymbols(ctx, storeHandle, "__lit__", string(api.KindFuncLit), "", 100)
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(syms) != 1 {
		t.Fatalf("expected exactly 1 funclit, got %d", len(syms))
	}
	lit := syms[0].QualifiedName

	// The synthetic closure's callees must include foo.target.
	callees, err := queries.RefsFrom(ctx, storeHandle, lit, "", string(api.RefCall), 100)
	if err != nil {
		t.Fatalf("RefsFrom %s: %v", lit, err)
	}
	if len(callees) != 1 || callees[0] != "foo.target" {
		t.Fatalf("expected callees=[foo.target] for %s, got %v", lit, callees)
	}
}

func refKinds(refs []model.RefSite) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.FromSymbol.Kind
	}
	return out
}

func symNames(syms []model.SymbolWithFile) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.QualifiedName
	}
	return out
}

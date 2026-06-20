//go:build integration
// +build integration

package integration_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-core/ingest"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-core/testutil"
)

// TestAddCompositeRef_StarExprLineNumber verifies that the type-use
// ref recorded for a composite literal whose type is a *T (or *pkg.T)
// has the line of the literal, not the line of the type expression
// that would be re-walked by a synthetic AST node.
func TestAddCompositeRef_StarExprLineNumber(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	libDir := filepath.Join(dir, "lib")
	testutil.MustMkdir(t, appDir, libDir)

	testutil.MustWrite(t, filepath.Join(dir, "go.work"), "go 1.22\n\nuse ./app\nuse ./lib\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(libDir, "go.mod"), "module example.com/lib\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(libDir, "lib.go"), "package lib\ntype Thing struct{ X int }\n")

	// Line 5 holds the *pkg.Thing composite literal. The line of the
	// ref must be 5, not any earlier line. The literal is inside a
	// function body because addCompositeRef is only invoked from
	// visitBody — value-level composite literals on a `var` decl are
	// handled by collectTypeRefsExpr, not the helper under test.
	src := "package app\n" +
		"\n" +
		"import \"example.com/lib\"\n" +
		"\n" +
		"type applocal struct{ X int }\n" +
		"func init() {\n" +
		"	_ = &lib.Thing{X: 1}\n" +
		"	_ = &applocal{}\n" +
		"}\n"
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), src)

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

	refs, err := queries.RefsTo(ctx, s, "lib.Thing", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected a ref to lib.Thing, got none")
	}
	if refs[0].Line != 7 {
		t.Errorf("ref to lib.Thing: expected line 7 (the &lib.Thing{...} line), got %d", refs[0].Line)
	}

	refs2, err := queries.RefsTo(ctx, s, "app.applocal", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs2) == 0 {
		// Local same-package type-use is recorded; if missing the test
		// should fail so we don't regress.
		t.Fatalf("expected a ref to app.applocal, got none; refs: %+v", mustAllRefs(t, s, ctx, "app."))
	}
	if refs2[0].Line != 8 {
		t.Errorf("ref to app.applocal: expected line 8 (the &applocal{} line), got %d", refs2[0].Line)
	}

	// Sanity: the ref's file path is correct.
	if !strings.HasSuffix(refs[0].FromSymbol.FilePath, "main.go") {
		t.Errorf("expected ref source in main.go, got %q", refs[0].FromSymbol.FilePath)
	}
}

// mustAllRefs is a tiny test helper used when an assertion needs to
// fail with a full dump of the refs table for a given prefix.
func mustAllRefs(t *testing.T, s *store.Store, ctx context.Context, qnPrefix string) []refSiteView {
	t.Helper()
	all, err := queries.RefsTo(ctx, s, qnPrefix, "", "", 1000)
	if err != nil {
		t.Fatalf("dump refs: %v", err)
	}
	out := make([]refSiteView, len(all))
	for i, r := range all {
		out[i] = refSiteView{
			FromQN: r.FromSymbol.QualifiedName,
			ToQN:   r.ToQName,
			Kind:   r.Kind,
			Line:   r.Line,
		}
	}
	return out
}

// refSiteView is a tiny DTO used by mustAllRefs to keep the dump
// format stable across schema changes.
type refSiteView struct {
	FromQN string
	ToQN   string
	Kind   string
	Line   int
}

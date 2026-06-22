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

// buildHandlerGraph is a thin variant of buildTestGraph (bridge_test.go)
// that returns the open store plus a t.Cleanup callback. The handler
// tests need the store to be alive for the duration of the test, so
// the existing helper (which defers Close) is not quite right.
func buildHandlerGraph(t *testing.T, gomod, src, fileName string) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, fileName)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(src), 0o644); err != nil {
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

func TestFindSymbols_ExactMatch(t *testing.T) {
	s := buildHandlerGraph(t,
		"module testmod\n\ngo 1.22\n",
		"package foo\nfunc Hello() {}\nfunc World() {}\n",
		"main.go",
	)
	_ = s
	// We use a second helper that does not need integration
	// tag because FindSymbols itself does not require a real
	// Go frontend — only SearchSymbols does, and the index
	// in this test is built by the integration helper.
	out, err := handlers.FindSymbols(context.Background(), s, naming.ArgMap{"query": "Hello"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res, ok := out.(handlers.Result)
	if !ok {
		t.Fatalf("expected Result, got %T", out)
	}
	if !strings.Contains(res.Text, "foo.Hello") {
		t.Errorf("text missing foo.Hello: %q", res.Text)
	}
	if strings.Contains(res.Text, "foo.World") {
		t.Errorf("text should not mention foo.World: %q", res.Text)
	}
}

func TestFindSymbols_NoMatch(t *testing.T) {
	s := buildHandlerGraph(t,
		"module testmod\n\ngo 1.22\n",
		"package foo\nfunc Hello() {}\n",
		"main.go",
	)
	out, err := handlers.FindSymbols(context.Background(), s, naming.ArgMap{"query": "Nonexistent"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "no symbols matching") {
		t.Errorf("expected 'no symbols matching' in text, got %q", res.Text)
	}
	if res.Data != nil {
		t.Errorf("expected nil Data for empty result, got %v", res.Data)
	}
}

func TestFindSymbols_KindFilter(t *testing.T) {
	s := buildHandlerGraph(t,
		"module testmod\n\ngo 1.22\n",
		"package foo\ntype MyType struct{}\nfunc MyFunc() {}\n",
		"main.go",
	)
	out, err := handlers.FindSymbols(context.Background(), s, naming.ArgMap{
		"query": "My",
		"kind":  "type",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "foo.MyType") {
		t.Errorf("expected foo.MyType in text, got %q", res.Text)
	}
	if strings.Contains(res.Text, "foo.MyFunc") {
		t.Errorf("kind=type should not include MyFunc, got %q", res.Text)
	}
}

func TestFindSymbols_EmptyQuery(t *testing.T) {
	s := buildHandlerGraph(t,
		"module testmod\n\ngo 1.22\n",
		"package foo\nfunc A() {}\n",
		"main.go",
	)
	out, err := handlers.FindSymbols(context.Background(), s, naming.ArgMap{"query": ""})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "query is required") {
		t.Errorf("expected 'query is required' message, got %q", res.Text)
	}
}

// TestDispatchRead_FindSymbols guards against the centralized
// dispatch forgetting the new tool. A typo in DispatchRead's
// switch silently routes to the unknown-command error.
func TestDispatchRead_FindSymbols(t *testing.T) {
	s := buildHandlerGraph(t,
		"module testmod\n\ngo 1.22\n",
		"package foo\nfunc Hello() {}\n",
		"main.go",
	)
	out, err := handlers.DispatchRead(context.Background(), s, "find_symbols", naming.ArgMap{"query": "Hello"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "foo.Hello") {
		t.Errorf("dispatch did not route to FindSymbols: %q", res.Text)
	}
}

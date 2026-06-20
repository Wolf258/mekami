package handlers

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

type testStore struct {
	*store.Store
	dbPath string
}

func newTestStore(t *testing.T) *testStore {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &testStore{Store: s}
}

func (ts *testStore) seedModule(t *testing.T, modPath string, packages ...string) {
	t.Helper()
	ctx := context.Background()
	tx, err := ts.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.UpsertModule(modPath); err != nil {
		t.Fatalf("upsert module %q: %v", modPath, err)
	}
	for _, pid := range packages {
		dir := pid
		if idx := strings.LastIndex(pid, "/"); idx >= 0 {
			dir = pid[idx+1:]
		}
		if _, err := tx.UpsertPackage(model.Package{
			ModuleID:  modPath,
			PackageID: pid,
			Name:      dir,
			Dir:       dir,
		}); err != nil {
			t.Fatalf("upsert package %q: %v", pid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestResolvePackageID_Canonical(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli", "github.com/Wolf258/mekami-cli/internal/mcp")

	got, err := resolvePackageID(context.Background(), ts.Store, "github.com/Wolf258/mekami-cli/internal/mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "github.com/Wolf258/mekami-cli/internal/mcp" {
		t.Fatalf("got %q, want canonical echoed back", got)
	}
}

func TestResolvePackageID_Empty(t *testing.T) {
	ts := newTestStore(t)
	_, err := resolvePackageID(context.Background(), ts.Store, "")
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
	if !strings.Contains(err.Error(), "package_id is required") {
		t.Fatalf("error %q does not mention 'package_id is required'", err)
	}
}

func TestResolvePackageID_ModuleRelativeSuffix_Unique(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli", "github.com/Wolf258/mekami-cli/internal/mcp")

	got, err := resolvePackageID(context.Background(), ts.Store, "internal/mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "github.com/Wolf258/mekami-cli/internal/mcp"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolvePackageID_BareSuffix_Ambiguous(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli",
		"github.com/Wolf258/mekami-cli/internal/mcp",
		"github.com/Wolf258/mekami-cli/cmd/mcp",
	)

	_, err := resolvePackageID(context.Background(), ts.Store, "mcp")
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous package_id") {
		t.Fatalf("error %q does not flag ambiguity", msg)
	}
	for _, want := range []string{
		"github.com/Wolf258/mekami-cli/internal/mcp",
		"github.com/Wolf258/mekami-cli/cmd/mcp",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing candidate %q", msg, want)
		}
	}
}

func TestResolvePackageID_BareSuffix_Unique(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli",
		"github.com/Wolf258/mekami-cli/internal/other",
		"github.com/Wolf258/mekami-cli/internal/mcp",
	)

	got, err := resolvePackageID(context.Background(), ts.Store, "mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "github.com/Wolf258/mekami-cli/internal/mcp"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolvePackageID_BareSuffix_CrossModule_Ambiguous(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mcp-a", "github.com/Wolf258/mcp-a/mcp")
	ts.seedModule(t, "github.com/Wolf258/mcp-b", "github.com/Wolf258/mcp-b/mcp")

	_, err := resolvePackageID(context.Background(), ts.Store, "mcp")
	if err == nil {
		t.Fatal("expected ambiguity error across modules, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous package_id") {
		t.Fatalf("error %q does not flag ambiguity", err)
	}
}

func TestResolvePackageID_NoMatch(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli", "github.com/Wolf258/mekami-cli/internal/mcp")

	got, err := resolvePackageID(context.Background(), ts.Store, "definitely/not/here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "definitely/not/here" {
		t.Fatalf("got %q, want raw input echoed back on no match", got)
	}
}

func TestResolvePackageIDCandidates_Dedup(t *testing.T) {
	ts := newTestStore(t)
	ts.seedModule(t, "github.com/Wolf258/mekami-cli",
		"github.com/Wolf258/mekami-cli/internal/mcp",
	)

	matches, err := resolvePackageIDCandidates(context.Background(), ts.Store, "mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(matches)
	want := []string{"github.com/Wolf258/mekami-cli/internal/mcp"}
	if len(matches) != len(want) || matches[0] != want[0] {
		t.Fatalf("got %v, want %v (dedup expected)", matches, want)
	}
}

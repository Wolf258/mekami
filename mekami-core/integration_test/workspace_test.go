//go:build integration
// +build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-core/ingest"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-core/testutil"
)

func TestBuildFromWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	// Create two modules under the workspace root.
	appDir := filepath.Join(root, "app")
	coreDir := filepath.Join(root, "core")
	testutil.MustMkdir(t, appDir, coreDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\nuse ./core\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "go.mod"), "module example.com/core\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "core.go"), "package core\nfunc World() {}\n")

	dbPath := filepath.Join(t.TempDir(), "ws.db")
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   root,
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIngested != 2 {
		t.Fatalf("expected 2 files ingested (app/main.go + core/core.go), got %d", stats.FilesIngested)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	mods, err := queries.ListModules(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules, got %d: %+v", len(mods), mods)
	}

	overview, err := queries.ModuleOverview(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview) != 2 {
		t.Fatalf("expected overview to contain 2 modules, got %d", len(overview))
	}
	var sawApp, sawCore bool
	for _, m := range overview {
		switch m.ModuleID {
		case "example.com/app":
			sawApp = true
		case "example.com/core":
			sawCore = true
		}
	}
	if !sawApp || !sawCore {
		t.Fatalf("overview missing modules: %+v", overview)
	}
}

func TestBuildFromSubModuleSkipsSiblings(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	coreDir := filepath.Join(root, "core")
	testutil.MustMkdir(t, appDir, coreDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\nuse ./core\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "go.mod"), "module example.com/core\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "core.go"), "package core\nfunc World() {}\n")

	dbPath := filepath.Join(t.TempDir(), "sub.db")
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   appDir,
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIngested != 1 {
		t.Fatalf("expected 1 file ingested (only app/), got %d", stats.FilesIngested)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mods, err := queries.ListModules(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("expected 1 module (sub-build), got %d: %+v", len(mods), mods)
	}
	if mods[0].Path != "example.com/app" {
		t.Fatalf("expected example.com/app, got %q", mods[0].Path)
	}

	// Sub-module builds must NOT persist workspace-level meta. Otherwise
	// this single-module DB would be reported as a workspace, polluting
	// ListModules / ModuleOverview with sibling module paths.
	isWS, err := s.GetMeta(context.Background(), store.MetaIsWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if isWS != "0" {
		t.Errorf("sub-module build: is_workspace should be 0, got %q", isWS)
	}
	wsMods, err := s.GetMeta(context.Background(), store.MetaWorkspaceMods)
	if err != nil {
		t.Fatal(err)
	}
	if wsMods != "" {
		t.Errorf("sub-module build: workspace_modules should be empty, got %q", wsMods)
	}
	primary, err := s.GetMeta(context.Background(), store.MetaPrimaryModule)
	if err != nil {
		t.Fatal(err)
	}
	if primary != "" {
		t.Errorf("sub-module build: primary_module should be empty, got %q", primary)
	}
}

// TestBuildFromSubModuleClearsStaleWorkspaceMeta verifies that a
// re-build of a sub-module of a workspace clears any stale workspace
// meta that was persisted by a previous build at the workspace root.
func TestBuildFromSubModuleClearsStaleWorkspaceMeta(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	coreDir := filepath.Join(root, "core")
	testutil.MustMkdir(t, appDir, coreDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\nuse ./core\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "go.mod"), "module example.com/core\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "core.go"), "package core\nfunc World() {}\n")

	dbPath := filepath.Join(t.TempDir(), "shared.db")

	// First build at the workspace root: populates workspace_modules.
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   root,
		DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}

	// Second build from a sub-module: must clear the workspace meta.
	// Use --clean so the test can pivot the root from the workspace
	// root to a sub-module without --force-root.
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root:   appDir,
		DBPath: dbPath,
		Clean:  true,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	isWS, _ := s.GetMeta(context.Background(), store.MetaIsWorkspace)
	if isWS != "0" {
		t.Errorf("after sub-module rebuild, is_workspace should be 0, got %q", isWS)
	}
	wsMods, _ := s.GetMeta(context.Background(), store.MetaWorkspaceMods)
	if wsMods != "" {
		t.Errorf("after sub-module rebuild, workspace_modules should be empty, got %q", wsMods)
	}
	mods, err := queries.ListModules(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Path != "example.com/app" {
		t.Errorf("expected only example.com/app after sub-module rebuild, got %+v", mods)
	}
}

func TestBuildSingleModuleUnchanged(t *testing.T) {
	// No go.work → IsWorkspace=false → behavior matches the pre-workspace code.
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module solo\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "a.go"), "package solo\nfunc A() {}\n")
	testutil.MustWrite(t, filepath.Join(dir, "b.go"), "package solo\nfunc B() {}\n")

	dbPath := filepath.Join(t.TempDir(), "solo.db")
	stats, err := ingest.Build(context.Background(), ingest.BuildOptions{Root: dir, DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIngested != 2 {
		t.Fatalf("expected 2 files, got %d", stats.FilesIngested)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	isWS, _ := s.GetMeta(context.Background(), store.MetaIsWorkspace)
	if isWS != "0" {
		t.Fatalf("expected is_workspace=0, got %q", isWS)
	}
	mods, _ := queries.ListModules(context.Background(), s)
	if len(mods) != 1 || mods[0].Path != "solo" {
		t.Fatalf("expected one module 'solo', got %+v", mods)
	}
}

// TestListModules_NoFSRead verifies that after Build, ListModules and
// ModuleOverview return the persisted module paths even when the
// go.mod files have been deleted from disk. The build is the source of
// truth at query time.
func TestListModules_NoFSRead(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	coreDir := filepath.Join(root, "core")
	testutil.MustMkdir(t, appDir, coreDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\nuse ./core\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "go.mod"), "module example.com/core\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")
	testutil.MustWrite(t, filepath.Join(coreDir, "core.go"), "package core\nfunc World() {}\n")

	dbPath := filepath.Join(t.TempDir(), "nofsread.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: root, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}

	// Delete every go.mod after build. If the readers fall back to
	// the FS, the test will observe an empty/short result.
	if err := os.Remove(filepath.Join(appDir, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(coreDir, "go.mod")); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mods, err := queries.ListModules(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules after go.mod removal, got %d: %+v", len(mods), mods)
	}
	got := map[string]bool{}
	for _, m := range mods {
		got[m.Path] = true
	}
	if !got["example.com/app"] || !got["example.com/core"] {
		t.Fatalf("missing expected modules: %+v", mods)
	}

	overview, err := queries.ModuleOverview(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview) != 2 {
		t.Fatalf("expected overview to have 2 modules after go.mod removal, got %d", len(overview))
	}
	ovPaths := map[string]bool{}
	for _, m := range overview {
		ovPaths[m.ModuleID] = true
	}
	if !ovPaths["example.com/app"] || !ovPaths["example.com/core"] {
		t.Fatalf("overview missing modules: %+v", overview)
	}
}

// TestListModules_PersistedPathMatchesBuild verifies that the path
// persisted in workspace_modules JSON matches the module path that
// the on-disk go.mod would resolve to.
func TestListModules_PersistedPathMatchesBuild(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	testutil.MustMkdir(t, appDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/app/v2\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")

	dbPath := filepath.Join(t.TempDir(), "persist.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: root, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, err := s.GetMeta(context.Background(), store.MetaWorkspaceMods)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "{") {
		t.Fatalf("expected JSON-formatted workspace_modules, got %q", raw)
	}
	if !strings.Contains(raw, `"example.com/app/v2"`) {
		t.Fatalf("persisted path missing: %q", raw)
	}
}

// TestListModules_LegacyFallback verifies that a workspace_modules
// meta written in the old plain-dir format is still readable.
// After the core became language-agnostic the FS-fallback that
// read go.mod was removed; legacy entries surface the dir
// until a full rebuild re-stamps the Path field via the
// frontend.
func TestListModules_LegacyFallback(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	testutil.MustMkdir(t, appDir)
	testutil.MustWrite(t, filepath.Join(root, "go.work"), "go 1.22\n\nuse ./app\n")
	testutil.MustWrite(t, filepath.Join(appDir, "go.mod"), "module example.com/legacy\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(appDir, "main.go"), "package app\nfunc Hello() {}\n")

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: root, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	// Overwrite the persisted meta with the legacy plain-dir format.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetMeta(context.Background(), store.MetaWorkspaceMods, "app"); err != nil {
		t.Fatal(err)
	}
	mods, err := queries.ListModules(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 {
		t.Fatalf("expected 1 module via legacy fallback, got %d: %+v", len(mods), mods)
	}
	// Without a frontend in the loop the legacy entry surfaces
	// the dir (joined with lastRoot). A full rebuild re-stamps
	// the canonical Path.
	if mods[0].Path == "" {
		t.Fatalf("legacy fallback should not return empty path, got %+v", mods[0])
	}
}

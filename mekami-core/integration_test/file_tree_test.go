//go:build integration
// +build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-core/ingest"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-core/testutil"
)

// TestFileTree_PrefixIsFile verifies that when prefix is an exact file
// path, FileTree returns a single file node (no wrapping dir) with the
// file's size and lang metadata populated.
func TestFileTree_PrefixIsFile(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ft\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "main.go"), "package ft\nfunc A() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ft.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "main.go", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Type != "file" {
		t.Fatalf("expected type=file, got %q", node.Type)
	}
	if node.Name != "main.go" {
		t.Fatalf("expected name=main.go, got %q", node.Name)
	}
	if node.Path != "main.go" {
		t.Fatalf("expected path=main.go, got %q", node.Path)
	}
	if node.Size == 0 {
		t.Fatalf("expected non-zero size, got 0")
	}
	if node.Lang != "go" {
		t.Fatalf("expected lang=go, got %q", node.Lang)
	}
	if len(node.Children) != 0 {
		t.Fatalf("expected no children for file node, got %d", len(node.Children))
	}
}

// TestFileTree_PrefixIsDir verifies the standard case: prefix points to
// a directory with nested files; the root is a dir node whose children
// are the nested files.
func TestFileTree_PrefixIsDir(t *testing.T) {
	dir := t.TempDir()
	testutil.MustMkdir(t, filepath.Join(dir, "sub"))
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ftd\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "sub", "a.go"), "package ftd\nfunc A() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ftd.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "sub", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if node.Type != "dir" {
		t.Fatalf("expected type=dir, got %q", node.Type)
	}
	if node.Name != "sub" {
		t.Fatalf("expected name=sub, got %q", node.Name)
	}
	if len(node.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(node.Children))
	}
	if node.Children[0].Type != "file" || node.Children[0].Name != "a.go" {
		t.Fatalf("unexpected child: %+v", node.Children[0])
	}
}

// TestFileTree_PrefixUnknown verifies that a non-existent prefix returns
// an empty dir node (not nil, not an error). Callers can present this as
// "no results".
func TestFileTree_PrefixUnknown(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ftu\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "main.go"), "package ftu\nfunc A() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ftu.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "does/not/exist", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("expected non-nil dir node for unknown prefix")
	}
	if node.Type != "dir" {
		t.Fatalf("expected type=dir, got %q", node.Type)
	}
	if node.Name != "does/not/exist" {
		t.Fatalf("expected name=does/not/exist, got %q", node.Name)
	}
	if len(node.Children) != 0 {
		t.Fatalf("expected no children, got %d", len(node.Children))
	}
}

// TestFileTree_EmptyPrefix verifies that the empty prefix still produces
// the "." root.
func TestFileTree_EmptyPrefix(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/fte\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "main.go"), "package fte\nfunc A() {}\n")
	dbPath := filepath.Join(t.TempDir(), "fte.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil || node.Name != "." || node.Type != "dir" {
		t.Fatalf("expected root dir named '.', got %+v", node)
	}
	if len(node.Children) == 0 {
		t.Fatalf("expected at least one child")
	}
}

// TestFileTree_PrefixIsFileMissingIndexed verifies that after a file is
// deleted from disk, the graph still returns it as a file node (we serve
// from the indexed snapshot, not the FS). This guards against a future
// regression where FileTree might fall back to os.Stat.
func TestFileTree_PrefixIsFileMissingIndexed(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ftm\n\ngo 1.22\n")
	testutil.MustWrite(t, filepath.Join(dir, "main.go"), "package ftm\nfunc A() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ftm.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "main.go", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil || node.Type != "file" {
		t.Fatalf("expected file node from indexed snapshot, got %+v", node)
	}
}

// TestFileTree_ZeroMaxDepthIsUnlimited verifies that passing maxDepth=0
// returns the full tree regardless of how deep the files live. The
// previous behavior (silently treating 0 as 4) hid roughly a third of
// the file set on real Go workspaces whose tree goes 6-9 segments
// deep.
func TestFileTree_ZeroMaxDepthIsUnlimited(t *testing.T) {
	dir := t.TempDir()
	// Build a 9-segment file path (a/a/a/a/a/a/a/a/leaf.go). With the
	// old default of maxDepth=4 this file would be silently dropped.
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ftz\n\ngo 1.22\n")
	deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "g", "h")
	testutil.MustMkdir(t, deep)
	testutil.MustWrite(t, filepath.Join(deep, "leaf.go"), "package z\nfunc Leaf() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ftz.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if node == nil || node.Type != "dir" {
		t.Fatalf("expected root dir, got %+v", node)
	}
	// Walk the tree and confirm leaf.go is reachable from the root.
	found := walkForFile(node, "a/b/c/d/e/f/g/h/leaf.go")
	if !found {
		t.Fatalf("expected leaf.go at a/b/c/d/e/f/g/h/leaf.go with maxDepth=0, but it was truncated")
	}
}

// TestFileTree_ExplicitCapStillTruncates verifies that an explicit
// maxDepth=4 keeps the legacy truncation behavior. Callers that pass a
// concrete cap must still see the cap honored.
func TestFileTree_ExplicitCapStillTruncates(t *testing.T) {
	dir := t.TempDir()
	testutil.MustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/ftc\n\ngo 1.22\n")
	deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "g", "h")
	testutil.MustMkdir(t, deep)
	testutil.MustWrite(t, filepath.Join(deep, "leaf.go"), "package c\nfunc Leaf() {}\n")
	dbPath := filepath.Join(t.TempDir(), "ftc.db")
	if _, err := ingest.Build(context.Background(), ingest.BuildOptions{
		Root: dir, DBPath: dbPath,
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := queries.FileTree(context.Background(), s, "", 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if walkForFile(node, "a/b/c/d/e/f/g/h/leaf.go") {
		t.Fatalf("did not expect leaf.go under explicit maxDepth=4")
	}
}

// walkForFile returns true if `target` appears anywhere in the tree
// rooted at `n`. Used by the depth-cap tests.
func walkForFile(n *model.FileNode, target string) bool {
	if n == nil {
		return false
	}
	if n.Type == "file" && n.Path == target {
		return true
	}
	for _, c := range n.Children {
		if walkForFile(c, target) {
			return true
		}
	}
	return false
}

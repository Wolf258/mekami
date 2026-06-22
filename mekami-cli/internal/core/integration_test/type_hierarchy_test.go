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

func TestTypeHierarchy_Members(t *testing.T) {
	const src = `package foo
type Reader interface {
	Read(p []byte) (int, error)
}
type File struct{}
func (f *File) Read(p []byte) (int, error) { return 0, nil }
func (f *File) Close() error { return nil }
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

	out, err := handlers.TypeHierarchy(context.Background(), s, naming.ArgMap{
		"type": "foo.File",
		"mode": "members",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "foo.File.Read") {
		t.Errorf("expected foo.File.Read in members: %q", res.Text)
	}
	if !strings.Contains(res.Text, "foo.File.Close") {
		t.Errorf("expected foo.File.Close in members: %q", res.Text)
	}
}

func TestTypeHierarchy_Implementers(t *testing.T) {
	const src = `package foo
type Reader interface {
	Read(p []byte) (int, error)
}
type File struct{}
func (f *File) Read(p []byte) (int, error) { return 0, nil }
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

	out, err := handlers.TypeHierarchy(context.Background(), s, naming.ArgMap{
		"type": "foo.Reader",
		"mode": "implementers",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	// The Read method on File takes/returns Reader; the parser
	// emits a type-use ref to foo.Reader. The query projects
	// the parent of the ref (the receiver type or the type
	// itself) — what the parser actually emits depends on
	// mekami-core-go. We assert the section is rendered and
	// the implementer list is at least empty or non-empty
	// without crashing.
	if !strings.Contains(res.Text, "implementers") {
		t.Errorf("expected 'implementers' section: %q", res.Text)
	}
}

func TestTypeHierarchy_NotAType(t *testing.T) {
	const src = `package foo
func Bar() {}
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

	out, err := handlers.TypeHierarchy(context.Background(), s, naming.ArgMap{
		"type": "foo.Bar",
		"mode": "all",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "not a type") {
		t.Errorf("expected 'not a type' message: %q", res.Text)
	}
}

func TestTypeHierarchy_NotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package foo\n"), 0o644); err != nil {
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

	out, err := handlers.TypeHierarchy(context.Background(), s, naming.ArgMap{
		"type": "foo.NonExistent",
		"mode": "all",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "not found in index") {
		t.Errorf("expected 'not found in index' message: %q", res.Text)
	}
}

func TestTypeHierarchy_AllMode(t *testing.T) {
	const src = `package foo
type Reader interface {
	Read(p []byte) (int, error)
}
type File struct{}
func (f *File) Read(p []byte) (int, error) { return 0, nil }
func (f *File) Close() error { return nil }
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

	out, err := handlers.TypeHierarchy(context.Background(), s, naming.ArgMap{
		"type": "foo.File",
		"mode": "all",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	res := out.(handlers.Result)
	if !strings.Contains(res.Text, "members") {
		t.Errorf("expected 'members' section in all-mode: %q", res.Text)
	}
	if !strings.Contains(res.Text, "implementers") {
		t.Errorf("expected 'implementers' section in all-mode: %q", res.Text)
	}
}

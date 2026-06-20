//go:build integration

package integration_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-cli/internal/core/modlayout"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/core/testutil"
)

// TestBuild_RootChangeRejected verifies that running build with a
// different --root against an existing DB returns an error and does
// NOT update last_root. This prevents the silent corruption where
// DiffSinceLastBuild would walk the new root and compare against
// file paths stored relative to the old root.
func TestBuild_RootChangeRejected(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	testutil.WriteModuleFiles(t, dirA)
	testutil.WriteModuleFiles(t, dirB)
	dbPath := filepath.Join(t.TempDir(), "shared.db")

	ctx := context.Background()
	// First build establishes last_root=dirA.
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dirA,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}

	// Second build with a different root, no --clean, no --force-root.
	_, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dirB,
		DBPath: dbPath,
		Clean:  false,
		Quiet:  true,
	})
	if err == nil {
		t.Fatal("expected error on root change, got nil")
	}
	if !strings.Contains(err.Error(), "last_root") {
		t.Errorf("error message should mention last_root, got: %v", err)
	}

	// last_root must remain pointing to the original dir.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !modlayout.SamePath(got, dirA) {
		t.Errorf("last_root should still be %q, got %q", dirA, got)
	}
}

// TestBuild_RootChangeWithForceRoot verifies that --force-root allows
// the root change. last_root is updated; the DB's other rows remain
// referencing the old root, which is the documented (and intentional)
// inconsistency of this flag.
func TestBuild_RootChangeWithForceRoot(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	testutil.WriteModuleFiles(t, dirA)
	testutil.WriteModuleFiles(t, dirB)
	dbPath := filepath.Join(t.TempDir(), "shared.db")

	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dirA,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:      dirB,
		DBPath:    dbPath,
		Clean:     false,
		Quiet:     true,
		ForceRoot: true,
	}); err != nil {
		t.Fatalf("force-root should allow root change, got: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !modlayout.SamePath(got, dirB) {
		t.Errorf("last_root should be %q, got %q", dirB, got)
	}
}

// TestBuild_RootChangeWithClean verifies that --clean allows the root
// change. The DB is wiped, so there is no inconsistency to worry about.
func TestBuild_RootChangeWithClean(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	testutil.WriteModuleFiles(t, dirA)
	testutil.WriteModuleFiles(t, dirB)
	dbPath := filepath.Join(t.TempDir(), "shared.db")

	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dirA,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dirB,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatalf("--clean should allow root change, got: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !modlayout.SamePath(got, dirB) {
		t.Errorf("last_root should be %q, got %q", dirB, got)
	}
}

// TestBuild_SameRootNoError verifies that re-running build with the
// same root never errors out, even without --clean or --force-root.
// This is the common case (incremental builds).
func TestBuild_SameRootNoError(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteModuleFiles(t, dir)
	dbPath := filepath.Join(t.TempDir(), "test.db")

	ctx := context.Background()
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	}); err != nil {
		t.Fatal(err)
	}
	// Re-run with the same root: no error.
	if _, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  false,
		Quiet:  true,
	}); err != nil {
		t.Fatalf("re-running build with same root should not error, got: %v", err)
	}
}

// TestBuild_RespectsCancelledContext verifies that Build returns
// promptly with a context error when the context is cancelled or
// expires mid-ingest, instead of processing every file.
func TestBuild_RespectsCancelledContext(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteModuleFiles(t, dir)
	dbPath := filepath.Join(t.TempDir(), "ctx.db")

	// Cancel synchronously before Build so ctx.Err() is set
	// immediately. This is deterministic: no reliance on a 1ns
	// timeout racing the scheduler.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   dir,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

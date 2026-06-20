//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
)

func TestGetMeta_NoLastRootReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = s.GetMeta(context.Background(), store.MetaLastRoot)
	if !errors.Is(err, store.ErrNoLastRoot) {
		t.Fatalf("expected ErrNoLastRoot, got %v", err)
	}
}

func TestSourceSlice_NoLastRootReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = queries.SourceSlice(context.Background(), s, "main.go", 1, 10, 0)
	if !errors.Is(err, store.ErrNoLastRoot) {
		t.Fatalf("expected ErrNoLastRoot, got %v", err)
	}
}

package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

func TestUpsertPackageIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	insert := func(mod, ip string) int64 {
		tx, err := s.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.UpsertModule(mod); err != nil {
			t.Fatal(err)
		}
		id, err := tx.UpsertPackage(model.Package{ModuleID: mod, PackageID: ip, Name: "n", Dir: "d"})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}

	id1 := insert("shared", "shared/types/execution")
	id2 := insert("shared", "shared/types/execution")
	t.Logf("id1=%d id2=%d", id1, id2)
	if id1 != id2 {
		t.Fatalf("expected same id, got %d and %d", id1, id2)
	}
}

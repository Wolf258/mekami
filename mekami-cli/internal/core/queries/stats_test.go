package queries_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// TestStore_StatsParityWithStatsTables pins the contract that the
// result map from Stats has exactly the same keys as StatsTables —
// and only those keys. This guards against drift when someone adds
// a new table to StatsTables and forgets to add the matching scalar
// subquery in Stats, which would silently drop that count from the
// result map.
func TestStore_StatsParityWithStatsTables(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	stats, err := queries.Stats(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != len(queries.StatsTables) {
		t.Errorf("len(stats)=%d != len(StatsTables)=%d (drift: query and StatsTables out of sync)",
			len(stats), len(queries.StatsTables))
	}
	for _, tbl := range queries.StatsTables {
		if _, ok := stats[tbl]; !ok {
			t.Errorf("StatsTables contains %q but Stats() result map does not (drift)", tbl)
		}
	}
	for k := range stats {
		found := false
		for _, tbl := range queries.StatsTables {
			if tbl == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Stats() result has key %q not in StatsTables (drift)", k)
		}
	}
}

// TestLastRoot pins the contract: an unset meta key returns
// store.ErrNoLastRoot, an empty string in the meta key also maps
// to ErrNoLastRoot (so callers can use a single errors.Is check),
// and a real path round-trips verbatim.
func TestLastRoot(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "lastroot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if _, err := queries.LastRoot(ctx, s); !errors.Is(err, store.ErrNoLastRoot) {
		t.Fatalf("unset meta: got %v, want ErrNoLastRoot", err)
	}
	if err := s.SetMeta(ctx, store.MetaLastRoot, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.LastRoot(ctx, s); !errors.Is(err, store.ErrNoLastRoot) {
		t.Fatalf("empty meta: got %v, want ErrNoLastRoot", err)
	}
	if err := s.SetMeta(ctx, store.MetaLastRoot, "/tmp/whatever"); err != nil {
		t.Fatal(err)
	}
	got, err := queries.LastRoot(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/whatever" {
		t.Fatalf("LastRoot: got %q, want /tmp/whatever", got)
	}
}

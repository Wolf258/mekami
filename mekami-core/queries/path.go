package queries

import (
	"context"

	"github.com/Wolf258/mekami-core/store"
)

// LastRoot returns the absolute path of the most recent successful
// build. It collapses the "meta key missing" / "meta key set to
// empty string" cases into a single store.ErrNoLastRoot so callers
// can use one errors.Is check.
func LastRoot(ctx context.Context, s *store.Store) (string, error) {
	root, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", store.ErrNoLastRoot
	}
	return root, nil
}

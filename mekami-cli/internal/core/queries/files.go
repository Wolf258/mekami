package queries

import (
	"context"
	"database/sql"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// ResolveFilePath maps a user-supplied path to a stored file path.
// It first tries exact match; if none, it tries suffix match preferring
// the shortest matching path (so "store.go" → "a/b/store.go", not
// "a/b/c/store.go" if both exist). Returns "" when no match.
func ResolveFilePath(ctx context.Context, s *store.Store, path string) (string, error) {
	var exact string
	err := s.DB().QueryRowContext(ctx, `SELECT path FROM files WHERE path = ?`, path).Scan(&exact)
	if err == nil {
		return exact, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	// Suffix match: find paths ending in /<path>. The exact match
	// was already attempted above, so this query only needs the LIKE
	// clause.
	like := "%/" + path
	var rows *sql.Rows
	rows, err = s.DB().QueryContext(ctx,
		`SELECT path FROM files WHERE path LIKE ? ORDER BY length(path) ASC LIMIT 1`,
		like)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return "", err
		}
		return p, nil
	}
	return "", nil
}

// FilePathCandidates returns every stored file path ending in /<path>
// or equal to <path>, plus the count of matches. Used by the MCP
// handler to tell the LLM when its input was ambiguous and which
// longer paths it could try instead.
func FilePathCandidates(ctx context.Context, s *store.Store, path string) (matches []string, count int, err error) {
	like := "%/" + path
	rows, err := s.DB().QueryContext(ctx,
		`SELECT path FROM files WHERE path = ? OR path LIKE ? ORDER BY length(path) ASC`,
		path, like)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, 0, err
		}
		matches = append(matches, p)
	}
	return matches, len(matches), rows.Err()
}

// AllFiles returns every indexed file row.
func AllFiles(ctx context.Context, s *store.Store) ([]model.File, error) {
	rows, err := s.DB().QueryContext(ctx,
		`SELECT id,path,hash,mtime,size,lang FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.File
	for rows.Next() {
		var f model.File
		if err := rows.Scan(&f.ID, &f.Path, &f.Hash, &f.Mtime, &f.Size, &f.Lang); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

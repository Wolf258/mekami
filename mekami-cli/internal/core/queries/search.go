package queries

import (
	"context"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// SearchSymbols searches symbols by name substring, with optional kind
// and path-prefix filters.
func SearchSymbols(ctx context.Context, s *store.Store, query, kind, pathPrefix string, limit int) ([]model.SymbolWithFile, error) {
	if limit <= 0 {
		limit = 50
	}
	sqlStr := `
		SELECT ` + store.SymbolWithFileSelect + `
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.name LIKE ?`
	args := []any{"%" + query + "%"}
	if kind != "" {
		sqlStr += " AND s.kind = ?"
		args = append(args, kind)
	}
	if pathPrefix != "" {
		sqlStr += " AND f.path LIKE ?"
		args = append(args, pathPrefix+"%")
	}
	sqlStr += " ORDER BY s.qualified_name LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB().QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SymbolWithFile
	for rows.Next() {
		swf, err := store.ScanSymbolWithFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, swf)
	}
	return out, rows.Err()
}

// SymbolByQName returns every symbol matching the given qualified name.
func SymbolByQName(ctx context.Context, s *store.Store, qn string) ([]model.SymbolWithFile, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT `+store.SymbolWithFileSelect+`
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.qualified_name = ?
		ORDER BY s.start_line`, qn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SymbolWithFile
	for rows.Next() {
		swf, err := store.ScanSymbolWithFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, swf)
	}
	return out, rows.Err()
}

// FileOutline returns all symbols defined in the given file (exact or
// suffix match). When the input matches multiple files, returns the
// shortest suffix match and lets the caller proceed; the matching path
// is at the top of the result (callers can re-query with the longer
// path if they need a specific one).
func FileOutline(ctx context.Context, s *store.Store, path string) ([]model.SymbolWithFile, error) {
	resolved, err := ResolveFilePath(ctx, s, path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, nil
	}
	rows, err := s.DB().QueryContext(ctx, `
		SELECT `+store.SymbolWithFileSelect+`
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE f.path = ?
		ORDER BY s.start_line`, resolved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SymbolWithFile
	for rows.Next() {
		swf, err := store.ScanSymbolWithFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, swf)
	}
	return out, rows.Err()
}

// PackageOutline returns all top-level symbols of a package,
// optionally filtered by kind.
func PackageOutline(ctx context.Context, s *store.Store, packageID string, kinds []string) ([]model.SymbolWithFile, error) {
	sqlStr := `
		SELECT ` + store.SymbolWithFileSelect + `
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		JOIN packages p ON p.id = s.package_id
		WHERE p.package_id = ? AND s.kind != '` + string(api.KindImports) + `'`
	args := []any{packageID}
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, k := range kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		sqlStr += " AND s.kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	sqlStr += " ORDER BY f.path, s.start_line"

	rows, err := s.DB().QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SymbolWithFile
	for rows.Next() {
		swf, err := store.ScanSymbolWithFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, swf)
	}
	return out, rows.Err()
}

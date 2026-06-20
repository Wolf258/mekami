package queries

import (
	"context"

	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/store"
)

// RefsTo returns incoming references to qn (callers / uses), with
// optional kind and path-prefix filters.
func RefsTo(ctx context.Context, s *store.Store, qn, kind, pathPrefix string, limit int) ([]model.RefSite, error) {
	if limit <= 0 {
		limit = 100
	}
	sqlStr := `
		SELECT ` + store.SymbolWithFileSelect + `,r.line,r.kind
		FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN files f ON f.id = s.file_id
		WHERE r.to_qualified = ?`
	args := []any{qn}
	if kind != "" {
		sqlStr += " AND r.kind = ?"
		args = append(args, kind)
	}
	if pathPrefix != "" {
		sqlStr += " AND f.path LIKE ?"
		args = append(args, pathPrefix+"%")
	}
	sqlStr += " ORDER BY f.path, r.line LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB().QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RefSite
	for rows.Next() {
		rs, err := store.ScanRefSite(rows)
		if err != nil {
			return nil, err
		}
		rs.ToQName = qn
		out = append(out, rs)
	}
	return out, rows.Err()
}

// RefsFrom returns the distinct qualified names referenced by the
// given symbol. With kind="" returns every ref kind; with kind set,
// filters to that kind.
func RefsFrom(ctx context.Context, s *store.Store, qn, pathPrefix, kind string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	sqlStr := `SELECT DISTINCT r.to_qualified FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN files f ON f.id = s.file_id
		WHERE s.qualified_name = ?`
	args := []any{qn}
	if pathPrefix != "" {
		sqlStr += " AND f.path LIKE ?"
		args = append(args, pathPrefix+"%")
	}
	if kind != "" {
		sqlStr += " AND r.kind = ?"
		args = append(args, kind)
	}
	sqlStr += " ORDER BY r.to_qualified LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB().QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

package queries

import (
	"context"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// TypeMembers returns every method (kind=method) and funclit
// (kind=funclit) whose parent_symbol links to the type
// identified by typeQN. The type itself is not returned; the
// caller already has it via the qualified_name it passed in.
//
// typeQN is resolved to a symbol id with a subquery, so the
// caller does not need to know the numeric id. If multiple
// symbols share the qualified name (should not happen for
// type declarations, but defensive), only the lowest-id row
// is used as the parent.
func TypeMembers(ctx context.Context, s *store.Store, typeQN string) ([]model.SymbolWithFile, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT `+store.SymbolWithFileSelect+`
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.parent_symbol = (
		  SELECT id FROM symbols WHERE qualified_name = ? ORDER BY id LIMIT 1
		)
		ORDER BY s.start_line
	`, typeQN)
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

// InterfaceImplementers returns every type (kind=type) that has
// at least one type-use ref to interfaceQN. This is a NAME-based
// approximation: Go interfaces are structurally satisfied, so a
// type can implement an interface WITHOUT naming it. The query
// catches only the types whose source code mentions the
// interface in a type position (parameter, return, embedded,
// alias, struct field).
//
// For the LLM's use case (answering "who implements io.Reader
// in this project?") this is usually enough — the conventional
// practice is to declare interface satisfaction explicitly in
// the type, even though the compiler does not require it.
func InterfaceImplementers(ctx context.Context, s *store.Store, interfaceQN string) ([]model.SymbolWithFile, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT `+store.SymbolWithFileSelect+`
		FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN files f ON f.id = s.file_id
		WHERE r.kind = 'type-use'
		  AND r.to_qualified = ?
		  AND s.kind = 'type'
		ORDER BY s.qualified_name
	`, interfaceQN)
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

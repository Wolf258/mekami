package queries

import (
	"context"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// UnusedSymbols returns exported symbols that have no incoming
// refs of any kind. The query excludes the synthetic __imports__
// anchor (kind='imports') and symbols that are themselves method
// bodies or funclits (parent_symbol IS NOT NULL) so a method
// nested under a type is not flagged just because the type was
// the user-facing surface.
//
// includeUnexported=false keeps the default conservative: only
// exported symbols are reported, which is what the LLM usually
// wants ("can I remove this? did anyone import it?"). Setting
// it to true reports unexported helpers too; useful for
// "cleanup my internal pkg" passes.
//
// limit caps the result; callers should pass a value >= the
// expected working set, since the query is not ordered in a
// way that puts "most likely unused" first. The default 200 is
// the same budget the other read tools use.
func UnusedSymbols(ctx context.Context, s *store.Store, includeUnexported bool, limit int) ([]model.SymbolWithFile, error) {
	if limit <= 0 {
		limit = 200
	}
	exportedClause := "1"
	if includeUnexported {
		exportedClause = "s.exported"
	}
	sqlStr := `
		SELECT ` + store.SymbolWithFileSelect + `
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.kind != 'imports'
		  AND s.parent_symbol IS NULL
		  AND ` + exportedClause + ` = 1
		  AND NOT EXISTS (
		    SELECT 1 FROM refs r WHERE r.to_qualified = s.qualified_name
		  )
		ORDER BY s.qualified_name
		LIMIT ?
	`
	rows, err := s.DB().QueryContext(ctx, sqlStr, limit)
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

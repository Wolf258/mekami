package queries

import (
	"context"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/store"
)

// ListImports returns the top-level symbols (func, type, var, const,
// method) declared in files that import the given package. Results
// are ordered by file path then start line. The synthetic __imports__
// anchor and any non-top-level symbols are excluded.
func ListImports(ctx context.Context, s *store.Store, packageID string) ([]model.SymbolWithFile, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT `+store.SymbolWithFileSelect+`
		FROM refs r
		JOIN symbols anchor ON anchor.id = r.from_symbol AND anchor.kind = '`+string(api.KindImports)+`'
		JOIN files f ON f.id = anchor.file_id
		JOIN symbols s ON s.file_id = f.id
		WHERE r.kind = '`+string(api.RefImport)+`' AND r.to_qualified = ?
		  AND s.kind != '`+string(api.KindImports)+`' AND s.parent_symbol IS NULL
		ORDER BY f.path, s.start_line`, packageID)
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

// ListImporters returns the packages in this project that import the
// given package id.
func ListImporters(ctx context.Context, s *store.Store, packageID string) ([]model.Package, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT p.id, p.module_id, p.package_id, p.name, p.dir
		FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN packages p ON p.id = s.package_id
		WHERE r.kind = '`+string(api.RefImport)+`' AND r.to_qualified = ?
		ORDER BY p.package_id`, packageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Package
	for rows.Next() {
		var p model.Package
		if err := rows.Scan(&p.ID, &p.ModuleID, &p.PackageID, &p.Name, &p.Dir); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

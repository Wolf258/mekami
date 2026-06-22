package queries

import (
	"context"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// SymbolCallers returns the distinct qualified names of symbols
// that have an incoming ref of the given kind to qn. It is the
// set of "direct callers / users" used as the first level of a
// dependents BFS. With kind="" it returns all ref kinds (call,
// type-use, value, field, embed, import).
//
// The implementation reuses RefsTo (refs.go) but projects only
// the qname — callers that need the full RefSite (with file:line)
// should use RefsTo directly. dependents only needs the node set.
func SymbolCallers(ctx context.Context, s *store.Store, qn, kind string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT sym.qualified_name
		FROM refs r
		JOIN symbols sym ON sym.id = r.from_symbol
		WHERE r.to_qualified = ?
	`+whereKind(kind),
		qn,
	)
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

// SymbolCallees is a thin alias over RefsFrom restricted to
// distinct qnames. It exists so dependents (which injects an
// Expand closure into graph.BFS) has a stable contract
// independent of RefsFrom's signature (which carries a
// pathPrefix arg).
func SymbolCallees(ctx context.Context, s *store.Store, qn, kind string) ([]string, error) {
	return RefsFrom(ctx, s, qn, "", kind, 0)
}

// PackageImporters returns the distinct package_ids of packages
// that import pkgID. Equivalent to ListImporters but projected
// to a flat []string so it fits the graph.Expand contract.
func PackageImporters(ctx context.Context, s *store.Store, pkgID string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT p.package_id
		FROM refs r
		JOIN symbols sym ON sym.id = r.from_symbol
		JOIN packages p ON p.id = sym.package_id
		WHERE r.kind = 'import' AND r.to_qualified = ?
	`, pkgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ModuleImporters returns the distinct module paths that
// contain a package importing modulePath. The query joins
// refs -> symbols -> packages -> modules so a module that
// imports the target through any of its packages is reported.
// Excludes self-imports (a module is not its own importer).
func ModuleImporters(ctx context.Context, s *store.Store, modulePath string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT importer.name
		FROM refs r
		JOIN symbols sym ON sym.id = r.from_symbol
		JOIN packages p ON p.id = sym.package_id
		JOIN modules importer ON importer.name = p.module_id
		JOIN packages target ON target.package_id = r.to_qualified
		JOIN modules target_mod ON target_mod.name = target.module_id
		WHERE r.kind = 'import'
		  AND target_mod.name = ?
		  AND importer.name != ?
	`, modulePath, modulePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// whereKind returns the SQL fragment that filters r.kind, or
// an empty string when kind is "". The fragment is built as a
// raw string because kind is a controlled enum value (call /
// type-use / value / field / embed / import) and we want the
// query to compile to a single static statement for the SQLite
// planner's benefit.
func whereKind(kind string) string {
	if kind == "" {
		return ""
	}
	return " AND r.kind = ?"
}

// (use of model import to keep the package surface stable across
// the dependents refactors even if the queries don't reference it
// directly today)
var _ = model.SymbolWithFile{}

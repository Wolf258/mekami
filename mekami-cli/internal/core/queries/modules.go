package queries

import (
	"context"
	"path/filepath"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/modlayout"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// overviewModule is an internal row used by ModuleOverview to list
// modules with their dir under last_root.
type overviewModule struct {
	Path string // module path (e.g. github.com/foo/app)
	Dir  string // relative dir to the module root inside the workspace
}

// ModuleOverview returns package summaries grouped by module. In a
// workspace (is_workspace=1) it iterates every module persisted in
// workspace_modules; otherwise it returns packages of root_module.
func ModuleOverview(ctx context.Context, s *store.Store) ([]model.ModuleSummary, error) {
	isWS, _ := s.GetMeta(ctx, store.MetaIsWorkspace)
	modules, err := listOverviewModules(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(modules) == 0 {
		return nil, nil
	}

	out := make([]model.ModuleSummary, 0, len(modules))
	for _, m := range modules {
		rows, err := s.DB().QueryContext(ctx, `
			SELECT p.package_id, p.name, p.dir, p.module_id,
			       (SELECT COUNT(DISTINCT f.id) FROM files f JOIN symbols s ON s.file_id=f.id WHERE s.package_id=p.id) AS files,
			(SELECT COUNT(*) FROM symbols s WHERE s.package_id=p.id AND s.kind != '`+string(api.KindImports)+`') AS symbols,
			       (SELECT COUNT(*) FROM symbols s WHERE s.package_id=p.id AND s.exported=1 AND s.kind != '`+string(api.KindImports)+`') AS exported
			FROM packages p
			WHERE p.module_id = ?
			ORDER BY p.package_id`, m.Path)
		if err != nil {
			return nil, err
		}
		var ps []model.PackageSummary
		for rows.Next() {
			var pkg model.PackageSummary
			if err := rows.Scan(&pkg.PackageID, &pkg.Name, &pkg.Dir, &pkg.ModuleID,
				&pkg.Files, &pkg.Symbols, &pkg.Exported); err != nil {
				rows.Close()
				return nil, err
			}
			ps = append(ps, pkg)
		}
		rows.Close()
		if len(ps) == 0 && isWS != "1" {
			// Single-module: skip empty entries to avoid noise.
			continue
		}
		out = append(out, model.ModuleSummary{
			ModuleID: m.Path,
			Dir:      m.Dir,
			Packages: ps,
		})
	}
	return out, nil
}

func listOverviewModules(ctx context.Context, s *store.Store) ([]overviewModule, error) {
	isWS, _ := s.GetMeta(ctx, store.MetaIsWorkspace)
	if isWS == "1" {
		lastRoot, _ := s.GetMeta(ctx, store.MetaLastRoot)
		raw, err := s.GetMeta(ctx, store.MetaWorkspaceMods)
		if err != nil || raw == "" || lastRoot == "" {
			return nil, nil
		}
		entries := modlayout.ParseModuleEntries(raw)
		out := make([]overviewModule, 0, len(entries))
		for _, e := range entries {
			out = append(out, overviewModule{Path: resolveModulePath(e, lastRoot), Dir: e.Dir})
		}
		return out, nil
	}
	// Single module: just use root_module.
	rootModule, err := s.GetMeta(ctx, store.MetaRootModule)
	if err != nil || rootModule == "" {
		// Fallback: pick the module with the most packages.
		row := s.DB().QueryRowContext(ctx,
			`SELECT p.module_id FROM packages p GROUP BY p.module_id ORDER BY COUNT(DISTINCT p.id) DESC LIMIT 1`)
		var rm string
		if err := row.Scan(&rm); err != nil {
			return nil, nil
		}
		rootModule = rm
	}
	if rootModule == "" {
		return nil, nil
	}
	return []overviewModule{{Path: rootModule}}, nil
}

// resolveModulePath returns e.Path when set, otherwise falls
// back to the dir. The on-disk go.mod read was removed when
// the core became language-agnostic; legacy builds that
// persisted only the dir in workspace_modules therefore
// surface the dir as the module path until a full rebuild
// re-stamps the Path field.
func resolveModulePath(e modlayout.ModuleEntry, lastRoot string) string {
	if e.Path != "" {
		return e.Path
	}
	if e.Dir == "" {
		return ""
	}
	if lastRoot == "" {
		return e.Dir
	}
	return filepath.Join(lastRoot, e.Dir)
}

// ListModules returns the modules indexed in the current graph.
func ListModules(ctx context.Context, s *store.Store) ([]model.ModuleInfo, error) {
	isWS, _ := s.GetMeta(ctx, store.MetaIsWorkspace)
	if isWS == "1" {
		raw, err := s.GetMeta(ctx, store.MetaWorkspaceMods)
		if err != nil || raw == "" {
			return nil, nil
		}
		lastRoot, _ := s.GetMeta(ctx, store.MetaLastRoot)
		primary, _ := s.GetMeta(ctx, store.MetaPrimaryModule)
		entries := modlayout.ParseModuleEntries(raw)
		var out []model.ModuleInfo
		for _, e := range entries {
			path := resolveModulePath(e, lastRoot)
			out = append(out, model.ModuleInfo{
				Path:        path,
				Dir:         e.Dir,
				IsWorkspace: true,
				Primary:     path == primary,
			})
		}
		return out, nil
	}
	rootModule, _ := s.GetMeta(ctx, store.MetaRootModule)
	if rootModule == "" {
		return nil, nil
	}
	return []model.ModuleInfo{{Path: rootModule, IsWorkspace: false, Primary: true}}, nil
}

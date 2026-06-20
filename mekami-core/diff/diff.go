package diff

import (
	"context"
	"path/filepath"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/modlayout"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-core/walk"
)

// SinceLastBuild walks the file system under `root` and compares to
// the indexed snapshot, returning Added / Modified / Removed /
// Inaccessible buckets. Inaccessible holds files that disappeared or
// errored during the walk; they also appear in Modified when their
// previous hash was non-empty.
//
// The workspace layout is read from the previous build's meta
// (workspace_mods / is_workspace) so the same logic applies
// whether or not a frontend is registered in the current
// process. This keeps the diff tool language-agnostic.
func SinceLastBuild(ctx context.Context, s *store.Store, root string) (model.FileDiff, error) {
	var diff model.FileDiff
	prev, err := queries.AllFiles(ctx, s)
	if err != nil {
		return diff, err
	}
	prevByPath := map[string]model.File{}
	for _, f := range prev {
		prevByPath[f.Path] = f
	}

	ws := workspaceFromMeta(ctx, s, root)
	absRoot, _ := filepath.Abs(root)
	rootIsWorkspaceRoot := ws.IsWorkspace && modlayout.SamePath(ws.WorkspaceDir, absRoot)

	seen := map[string]bool{}
	wsPtr := ws
	err = walk.GoFiles(root, wsPtr, rootIsWorkspaceRoot, func(rel string) error {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		hash, _, _, ferr := walk.Fingerprint(abs)
		if ferr != nil {
			diff.Inaccessible = append(diff.Inaccessible, rel)
			prevF, ok := prevByPath[rel]
			if ok && prevF.Hash != "" {
				diff.Modified = append(diff.Modified, rel)
			}
			return nil
		}
		seen[rel] = true
		prevF, ok := prevByPath[rel]
		if !ok {
			diff.Added = append(diff.Added, rel)
			return nil
		}
		if prevF.Hash != hash {
			diff.Modified = append(diff.Modified, rel)
		}
		return nil
	})
	if err != nil {
		return diff, err
	}
	for _, f := range prev {
		if !seen[f.Path] {
			diff.Removed = append(diff.Removed, f.Path)
		}
	}
	return diff, nil
}

// workspaceFromMeta rebuilds an api.Workspace from the values
// the previous build stamped into the meta table. It is a
// best-effort reconstruction: missing keys yield a zero-value
// workspace, which diff treats as a single-module repo. The
// fallback path lets the diff tool work without a registered
// frontend in the current process (e.g. when mekami is run
// from a checkout that has not yet built mekami-core-go).
func workspaceFromMeta(ctx context.Context, s *store.Store, root string) *api.Workspace {
	ws := &api.Workspace{WorkspaceDir: root}
	isWS, err := s.GetMeta(ctx, store.MetaIsWorkspace)
	if err != nil || isWS != "1" {
		return ws
	}
	ws.IsWorkspace = true
	if raw, err := s.GetMeta(ctx, store.MetaWorkspaceMods); err == nil {
		for _, line := range splitLines(raw) {
			if line == "" {
				continue
			}
			ws.WorkspaceMods = append(ws.WorkspaceMods, filepath.Join(root, line))
		}
	}
	if mp, err := s.GetMeta(ctx, store.MetaPrimaryModule); err == nil {
		ws.PrimaryModPath = mp
	}
	return ws
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}


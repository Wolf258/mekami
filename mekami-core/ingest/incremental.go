package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/modlayout"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-core/walk"
)

// IsStructural reports whether a relative path corresponds to a file
// whose change invalidates the index beyond what an incremental
// re-ingest can repair. The check is delegated to the registered
// frontends (Go: go.mod / go.sum / go.work; Rust: Cargo.toml; etc.).
//
// Exported so the watcher can make the same decision before it
// decides to call BuildIncremental vs. fall back to a full Build.
func IsStructural(rel string) bool {
	return api.IsStructural(rel)
}

// BuildIncremental re-indexes the supplied set of files without
// re-walking the entire tree. It is the workhorse behind `mekami
// watch`: when the FS reports a change, the watcher passes the
// affected paths here.
//
// Behaviour:
//   - paths is treated as a set of "touched" files. Each path is
//     classified as Added (not in prev), Modified (hash changed),
//     Unchanged (hash matches prev), or Removed (no longer on disk).
//   - For Added/Modified: the file is fingerprinted and, if its hash
//     matches prev, the work is skipped; otherwise it is parsed and
//     written (overwriting the previous symbols/refs for that file).
//   - For Removed: the file row is deleted (CASCADE removes its
//     symbols and refs).
//   - If the supplied set touches a structural file or any path is
//     not in a language the active frontend can ingest, the function
//     returns ErrStructuralChange. The caller should then fall back
//     to a full Build.
//   - If the DB has no last_root, returns ErrNoLastRoot. The caller
//     should run a full Build first.
//
// BuildIncremental is safe to call after Build has run on the same
// root. It reuses the same prepared context so meta values stay in
// sync with the latest successful build.
func BuildIncremental(ctx context.Context, opts BuildOptions, paths []string) (BuildStats, error) {
	stats := BuildStats{Mode: "incremental"}
	start := time.Now()

	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if opts.DBPath == "" {
		return stats, fmt.Errorf("ingest.BuildIncremental: DBPath is required")
	}
	for _, p := range paths {
		if IsStructural(filepath.ToSlash(p)) {
			stats.StructuralFull = true
			return stats, ErrStructuralChange
		}
	}

	s, err := store.Open(opts.DBPath)
	if err != nil {
		return stats, err
	}
	defer s.Close()

	prevRoot, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		return stats, ErrNoLastRoot
	}
	if opts.Root == "" {
		opts.Root = prevRoot
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return stats, fmt.Errorf("abs root: %w", err)
	}
	if !modlayout.SamePath(prevRoot, absRoot) {
		return stats, fmt.Errorf("last_root is %q, requested %q; run a full build", prevRoot, absRoot)
	}
	opts.Root = absRoot
	if err := s.SetMeta(ctx, store.MetaLastBuildAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return stats, err
	}

	if opts.Lang == "" {
		opts.Lang = "go"
	}
	fe, err := api.Get(opts.Lang)
	if err != nil {
		return stats, err
	}

	ws, err := fe.ResolveLayout(opts.Root)
	if err != nil {
		return stats, fmt.Errorf("workspace: %w", err)
	}
	rootIsWorkspaceRoot := ws.IsWorkspace && modlayout.SamePath(ws.WorkspaceDir, opts.Root)

	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		p = filepath.ToSlash(filepath.Clean(p))
		// Reject paths that escape the root or contain traversal.
		if strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
			return stats, fmt.Errorf("incremental: path %q escapes root", p)
		}
		if !hasExt(p, fe.Extensions()) {
			stats.StructuralFull = true
			return stats, fmt.Errorf("incremental: path %q is not handled by language %q; run a full build", p, fe.Name())
		}
		if !fe.IsIndexable(p) {
			// Same-package test files, build artifacts, etc. The
			// build walker would have skipped them; we silently
			// drop the event so editors that touch a _test.go
			// don't trigger a rebuild.
			continue
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		stats.Duration = time.Since(start)
		return stats, nil
	}
	sort.Strings(cleaned)

	_, prevByPath, err := loadPrevFiles(ctx, s)
	if err != nil {
		return stats, err
	}

	for _, p := range cleaned {
		if !isIndexablePath(opts.Root, ws, rootIsWorkspaceRoot, p) {
			return stats, fmt.Errorf("incremental: path %q is not under any indexable directory", p)
		}
	}

	tx, err := s.Begin(ctx)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	beforeStats, err := queries.Stats(ctx, s)
	if err != nil {
		return stats, err
	}

	prog := NewProgress(ctx, os.Stderr, opts.Quiet)
	defer prog.Done()

	toIngest := make([]string, 0, len(cleaned))
	for _, p := range cleaned {
		prev, known := prevByPath[p]
		abs := filepath.Join(opts.Root, filepath.FromSlash(p))
		hash, _, _, ferr := walk.Fingerprint(abs)
		if ferr != nil {
			if !known {
				prog.Event("skip", fmt.Sprintf("%s: %v", p, ferr))
				continue
			}
			if prev.Hash == "" {
				continue
			}
			prog.Event("delete", p)
			if _, _, err := tx.RemoveFileByPath(p); err != nil {
				return stats, fmt.Errorf("remove %s: %w", p, err)
			}
			stats.FilesRemoved++
			continue
		}
		if known && prev.Hash == hash && prev.Lang == fe.Name() {
			continue
		}
		toIngest = append(toIngest, p)
	}

	if len(toIngest) > 0 {
		prevSubset := make(map[string]model.File, len(toIngest))
		for _, p := range toIngest {
			if f, ok := prevByPath[p]; ok {
				prevSubset[p] = f
			}
		}
		if err := ingestFilesParallel(ctx, tx, opts.Root, toIngest, prevSubset, fe, prog, &stats, opts.Jobs); err != nil {
			return stats, err
		}
	}

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	if err := tx.Commit(); err != nil {
		return stats, err
	}

	afterStats, err := queries.Stats(ctx, s)
	if err != nil {
		return stats, err
	}
	stats.FilesScanned = len(cleaned)
	stats.SymbolsAdded = afterStats["symbols"] - beforeStats["symbols"]
	stats.RefsAdded = afterStats["refs"] - beforeStats["refs"]
	stats.Duration = time.Since(start)
	return stats, nil
}

// ErrStructuralChange is returned by BuildIncremental when the
// supplied set of paths touches a structural file or a non-handled
// path. Callers should treat it as a signal to fall back to Build.
var ErrStructuralChange = fmt.Errorf("structural change; full rebuild required")

// ErrNoLastRoot is returned by BuildIncremental when the DB has not
// been built yet. Callers should run a full Build first. Aliased to
// the canonical error in package store so all call sites can match
// it with a single errors.Is check.
var ErrNoLastRoot = store.ErrNoLastRoot

// hasExt reports whether `path` ends in any of the supplied
// extensions. Case-insensitive. Empty exts matches everything.
func hasExt(path string, exts []string) bool {
	lower := strings.ToLower(path)
	for _, e := range exts {
		if e == "" {
			return true
		}
		if strings.HasSuffix(lower, strings.ToLower(e)) {
			return true
		}
	}
	return false
}

// isIndexablePath checks that a relative file path is inside a
// directory the walker would have visited. It mirrors the
// exclusion rules in walk.anyFile: .git, .mekami, node_modules,
// vendor, and _dev are skipped.
//
// Sibling modules of a workspace are only excluded when the build
// root is not the workspace root: in a workspace-root build every
// `use`d module is indexable, so a path inside any of them should
// pass the check.
func isIndexablePath(root string, ws *api.Workspace, rootIsWorkspaceRoot bool, rel string) bool {
	abs, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	cur := filepath.Dir(abs)
	for {
		base := filepath.Base(cur)
		switch base {
		case ".git", ".mekami", "node_modules", "vendor", "_dev":
			return false
		}
		if ws != nil && ws.IsWorkspace && !rootIsWorkspaceRoot {
			if modlayout.IsAPIModuleRoot(ws, cur) {
				return false
			}
		}
		if cur == root {
			return true
		}
		if cur == filepath.Dir(cur) {
			return true
		}
		cur = filepath.Dir(cur)
	}
}

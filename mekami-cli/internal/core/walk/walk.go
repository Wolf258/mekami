// Package walk implements the filesystem walker the build pipeline
// uses to enumerate source files, plus the file-fingerprint helper
// that lets the build skip unchanged files cheaply.
//
// The walker respects a fixed set of exclusions: .git, .mekami,
// node_modules, vendor, _dev, plus sibling modules of a workspace
// when the build root is a sub-module. A workspace-root build
// indexes every use'd module.
package walk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/modlayout"
)

// extSet is a small lookup helper. Keeping it as a map (rather
// than a slice scan) is a micro-optimisation but the build
// walker is the hot loop and walking thousands of files per
// second is normal.
type extSet map[string]struct{}

func (e extSet) has(ext string) bool {
	if e == nil {
		return false
	}
	_, ok := e[ext]
	return ok
}

// MatchingFiles walks `root` and invokes `visit` for every
// regular file whose extension is in `exts`. The same exclusion
// rules apply as the legacy GoFiles walker:
//   - skip hidden/build directories: .git, .mekami, node_modules,
//     vendor, _dev
//   - when `root` is a single module inside a workspace, skip
//     sibling modules so we don't cross-contaminate the graph
//
// The path passed to `visit` is relative to `root` and uses
// forward slashes. Returning an error from `visit` aborts the
// walk. A walk error on a single entry (e.g. permission denied)
// is treated as skip-this-entry: the walk continues so one
// unreadable subdirectory does not abort the entire build.
func MatchingFiles(root string, ws *api.Workspace, rootIsWorkspaceRoot bool, exts []string, visit func(relPath string) error) error {
	set := extSet{}
	for _, e := range exts {
		set[strings.ToLower(e)] = struct{}{}
	}
	return anyFile(root, ws, rootIsWorkspaceRoot, func(path string, d os.DirEntry) (bool, error) {
		ext := strings.ToLower(filepath.Ext(path))
		return set.has(ext), nil
	}, visit)
}

// GoFiles walks `root` and invokes `visit` for every Go source
// file that should be indexed. It is a thin wrapper around
// MatchingFiles kept for backwards-compat with callers that
// hardcode the Go extension. New code should call MatchingFiles
// with the extension list obtained from a frontend.
func GoFiles(root string, ws *api.Workspace, rootIsWorkspaceRoot bool, visit func(relPath string) error) error {
	return MatchingFiles(root, ws, rootIsWorkspaceRoot, []string{".go"}, visit)
}

// AnyFile walks `root` and invokes `visit` for every regular
// file (any extension) that survives the same exclusion rules
// as GoFiles. Used by the grep tool, which needs to search
// .md/.txt/.yaml in addition to .go.
//
// Returning an error from `visit` aborts the walk. Per-entry
// errors (permission denied, broken symlink) are reported on
// stderr and the walk continues so one bad subdirectory does
// not abort the search.
func AnyFile(root string, ws *api.Workspace, rootIsWorkspaceRoot bool, visit func(relPath string) error) error {
	return anyFile(root, ws, rootIsWorkspaceRoot, func(path string, d os.DirEntry) (bool, error) {
		return true, nil
	}, visit)
}

func anyFile(
	root string,
	ws *api.Workspace,
	rootIsWorkspaceRoot bool,
	accept func(path string, d os.DirEntry) (bool, error),
	visit func(relPath string) error,
) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "mekami: walk %s: %v\n", path, walkErr)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root {
				if name == ".git" || name == ".mekami" || name == "node_modules" ||
					name == "vendor" || name == "_dev" {
					return filepath.SkipDir
				}
				if ws.IsWorkspace && !rootIsWorkspaceRoot && modlayout.IsAPIModuleRoot(ws, path) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ok, err := accept(path, d)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("rel: %w", err)
		}
		return visit(filepath.ToSlash(rel))
	})
}

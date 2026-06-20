// Package modlayout holds the language-agnostic data shapes and
// helpers for working with multi-module project layouts.
//
// Mekami indexes languages that have the concept of a workspace
// (Go: go.work, Rust: Cargo workspace, Bazel: WORKSPACE). The
// Workspace struct here is the shape every frontend returns from
// ResolveLayout; the language-specific parsing of the manifest
// file lives in the per-language core (e.g. mekami-core-go).
//
// ModuleEntry is the persisted form of a workspace module. The
// core stores one JSON-encoded ModuleEntry per line in the
// workspace_modules meta key and reads it back through
// ParseModuleEntries. ResolveModuleList is the legacy
// compatibility helper that mixes JSON entries with plain-dir
// entries and resolves a module path on demand via a
// caller-supplied resolver (so the package stays free of
// language-specific code).
package modlayout

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
)

// ModuleEntry is the persisted form of a single workspace
// module. See package doc for the on-disk format.
type ModuleEntry struct {
	Dir  string `json:"dir"`
	Path string `json:"path"`
}

// Workspace describes a multi-module layout. IsWorkspace=false
// means the build root is a single module; WorkspaceMods is
// empty in that case. When IsWorkspace is true, WorkspaceMods
// holds the absolute paths of every "use"d / "member" module
// and PrimaryModPath / PrimaryModuleDir identify the primary
// one (used to stamp the root_module meta key).
type Workspace struct {
	IsWorkspace      bool
	WorkFile         string
	WorkspaceDir     string
	WorkspaceMods    []string
	PrimaryModPath   string
	PrimaryModuleDir string
}

// IsModuleRoot reports whether absPath is itself a workspace
// module directory (a module root, not a subdir). Returns false
// when the workspace is not configured.
func (w *Workspace) IsModuleRoot(absPath string) bool {
	if w == nil || !w.IsWorkspace {
		return false
	}
	for _, modDir := range w.WorkspaceMods {
		if SamePath(modDir, absPath) {
			return true
		}
	}
	return false
}

// IsAPIModuleRoot is the equivalent of Workspace.IsModuleRoot
// for an *api.Workspace. The walk package uses this so the
// public api.Workspace type can be passed around without the
// core translating to its internal modlayout.Workspace first.
func IsAPIModuleRoot(w *api.Workspace, absPath string) bool {
	if w == nil || !w.IsWorkspace {
		return false
	}
	for _, modDir := range w.WorkspaceMods {
		if SamePath(modDir, absPath) {
			return true
		}
	}
	return false
}

// IsAncestor reports whether ancestor is a directory ancestor of
// descendant (both absolute paths). The two paths are cleaned
// and compared with a trailing separator to avoid prefix
// collisions (e.g. /foo/bar vs /foo/barbaz).
func IsAncestor(ancestor, descendant string) bool {
	ancestor = filepath.Clean(ancestor) + string(filepath.Separator)
	d := filepath.Clean(descendant) + string(filepath.Separator)
	return len(d) > len(ancestor) && d[:len(ancestor)] == ancestor
}

// SamePath reports whether two paths refer to the same location
// (after filepath.Clean and filepath.Abs). Tolerates unresolved
// relative inputs by Abs-ing them; on error falls back to the
// cleaned string.
func SamePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return filepath.Clean(a) == filepath.Clean(b)
}

// ParseModuleEntries decodes the workspace_modules meta. Newer
// builds emit JSON lines ("{...}"); older builds (or modules
// without a resolvable module path) used the plain-dir form.
// Each line is decoded independently: a JSON line that fails to
// parse, or a path that is not JSON, falls back per-line to the
// plain-dir form. A failure in one line never poisons the
// others.
func ParseModuleEntries(raw string) []ModuleEntry {
	var entries []ModuleEntry
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var e ModuleEntry
			if err := json.Unmarshal([]byte(line), &e); err == nil && e.Dir != "" {
				entries = append(entries, e)
				continue
			}
		}
		entries = append(entries, ModuleEntry{Dir: line})
	}
	return entries
}

// ResolveModuleList walks the raw workspace_modules meta plus
// the lastRoot path and produces a slice of (path, dir) for
// every resolvable module. Entries whose module path cannot be
// resolved are dropped. The resolve callback is the
// language-specific module-path resolver (e.g. reads go.mod for
// Go, Cargo.toml for Rust) and is supplied by the caller so
// this package stays language-agnostic.
func ResolveModuleList(raw, lastRoot string, resolve func(lastRoot, dir string) (string, error)) []ModuleEntry {
	entries := ParseModuleEntries(raw)
	out := make([]ModuleEntry, 0, len(entries))
	for _, e := range entries {
		mp := e.Path
		if mp == "" {
			resolved, err := resolve(lastRoot, e.Dir)
			if err != nil || resolved == "" {
				continue
			}
			mp = resolved
		}
		out = append(out, ModuleEntry{Dir: e.Dir, Path: mp})
	}
	return out
}

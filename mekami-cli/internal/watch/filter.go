package watch

import (
	"path/filepath"
	"strings"
)

// Filter decides whether a relative path emitted by the FS layer
// should be passed to the build pipeline. The base rules mirror the
// build walker: hidden/build directories and _test.go files are
// dropped because the walker does not index them, so changes to
// them cannot affect the graph.
//
// A user can layer additional globs (relative to root) on top via
// IgnorePatterns. Matches are evaluated with filepath.Match against
// the base name, not the full path, so a pattern like "*.swp" hits
// any "foo.swp" regardless of its directory.
type Filter struct {
	// IgnorePatterns are additional basenames to drop (e.g.
	// "*.tmp"). Empty entries are ignored. Evaluated with
	// filepath.Match against filepath.Base(path).
	IgnorePatterns []string
}

// DefaultFilter returns a Filter with the same exclusions the
// build walker uses for directories, plus the conventional editor
// noise patterns.
func DefaultFilter() *Filter {
	return &Filter{
		IgnorePatterns: []string{
			"*.tmp",
			"*.swp",
			"*.swo",
			".DS_Store",
			"*~",
		},
	}
}

// Accept reports whether path (relative to root, forward slashes)
// should be passed to the build pipeline. The full set of rules:
//   - hidden/build directories: .git, .mekami, node_modules,
//     vendor, _dev. A path inside any of them is rejected, even
//     if the basename is a real Go file. This matches the walker's
//     SkipDir behaviour.
//   - non-Go files, EXCEPT for structural files (go.mod, go.work,
//     go.sum) which the watcher must surface so the handler can
//     promote the batch to a full rebuild.
//   - test files (*_test.go) — the build walker skips these.
//   - any basename matching an entry in IgnorePatterns.
//
// Accept is pure: no FS access, no allocation beyond filepath.Match
// internals. It is safe to call from the hot path of the event loop.
func (f *Filter) Accept(rel string) bool {
	if rel == "" {
		return false
	}
	// Normalise so callers can pass either form.
	rel = filepath.ToSlash(rel)
	// Directory check: split the path and reject if any segment
	// is a hidden/build directory. This matches the walker's
	// behaviour where a single bad ancestor excludes the whole
	// subtree.
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			continue
		}
		switch seg {
		case ".git", ".mekami", "node_modules", "vendor", "_dev":
			return false
		}
	}
	base := filepath.Base(rel)
	// Structural files: the watcher needs to see these so it can
	// promote the batch to a full rebuild. They are not Go files
	// but they are first-class events.
	if base == "go.mod" || base == "go.work" || base == "go.sum" {
		// Still honour user ignore patterns.
		for _, pattern := range f.IgnorePatterns {
			if pattern == "" {
				continue
			}
			ok, err := filepath.Match(pattern, base)
			if err == nil && ok {
				return false
			}
		}
		return true
	}
	// Non-Go files: the build walker only indexes .go. Anything
	// else (md, txt, yml, etc.) is structurally irrelevant.
	if !strings.HasSuffix(base, ".go") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	// User-supplied ignore patterns. We match against the basename
	// only, so a single pattern covers every directory. This is
	// the same convention ripgrep and gitignore use for "bare"
	// patterns.
	for _, pattern := range f.IgnorePatterns {
		if pattern == "" {
			continue
		}
		ok, err := filepath.Match(pattern, base)
		if err == nil && ok {
			return false
		}
	}
	return true
}

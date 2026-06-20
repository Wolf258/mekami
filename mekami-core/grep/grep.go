// Package grep implements a server-side text search over the indexed
// source tree. Unlike find_symbol and friends, which only know
// about symbol names and ref edges, find_text reads file contents
// directly off disk and matches a regex.
//
// This is the fallback the LLM is told to use when the indexed
// information is not enough — substring search inside function
// bodies, comments, log strings, or any arbitrary text.
package grep

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/modlayout"
	"github.com/Wolf258/mekami-core/walk"
)

// Match is one regex hit. Line is 1-based.
type Match struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// Result is the response payload of Grep. The match list is truncated
// to the caller's MaxResults; the truncated field tells the LLM
// whether to re-query with a tighter scope.
type Result struct {
	Pattern   string  `json:"pattern"`
	Root      string  `json:"root"`
	Truncated bool    `json:"truncated"`
	Total     int     `json:"total"`
	Matches   []Match `json:"matches"`
}

// Options configures a Grep call. Zero values are sensible: any
// extension is allowed, all matches returned (capped by the caller's
// MaxResults), no context lines.
type Options struct {
	Pattern     string         // required, regex
	Root        string         // required, source root (last_root from meta)
	PathPrefix  string         // optional, restrict to files whose path starts with this
	IncludeExt  []string       // optional, e.g. []string{"go"}; if empty, all extensions allowed
	MaxResults  int            // cap on number of matches; 0 means 200
	Context     int            // number of context lines to attach to each match (0-5)
	Compiled    *regexp.Regexp // optional, pre-compiled; avoids recompiling per call
}

// Grep walks `root` (respecting the standard Mekami ignore rules),
// matches files against the regex, and returns the first N hits.
// When MaxResults is exceeded, Grep stops early and sets
// Result.Truncated.
func Grep(ctx context.Context, opts Options) (*Result, error) {
	if opts.Pattern == "" {
		return nil, fmt.Errorf("grep: pattern is required")
	}
	if opts.Root == "" {
		return nil, fmt.Errorf("grep: root is required")
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 200
	}
	if opts.Context < 0 {
		opts.Context = 0
	}
	if opts.Context > 5 {
		opts.Context = 5
	}
	re := opts.Compiled
	if re == nil {
		var err error
		re, err = regexp.Compile(opts.Pattern)
		if err != nil {
			return nil, fmt.Errorf("grep: invalid pattern: %w", err)
		}
	}

	extSet := map[string]bool{}
	for _, e := range opts.IncludeExt {
		if e == "" {
			continue
		}
		extSet[strings.ToLower(strings.TrimPrefix(e, "."))] = true
	}

	prefix := strings.TrimPrefix(opts.PathPrefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	// The walker is .go-only by design, so we cannot reuse it for grep
	// (we want md, txt, go, etc.). Reuse the same ignore rules but
	// walk every regular file. We still skip the same build/hidden
	// directories the build walker skips. Without a registered
	// frontend we default to a single-module workspace, which is
	// the common case for ad-hoc greps.
	ws := defaultWorkspace(opts.Root)
	absRoot, _ := filepath.Abs(opts.Root)
	rootIsWorkspaceRoot := ws.IsWorkspace && modlayout.SamePath(ws.WorkspaceDir, absRoot)

	result := &Result{
		Pattern: opts.Pattern,
		Root:    opts.Root,
	}
	limit := opts.MaxResults + 1 // +1 lets us detect truncation cheaply

	wsPtr := ws
	walkErr := walk.AnyFile(opts.Root, wsPtr, rootIsWorkspaceRoot, func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if prefix != "" && !strings.HasPrefix(rel, prefix) {
			return nil
		}
		if len(extSet) > 0 {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
			if !extSet[ext] {
				return nil
			}
		}
		abs := filepath.Join(opts.Root, filepath.FromSlash(rel))
		hits, total, err := scanFile(abs, rel, re, opts.Context, limit-result.Total)
		if err != nil {
			// Unreadable file: skip silently rather than aborting
			// the whole grep, mirroring the build walker's policy.
			return nil
		}
		result.Total += total
		result.Matches = append(result.Matches, hits...)
		if result.Total >= limit {
			return errStop
		}
		return nil
	})
	if walkErr != nil && walkErr != errStop {
		return nil, walkErr
	}
	if result.Total > opts.MaxResults {
		result.Truncated = true
		if len(result.Matches) > opts.MaxResults {
			result.Matches = result.Matches[:opts.MaxResults]
		}
	}
	return result, nil
}

// errStop is a sentinel returned from the walk visitor to abort the
// walk once enough matches have been collected.
var errStop = fmt.Errorf("grep: stop")

// scanFile reads `abs` line by line and returns every match. If
// `remaining` is non-positive, the scan stops as soon as the first
// match is found (used to short-circuit on full result sets).
func scanFile(abs, rel string, re *regexp.Regexp, ctxLines int, remaining int) ([]Match, int, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow long lines (Go source can have them). 1 MiB matches
	// bufio's max-token-size convention; 4 MiB covers pathological
	// generated files.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var matches []Match
	matchCount := 0
	// Rolling window: at most ctxLines of recent non-matching lines,
	// emitted as context just before the next match.
	window := make([]string, 0, ctxLines)
	windowStart := 0
	lineNo := 0

	emit := func(line string) bool {
		if remaining <= 0 {
			return false
		}
		// Emit the rolling window as context, then the match.
		for i, w := range window {
			matches = append(matches, Match{
				Path:    rel,
				Line:    windowStart + i,
				Content: w,
			})
		}
		window = window[:0]
		windowStart = 0
		matches = append(matches, Match{Path: rel, Line: lineNo, Content: line})
		remaining--
		return true
	}

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++

		if re.MatchString(line) {
			matchCount++
			if !emit(line) {
				return matches, matchCount, nil
			}
			continue
		}
		// Track for context: only meaningful if ctxLines > 0.
		if ctxLines > 0 {
			if len(window) == 0 {
				windowStart = lineNo
			}
			if len(window) == ctxLines {
				// Slide: drop the oldest.
				window = window[1:]
				windowStart++
			}
			window = append(window, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return matches, matchCount, err
	}
	return matches, matchCount, nil
}

// defaultWorkspace returns a single-module api.Workspace for
// the given root. The grep tool runs without a registered
// frontend (it does not need parsing), so the workspace concept
// is collapsed to a no-op.
func defaultWorkspace(root string) *api.Workspace {
	return &api.Workspace{WorkspaceDir: root}
}

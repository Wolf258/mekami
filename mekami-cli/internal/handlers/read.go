// Package handlers implements the read-side graph operations that
// back both the CLI subcommands and the MCP tools. The functions
// take an ArgMap (named-naming spec) and a context, return a value
// suitable for either stdout (via format.JSON) or MCP text content.
//
// Keeping the implementations here means the CLI and the MCP server
// share the same code path: the cobra runner decodes flags into an
// ArgMap and the MCP SDK decodes JSON-RPC params into one, and
// both call the same function.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/core/diff"
	"github.com/Wolf258/mekami-cli/internal/core/grep"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/path"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/naming"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolResult wraps v in an MCP text-content result. Mirrors the
// helper that used to live in internal/mcp.
func ToolResult(v any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: format.JSON(v)}},
	}
}

// SourceSourceError is the human-readable form of a store-level
// "no last_root" / file-not-found error. The MCP layer surfaces it
// as a text result so the LLM can self-correct; the CLI prints it
// to stderr and exits with code 2.
func SourceError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, store.ErrNoLastRoot) {
		return err.Error()
	}
	return "error: " + err.Error()
}

// defaultHead is the default value of --head when the caller omits
// the flag. Matches the description in naming.Specs.
const defaultHead = 30

// capFor returns a format.Cap from args["head"]. head=0 disables
// the cap (Shown==Total, Truncated=false). The hint is looked up
// from the kind so a downstream formatter can render the footer.
func capFor(total int, args naming.ArgMap, kind format.ListKind) format.Cap {
	head := args.GetInt("head", defaultHead)
	if head < 0 {
		head = 0
	}
	if head == 0 || total <= head {
		return format.Cap{Total: total, Shown: total, Truncated: false, Hint: format.HintFor(kind)}
	}
	return format.Cap{Total: total, Shown: head, Truncated: true, Hint: format.HintFor(kind)}
}

// listPayload wraps a list-shaped result with its truncation
// metadata. The JSON shape is { items: [...], cap: {...} }.
//
// When the cap is uninteresting (nothing was truncated) the
// caller returns the plain slice so the historical JSON shape
// (a bare list) is preserved for clients that already parse
// the response as a list.
type listPayload struct {
	Items any        `json:"items"`
	Cap   format.Cap `json:"cap"`
}

func payloadOrString(items any, cap format.Cap) any {
	if !cap.Truncated {
		return items
	}
	return listPayload{Items: applyCapToSlice(items, cap.Shown), Cap: cap}
}

// applyCapToSlice truncates a slice-shaped `items` to the first n
// entries when n is positive and the underlying value is a slice.
// Non-slice inputs are returned unchanged (defensive: callers
// should pass a slice). Used by payloadOrString to drop the tail
// of the result before wrapping it in listPayload.
//
// The function works via reflect so it does not depend on the
// concrete element type of each handler. For an unrecognized
// shape (e.g. *model.FileNode) the caller is expected to have
// already truncated; we return the original.
func applyCapToSlice(items any, n int) any {
	if n <= 0 {
		return items
	}
	v := reflect.ValueOf(items)
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return items
	}
	if v.Len() <= n {
		return items
	}
	return v.Slice(0, n).Interface()
}

// FindSymbol returns the symbol search results.
func FindSymbol(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	q := args.GetString("query", "")
	kind := args.GetString("kind", "")
	prefix := args.GetString("path_prefix", "")
	limit := args.GetInt("limit", 200)
	syms, err := queries.SearchSymbols(ctx, s, q, kind, prefix, limit)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(syms), args, format.KindSymbols)
	return payloadOrString(syms, cap), nil
}

// GetSymbol returns a symbol's source. With body=false (the default)
// it returns the header block. With body=true it returns the
// numbered body. Callers that want header+body should use the CLI
// `show` command, which composes them client-side; the MCP tool
// keeps the header-only default to match the historical get_symbol
// shape.
func GetSymbol(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	qn := args.GetString("qualified_name", "")
	syms, err := queries.SymbolByQName(ctx, s, qn)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("no symbol found for %q", qn), nil
	}
	body := args.GetBool("body", false)
	maxLines := args.GetInt("max_lines", 200)
	if !body {
		// Default and header-only path: the header block.
		return format.Symbol(syms), nil
	}
	// body=true: numbered body, with max_lines cap. Use the first
	// matching symbol (qualified names are unique per definition).
	sym := syms[0]
	lines, err := queries.SourceSlice(ctx, s, sym.FilePath, sym.StartLine, sym.EndLine, maxLines)
	if err != nil {
		return nil, err
	}
	return format.SymbolBody(sym, lines, maxLines), nil
}

// ShowBody returns just the numbered body.
func ShowBody(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	qn := args.GetString("qualified_name", "")
	maxLines := args.GetInt("max_lines", 200)
	syms, err := queries.SymbolByQName(ctx, s, qn)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("no symbol found for %q", qn), nil
	}
	sym := syms[0]
	lines, err := queries.SourceSlice(ctx, s, sym.FilePath, sym.StartLine, sym.EndLine, maxLines)
	if err != nil {
		return nil, err
	}
	return format.SymbolBody(sym, lines, maxLines), nil
}

// ShowLines returns a range of lines from a file.
func ShowLines(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	path := args.GetString("path", "")
	startLine := args.GetInt("start_line", 0)
	endLine := args.GetInt("end_line", 0)
	maxLines := args.GetInt("max_lines", 200)
	if startLine < 1 {
		return "start_line must be >= 1", nil
	}
	end := endLine
	if end <= 0 {
		end = startLine + 100
	}
	if end < startLine {
		return "end_line must be >= start_line", nil
	}
	lines, err := queries.SourceSlice(ctx, s, path, startLine, end, maxLines)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return fmt.Sprintf("no content in %s:%d-%d (file may be shorter than the requested range)", path, startLine, end), nil
	}
	return format.FileRange(path, startLine, end, lines, maxLines), nil
}

// WhoCalls returns incoming references to a symbol.
func WhoCalls(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	qn := args.GetString("qualified_name", "")
	refKind := args.GetString("ref_kind", "")
	prefix := args.GetString("path_prefix", "")
	limit := args.GetInt("limit", 200)
	refs, err := queries.RefsTo(ctx, s, qn, refKind, prefix, limit)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(refs), args, format.KindRefs)
	if !cap.Truncated {
		return format.RefsTo(qn, refs, cap), nil
	}
	return payloadOrString(refs, cap), nil
}

// WhatCalls returns outgoing references from a symbol.
func WhatCalls(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	qn := args.GetString("qualified_name", "")
	prefix := args.GetString("path_prefix", "")
	limit := args.GetInt("limit", 200)
	refs, err := queries.RefsFrom(ctx, s, qn, prefix, "", limit)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(refs), args, format.KindOutgoing)
	if !cap.Truncated {
		return format.RefsFrom(qn, refs, cap), nil
	}
	return payloadOrString(refs, cap), nil
}

// ListFile returns the symbols in a file.
func ListFile(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	path := args.GetString("path", "")
	candidates, count, err := queries.FilePathCandidates(ctx, s, path)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return fmt.Sprintf("no file found for %q (check path; use list_files to see indexed paths)", path), nil
	}
	if count > 1 {
		syms, err := queries.FileOutline(ctx, s, path)
		if err != nil {
			return nil, err
		}
		other := candidates
		if len(other) > 0 && other[0] == syms[0].FilePath {
			other = other[1:]
		}
		cap := capFor(len(syms), args, format.KindSymbols)
		hdr := fmt.Sprintf("note: %q is ambiguous; matched %d files. Showing %s. Other matches: %v\n\n",
			path, count, syms[0].FilePath, other)
		return hdr + format.FileOutline(syms, cap), nil
	}
	syms, err := queries.FileOutline(ctx, s, path)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("file %q has no indexed symbols (it may be empty or all in test files)", candidates[0]), nil
	}
	cap := capFor(len(syms), args, format.KindSymbols)
	if !cap.Truncated {
		return format.FileOutline(syms, cap), nil
	}
	return payloadOrString(syms, cap), nil
}

// TraceCalls returns the call-path edges between two symbols.
func TraceCalls(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	from := args.GetString("from", "")
	to := args.GetString("to", "")
	maxDepth := args.GetInt("max_depth", 6)
	edges, err := path.Between(ctx, s, from, to, maxDepth)
	if werr := path.WrapError(err); werr != nil {
		var pe *path.Error
		if errors.As(werr, &pe) {
			switch pe.Kind {
			case path.PathSameSymbol:
				return fmt.Sprintf("from and to are the same symbol: %q", from), nil
			case path.PathSymbolNotFound:
				return pe.Error() + " — check the qualified name (use find to find it)", nil
			}
		}
		return nil, werr
	}
	if len(edges) == 0 {
		return fmt.Sprintf("no path found from %q to %q within depth %d", from, to, maxDepth), nil
	}
	return edges, nil
}

// ListFiles returns the project file tree. The --head cap is
// applied to the total count of file leaves in the tree (a
// directory count would be misleading because the tree is
// dominated by nested sub-trees, and an LLM asking for "the
// file list" cares about the leaves, not the top-level
// directory fan-out). When the cap is hit, the tree is rebuilt
// to include only the first Shown leaves in pre-order, preserving
// the directory scaffold so the response still looks like a tree.
func ListFiles(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	prefix := args.GetString("prefix", "")
	depth := args.GetInt("max_depth", 12)
	include := args.GetStringSlice("include", nil)
	tree, err := queries.FileTree(ctx, s, prefix, depth, include)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		return &model.FileNode{Name: ".", Path: ".", Type: "dir"}, nil
	}
	leaves := countFileLeaves(tree)
	cap := capFor(leaves, args, format.KindFiles)
	if !cap.Truncated {
		return tree, nil
	}
	trimmed := trimFileTree(tree, cap.Shown)
	return listPayload{Items: trimmed, Cap: cap}, nil
}

// countFileLeaves reports how many file nodes (Type=="file")
// live under n, including n itself when it is a file.
func countFileLeaves(n *model.FileNode) int {
	if n == nil {
		return 0
	}
	if n.Type == "file" {
		return 1
	}
	total := 0
	for _, c := range n.Children {
		total += countFileLeaves(c)
	}
	return total
}

// trimFileTree returns a copy of n containing only the first
// `max` file leaves in pre-order. Directory nodes are kept as a
// scaffold so the result still parses as a tree. If the source
// tree has fewer than max leaves the original is returned
// (callers already check this in capFor, but we double-check
// here so the function is safe to call independently).
func trimFileTree(n *model.FileNode, max int) *model.FileNode {
	if n == nil || max <= 0 {
		return n
	}
	total := countFileLeaves(n)
	if total <= max {
		return n
	}
	if n.Type == "file" {
		copy := *n
		copy.Children = nil
		return &copy
	}
	dir := *n
	dir.Children = nil
	kept := 0
	for _, c := range n.Children {
		if kept >= max {
			break
		}
		child := trimFileTree(c, max-kept)
		if child == nil {
			continue
		}
		// When the cap is consumed entirely inside this child,
		// the recursive call returns a partial subtree; we
		// keep it so the LLM sees the truncation boundary.
		dir.Children = append(dir.Children, child)
		kept += countFileLeaves(child)
	}
	return &dir
}

// ListPackage returns the top-level symbols of a package.
func ListPackage(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	pkgID := args.GetString("package_id", "")
	kinds := args.GetStringSlice("kinds", nil)
	resolved, err := resolvePackageID(ctx, s, pkgID)
	if err != nil {
		return nil, err
	}
	syms, err := queries.PackageOutline(ctx, s, resolved, kinds)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("no symbols for package %q (check package_id)", resolved), nil
	}
	cap := capFor(len(syms), args, format.KindSymbols)
	if !cap.Truncated {
		return format.PackageOutline(resolved, syms, cap), nil
	}
	return payloadOrString(syms, cap), nil
}

// resolvePackageID normalizes the user-supplied package_id. It accepts
// the canonical Go import path (e.g. "github.com/Wolf258/mekami-cli/internal/mcp"),
// a module-relative suffix (e.g. "internal/mcp" against a single module),
// or a bare last-segment name (e.g. "mcp"). Resolution order:
//
//  1. If the input is already a known canonical package_id, return it.
//  2. Otherwise, for each indexed module, try "<module>/<input>".
//  3. Otherwise, search the packages table for an exact match or a
//     suffix match ("/<input>"). This closes the gap where two
//     packages share the same last segment across different modules
//     (e.g. "internal/mcp" and "cmd/mcp") and the user passes "mcp".
//
// If exactly one candidate survives any of the passes, return it. If
// more than one survives, return an error listing the candidates so
// the caller can disambiguate (e.g. via list_modules).
func resolvePackageID(ctx context.Context, s *store.Store, input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("package_id is required")
	}
	if isCanonicalPackageID(ctx, s, input) {
		return input, nil
	}
	matches, err := resolvePackageIDCandidates(ctx, s, input)
	if err != nil {
		return input, err
	}
	switch len(matches) {
	case 0:
		return input, nil
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("ambiguous package_id %q; matches: %s", input, strings.Join(matches, ", "))
	}
}

// resolvePackageIDCandidates collects every canonical package_id that
// could match the user-supplied input, in two passes:
//
//   - Pass A: <module_path>/<input> (covers "internal/mcp" and similar
//     relative suffixes against a single module).
//   - Pass B: exact match on package_id OR suffix match "/<input>"
//     against the packages table (covers the bare last-segment case,
//     e.g. "mcp" matching both ".../internal/mcp" and ".../cmd/mcp").
//
// The returned slice is deduplicated and order is not guaranteed;
// callers must sort before formatting.
func resolvePackageIDCandidates(ctx context.Context, s *store.Store, input string) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(candidate string) {
		if candidate == "" {
			return
		}
		if isCanonicalPackageID(ctx, s, candidate) {
			seen[candidate] = struct{}{}
		}
	}

	mods, err := queries.ListModules(ctx, s)
	if err != nil {
		return nil, err
	}
	for _, m := range mods {
		add(m.Path + "/" + input)
	}

	rows, err := s.DB().QueryContext(ctx,
		`SELECT package_id FROM packages WHERE package_id = ? OR package_id LIKE ?`,
		input, "%/"+input)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		seen[pid] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out, nil
}

// isCanonicalPackageID reports whether id is a known package_id in the
// index. It uses a cheap COUNT-style query against the packages table.
func isCanonicalPackageID(ctx context.Context, s *store.Store, id string) bool {
	var n int
	row := s.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM packages WHERE package_id = ?`, id)
	if err := row.Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// ListPackageSymbols returns the top-level symbols declared in
// a package as JSON. It shares its implementation with
// ListPackage so resolution, kind filtering, and formatting
// stay identical across the two tools.
func ListPackageSymbols(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	return ListPackage(ctx, s, args)
}

// ListImporters returns the packages that import pkgID.
func ListImporters(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	pkgID := args.GetString("package_id", "")
	pkgs, err := queries.ListImporters(ctx, s, pkgID)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(pkgs), args, format.KindImporters)
	return payloadOrString(pkgs, cap), nil
}

// ListModules returns the indexed modules.
func ListModules(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	mods, err := queries.ListModules(ctx, s)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(mods), args, format.KindModules)
	return payloadOrString(mods, cap), nil
}

// ShowModules returns the per-module package summary.
func ShowModules(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	mods, err := queries.ModuleOverview(ctx, s)
	if err != nil {
		return nil, err
	}
	cap := capFor(len(mods), args, format.KindModules)
	if !cap.Truncated {
		return format.ModuleOverview(mods, cap), nil
	}
	return payloadOrString(mods, cap), nil
}

// diffPayload wraps a diff.FileDiff with truncation metadata. The
// diff has four sub-lists (added/modified/removed/inaccessible);
// the cap is reported on the union and the visible items are kept
// in their natural order so the caller can see what the diff
// actually contains.
type diffPayload struct {
	Added        []string    `json:"added"`
	Modified     []string    `json:"modified"`
	Removed      []string    `json:"removed"`
	Inaccessible []string    `json:"inaccessible,omitempty"`
	Cap          format.Cap  `json:"cap"`
}

// ShowChanges returns the diff against the last build.
func ShowChanges(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	root, err := queries.LastRoot(ctx, s)
	if err != nil {
		return SourceError(err), nil
	}
	d, err := diff.SinceLastBuild(ctx, s, root)
	if err != nil {
		return nil, err
	}
	total := len(d.Added) + len(d.Modified) + len(d.Removed) + len(d.Inaccessible)
	cap := capFor(total, args, format.KindChanges)
	if !cap.Truncated {
		return d, nil
	}
	// Truncate per-list proportionally: each sub-list is clipped
	// so the union never exceeds cap.Shown. The cap rounds in
	// favor of the first lists (added, modified) which are the
	// more actionable signals for the LLM.
	shown := cap.Shown
	added := take(&shown, d.Added)
	modified := take(&shown, d.Modified)
	removed := take(&shown, d.Removed)
	inacc := take(&shown, d.Inaccessible)
	return diffPayload{
		Added:        added,
		Modified:     modified,
		Removed:      removed,
		Inaccessible: inacc,
		Cap:          cap,
	}, nil
}

// take removes up to *remaining items from s and returns the
// taken prefix. *remaining is decremented by the number taken.
func take(remaining *int, s []string) []string {
	if *remaining <= 0 || len(s) == 0 {
		return nil
	}
	if len(s) <= *remaining {
		*remaining -= len(s)
		return s
	}
	out := s[:*remaining]
	*remaining = 0
	return out
}

// FindText runs a server-side regex search.
func FindText(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	root, err := queries.LastRoot(ctx, s)
	if err != nil {
		return SourceError(err), nil
	}
	pattern := args.GetString("pattern", "")
	prefix := args.GetString("path_prefix", "")
	exts := args.GetStringSlice("include_ext", nil)
	maxResults := args.GetInt("max_results", 200)
	context := args.GetInt("context", 2)
	res, err := grep.Grep(ctx, grep.Options{
		Pattern:    pattern,
		Root:       root,
		PathPrefix: prefix,
		IncludeExt: exts,
		MaxResults: maxResults,
		Context:    context,
	})
	if err != nil {
		return "error: " + err.Error(), nil
	}
	// find_text already returns total/truncated. Apply the
	// visible --head cap on top. The resulting Cap reports the
	// post-truncation counts and folds both truncation sources
	// into a single hint.
	total := res.Total
	matches := res.Matches
	hint := format.HintFor(format.KindMatches)
	if res.Truncated {
		hint = hint + " (or raise --max-results)"
	}
	head := args.GetInt("head", defaultHead)
	if head < 0 {
		head = 0
	}
	cap := format.Cap{Total: total, Shown: len(matches), Truncated: res.Truncated, Hint: hint}
	if head > 0 && len(matches) > head {
		matches = matches[:head]
		cap = format.Cap{Total: total, Shown: head, Truncated: true, Hint: hint}
	}
	out := struct {
		Pattern   string       `json:"pattern"`
		Root      string       `json:"root"`
		Total     int          `json:"total"`
		Truncated bool         `json:"truncated"`
		Shown     int          `json:"shown"`
		Cap       format.Cap   `json:"cap"`
		Matches   []grep.Match `json:"matches"`
	}{
		Pattern:   res.Pattern,
		Root:      res.Root,
		Total:     total,
		Truncated: cap.Truncated,
		Shown:     cap.Shown,
		Cap:       cap,
		Matches:   matches,
	}
	return out, nil
}

// IndexStatus returns the high-level DB snapshot.
func IndexStatus(ctx context.Context, s *store.Store, _ naming.ArgMap) (any, error) {
	st, err := queries.IndexStatus(ctx, s)
	if err != nil {
		return SourceError(err), nil
	}
	return st, nil
}

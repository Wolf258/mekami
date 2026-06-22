// dependents.go implements the `dependents` MCP tool / CLI command.
// It runs a multi-source BFS over the reference graph (call or
// import edges, depending on --level) and returns a tree of
// reachable nodes so the LLM can answer "what is affected if I
// change X".
//
// The handler is intentionally thin: it picks the right Expand
// closure for the (level, direction) pair, runs graph.BFS, then
// walks the resulting Tree to build a DependentNode hierarchy for
// the format layer. The actual graph traversal lives in
// internal/core/graph; the SQL lives in internal/core/queries.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/graph"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

func dependents(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	target := args.GetString("target", "")
	if target == "" {
		return AsResult("target is required", nil), nil
	}
	level := args.GetString("level", "symbol")
	direction := args.GetString("direction", "callers")
	transitive := args.GetBool("transitive", true)
	maxDepth := args.GetInt("max_depth", 4)
	maxNodes := args.GetInt("max_nodes", 500)
	refKind := args.GetString("ref_kind", "")
	pathPrefix := args.GetString("path_prefix", "")

	if !transitive {
		maxDepth = 1
	}
	opts := graph.Options{MaxDepth: maxDepth, MaxNodes: maxNodes}

	// Resolve the target into a single canonical id, and pick the
	// matching Expand closure. Each branch validates that the
	// combination of (level, direction) is meaningful — for
	// example, "callees" is only defined at the symbol level.
	var roots []string
	var expand graph.Expand
	var label string
	var levelDesc string
	switch level {
	case "symbol":
		// Verify the symbol exists so a typo is distinguishable
		// from "no dependents".
		syms, err := queries.SymbolByQName(ctx, s, target)
		if err != nil {
			return nil, err
		}
		if len(syms) == 0 {
			return AsResult(fmt.Sprintf("symbol %q not found in index — check the qualified name (use find_symbols to look it up)", target), nil), nil
		}
		roots = []string{syms[0].QualifiedName}
		switch direction {
		case "callers":
			expand = func(ctx context.Context, node string) ([]string, error) {
				return queries.SymbolCallers(ctx, s, node, refKind)
			}
			levelDesc = fmt.Sprintf("symbol, callers (ref_kind=%q)", refKind)
		case "callees":
			if pathPrefix != "" {
				// RefsFrom supports path_prefix; use it directly.
				expand = func(ctx context.Context, node string) ([]string, error) {
					return queries.RefsFrom(ctx, s, node, pathPrefix, refKind, 0)
				}
			} else {
				expand = func(ctx context.Context, node string) ([]string, error) {
					return queries.SymbolCallees(ctx, s, node, refKind)
				}
			}
			levelDesc = fmt.Sprintf("symbol, callees (ref_kind=%q)", refKind)
		default:
			return AsResult(fmt.Sprintf("invalid direction %q; use callers|callees", direction), nil), nil
		}
		label = target
	case "package":
		// Resolve to a canonical package_id (the resolver
		// handles short suffixes and bare names).
		pkgID, err := resolvePackageID(ctx, s, target)
		if err != nil {
			return AsResult(err.Error(), nil), nil
		}
		roots = []string{pkgID}
		expand = func(ctx context.Context, node string) ([]string, error) {
			return queries.PackageImporters(ctx, s, node)
		}
		levelDesc = "package, importers"
		label = pkgID
	case "module":
		mods, err := queries.ListModules(ctx, s)
		if err != nil {
			return nil, err
		}
		found := false
		for _, m := range mods {
			if m.Path == target {
				found = true
				break
			}
		}
		if !found {
			return AsResult(fmt.Sprintf("module %q not found in index — use list_modules to see indexed modules", target), nil), nil
		}
		roots = []string{target}
		expand = func(ctx context.Context, node string) ([]string, error) {
			return queries.ModuleImporters(ctx, s, node)
		}
		levelDesc = "module, importers"
		label = target
	default:
		return AsResult(fmt.Sprintf("invalid level %q; use symbol|package|module", level), nil), nil
	}

	tree, bfsErr := graph.BFS(ctx, roots, expand, opts)
	// graph.BFS returns ErrTooManyNodes alongside a partial tree
	// when the cap is hit. We keep the partial tree and surface
	// the truncation via the cap so the formatter can hint.
	// Other errors abort.
	if bfsErr != nil && !errors.Is(bfsErr, graph.ErrTooManyNodes) {
		return nil, bfsErr
	}
	if tree == nil || len(tree.Nodes) <= 1 {
		// Only the root was visited; nothing depends on the target.
		// Render a minimal tree showing the target itself so the
		// caller can confirm the lookup hit the right symbol.
		root := &format.DependentNode{Name: label, Detail: "(no dependents)"}
		cap := format.Cap{Total: 1, Shown: 1, Truncated: false, Hint: format.HintFor(format.KindDependents)}
		headerLabel := fmt.Sprintf("%s  (%s, depth=%d, nodes=%d)", label, levelDesc, maxDepth, 1)
		return AsResult(format.DependentTree(headerLabel, root, 1, cap), tree), nil
	}

	// Walk the BFS tree and build a DependentNode hierarchy so
	// the formatter can render with indentation. We resolve
	// each node id to a display string and an optional file:line
	// detail (call site line for the parent edge).
	root := buildDependentTree(ctx, s, tree, roots[0], level, direction)
	cap := capFor(len(tree.Nodes), args, format.KindDependents)
	headerLabel := fmt.Sprintf("%s  (%s, depth=%d, nodes=%d)", label, levelDesc, maxDepth, len(tree.Nodes))
	return AsResult(format.DependentTree(headerLabel, root, len(tree.Nodes), cap), tree), nil
}

// buildDependentTree walks the BFS tree from `rootID` and produces
// a DependentNode hierarchy. The root node is the target itself;
// its children are its direct dependents; grandchildren are the
// transitive dependents.
//
// Each child's Detail field carries a file:line hint when
// available (the call site line for symbol-level callers, or
// the file path for package/module levels). This makes the
// rendered tree self-explanatory without a second query.
func buildDependentTree(ctx context.Context, s *store.Store, tree *graph.Tree, rootID, level, direction string) *format.DependentNode {
	// Map every visited node id to its display metadata. We
	// query once and cache; this avoids hitting the DB for
	// every node in the tree.
	meta := map[string]dependentMeta{}
	ids := make([]string, 0, len(tree.Nodes))
	for _, n := range tree.Nodes {
		ids = append(ids, n)
	}
	resolveMeta(ctx, s, ids, level, meta)

	return buildNode(ctx, tree, rootID, 0, meta, level, direction)
}

// dependentMeta is the resolved display info for a single node
// in the dependents tree. Only one of the fields is meaningful
// per level (symbol -> qname + file:line, package -> package_id,
// module -> module path).
type dependentMeta struct {
	Display  string
	Detail   string
	Kind     string
	FilePath string
	Line     int
}

func buildNode(_ context.Context, tree *graph.Tree, node string, depth int, meta map[string]dependentMeta, level, direction string) *format.DependentNode {
	m := meta[node]
	dn := &format.DependentNode{
		Name:  m.Display,
		Depth: depth,
	}
	if m.FilePath != "" {
		if m.Line > 0 {
			dn.Detail = fmt.Sprintf("%s:%d  [%s]", m.FilePath, m.Line, m.Kind)
		} else {
			dn.Detail = m.FilePath
		}
	}
	if dn.Name == "" {
		dn.Name = node
	}
	children := tree.Children(node)
	// Stable order: sort children by name so two traversals of
	// the same graph produce the same output.
	sort.SliceStable(children, func(i, j int) bool {
		return meta[children[i]].Display < meta[children[j]].Display ||
			(meta[children[i]].Display == meta[children[j]].Display && children[i] < children[j])
	})
	for _, c := range children {
		dn.Children = append(dn.Children, buildNode(nil, tree, c, depth+1, meta, level, direction))
	}
	// Mark the last level as truncated when the BFS hit the
	// depth or nodes cap. We only flag the leaves of the
	// returned tree.
	if len(children) == 0 && (tree.Truncated && tree.Depth[node] == tree.MaxDepthHint()) {
		dn.Truncated = true
	}
	return dn
}

// resolveMeta fetches the display metadata for every node id in
// the BFS tree. It issues a single batched query per kind of id
// (symbol qname vs package_id vs module path) and writes the
// result into the meta map.
func resolveMeta(ctx context.Context, s *store.Store, ids []string, level string, meta map[string]dependentMeta) {
	switch level {
	case "symbol":
		// One query: SELECT <SymbolWithFileSelect> WHERE
		// qualified_name IN (?, ?, ...). Distinct because two
		// files in different packages could share a qname (rare
		// but possible). For dependents the LLM cares about the
		// first match — we project a single row per qname.
		rows, err := symbolRowsByQName(ctx, s, ids)
		if err != nil {
			// On error, fall back to raw ids as display.
			for _, id := range ids {
				if _, ok := meta[id]; !ok {
					meta[id] = dependentMeta{Display: id}
				}
			}
			return
		}
		for id, swf := range rows {
			meta[id] = dependentMeta{
				Display:  swf.QualifiedName,
				FilePath: swf.FilePath,
				Line:     swf.StartLine,
				Kind:     swf.Kind,
			}
		}
	case "package":
		for _, id := range ids {
			meta[id] = dependentMeta{Display: id}
		}
	case "module":
		for _, id := range ids {
			meta[id] = dependentMeta{Display: id}
		}
	}
	// Fill in any missing ids with the raw id so the renderer
	// never has a nil name.
	for _, id := range ids {
		if _, ok := meta[id]; !ok {
			meta[id] = dependentMeta{Display: id}
		}
	}
}

// symbolRowsByQName issues a single batched SELECT for all the
// qualified names in `ids` and returns a map keyed by qname. When
// multiple symbols share a qname (rare; happens for funclits in
// different files), the first row wins.
func symbolRowsByQName(ctx context.Context, s *store.Store, ids []string) (map[string]model.SymbolWithFile, error) {
	out := map[string]model.SymbolWithFile{}
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	sql := `
		SELECT ` + store.SymbolWithFileSelect + `
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE s.qualified_name IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY s.qualified_name, s.start_line
	`
	rows, err := s.DB().QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		swf, err := store.ScanSymbolWithFile(rows)
		if err != nil {
			return nil, err
		}
		if _, exists := out[swf.QualifiedName]; !exists {
			out[swf.QualifiedName] = swf
		}
	}
	return out, rows.Err()
}

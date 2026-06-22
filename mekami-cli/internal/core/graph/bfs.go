// Package graph exposes a small breadth-first traversal primitive
// over the mekami reference graph. It is deliberately decoupled from
// the queries package: callers inject an `Expand` closure that turns
// a node id (a qualified name, package_id, or module path — the unit
// the caller is traversing on) into its successor ids. The BFS does
// not care what the ids mean; it only tracks parent pointers and
// depth so callers can reconstruct trees, paths, or reachable sets.
//
// This package is the shared foundation for `dependents` (multi-
// source BFS over callers / callees / importers) and `circular_imports`
// (DFS cycle detection reusing the same Expand contract). It is
// NOT used by `trace_calls`, which keeps its dedicated single-source
// single-target BFS in internal/core/path.
package graph

import (
	"context"
	"errors"
	"fmt"
)

// ErrTooManyNodes is returned by BFS when the traversal hit the
// MaxNodes cap before exhausting the frontier. The partial Tree is
// still returned alongside the error so callers can render what was
// discovered.
var ErrTooManyNodes = errors.New("graph.BFS: max_nodes cap reached before frontier exhausted")

// Expand returns the successor ids of `node`. It is injected by the
// caller so this package does not depend on queries. The closure may
// return an empty slice (a leaf) but must return a non-nil error
// only on actual failure; transient empty results are normal.
//
// Implementations should be deterministic so the resulting tree is
// stable across calls. SQLite ORDER BY in the underlying query is
// the easiest way to guarantee that.
type Expand func(ctx context.Context, node string) ([]string, error)

// Options configures a BFS run. Zero values are NOT safe — callers
// must set MaxDepth and MaxNodes to explicit limits. The defaults
// below (DefaultMaxDepth, DefaultMaxNodes) are reasonable starting
// points for call-graph and import-graph traversals.
type Options struct {
	// MaxDepth caps how many BFS levels are expanded. A value of
	// 1 means "roots and their direct successors". 0 means "use
	// DefaultMaxDepth" (NOT "no expansion" — callers that want
	// only the roots should pass an empty frontier). Required.
	MaxDepth int
	// MaxNodes caps how many distinct nodes the traversal may
	// visit (including the roots). When the cap is hit, BFS
	// stops and returns ErrTooManyNodes alongside the partial
	// tree. Required.
	MaxNodes int
}

const (
	DefaultMaxDepth = 4
	DefaultMaxNodes = 500
)

// Tree is the result of a BFS run. Roots are not present in the
// Parent map (they have no parent); every other visited node has
// exactly one Parent entry. Depth is filled for every visited node,
// including roots (depth 0).
type Tree struct {
	// Roots is the input list, echoed back for convenience.
	Roots []string
	// Nodes is every visited node id in BFS-discovery order.
	// Roots come first, in their input order.
	Nodes []string
	// Parent maps a non-root node id to its parent id.
	Parent map[string]string
	// Depth maps every visited node id to its BFS depth (0 for
	// roots).
	Depth map[string]int
	// Truncated is true when the BFS stopped because of a cap
	// (MaxDepth or MaxNodes). When true, Reason explains which
	// cap fired so callers can render a precise hint.
	Truncated bool
	// Reason is set when Truncated is true. One of:
	//   "max_depth"   — stopped after expanding the last level
	//                    allowed by MaxDepth.
	//   "max_nodes"   — hit the MaxNodes cap mid-frontier.
	Reason string
}

// BFS runs a breadth-first traversal starting from `roots`. Every
// root is added to the visited set at depth 0 and the frontier is
// expanded level by level up to opts.MaxDepth. A node discovered
// multiple times keeps its first parent (standard BFS property).
//
// The traversal never revisits a node. Self-loops (a node that
// expands to itself) are silently dropped — this is the only
// special case beyond the visited map.
//
// When the MaxNodes cap is hit mid-frontier, BFS returns the
// partial tree AND ErrTooManyNodes. Callers that want to surface
// the cap (e.g. dependents) can check errors.Is(err, ErrTooManyNodes)
// and use tree.Truncated to render a hint. Callers that just want
// the reachable set can ignore the error.
func BFS(ctx context.Context, roots []string, expand Expand, opts Options) (*Tree, error) {
	if expand == nil {
		return nil, fmt.Errorf("graph.BFS: nil Expand closure")
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	t := &Tree{
		Roots:  append([]string(nil), roots...),
		Parent: map[string]string{},
		Depth:  map[string]int{},
	}
	visited := map[string]bool{}
	frontier := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" || visited[r] {
			continue
		}
		visited[r] = true
		t.Depth[r] = 0
		t.Nodes = append(t.Nodes, r)
		frontier = append(frontier, r)
	}
	if len(frontier) == 0 {
		return t, nil
	}
	if len(t.Nodes) >= opts.MaxNodes {
		t.Truncated = true
		t.Reason = "max_nodes"
		return t, ErrTooManyNodes
	}

	for depth := 0; depth < opts.MaxDepth; depth++ {
		if len(frontier) == 0 {
			break
		}
		next := make([]string, 0, len(frontier)*2)
		for _, cur := range frontier {
			succs, err := expand(ctx, cur)
			if err != nil {
				return t, err
			}
			for _, s := range succs {
				if s == "" || s == cur || visited[s] {
					continue
				}
				visited[s] = true
				t.Parent[s] = cur
				t.Depth[s] = depth + 1
				t.Nodes = append(t.Nodes, s)
				next = append(next, s)
				if len(t.Nodes) >= opts.MaxNodes {
					t.Truncated = true
					t.Reason = "max_nodes"
					return t, ErrTooManyNodes
				}
			}
		}
		frontier = next
	}
	// We exited the loop because depth == MaxDepth. If there is
	// still a non-empty frontier, the depth cap was the limiting
	// factor — flag it so callers can hint at raising --max-depth.
	if len(frontier) > 0 {
		t.Truncated = true
		t.Reason = "max_depth"
	}
	return t, nil
}

// PathFromRoot returns the chain of node ids from the closest root
// down to `node` (inclusive). If `node` is not in the tree, returns
// nil. The chain is ordered root-first.
func (t *Tree) PathFromRoot(node string) []string {
	if t == nil {
		return nil
	}
	if _, ok := t.Depth[node]; !ok {
		return nil
	}
	// Walk parent pointers to a root, then reverse.
	chain := []string{node}
	cur := node
	for {
		p, ok := t.Parent[cur]
		if !ok {
			break
		}
		chain = append(chain, p)
		cur = p
	}
	// Reverse in place.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// MaxDepthHint returns the maximum depth observed in the tree.
// Used by the dependents renderer to decide whether to flag a
// leaf as truncated.
func (t *Tree) MaxDepthHint() int {
	if t == nil {
		return 0
	}
	max := 0
	for _, d := range t.Depth {
		if d > max {
			max = d
		}
	}
	return max
}

// Children returns the direct successors of `node` in the tree.
// The order matches discovery order (BFS), which is stable when the
// Expand closure is deterministic. Returns nil for unknown nodes.
func (t *Tree) Children(node string) []string {
	if t == nil {
		return nil
	}
	if _, ok := t.Depth[node]; !ok {
		return nil
	}
	var out []string
	for _, n := range t.Nodes {
		if t.Parent[n] == node {
			out = append(out, n)
		}
	}
	return out
}

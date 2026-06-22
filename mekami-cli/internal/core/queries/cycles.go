package queries

import (
	"context"
	"sort"

	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// ImportCycles returns every cycle in the package import graph
// restricted to packages indexed in the project (i.e. rows in
// the `packages` table — stdlib and external deps are not in
// the graph). Each cycle is returned once in canonical form:
// the cycle is rotated so it starts with the lexicographically
// smallest package_id, and the order is preserved (not reversed),
// so the same cycle discovered via different starting nodes
// produces the same output.
//
// Algorithm: collect the graph as a (package_id -> []package_id)
// adjacency map (one SQL query), then run a DFS from every node
// with a stack of visited nodes (NOT a "fully visited" set) —
// when we reach a node already in the stack, we found a
// back-edge and the stack slice from the back-edge to the end
// is a cycle. Canonicalize and dedup.
//
// Complexity: O(V + E) in the size of the project package
// graph. The number of packages in a single project is bounded
// in practice (hundreds, not millions), so the in-Go DFS is
// cheaper than maintaining a complex WITH RECURSIVE query.
func ImportCycles(ctx context.Context, s *store.Store) ([][]string, error) {
	// Build the adjacency map: package_id -> distinct imported
	// package_ids that ALSO exist in the packages table. The
	// inner filter is the "project only" restriction: an import
	// of "fmt" or "github.com/x/y" is dropped.
	rows, err := s.DB().QueryContext(ctx, `
		SELECT DISTINCT p.package_id, r.to_qualified
		FROM refs r
		JOIN symbols sym ON sym.id = r.from_symbol
		JOIN packages p ON p.id = sym.package_id
		JOIN packages target ON target.package_id = r.to_qualified
		WHERE r.kind = 'import'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	adj := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		if from == to {
			// Self-imports are not import cycles in the
			// compiler-error sense; skip them.
			continue
		}
		adj[from] = append(adj[from], to)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// DFS from every node. onStack tracks the current path;
	// finished tracks nodes whose subtree has been fully
	// explored (so we don't re-walk their successors from a
	// second root).
	var cycles [][]string
	onStack := map[string]bool{}
	finished := map[string]bool{}
	seen := map[string]bool{} // for canonical-form dedup

	var dfs func(node string, stack []string)
	dfs = func(node string, stack []string) {
		onStack[node] = true
		stack = append(stack, node)
		for _, nxt := range adj[node] {
			if onStack[nxt] {
				// Back-edge: extract the cycle from the
				// stack, canonicalize, dedup.
				cycle := extractCycle(stack, nxt)
				key := canonicalKey(cycle)
				if !seen[key] {
					seen[key] = true
					cycles = append(cycles, cycle)
				}
				continue
			}
			if finished[nxt] {
				continue
			}
			dfs(nxt, stack)
		}
		onStack[node] = false
		finished[node] = true
	}

	// Iterate nodes in sorted order so the output is stable
	// across calls (the DFS order influences the discovery
	// order of cycles, but canonicalization handles duplicates).
	nodes := make([]string, 0, len(adj))
	for n := range adj {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if finished[n] {
			continue
		}
		dfs(n, nil)
	}
	// Stable output order: shortest cycle first, then by
	// canonical key. Keeps the LLM's reading of the list
	// consistent across runs of the same project.
	sort.Slice(cycles, func(i, j int) bool {
		if len(cycles[i]) != len(cycles[j]) {
			return len(cycles[i]) < len(cycles[j])
		}
		return canonicalKey(cycles[i]) < canonicalKey(cycles[j])
	})
	return cycles, nil
}

// extractCycle walks stack backwards from the end until it
// finds `start`, then returns the slice from `start` to the
// end. The returned slice keeps the discovery-time direction
// (start -> ... -> start).
func extractCycle(stack []string, start string) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == start {
			out := make([]string, len(stack)-i)
			copy(out, stack[i:])
			return out
		}
	}
	// Should not happen: the caller only invokes this when
	// start was found in onStack, and onStack mirrors stack.
	return nil
}

// canonicalKey returns a stable string representation of cycle
// that is invariant under rotation and direction. Two cycles
// that visit the same set of nodes in the same cyclic order
// (regardless of which node is the "start" of the slice) get
// the same key.
func canonicalKey(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	// Drop the trailing duplicate (cycle always ends with its
	// start node, so the last element equals the first).
	trimmed := cycle
	if len(trimmed) > 0 && trimmed[0] == trimmed[len(trimmed)-1] {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 {
		return ""
	}
	// Find the index of the lexicographically smallest node.
	minIdx := 0
	for i := 1; i < len(trimmed); i++ {
		if trimmed[i] < trimmed[minIdx] {
			minIdx = i
		}
	}
	// Build the forward rotation starting at minIdx.
	rotated := make([]string, len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		rotated[i] = trimmed[(minIdx+i)%len(trimmed)]
	}
	// Build the reverse rotation (would be the same cycle if
	// discovered from the "other direction"). Compare and pick
	// the lexicographically smaller one as the canonical key
	// so A->B->C and C->B->A hash to the same string.
	reverse := make([]string, len(trimmed))
	for i := 0; i < len(trimmed); i++ {
		reverse[i] = trimmed[(minIdx-i+len(trimmed)*2)%len(trimmed)]
	}
	fwd := joinSlash(rotated)
	rev := joinSlash(reverse)
	if fwd < rev {
		return fwd
	}
	return rev
}

func joinSlash(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "->"
		}
		out += x
	}
	return out
}

package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// staticExpand builds an Expand closure from an adjacency map. It
// makes BFS testable without touching SQLite.
func staticExpand(adj map[string][]string) Expand {
	return func(_ context.Context, node string) ([]string, error) {
		return adj[node], nil
	}
}

func TestBFS_LinearChain(t *testing.T) {
	adj := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	wantNodes := []string{"A", "B", "C", "D"}
	if len(tree.Nodes) != len(wantNodes) {
		t.Fatalf("got %d nodes, want %d: %v", len(tree.Nodes), len(wantNodes), tree.Nodes)
	}
	for i, w := range wantNodes {
		if tree.Nodes[i] != w {
			t.Errorf("node[%d] = %q, want %q", i, tree.Nodes[i], w)
		}
	}
	if got, want := tree.Depth["D"], 3; got != want {
		t.Errorf("depth(D) = %d, want %d", got, want)
	}
	if got, want := tree.Parent["B"], "A"; got != want {
		t.Errorf("parent(B) = %q, want %q", got, want)
	}
}

func TestBFS_MaxDepthCap(t *testing.T) {
	adj := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth:  2,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// depth=2 means A,B,C are visited (depths 0,1,2). D is NOT
	// visited because expanding C would be depth=2 -> 3, beyond cap.
	wantVisited := map[string]bool{"A": true, "B": true, "C": true}
	if len(tree.Nodes) != len(wantVisited) {
		t.Fatalf("got %d nodes, want %d: %v", len(tree.Nodes), len(wantVisited), tree.Nodes)
	}
	for _, n := range tree.Nodes {
		if !wantVisited[n] {
			t.Errorf("unexpected node %q", n)
		}
	}
	if !tree.Truncated {
		t.Error("expected Truncated=true (max_depth)")
	}
	if tree.Reason != "max_depth" {
		t.Errorf("Reason = %q, want max_depth", tree.Reason)
	}
}

func TestBFS_MaxNodesCap(t *testing.T) {
	adj := map[string][]string{
		"A": {"B", "C", "D"},
		"B": {},
		"C": {},
		"D": {},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth:  10,
		MaxNodes:  3,
	})
	if !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("expected ErrTooManyNodes, got %v", err)
	}
	if !tree.Truncated {
		t.Error("expected Truncated=true")
	}
	if tree.Reason != "max_nodes" {
		t.Errorf("Reason = %q, want max_nodes", tree.Reason)
	}
	// We expect A plus B and C (or B, C, D depending on iteration
	// order — but at most 3).
	if len(tree.Nodes) > 3 {
		t.Errorf("got %d nodes, want <= 3", len(tree.Nodes))
	}
	if len(tree.Nodes) < 2 {
		t.Errorf("got %d nodes, want >= 2 (root + at least one)", len(tree.Nodes))
	}
}

func TestBFS_SelfLoop(t *testing.T) {
	adj := map[string][]string{
		"A": {"A", "B"},
		"B": {},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(tree.Nodes) != 2 {
		t.Fatalf("expected 2 nodes (A, B), got %v", tree.Nodes)
	}
}

func TestBFS_CycleDoesNotInfiniteLoop(t *testing.T) {
	// A -> B -> C -> A forms a 3-cycle. BFS must terminate thanks
	// to the visited map.
	adj := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(tree.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %v", tree.Nodes)
	}
	if tree.Truncated {
		t.Error("did not expect truncation on a closed 3-cycle")
	}
}

func TestBFS_MultipleRoots(t *testing.T) {
	adj := map[string][]string{
		"A": {"C"},
		"B": {"C"},
		"C": {"D"},
		"D": {},
	}
	tree, err := BFS(context.Background(), []string{"A", "B"}, staticExpand(adj), Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// C is reachable from both A and B; its parent is whichever
	// root's frontier reached it first (A in this case).
	if tree.Parent["C"] != "A" {
		t.Errorf("parent(C) = %q, want A (first root)", tree.Parent["C"])
	}
	if got, want := tree.Depth["D"], 2; got != want {
		t.Errorf("depth(D) = %d, want %d", got, want)
	}
}

func TestBFS_EmptyRoots(t *testing.T) {
	tree, err := BFS(context.Background(), nil, staticExpand(nil), Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(tree.Nodes) != 0 {
		t.Errorf("expected empty tree, got %v", tree.Nodes)
	}
}

func TestBFS_NilExpandReturnsError(t *testing.T) {
	_, err := BFS(context.Background(), []string{"A"}, nil, Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err == nil {
		t.Fatal("expected error on nil Expand, got nil")
	}
	if !strings.Contains(err.Error(), "nil Expand") {
		t.Errorf("error %q does not mention 'nil Expand'", err)
	}
}

func TestBFS_ExpandErrorPropagates(t *testing.T) {
	boom := func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("explode")
	}
	_, err := BFS(context.Background(), []string{"A"}, boom, Options{
		MaxDepth:  10,
		MaxNodes:  100,
	})
	if err == nil || !strings.Contains(err.Error(), "explode") {
		t.Fatalf("expected 'explode' error, got %v", err)
	}
}

func TestBFS_DefaultsAppliedOnZeroOptions(t *testing.T) {
	adj := map[string][]string{
		"A": {"B"}, "B": {"C"}, "C": {"D"}, "D": {"E"}, "E": {},
	}
	tree, err := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// DefaultMaxDepth=4 means we visit A,B,C,D,E (depths 0-4).
	if len(tree.Nodes) != 5 {
		t.Errorf("expected 5 nodes with default max_depth=4, got %v", tree.Nodes)
	}
}

func TestTree_PathFromRoot(t *testing.T) {
	adj := map[string][]string{
		"A": {"B"}, "B": {"C"}, "C": {"D"},
	}
	tree, _ := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth: 10, MaxNodes: 100,
	})
	chain := tree.PathFromRoot("D")
	want := []string{"A", "B", "C", "D"}
	if len(chain) != len(want) {
		t.Fatalf("got %v, want %v", chain, want)
	}
	for i, w := range want {
		if chain[i] != w {
			t.Errorf("chain[%d] = %q, want %q", i, chain[i], w)
		}
	}
	if got := tree.PathFromRoot("missing"); got != nil {
		t.Errorf("expected nil for unknown node, got %v", got)
	}
}

func TestTree_Children(t *testing.T) {
	adj := map[string][]string{
		"A": {"B", "C"},
		"B": {"D"},
		"C": {},
		"D": {},
	}
	tree, _ := BFS(context.Background(), []string{"A"}, staticExpand(adj), Options{
		MaxDepth: 10, MaxNodes: 100,
	})
	got := tree.Children("A")
	if len(got) != 2 {
		t.Fatalf("expected 2 children of A, got %v", got)
	}
	// Discovery order is preserved.
	if got[0] != "B" || got[1] != "C" {
		t.Errorf("children = %v, want [B C]", got)
	}
	if got := tree.Children("missing"); got != nil {
		t.Errorf("expected nil for unknown node, got %v", got)
	}
}

package path

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// ErrSameSymbol is returned by PathBetween when from and to are equal,
// so callers can distinguish "no path" from "trivially the same node".
var ErrSameSymbol = errors.New("trace_calls: from and to are the same symbol")

// ErrSymbolNotFound is returned when either endpoint of the requested
// path does not exist in the index. It is a typed error so the MCP
// handler can surface a precise message (e.g. "symbol 'foo.Bar' not
// found in index") instead of the generic "no path" result.
type ErrSymbolNotFound struct {
	QName string
}

func (e *ErrSymbolNotFound) Error() string {
	return fmt.Sprintf("symbol %q not found in index", e.QName)
}

// ErrorKind classifies a path.Between error for callers that want
// to render a uniform message without re-deriving the diagnosis.
type ErrorKind int

const (
	PathOK ErrorKind = iota
	PathSameSymbol
	PathSymbolNotFound
	PathOther
)

// Error wraps a path.Between error with a stable kind tag and the
// qualified name when the cause was a missing endpoint. Callers
// use WrapError to obtain one of these from a raw error.
type Error struct {
	Kind  ErrorKind
	QName string
	Err   error
}

func (e *Error) Error() string {
	switch e.Kind {
	case PathSameSymbol:
		return "from and to are the same symbol"
	case PathSymbolNotFound:
		return "symbol " + e.QName + " not found in index"
	default:
		return e.Err.Error()
	}
}

func (e *Error) Unwrap() error { return e.Err }

// WrapError classifies the error returned by Between into a typed
// Error so callers can render a consistent message. nil in → nil
// out.
func WrapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrSameSymbol) {
		return &Error{Kind: PathSameSymbol}
	}
	var nf *ErrSymbolNotFound
	if errors.As(err, &nf) {
		return &Error{Kind: PathSymbolNotFound, QName: nf.QName}
	}
	return &Error{Kind: PathOther, Err: err}
}

// Between finds a shortest call path from `from` to `to` via refs,
// returning the edges (RefSite list) that compose the path. Returns
// nil, nil if no path exists within maxDepth. If multiple shortest
// paths exist, the first one discovered is returned.
func Between(ctx context.Context, s *store.Store, from, to string, maxDepth int) ([]model.RefSite, error) {
	if from == to {
		return nil, ErrSameSymbol
	}
	if maxDepth <= 0 {
		maxDepth = 6
	}
	// Validate both endpoints exist in the index. Without this, a typo
	// in `from` or `to` is indistinguishable from "no path exists"
	// (both produce an empty result), which confuses the LLM into
	// assuming the codebase has no relationship between the two.
	fromSyms, err := queries.SymbolByQName(ctx, s, from)
	if err != nil {
		return nil, err
	}
	if len(fromSyms) == 0 {
		return nil, &ErrSymbolNotFound{QName: from}
	}
	toSyms, err := queries.SymbolByQName(ctx, s, to)
	if err != nil {
		return nil, err
	}
	if len(toSyms) == 0 {
		return nil, &ErrSymbolNotFound{QName: to}
	}

	// parent maps a visited qname → its parent qname in the BFS tree.
	// For `from`, parent is not set. We reconstruct the path by
	// walking from `to` back to `from` via parent pointers.
	parent := map[string]string{}
	visited := map[string]bool{from: true}

	// frontier: qnames to expand at this depth.
	frontier := []string{from}

	// Cache: qname -> sorted distinct outgoing call qnames, to avoid
	// re-running the same query for nodes revisited across BFS levels.
	calleeCache := map[string][]string{}

	getCallees := func(qn string) ([]string, error) {
		if cached, ok := calleeCache[qn]; ok {
			return cached, nil
		}
		// PathBetween follows call-graph edges only. type-use, embed,
		// field, value and import refs are excluded so that paths
		// reflect actual call relationships.
		cs, err := queries.RefsFrom(ctx, s, qn, "", string(api.RefCall), 200)
		if err != nil {
			return nil, err
		}
		calleeCache[qn] = cs
		return cs, nil
	}

	for depth := 0; depth < maxDepth; depth++ {
		next := make([]string, 0, len(frontier)*2)
		for _, cur := range frontier {
			callees, err := getCallees(cur)
			if err != nil {
				return nil, err
			}
			for _, c := range callees {
				if c == cur {
					continue // self-loop
				}
				if visited[c] {
					continue
				}
				visited[c] = true
				parent[c] = cur
				if c == to {
					return reconstruct(ctx, s, from, to, parent)
				}
				next = append(next, c)
			}
		}
		if len(next) == 0 {
			return nil, nil
		}
		frontier = next
	}
	return nil, nil
}

// reconstruct walks parent pointers from `to` back to `from`,
// reversing the chain into a forward []RefSite. All hop ref rows are
// fetched in a single batched query (keyed by from_qn+to_qn) rather
// than one round-trip per hop. If a hop has no concrete ref row in
// the DB, the symbol table is consulted and a minimal RefSite is
// synthesized.
func reconstruct(ctx context.Context, s *store.Store, from, to string, parent map[string]string) ([]model.RefSite, error) {
	// Walk backwards: to → ... → from.
	chain := []string{to}
	cur := to
	for cur != from {
		prev, ok := parent[cur]
		if !ok {
			// Should not happen if BFS was correct.
			return nil, fmt.Errorf("path reconstruction: missing parent for %q", cur)
		}
		chain = append(chain, prev)
		cur = prev
	}
	// Reverse in place.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	if len(chain) < 2 {
		return nil, nil
	}

	// Build the (from_qn, to_qn) pairs to look up, in forward order.
	type hop struct{ from, to string }
	hops := make([]hop, 0, len(chain)-1)
	for i := 0; i < len(chain)-1; i++ {
		hops = append(hops, hop{from: chain[i], to: chain[i+1]})
	}

	// Single batched query: each pair is a (from_qn = ? AND to_qn = ?)
	// clause OR-ed together. We GROUP BY the same pair and pick the
	// lowest r.line as a deterministic representative (the previous
	// per-hop query used ORDER BY r.line LIMIT 1). The (to_qn, line,
	// kind) leading columns are stitched with the symbolWithFileSelect
	// columns so scanSymbolWithFile can decode the trailing slice.
	var b strings.Builder
	b.WriteString(`
		SELECT r.to_qualified, MIN(r.line), r.kind,
		       ` + store.SymbolWithFileSelect + `
		FROM refs r
		JOIN symbols s ON s.id = r.from_symbol
		JOIN files f ON f.id = s.file_id
		WHERE `)
	args := make([]any, 0, 2*len(hops))
	for i, h := range hops {
		if i > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString("(s.qualified_name = ? AND r.to_qualified = ?)")
		args = append(args, h.from, h.to)
	}
	b.WriteString(` GROUP BY s.qualified_name, r.to_qualified`)

	rows, err := s.DB().QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type refKey struct{ from, to string }
	found := make(map[refKey]model.RefSite, len(hops))
	for rows.Next() {
		var tqn, kind string
		var line int
		swf, err := store.ScanRefFromSymbolAt(rows, &tqn, &line, &kind)
		if err != nil {
			return nil, err
		}
		found[refKey{from: swf.QualifiedName, to: tqn}] = model.RefSite{
			FromSymbol: swf,
			ToQName:    tqn,
			Kind:       kind,
			Line:       line,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]model.RefSite, 0, len(hops))
	for _, h := range hops {
		if rs, ok := found[refKey{from: h.from, to: h.to}]; ok {
			out = append(out, rs)
			continue
		}
		// Synthesize from the symbol table.
		syms, err := queries.SymbolByQName(ctx, s, h.to)
		if err != nil || len(syms) == 0 {
			out = append(out, model.RefSite{ToQName: h.to, Kind: string(api.RefCall)})
			continue
		}
		out = append(out, model.RefSite{FromSymbol: syms[0], ToQName: h.to, Kind: string(api.RefCall)})
	}
	return out, nil
}

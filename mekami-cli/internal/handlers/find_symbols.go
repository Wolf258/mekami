// find_symbols.go implements the `find_symbols` MCP tool / CLI
// command. It exposes the existing queries.SearchSymbols function
// (search.go) which already supports substring search over
// `symbols.name` with optional kind and path-prefix filters.
//
// This tool is NOT a code search engine: it only matches against
// declared symbol names, not arbitrary source text. The README
// still says "Mekami is not a code search engine" — this tool
// closes the narrowest possible gap (the LLM can find a symbol
// by partial name without knowing the qualified name up front).
package handlers

import (
	"context"
	"fmt"

	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

func findSymbols(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	q := args.GetString("query", "")
	if q == "" {
		return AsResult("query is required", nil), nil
	}
	kind := args.GetString("kind", "")
	pathPrefix := args.GetString("path_prefix", "")
	limit := args.GetInt("limit", 50)
	syms, err := queries.SearchSymbols(ctx, s, q, kind, pathPrefix, limit)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return AsResult(fmt.Sprintf("no symbols matching %q (kind=%q, path_prefix=%q)", q, kind, pathPrefix), nil), nil
	}
	cap := capFor(len(syms), args, format.KindSymbols)
	return AsResult(format.SymbolList(syms, cap), payloadOrString(syms, cap)), nil
}

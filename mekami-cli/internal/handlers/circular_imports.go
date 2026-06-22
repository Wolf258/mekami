// circular_imports.go implements the `circular_imports` MCP tool /
// CLI command. It detects cycles in the package import graph,
// restricted to packages indexed in the project (not stdlib or
// external dependencies).
package handlers

import (
	"context"
	"fmt"

	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

func circularImports(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	cycles, err := queries.ImportCycles(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(cycles) == 0 {
		return AsResult("no circular imports detected", nil), nil
	}
	cap := capFor(len(cycles), args, format.KindCycles)
	return AsResult(format.Cycles(cycles, cap), payloadOrString(cycles, cap)), nil
}

// silence the unused import if we add helpers later
var _ = fmt.Sprintf

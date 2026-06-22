// Package handlers_shared holds the single source of truth for
// graph-read dispatch. Both the CLI runner (cmd/mekami/runner.go)
// and the MCP server (internal/mcp/server.go) need to map a
// tool name to the matching handler. Centralizing the switch
// here means a new tool requires one edit, not two.
package handlers

import (
	"context"
	"fmt"

	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

// DispatchRead is the single dispatch point for graph-read tools.
// Tool name is the MCP `Name` field of the Spec (snake_case).
// The CLI runner and the MCP server both call this.
func DispatchRead(ctx context.Context, s *store.Store, name string, args naming.ArgMap) (any, error) {
	switch name {
	case "get_symbol":
		return GetSymbol(ctx, s, args)
	case "who_calls":
		return WhoCalls(ctx, s, args)
	case "what_calls":
		return WhatCalls(ctx, s, args)
	case "list_file":
		return ListFile(ctx, s, args)
	case "trace_calls":
		return TraceCalls(ctx, s, args)
	case "list_files":
		return ListFiles(ctx, s, args)
	case "list_package":
		return ListPackage(ctx, s, args)
	case "list_importers":
		return ListImporters(ctx, s, args)
	case "list_modules":
		return ListModules(ctx, s, args)
	case "show_modules":
		return ShowModules(ctx, s, args)
	case "show_changes":
		return ShowChanges(ctx, s, args)
	case "index_status":
		return IndexStatus(ctx, s, args)
	case "find_symbols":
		return FindSymbols(ctx, s, args)
	case "unused":
		return Unused(ctx, s, args)
	case "circular_imports":
		return CircularImports(ctx, s, args)
	case "dependents":
		return Dependents(ctx, s, args)
	case "type_hierarchy":
		return TypeHierarchy(ctx, s, args)
	}
	return nil, fmt.Errorf("unknown read command %q", name)
}

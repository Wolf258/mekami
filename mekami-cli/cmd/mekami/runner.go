package mekami

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-cli/internal/handlers"
	"github.com/Wolf258/mekami-cli/internal/naming"
	"github.com/spf13/cobra"
)

// runCommand returns a cobra RunE that dispatches a top-level
// read command to the matching handler in internal/handlers.
// The CLI does the same work as the MCP server, just with the
// args encoded as cobra flags and the response written to stdout
// (or rendered as formatted text).
func runCommand(spec *naming.Spec) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		switch spec.Use {
		case "init":
			return runInit(ctx, cmd, args)
		case "serve":
			return runServe(ctx, cmd)
		case "build":
			return runBuild(ctx, cmd)
		case "stats":
			return runStats(ctx, cmd)
		case "start", "stop", "status", "restart", "reload", "logs":
			return runDaemon(spec.Use, ctx, cmd, args)
		case "service":
			return runService(ctx, args)
		case "mcp-install":
			return runMCPInstall(ctx, args, flagVals(cmd, spec))
		case "mcp-uninstall":
			return runMCPUninstall(ctx, args)
		case "mcp-test":
			return runMCPTest(ctx, cmd)
		case "core-install <lang>[@<version>]":
			return runCoreInstall(ctx, cmd, args)
		case "core-list":
			return runCoreList(ctx, cmd)
		}
		// Default: graph read.
		return runGraphRead(ctx, cmd, spec, args)
	}
}

// hiddenRunner is a stub for the hidden commands (daemon entry
// point, supervisor control, service-install/uninstall). Each
// has its own RunE supplied by the relevant file. The cobra
// spec only carries the metadata.
func hiddenRunner(spec *naming.Spec) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		switch spec.Use {
		case "_daemon":
			return runDaemonEntry(cmd)
		case "supervise":
			return runSupervise(cmd.Context(), args)
		case "service-install":
			return runServiceInstall()
		case "service-uninstall":
			return runServiceUninstall()
		}
		return fmt.Errorf("unhandled hidden command %q", spec.Use)
	}
}

// flagVals reads all flags declared in spec from cmd and returns
// them as a snake_case ArgMap. The runner passes this map to
// dispatchTable.
func flagVals(cmd *cobra.Command, spec *naming.Spec) naming.ArgMap {
	out := naming.ArgMap{}
	for _, f := range spec.Flags {
		name := naming.Kebab(f.Name)
		switch f.Type {
		case "string":
			s, _ := cmd.Flags().GetString(name)
			out[f.Name] = s
		case "int":
			n, _ := cmd.Flags().GetInt(name)
			out[f.Name] = n
		case "bool":
			b, _ := cmd.Flags().GetBool(name)
			out[f.Name] = b
		case "stringSlice":
			xs, _ := cmd.Flags().GetStringSlice(name)
			out[f.Name] = xs
		}
	}
	return out
}

// runGraphRead opens the graph store, dispatches the read, and
// prints the result. With --json, prints raw JSON; otherwise
// formats as human-readable text (when the handler returned a
// string) or pretty-prints JSON.
func runGraphRead(ctx context.Context, cmd *cobra.Command, spec *naming.Spec, args []string) error {
	// Decode positional args (with type validation) BEFORE opening
	// the store so a bad integer or missing required arg fails fast
	// with a usage error, instead of leaking a runtime error from
	// openStore.
	am, err := decodePositionalArgs(spec, args)
	if err != nil {
		return err
	}
	for k, v := range flagVals(cmd, spec) {
		am[k] = v
	}

	s, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out, err := dispatchRead(ctx, s, spec.Name, am)
	if err != nil {
		// User-input errors are returned as text results; surface
		// them to stderr and exit 2.
		if msg := handlers.SourceError(err); msg != "" {
			return cliError{code: 2, msg: msg}
		}
		return cliError{code: 1, msg: err.Error()}
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		return printJSON(out)
	}
	if s, ok := out.(string); ok {
		fmt.Fprint(os.Stdout, s)
		if len(s) > 0 && s[len(s)-1] != '\n' {
			fmt.Fprintln(os.Stdout)
		}
		return nil
	}
	return printJSON(out)
}

// decodePositionalArgs walks spec.Args and assigns each non-MCPOnly
// arg from the cobra args slice. MCPOnly args are skipped — they are
// wire-only and the handler falls back to its default for absent
// values. Optional args that the user did not supply are skipped
// (the handler's default applies).
//
// A type mismatch on an int arg is returned as a cliError with code
// 2 (usage error) so the LLM gets a precise hint instead of a
// downstream "value out of range" message.
func decodePositionalArgs(spec *naming.Spec, args []string) (naming.ArgMap, error) {
	am := naming.ArgMap{}
	for _, a := range spec.Args {
		if a.MCPOnly {
			continue
		}
		// Optional positionals may be absent; consume from the slice
		// only when the user actually supplied one. Cobra's RangeArgs
		// validator already guarantees the count, so this is just a
		// defensive guard against the trailing args[1:] slice op.
		if a.Optional && len(args) == 0 {
			continue
		}
		raw := args[0]
		args = args[1:]
		switch a.Type {
		case "int":
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, cliError{code: 2, msg: fmt.Sprintf("%s: invalid integer %q for %s", spec.Use, raw, a.Name)}
			}
			am[a.Name] = n
		default:
			am[a.Name] = raw
		}
	}
	return am, nil
}

// dispatchRead is the CLI-side mirror of mcp.dispatch: each MCP
// tool name maps to the same handler.
func dispatchRead(ctx context.Context, s *store.Store, name string, args naming.ArgMap) (any, error) {
	switch name {
	case "find_symbol":
		return handlers.FindSymbol(ctx, s, args)
	case "get_symbol":
		return handlers.GetSymbol(ctx, s, args)
	case "show_body":
		return handlers.ShowBody(ctx, s, args)
	case "show_lines":
		return handlers.ShowLines(ctx, s, args)
	case "who_calls":
		return handlers.WhoCalls(ctx, s, args)
	case "what_calls":
		return handlers.WhatCalls(ctx, s, args)
	case "list_file":
		return handlers.ListFile(ctx, s, args)
	case "trace_calls":
		return handlers.TraceCalls(ctx, s, args)
	case "list_files":
		return handlers.ListFiles(ctx, s, args)
	case "list_package":
		return handlers.ListPackage(ctx, s, args)
	case "list_package_symbols":
		return handlers.ListPackageSymbols(ctx, s, args)
	case "list_importers":
		return handlers.ListImporters(ctx, s, args)
	case "list_modules":
		return handlers.ListModules(ctx, s, args)
	case "show_modules":
		return handlers.ShowModules(ctx, s, args)
	case "show_changes":
		return handlers.ShowChanges(ctx, s, args)
	case "find_text":
		return handlers.FindText(ctx, s, args)
	case "index_status":
		return handlers.IndexStatus(ctx, s, args)
	}
	return nil, fmt.Errorf("unknown read command %q", name)
}

func firstOrEmpty(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// cliError is the error type cobra prints to stderr and exits
// with the given code. The codes follow the BSD conventions:
//
//	0   success (returned as nil)
//	1   runtime / io error
//	2   user input error (not found, bad symbol name, etc.)
//	64  usage error (cobra already does this)
type cliError struct {
	code int
	msg  string
}

func (e cliError) Error() string { return e.msg }

// _ keeps the import set in sync if a future refactor drops one
// of the helpers above.
var _ = openStore

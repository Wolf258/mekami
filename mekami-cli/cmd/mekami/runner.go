package mekami

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/handlers"
	"github.com/Wolf258/mekami-cli/internal/naming"
	"github.com/spf13/cobra"
)

// buildRunners returns the full DispatcherKey -> runner map used
// by registerAll (via buildSubcommandTree). Adding a new Spec
// means adding a new entry here AND a matching DispatcherKey on
// the Spec. The wrapper closures are the place that adapts the
// cobra signature (cmd, args) to each run*'s internal shape
// (some take ctx, some take flagVals, some take the spec for
// the graph read).
//
// Graph-read commands (find, show, who-calls, ...) are NOT
// listed here individually. They are dispatched through
// runGraphRead, which knows how to handle the full
// Spec -> handler mapping. Each Spec in that family sets a
// DispatcherKey like "find" or "show-body"; for those keys
// buildRunners returns a wrapper that defers to
// runGraphRead. This keeps the registry short: the Spec
// itself carries the (Name, Args, Flags) that runGraphRead
// needs, so duplicating them in this map would be redundant.
//
// The map is the single source of truth for "what code runs
// for this DispatcherKey". cobra spec changes (Use, Parent,
// Args, Flags) do not require touching this map; the wrappers
// below resolve the spec from the cmd via cmd.Name() when
// they need it. In practice, only the graph-read wrapper and
// the mcp.install wrapper need the spec for arg decoding —
// every other runner already has the cmd and can read flags
// directly.
func buildRunners() map[string]naming.CobraRunner {
	daemon := func(name string) naming.CobraRunner {
		return func(cmd *cobra.Command, args []string) error {
			return runDaemon(name, cmd.Context(), cmd, args)
		}
	}
	graphRead := func(cmd *cobra.Command, args []string) error {
		spec := lookupSpecByCmd(cmd)
		if spec == nil {
			return fmt.Errorf("internal: no spec matches %q", cmd.Name())
		}
		return runGraphRead(cmd.Context(), cmd, spec, args)
	}
	runners := map[string]naming.CobraRunner{
		// ── top-level lifecycle ────────────────────────────────
		"init":  func(cmd *cobra.Command, args []string) error { return runInit(cmd.Context(), cmd, args) },
		"serve": func(cmd *cobra.Command, args []string) error { return runServe(cmd.Context(), cmd) },
		"build": func(cmd *cobra.Command, args []string) error { return runBuild(cmd.Context(), cmd) },
		"stats": func(cmd *cobra.Command, args []string) error { return runStats(cmd.Context(), cmd) },

		// ── daemon controls (top-level) ────────────────────────
		"start":   daemon("start"),
		"stop":    daemon("stop"),
		"status":  daemon("status"),
		"restart": daemon("restart"),
		"reload":  daemon("reload"),
		"logs":    daemon("logs"),

		// ── service subcommand group ───────────────────────────
		"service.install":   func(cmd *cobra.Command, args []string) error { return runServiceInstall() },
		"service.uninstall": func(cmd *cobra.Command, args []string) error { return runServiceUninstall() },
		"service.status":    func(cmd *cobra.Command, args []string) error { return runServiceStatus(cmd) },

		// ── mcp subcommand group ───────────────────────────────
		"mcp.install": func(cmd *cobra.Command, args []string) error {
			spec := lookupSpecByCmd(cmd)
			return runMCPInstall(cmd.Context(), args, flagVals(cmd, spec))
		},
		"mcp.uninstall": func(cmd *cobra.Command, args []string) error { return runMCPUninstall(cmd.Context(), args) },
		"mcp.test":      func(cmd *cobra.Command, args []string) error { return runMCPTest(cmd.Context(), cmd) },

		// ── core subcommand group ──────────────────────────────
		"core.install":   func(cmd *cobra.Command, args []string) error { return runCoreInstall(cmd.Context(), cmd, args) },
		"core.list":      func(cmd *cobra.Command, args []string) error { return runCoreList(cmd.Context(), cmd) },
		"core.uninstall": func(cmd *cobra.Command, args []string) error { return runCoreUninstall(cmd.Context(), cmd, args) },
		"core.status":    func(cmd *cobra.Command, args []string) error { return runCoreStatus(cmd.Context(), cmd) },

		// ── hidden / internal re-exec entry points ─────────────
		// Hidden commands: the user cannot invoke them, but
		// buildSubcommandTree still requires a runner entry.
		// The supervisor flow (supervise start/stop/...) is
		// handled inside the same hidden command by parsing
		// args[0]; the runner delegates to runSupervise.
		"_daemon":   func(cmd *cobra.Command, args []string) error { return runDaemonEntry(cmd) },
		"supervise": func(cmd *cobra.Command, args []string) error { return runSupervise(cmd.Context(), args) },
	}
	// Wire every graph-read Spec to the shared graphRead
	// wrapper. The set of graph-read DispatcherKeys is the
	// list of Specs whose Name (the MCP tool name) is set —
	// those are the Specs that have a handler in
	// internal/handlers. Adding a new graph read means
	// adding a Spec with Name!=""; the wiring is automatic.
	for i := range naming.Specs {
		s := &naming.Specs[i]
		if s.Name == "" {
			continue
		}
		if _, exists := runners[s.DispatcherKey]; exists {
			continue
		}
		runners[s.DispatcherKey] = graphRead
	}
	return runners
}

// lookupSpecByCmd finds the Spec whose Use starts with cmd.Name()
// and whose Parent matches the parent cmd. The Use field carries
// placeholder tokens like "<query>" that we strip before comparing
// against cmd.Name() (cobra drops the placeholders from cmd.Name
// when it builds the command tree, so a literal == match fails
// for every spec with positional args).
//
// Parent matching is a small concession to the cobra tree shape:
// cobra attaches every top-level subcommand to the binary root,
// whose Name() is the binary name ("mekami"). Specs at the top
// level declare Parent=="", so a literal Name() comparison never
// matches. We translate "the cmd lives under the binary root" to
// Parent=="" by checking the parent against the Parents map: only
// registered carrier parents (mcp, core, service) carry a Parent
// field in the spec; anything else is the binary root.
func lookupSpecByCmd(cmd *cobra.Command) *naming.Spec {
	parentKey := ""
	if cmd.Parent() != nil {
		pname := cmd.Parent().Name()
		if _, isCarrier := naming.Parents[pname]; isCarrier {
			parentKey = pname
		}
	}
	wanted := cmd.Name()
	for i := range naming.Specs {
		s := &naming.Specs[i]
		if s.Parent != parentKey {
			continue
		}
		head := s.Use
		if i := strings.IndexByte(head, ' '); i >= 0 {
			head = head[:i]
		}
		if head == wanted {
			return s
		}
	}
	return nil
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
//
// After the Result refactor, every handler returns a Result
// with a Text view (compact) and a Data view (structured). The
// runner chooses which one to print: --json picks Data, the
// default picks Text. The same shape feeds the MCP server
// (see internal/mcp/server.go makeHandler) so CLI and MCP
// share a single serialization path.
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
		return printJSON(handlers.ExtractData(out))
	}
	text := handlers.TextView(out)
	if text == "" {
		// Fallback for handlers that haven't been migrated to
		// Result yet (or returned nil). Serialize whatever we
		// got so the caller still gets a parseable payload.
		return printJSON(handlers.ExtractData(out))
	}
	fmt.Fprint(os.Stdout, text)
	if len(text) > 0 && text[len(text)-1] != '\n' {
		fmt.Fprintln(os.Stdout)
	}
	return nil
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

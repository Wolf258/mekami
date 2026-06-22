package naming

// Specs is the ordered list of every user-facing command. The order
// here drives the order in `mekami --help`. Add new commands at the
// end of their logical group, not in the middle, to keep the help
// output stable.
//
// The list is split into seven sections below:
//
//  1. Lifecycle (top-level): init, serve, build, stats.
//  2. Graph reads (top-level): show, who-calls, what-calls,
//     trace, list-*, show-modules, show-changes, index-status.
//  3. Daemon controls (top-level): start, stop, status, restart,
//     reload, logs.
//  4. service subcommand group: install, uninstall, status.
//  5. mcp subcommand group: install, uninstall, test.
//  6. core subcommand group: install, list, uninstall, status.
//  7. Hidden / internal re-exec entry points: _daemon, supervise.
var Specs = []Spec{
	// ───── 1. Lifecycle ─────────────────────────────────────────
	{
		Use:           "init",
		DispatcherKey: "init",
		Short:         "Create .mekami/config.json, pick language cores, and run an initial build",
		Long: `Create .mekami/config.json with sensible defaults, persist the
set of language cores selected for this project, and run an
initial build so the graph database is ready before the daemon
(if any) starts.

By default init picks every core registered in the running
binary (all-available). With --lang <name>[,<name>...] you can
restrict the selection to a subset. The selection is written
to the indexers field of .mekami/config.json, so a subsequent
mekami build can resolve a language without flags.

Re-running init is idempotent: existing indexers are kept when
--lang is omitted, and overwritten by the explicit list when
--lang is provided.

init requires at least one core to be registered in the
binary; otherwise it errors out and points at core install
and ./build.sh.`,
		Flags: []Flag{
			{Name: "lang", Type: "stringSlice", Default: "", Description: "comma-separated language list (default: all cores registered in this binary)", CLIOnly: true},
			{Name: "daemon", Type: "string", Default: "auto", Description: "start the watcher daemon: auto|yes|no"},
			{Name: "yes", Type: "bool", Default: "false", Description: "assume yes for the daemon prompt in non-interactive shells", CLIOnly: true},
			{Name: "verbose", Type: "bool", Default: "false", Description: "show build progress during init", CLIOnly: true},
		},
	},
	{
		Use:           "serve",
		DispatcherKey: "serve",
		Short:         "Run the MCP server (stdio)",
		Long:          "Start the mekami MCP server on stdio. Connect an agent client to this process.",
	},
	{
		Use:           "build",
		DispatcherKey: "build",
		Short:         "Build the code graph database",
		Long:          "Walk the source tree, parse files, and write the graph to ./.mekami/graph.db (or --db).",
		Flags: []Flag{
			{Name: "root", Type: "string", Default: "", Description: "source root (default: cwd)", CLIOnly: true},
			{Name: "lang", Type: "string", Default: "", Description: "language to ingest; defaults to the single indexer in .mekami/config.json or 'go' (builtin)"},
			{Name: "clean", Type: "bool", Default: "false", Description: "delete db and rebuild from scratch"},
			{Name: "quiet", Type: "bool", Default: "false", Description: "suppress per-file progress", CLIOnly: true},
			{Name: "jobs", Type: "int", Default: "0", Description: "parse workers (0 = NumCPU)"},
		},
	},
	{
		Use:           "stats",
		DispatcherKey: "stats",
		Short:         "Show graph database statistics",
		Long:          "Print per-table row counts plus the last_root pointer. Use --json for machine-readable output.",
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
		},
	},

	// ───── 2. Graph reads (top-level) ───────────────────────────
	{
		Name:          "get_symbol",
		Use:           "show <qualified_name>",
		DispatcherKey: "show",
		Short:         "Show a symbol's source",
		Long: `Show a symbol by qualified name. Default output includes the
file:line header and the source body. Use --body to print only the
numbered body, or --header to print only the file:line header.

PREFERRED over ` + "`grep`+`read`" + ` when the qualified name is already known:
one call returns the body with exact file:line, vs two calls and a manual
line alignment. The --body cap is --max_lines (default 200). For raw
context around a known location that is not tied to a single symbol,
use ` + "`sed -n A,Bp`" + ` from a shell.`,
		Args: []Arg{
			{Name: "qualified_name", Description: "fully qualified symbol name"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "body", Type: "bool", Default: "false", Description: "print only the numbered body", CLIOnly: true},
			{Name: "header", Type: "bool", Default: "false", Description: "print only the file:line header", CLIOnly: true},
			{Name: "max_lines", Type: "int", Default: "200", Description: "max lines to read"},
		},
	},
	{
		Name:          "who_calls",
		Use:           "who-calls <qualified_name>",
		DispatcherKey: "who-calls",
		Short:         "List who calls this symbol (incoming references)",
		Long: `Find references to a qualified name (call sites, type uses,
value reads, embeds, imports). By default returns ALL ref kinds;
pass --ref-kind to filter (call|type-use|value|field|embed|import).`,
		Args: []Arg{
			{Name: "qualified_name", Description: "fully qualified symbol name"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "ref_kind", Type: "string", Default: "", Description: "filter on the kind of reference edge"},
			{Name: "path_prefix", Type: "string", Default: "", Description: "filter by file path prefix"},
			{Name: "limit", Type: "int", Default: "0", Description: "max results (default 100)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "what_calls",
		Use:           "what-calls <qualified_name>",
		DispatcherKey: "what-calls",
		Short:         "List what this symbol calls (outgoing references)",
		Long: `List distinct qualified names referenced by a given symbol
(outgoing calls and type uses). ref_kind is ignored for outgoing
edges.`,
		Args: []Arg{
			{Name: "qualified_name", Description: "fully qualified symbol name"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "path_prefix", Type: "string", Default: "", Description: "filter by file path prefix"},
			{Name: "limit", Type: "int", Default: "0", Description: "max results (default 50)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "list_file",
		Use:           "list-file <file_path>",
		DispatcherKey: "list-file",
		Short:         "List all symbols in a file",
		Long: `List top-level declarations in a file (func, type, var, const,
method), ordered by line. Path accepts exact or suffix match.`,
		Args: []Arg{
			{Name: "path", Description: "relative file path inside the indexed project"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "trace_calls",
		Use:           "trace <from_qn> <to_qn>",
		DispatcherKey: "trace",
		Short:         "Trace the call path between two symbols (BFS)",
		Long: `BFS over call edges to find a shortest path from one qualified
name to another. Both endpoints must exist in the index — a typo
returns a clear 'symbol not found' error rather than an empty result.

Returns CALL SITES (the file:line where one symbol invokes the next),
not the definitions of the symbols along the chain. If you need the
definition of the target, follow up with ` + "`show`" + ` on the last
qualified name in the path.`,
		Args: []Arg{
			{Name: "from", Description: "qualified name of source symbol"},
			{Name: "to", Description: "qualified name of target symbol"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "max_depth", Type: "int", Default: "6", Description: "max BFS depth"},
		},
	},
	{
		Name:          "list_files",
		Use:           "list-files [prefix]",
		DispatcherKey: "list-files",
		Short:         "List files in the project tree from the indexed snapshot",
		Long: `Project file tree from the indexed snapshot. Returns nested
{name, path, type, children} JSON. Default max_depth is 12 on the
CLI; pass 0 for unlimited.`,
		Args: []Arg{
			{Name: "prefix", Description: "only show sub-tree starting at this path", MCPOnly: true},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "max_depth", Type: "int", Default: "12", Description: "max directory depth to expand (0 = unlimited)"},
			{Name: "include", Type: "stringSlice", Default: "", Description: "file extensions to include (e.g. go,md)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "list_package",
		Use:           "list-package <import_path>",
		DispatcherKey: "list-package",
		Short:         "List all symbols in a package",
		Long: `List all top-level symbols in a package, ordered by file then
line. Use this to see the public surface of a package once you
know its package_id. Pass --json for a structured list with file:line.

Accepts the canonical Go import path (e.g. ` + "`github.com/Wolf258/mekami-cli/internal/mcp`" + `)
OR a short suffix/relative path (e.g. ` + "`internal/mcp`" + `, ` + "`mcp`" + `) — the tool
resolves it against the indexed modules. If unsure, call ` + "`list_modules`" + ` first
to see what's indexed, or pass the shortest unambiguous suffix.

PREFERRED over ` + "`bash`+`ls`" + ` / ` + "`grep`" + ` for "list everything in this package" tasks:
one call returns every top-level symbol with file:line, vs several
shell calls that often miss non-exported symbols or misalign line numbers.`,
		Args: []Arg{
			{Name: "package_id", Description: "package identifier (Go: import path)"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "kinds", Type: "stringSlice", Default: "", Description: "filter by symbol kinds (func,type,var,const,method)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "list_importers",
		Use:           "list-importers <import_path>",
		DispatcherKey: "list-importers",
		Short:         "List packages in this project that import the given package",
		Long: `List packages in this project that import the given
package_id. Returns JSON list of packages.`,
		Args: []Arg{
			{Name: "package_id", Description: "package identifier (Go: import path)"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "list_modules",
		Use:           "list-modules",
		DispatcherKey: "list-modules",
		Short:         "List the Go modules indexed in the graph (JSON)",
		Long: `List the Go modules indexed in the graph. In a workspace
returns every use'd module; in a single-module repo returns that
one module. Call this before show-modules to discover what the
graph covers.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "show_modules",
		Use:           "show-modules",
		DispatcherKey: "show-modules",
		Short:         "Show a summary of the indexed modules",
		Long: `High-level summary of the indexed modules: each package with
file/symbol/export counts. First call when exploring a new
project.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "show_changes",
		Use:           "show-changes",
		DispatcherKey: "show-changes",
		Short:         "Show files added/modified/removed since the last build",
		Long: `Show files added, modified, removed, or made inaccessible
since the last 'mekami build'. Reads the filesystem and compares
to the indexed snapshot. Use this to detect when the index is
stale.`,
		Flags: []Flag{
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30). Truncated responses report the total count and a hint."},
		},
	},
	{
		Name:          "index_status",
		Use:           "index-status",
		DispatcherKey: "index-status",
		Short:         "Snapshot of the index (last_root, last_build_at, counts)",
		Long: `Snapshot of the index: last_root, last_build_at, is_workspace,
root_module, and per-table counts. Use this to check whether the
DB exists, when it was last refreshed, and what the graph covers
before running other tools. If no build has been run yet,
returns the 'no last_root' error.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
		},
	},
	{
		Name:          "find_symbols",
		Use:           "find-symbols <query>",
		DispatcherKey: "find-symbols",
		Short:         "Find symbols by name substring",
		Long: `Search symbols by name substring. Matches the symbol's
declared name (not arbitrary source text), with optional filters
on kind (func|type|var|const|method) and file path prefix.

PREFERRED over grep when you know a symbol exists but not its
exact qualified name. For example, "find-symbols NewReader"
returns every symbol whose name contains "NewReader" across the
index, with file:line and signature in the result.`,
		Args: []Arg{
			{Name: "query", Description: "substring to search in symbol name"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "kind", Type: "string", Default: "", Description: "filter on symbol kind (func|type|var|const|method)"},
			{Name: "path_prefix", Type: "string", Default: "", Description: "file path prefix filter"},
			{Name: "limit", Type: "int", Default: "50", Description: "max results (default 50)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30)"},
		},
	},
	{
		Name:          "circular_imports",
		Use:           "circular-imports",
		DispatcherKey: "circular-imports",
		Short:         "Detect circular import cycles between packages",
		Long: `Walk the package import graph and report every cycle
found between packages indexed in this project. Stdlib and
external dependencies are excluded — only the project's own
packages participate. Output: one cycle per line, starting with
the lexicographically smallest package_id for stable diffs.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30)"},
		},
	},
	{
		Name:          "unused",
		Use:           "unused",
		DispatcherKey: "unused",
		Short:         "List exported symbols with no incoming references",
		Long: `List exported symbols that have no incoming references of
any kind. Output is a CANDIDATE list for dead code — the entry-
point filter suppresses main, init, _test.go, fmt.Stringer/error
implementations, and common stdlib interface methods, but a
verified who_calls is still required before removal.

PREFERRED over grep -L when you want a structured set of
candidates with file:line.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "include_tests", Type: "bool", Default: "false", Description: "include _test.go symbols and Test*/Benchmark*/Example*/Fuzz* names"},
			{Name: "include_unexported", Type: "bool", Default: "false", Description: "include unexported symbols"},
			{Name: "limit", Type: "int", Default: "200", Description: "max results (default 200)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30)"},
		},
	},
	{
		Name:          "type_hierarchy",
		Use:           "type-hierarchy <type>",
		DispatcherKey: "type-hierarchy",
		Short:         "Show members of a type, or types implementing an interface",
		Long: `Two related views on a Go type:

  --mode=members        Methods and funclits whose parent is <type>.
  --mode=implementers   Types that NAME <type> in a type-use ref.
                        Structural implementers (types that satisfy
                        the interface without naming it) are NOT
                        reported — Go's interfaces are duck-typed.
  --mode=all            Both sections in one response.

PREFERRED over go doc -all for "what implements io.Reader in
this project" — go doc only sees the standard library, not the
project's own types.`,
		Args: []Arg{
			{Name: "type", Description: "qualified name of a type or interface"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "mode", Type: "string", Default: "all", Description: "members|implementers|all"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30)"},
		},
	},
	{
		Name:          "dependents",
		Use:           "dependents <target>",
		DispatcherKey: "dependents",
		Short:         "Tree of symbols/packages/modules affected by a change to <target>",
		Long: `BFS over the reference graph to answer "what is affected if
I change X". The target is interpreted according to --level:

  --level=symbol   target is a qualified_name; BFS follows refs
                   of --ref-kind (default: all kinds). Use
                   --direction=callees for "what does X touch".
  --level=package  target is a package_id; BFS follows imports
                   in the importers direction. "If I move this
                   package, who breaks?"
  --level=module   target is a module path; BFS follows modules
                   that import any of the target's packages.

The result is a tree (not a flat list) so the LLM can see
distance from the target. Bounded by --max-depth and
--max-nodes.`,
		Args: []Arg{
			{Name: "target", Description: "qualified_name (symbol), package_id (package), or module path (module)"},
		},
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
			{Name: "level", Type: "string", Default: "symbol", Description: "symbol|package|module"},
			{Name: "direction", Type: "string", Default: "callers", Description: "callers|callees (symbol level only)"},
			{Name: "transitive", Type: "bool", Default: "true", Description: "BFS beyond direct callers (default: true)"},
			{Name: "ref_kind", Type: "string", Default: "", Description: "filter on ref kind (call|type-use|value|import)"},
			{Name: "path_prefix", Type: "string", Default: "", Description: "filter by file path prefix (symbol level, direction=callees)"},
			{Name: "max_depth", Type: "int", Default: "4", Description: "BFS depth limit"},
			{Name: "max_nodes", Type: "int", Default: "500", Description: "node cap (prevents explosion on dense graphs)"},
			{Name: "head", Type: "int", Default: "30", Description: "max items in output (0 = unlimited; default 30)"},
		},
	},

	// ───── 3. Daemon controls (top-level) ───────────────────────
	{
		Use:           "start",
		DispatcherKey: "start",
		Short:         "Start the watcher daemon for the current project",
		Long: `Ask the supervisor to spawn a watcher daemon for the current
project. The supervisor is started if it is not already running.
Idempotent: re-running on a project that already has a daemon is
a no-op.`,
	},
	{
		Use:           "stop",
		DispatcherKey: "stop",
		Short:         "Stop the watcher daemon for the current project",
		Long:          "Ask the supervisor to stop the daemon for the current project. Exits 0 if the daemon was not running.",
	},
	{
		Use:           "status",
		DispatcherKey: "status",
		Short:         "Show the watcher daemon status",
		Long:          "Print the daemon's status: PID, uptime, source, batch counters, last batch timestamp. Use --json for machine-readable output.",
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout", CLIOnly: true},
		},
	},
	{
		Use:           "restart",
		DispatcherKey: "restart",
		Short:         "Restart the watcher daemon for the current project",
		Long:          "Stop the daemon, then start it again. Use this after changing `lang` or `on_start` in the config.",
	},
	{
		Use:           "reload",
		DispatcherKey: "reload",
		Short:         "Reload .mekami/config.json for the current project",
		Long: `Re-read .mekami/config.json. Hot-only changes (debounce,
ignore, log, fallback) are pushed to the live daemon; cold changes
(on_start, lang) trigger a stop+start.`,
	},
	{
		Use:           "logs",
		DispatcherKey: "logs",
		Short:         "Follow the watcher daemon log",
		Long:          "Stream the watcher daemon's log file to stdout (uses `tail -F` under the hood).",
	},

	// ───── 4. service subcommand group ───────────────────────────
	{
		Use:           "install",
		Parent:        "service",
		DispatcherKey: "service.install",
		Short:         "Register the supervisor with the host init system",
		Long: `Register the per-user supervisor with the host's init system so
it starts automatically when you log in. On Linux this writes and
enables the systemd --user unit (~/.config/systemd/user/mekami-supervisor.service);
on macOS it installs a LaunchAgent plist under ~/Library/LaunchAgents/.
The supervisor is then started (best effort) so the unit is active
right away.`,
	},
	{
		Use:           "uninstall",
		Parent:        "service",
		DispatcherKey: "service.uninstall",
		Short:         "Unregister the supervisor from the host init system",
		Long: `Tear down what ` + "`service install`" + ` set up. The supervisor
is asked to stop every daemon cleanly via IPC, then the unit/agent
is disabled (Linux: ` + "`systemctl --user disable --now`" + `, macOS:
` + "`launchctl unload`" + `) and the unit/agent file is removed.
Stale runtime state files (pid, socket, log, sentinel) are cleaned
up so a future ` + "`service install`" + ` starts from a clean slate.`,
	},
	{
		Use:           "status",
		Parent:        "service",
		DispatcherKey: "service.status",
		Short:         "Show the supervisor's init-system registration status",
		Long: `Report whether the per-user supervisor is registered with the
host init system (systemd --user on Linux, LaunchAgent on macOS),
whether it is enabled, whether it is active, and where its runtime
state lives. Use this to confirm ` + "`service install`" + ` set up the
unit/agent correctly, or to check whether ` + "`service uninstall`" + `
cleaned it up.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout"},
		},
	},

	// ───── 5. mcp subcommand group ──────────────────────────────
	{
		Use:           "install",
		Parent:        "mcp",
		DispatcherKey: "mcp.install",
		Short:         "Register the mekami MCP server in the host client",
		Long: `Register the mekami MCP server in the host's agent client
(OpenCode today). The registered command is ["mekami", "serve"],
so the config is portable: as long as 'mekami' is on PATH, the
client can start the server.

Pass --binary to pin the entry to an absolute path (e.g. when
running from a clone without installing).`,
		Flags: []Flag{
			{Name: "binary", Type: "string", Default: "", Description: "absolute path to the mekami binary"},
			{Name: "name", Type: "string", Default: "mekami", Description: "server name to register"},
			{Name: "disable", Type: "bool", Default: "false", Description: "register with enabled=false"},
			{Name: "env", Type: "stringSlice", Default: "", Description: "environment variables in KEY=VALUE form (repeatable)"},
		},
	},
	{
		Use:           "uninstall",
		Parent:        "mcp",
		DispatcherKey: "mcp.uninstall",
		Short:         "Remove the mekami MCP server entry from the host client",
		Long: `Tear down what ` + "`mcp install`" + ` set up. The matching entry
is removed and the original config file is restored from the
backup created at install time. Missing entries are reported as
a no-op so the command is idempotent.`,
	},
	{
		Use:           "test",
		Parent:        "mcp",
		DispatcherKey: "mcp.test",
		Short:         "Smoke test the MCP server end-to-end",
		Long: `Spawns ` + "`mekami serve`" + ` as a subprocess, connects over
stdio, lists tools, and calls a sample of them to verify the wire
works end-to-end against the local graph.`,
	},

	// ───── 6. core subcommand group ─────────────────────────────
	{
		Use:           "install <lang>[@<version>]",
		Parent:        "core",
		DispatcherKey: "core.install",
		Short:         "Install or upgrade a language indexer for this project",
		Long: `Resolve <lang> via the Go module proxy
(github.com/Wolf258/mekami-core-<lang>)@<version> (or @latest when
omitted), persist it to .mekami/config.json indexers[], and
regenerate mekami-core/frontend/all_gen/all_gen.go with a blank
import for it. Use ` + "`core list`" + ` to see what's installed.

The generated all_gen.go is picked up by the next build of the
mekami binary; core install does not recompile an already-
installed binary (AUR installs are read-only).`,
		Args: []Arg{
			{Name: "lang", Description: "language identifier (e.g. go, rust, c); optional @version suffix"},
		},
		Flags: []Flag{
			{Name: "version", Type: "string", Default: "", Description: "explicit version (e.g. v0.1.0); empty = @latest"},
		},
	},
	{
		Use:           "list",
		Parent:        "core",
		DispatcherKey: "core.list",
		Short:         "List language indexers active in this project",
		Long: `Reads .mekami/config.json indexers[] and lists the
registered frontends actually loaded into the running binary
(via api.Global.Names()). The two are compared so a config that
lists a language whose all_gen.go blank import is missing is
reported as missing.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout"},
		},
	},
	{
		Use:           "uninstall <lang>",
		Parent:        "core",
		DispatcherKey: "core.uninstall",
		Short:         "Remove a language indexer from this project",
		Long: `Remove <lang> from .mekami/config.json indexers[] and
regenerate mekami-core/frontend/all_gen/all_gen.go without the
blank import for it. Like ` + "`core install`" + `, this writes config
and generated code but does not recompile an already-installed
binary; the next ` + "`./build.sh`" + ` picks up the regenerated
all_gen.go.

Idempotent: removing a language that is not configured reports
a no-op rather than an error.`,
		Args: []Arg{
			{Name: "lang", Description: "language identifier to remove (e.g. go, rust, c)"},
		},
	},
	{
		Use:           "status",
		Parent:        "core",
		DispatcherKey: "core.status",
		Short:         "Show configured vs loaded language indexers",
		Long: `Report which language cores are listed in
.mekami/config.json and which frontends are actually linked into
the running binary. A language is "missing" when it is configured
but the binary's all_gen.go does not blank-import it; rebuilding
(or running ` + "`core install <lang>`" + `) fixes that.

Use ` + "`--json`" + ` for a machine-readable report.`,
		Flags: []Flag{
			{Name: "json", Type: "bool", Default: "false", Description: "emit JSON to stdout"},
		},
	},

	// ───── 7. Hidden / internal ──────────────────────────────────
	{
		Use:           "_daemon",
		DispatcherKey: "_daemon",
		Short:         "Internal watcher daemon entry point (do not invoke directly)",
		Hidden:        true,
	},
	{
		Use:           "supervise [subcommand]",
		DispatcherKey: "supervise",
		Args:          []Arg{{Name: "subcommand", Optional: true}},
		Short:         "Internal supervisor control (do not invoke directly)",
		Long:          "Hidden entry point used by the per-user supervisor process. Do not invoke from a shell.",
		Hidden:        true,
	},
}

// LookupByName returns the Spec with the given MCP tool Name (snake_case).
// Returns nil if not found.
func LookupByName(name string) *Spec {
	for i := range Specs {
		if Specs[i].Name == name {
			return &Specs[i]
		}
	}
	return nil
}

// LookupByPath returns the Spec for the given (parent, use) pair.
// For top-level specs pass "" for parent. The pair matches both
// the CLI's cobra tree (parent == parent command name) and the
// Spec's Use string. Returns nil if not found.
func LookupByPath(parent, use string) *Spec {
	for i := range Specs {
		s := &Specs[i]
		if s.Parent == parent && s.Use == use {
			return s
		}
	}
	return nil
}

// LookupByDispatcherKey returns the Spec whose DispatcherKey
// matches key. Used by the CLI runner to look up a Spec from
// the DispatcherKey -> runner map in cmd/mekami/runner.go.
// Returns nil if not found.
func LookupByDispatcherKey(key string) *Spec {
	for i := range Specs {
		if Specs[i].DispatcherKey == key {
			return &Specs[i]
		}
	}
	return nil
}

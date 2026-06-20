// Package naming is the single source of truth for the user-facing
// surface of mekami.
//
// Every command/tool has a Spec entry that names it on both sides of
// the wire:
//
//   - CLI: kebab-case verb (e.g. `who-calls`, `list-package`).
//   - MCP: snake_case tool name (e.g. `who_calls`, `list_package`).
//
// Args and flags are defined in snake_case (the MCP wire format) and
// translated to kebab-case for the CLI flag binding by this package.
// Adding a new command means adding a Spec here; both the CLI and the
// MCP server pick it up from the same definition.
package naming

// Spec describes one user-facing operation: a search/lookup, a
// daemon control action, a build command, etc. The same Spec feeds
// the cobra command tree and the MCP tool registry.
//
// Args and flags are defined in snake_case (the JSON wire format the
// MCP SDK speaks). The CLI binds them as kebab-case flags via
// FlagKebab().
//
// Use is the cobra Use string ("who-calls <qualified_name>") and is
// rendered with the placeholder tokens unchanged. The MCP tool name
// is Name (snake_case, no placeholders).
type Spec struct {
	// Name is the MCP tool name (snake_case). For CLI-only commands
	// (build, init, serve, etc.) Name is empty.
	Name string
	// Use is the cobra Use string. The placeholder tokens in angle
	// brackets are shared with cobra's parser and are NOT translated
	// to the MCP side; only Args[] is sent over MCP.
	Use string
	// Short is the one-line description shown by --help and embedded
	// in the MCP tool description.
	Short string
	// Long is the multi-line description shown by --help. Also
	// embedded in the MCP tool description; keep it short because
	// the LLM reads it on every call.
	Long string
	// Args lists positional arguments in declaration order. Each
	// entry is a snake_case name (matches the MCP wire field). For
	// CLI-only commands this can be empty.
	Args []Arg
	// Flags lists the optional flags. snake_case wire name, kebab-case
	// CLI name. CLI-only commands may include flags that are local
	// to the CLI (no MCP equivalent).
	Flags []Flag
	// Hidden hides the command from --help output. Use for internal
	// re-exec entry points.
	Hidden bool
	// Parent is the cobra parent command name. Empty = top-level
	// command. When set, the Spec is attached as a subcommand of
	// a synthesized parent (e.g. "mcp", "core", "service"). The
	// parent itself has no RunE; it is a namespace carrier.
	Parent string
	// DispatcherKey is the stable identifier used by the CLI
	// runner to look up the executor for this Spec. It is
	// independent of Use (which contains placeholder tokens like
	// "<lang>[@<version>]") and of Parent (which is structural).
	// Convention: "parent.use" for grouped specs (e.g.
	// "core.install", "mcp.uninstall"), bare "use" for top-level.
	// Every user-facing Spec MUST set this.
	DispatcherKey string
}

// Arg is a positional argument. Required, by cobra convention, unless
// the command sets Args=N..M explicitly.
type Arg struct {
	Name        string // snake_case, matches MCP field
	Description string
	// Type is the wire type, mirroring Flag.Type: "string" (default)
	// or "int". Empty string is treated as "string". Only the CLI
	// runner consumes this; the MCP wire already carries the JSON
	// type, so this field is essentially a CLI-side decoder hint.
	Type string
	// Optional marks the CLI positional as optional. MCP tools
	// always treat absent args as zero-values; this flag only
	// affects cobra arity (RangeArgs vs ExactArgs) and the runner
	// skipping the args[1:] step.
	Optional bool
	// CLIOnly means the argument exists on the CLI but is NOT exposed
	// as an MCP tool argument. Use for arguments that have no meaning
	// to an LLM.
	CLIOnly bool
	// MCPOnly means the argument is exposed on the MCP tool but is
	// not a CLI positional. Use sparingly — most commands have
	// matching args on both sides. The runner skips MCPOnly args when
	// consuming CLI positionals; cobra's arity check also excludes
	// them, so passing the bare command (e.g. `mekami list-files`)
	// stays valid.
	MCPOnly bool
}

// Flag is a single named option. The Type string is one of "string",
// "int", "bool", "stringSlice". The Default is the string form; it
// is parsed according to Type.
type Flag struct {
	Name        string // snake_case (MCP wire + CLI kebab)
	Type        string // "string" | "int" | "bool" | "stringSlice"
	Default     string
	Description string
	// CLIOnly means the flag exists on the CLI but is NOT exposed
	// as an MCP tool argument. Use for flags that have no meaning
	// to an LLM (e.g. --json output switch, --quiet progress).
	CLIOnly bool
	// MCPOnly means the argument is exposed on the MCP tool but is
	// not a CLI flag. Rare; mostly for MCP defaults the CLI does
	// not surface.
	MCPOnly bool
}

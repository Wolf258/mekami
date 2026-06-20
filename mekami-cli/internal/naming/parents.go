package naming

// ParentMeta is the human-facing description of a synthesized
// parent command. Used by buildSubcommandTree to fill in the
// carrier's Short/Long when the first child is registered. The
// carrier itself has no RunE; it is just a namespace under which
// the subcommands appear in `mekami --help`.
type ParentMeta struct {
	Short string
	Long  string
}

// Parents is the registry of synthesized parent commands.
// Adding a new parent means:
//   1. Add an entry here.
//   2. Add Spec entries with Parent=<name> in specs.go, each
//      carrying a DispatcherKey of the form "<name>.<use>".
//      buildSubcommandTree wires them in.
var Parents = map[string]ParentMeta{
	"core": {
		Short: "Manage language indexers (cores) for this project",
		Long: "Install, list, uninstall, and check the status of language cores " +
			"registered in this project's .mekami/config.json.",
	},
	"mcp": {
		Short: "Manage the mekami MCP server registration in the host client",
		Long: "Install, uninstall, and smoke-test the mekami MCP server entry " +
			"in the host agent client (OpenCode today).",
	},
	"service": {
		Short: "Manage the per-user mekami supervisor service",
		Long: "Register the per-user supervisor with the host init system " +
			"(systemd --user on Linux, LaunchAgent on macOS) and check its " +
			"registration status.",
	},
}

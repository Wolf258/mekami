// Package mekami is the CLI entry point. Commands are declared
// declaratively in internal/naming.Specs and registered at init
// time from a single loop. The same Spec feeds the MCP server.
//
// Command groups:
//
//	mekami <lifecycle>      init, serve, build, stats
//	mekami <graph read>     find, show, who-calls, what-calls, trace,
//	                        list-*, show-*, find-text, index-status
//	mekami <daemon>         start, stop, status, restart, reload, logs,
//	                        service-install, service-uninstall
//	mekami <mcp>            mcp-install, mcp-uninstall
//	mekami mcp-test         smoke test the MCP server end-to-end
//
// Hidden internal entry points (also in naming.Specs): _daemon and
// supervise (re-execed by the supervisor).
package mekami

import (
	"github.com/spf13/cobra"

	"github.com/Wolf258/mekami-cli/internal/install"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

// dbPath is the path to the graph database, set by the persistent
// --db flag. All subcommands (read and write) resolve it through
// this variable; the default is DefaultDBPath applied per subcommand.
var dbPath string

// rootCmd is the cobra root command. Subcommands are added in
// registerAll() so init() can stay tiny.
var rootCmd = &cobra.Command{
	Use:   "mekami",
	Short: "Mekami is a code graph tool for humans and LLMs",
	Long: `Mekami builds a SQLite-backed code graph and exposes it via the
Model Context Protocol (MCP) so LLM agents can query structure
instead of grepping. The same graph is also queryable from the
shell — every MCP tool is also a top-level mekami command.

Mekami answers structural questions (who calls X, where is X
defined, what does Y import, what is the call path between A and
B) but does not index raw source text. For substring search inside
function bodies, comments, log strings, or any arbitrary text, use
` + "`find-text`" + ` or your editor's read tool.`,
	Version: Version(),
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "graph db path (default: "+DefaultDBPath+")")
	rootCmd.SetVersionTemplate("mekami {{.Version}}\n")
	registerAll(rootCmd)
}

// Version returns the binary's version string. Cobra's --version
// calls this on every invocation, so a live re-stamp (e.g. after
// `mcp-install` re-execs the binary) is reflected without restart.
func Version() string { return install.Version() }

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

// registerAll walks naming.Specs and adds every Spec as a
// top-level cobra command. CLI-only behavior lives in a small
// switch in runCommand; everything else delegates to the shared
// dispatch table in internal/handlers (or the daemon/service
// commands in their own files).
func registerAll(root *cobra.Command) {
	for _, spec := range naming.Specs {
		spec := spec
		if spec.Hidden {
			root.AddCommand(buildCobra(&spec, hiddenRunner(&spec)))
			continue
		}
		root.AddCommand(buildCobra(&spec, runCommand(&spec)))
	}
}

// buildCobra attaches the spec's flags to a fresh cobra command
// and returns it. The runner is supplied by the caller.
func buildCobra(spec *naming.Spec, runE func(*cobra.Command, []string) error) *cobra.Command {
	return naming.CobraCommand(*spec, runE)
}

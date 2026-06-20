package naming

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// CobraCommand builds a *cobra.Command from a Spec. The RunE is
// supplied by the caller because Spec does not know how to execute
// the command — it only describes the surface.
//
// Flags are bound as kebab-case (the cobra convention). Each Spec
// flag is exposed on the cobra command under its kebab form. The
// runner receives the parsed values via the supplied lookup
// function, which translates kebab back to snake_case so the
// underlying handler sees the same names it would receive over
// the MCP wire.
//
// Positional arity is derived from spec.Args. CLIOnly args are
// counted like normal positionals; MCPOnly args are not consumed
// on the CLI and do not count toward the cobra arity check.
//
// CobraCommand does NOT attach the returned command to any tree.
// Use buildSubcommandTree to wire the full hierarchy at once.
func CobraCommand(spec Spec, runE func(cmd *cobra.Command, args []string) error) *cobra.Command {
	c := &cobra.Command{
		Use:    spec.Use,
		Short:  spec.Short,
		Long:   spec.Long,
		Hidden: spec.Hidden,
		RunE:   runE,
		Args:   cobraArity(spec.Args),
	}
	for _, f := range spec.Flags {
		bindCobraFlag(c, f)
	}
	return c
}

// CobraRunner is the executor signature for a Spec. The
// dispatcher in the CLI uses the Spec's DispatcherKey to look
// up the runner.
type CobraRunner func(cmd *cobra.Command, args []string) error

// BuildSubcommandTree walks specs and attaches every Spec to
// the cobra tree rooted at root. Top-level specs (Parent=="")
// are added directly; grouped specs (Parent!="") are routed to a
// synthesized carrier command created on first use from the
// Parents registry. Carriers have no RunE — they are pure
// namespaces. Hidden specs are still added (their Hidden flag
// is honored by Cobra).
//
// Every user-facing Spec MUST have a DispatcherKey; if any
// Spec is missing one, BuildSubcommandTree panics. The runner
// map is keyed by DispatcherKey and supplies the RunE for
// each non-hidden spec. Hidden specs (re-exec entry points)
// may pass a noop runner — Cobra will not invoke RunE for
// hidden commands the user cannot invoke directly, but the
// field is still required for consistency.
func BuildSubcommandTree(root *cobra.Command, specs []Spec, runners map[string]CobraRunner) {
	carriers := map[string]*cobra.Command{}
	for i := range specs {
		spec := specs[i]
		if spec.DispatcherKey == "" {
			panic(fmt.Sprintf("naming: spec %q has no DispatcherKey", spec.Use))
		}
		run, ok := runners[spec.DispatcherKey]
		if !ok {
			panic(fmt.Sprintf("naming: no runner registered for DispatcherKey %q (spec %q)", spec.DispatcherKey, spec.Use))
		}
		cmd := CobraCommand(spec, run)
		if spec.Parent == "" {
			root.AddCommand(cmd)
			continue
		}
		parent, exists := carriers[spec.Parent]
		if !exists {
			meta, ok := Parents[spec.Parent]
			if !ok {
				panic(fmt.Sprintf("naming: spec %q has unknown Parent %q (add it to Parents)", spec.Use, spec.Parent))
			}
			parent = &cobra.Command{
				Use:   spec.Parent,
				Short: meta.Short,
				Long:  meta.Long,
			}
			carriers[spec.Parent] = parent
			root.AddCommand(parent)
		}
		parent.AddCommand(cmd)
	}
}

// cobraArity returns a cobra.PositionalArgs validator derived from
// the Spec's positional list. The runner consumes every Arg that is
// NOT MCPOnly, but cobra's arity must accept the full set of CLI
// positionals the Spec advertises (including ones that the runner
// later skips). Otherwise trailing optional args — written as
// `[arg]` in the Spec's Use string, the standard cobra convention —
// would be misread as subcommands.
//
// Without this, cobra's default is ArbitraryArgs (no validation) and
// the runner used to panic on missing required positionals (1.3 / 1.5).
func cobraArity(args []Arg) cobra.PositionalArgs {
	required := 0
	optional := 0
	for _, a := range args {
		if a.CLIOnly {
			// CLIOnly args are still CLI positionals (just not on
			// the MCP wire), so they count toward the cobra arity.
			if a.Optional {
				optional++
			} else {
				required++
			}
			continue
		}
		if a.MCPOnly {
			// The runner will skip these, but cobra still needs to
			// accept them as trailing optional positionals.
			optional++
			continue
		}
		if a.Optional {
			optional++
		} else {
			required++
		}
	}
	switch {
	case required == 0 && optional == 0:
		return cobra.NoArgs
	case optional == 0:
		return cobra.ExactArgs(required)
	default:
		// optional args sit at the tail of the Spec, so the cobra
		// range is [required, required+optional].
		return cobra.RangeArgs(required, required+optional)
	}
}

// bindCobraFlag attaches f to cmd under its kebab-case name. The
// flag value is bound to a package-level sink so RunE can read it
// via cmd.Flags().GetString / GetInt / GetBool / GetStringSlice.
func bindCobraFlag(cmd *cobra.Command, f Flag) {
	name := Kebab(f.Name)
	def := f.Default
	switch f.Type {
	case "string":
		cmd.Flags().String(name, def, f.Description)
	case "int":
		// strconv.Atoi on an empty string returns 0, which is what
		// we want for unset int flags. For non-empty defaults we
		// parse them at registration time so cobra gets the real
		// int. A bad default is a programmer error — panic.
		if def == "" {
			cmd.Flags().Int(name, 0, f.Description)
		} else {
			n, err := strconv.Atoi(def)
			if err != nil {
				panic(fmt.Sprintf("naming: bad int default %q for %s.%s: %v", def, cmd.Use, name, err))
			}
			cmd.Flags().Int(name, n, f.Description)
		}
	case "bool":
		b, err := strconv.ParseBool(def)
		if err != nil {
			panic(fmt.Sprintf("naming: bad bool default %q for %s.%s: %v", def, cmd.Use, name, err))
		}
		cmd.Flags().Bool(name, b, f.Description)
	case "stringSlice":
		// stringSlice defaults are comma-separated lists. Empty
		// default is nil.
		if def == "" {
			cmd.Flags().StringSlice(name, nil, f.Description)
		} else {
			cmd.Flags().StringSlice(name, []string{def}, f.Description)
		}
	default:
		panic(fmt.Sprintf("naming: unknown flag type %q for %s.%s", f.Type, cmd.Use, name))
	}
}

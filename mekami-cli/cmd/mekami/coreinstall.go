package mekami

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/coreinstall"
)

// runCoreInstall is the runner for `mekami core install <lang>[@<version>]>`.
// The "<...>" tokens in spec.Use are not part of the cobra Use
// string cobra sees; we read args[0] and the --version flag and
// pass them to coreinstall.Install.
func runCoreInstall(ctx context.Context, cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cliError{code: 64, msg: "core install: <lang> is required"}
	}
	ref := args[0]
	if v, _ := cmd.Flags().GetString("version"); v != "" {
		// If the user passed a separate --version flag, attach it
		// to the lang. SplitLangRef tolerates a single @ form.
		if idx := indexAt(ref); idx < 0 {
			ref = ref + "@" + v
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return cliError{code: 1, msg: "getwd: " + err.Error()}
	}
	workDir, err := coreinstall.FindWorkDir(cwd)
	if err != nil {
		return cliError{code: 1, msg: "locate mekami source tree: " + err.Error() +
			"\nhint: run core install from inside the mekami checkout, where go.work lives."}
	}

	out, err := coreinstall.Install(workDir, ref)
	if err != nil {
		return cliError{code: 1, msg: "core install: " + err.Error()}
	}

	switch {
	case out.AlreadyPresent:
		fmt.Printf("core install: %s@%s already in %s; no changes\n",
			out.Language, out.Version, out.ConfigPath)
	case out.Upgraded:
		fmt.Printf("core install: %s -> %s in %s\n", out.Language, out.Version, out.ConfigPath)
	default:
		fmt.Printf("core install: %s@%s added to %s\n",
			out.Language, out.Version, out.ConfigPath)
	}
	if out.AllGenPath != "" {
		fmt.Printf("  regenerated: %s\n", out.AllGenPath)
		fmt.Fprintf(os.Stderr,
			"note: rebuild the mekami binary to load the new frontend (e.g. ./build.sh)\n")
	}
	return nil
}

// runCoreUninstall is the runner for `mekami core uninstall <lang>`.
// The "<...>" tokens in spec.Use are not part of the cobra Use
// string cobra sees; we read args[0] and pass it to
// coreinstall.Uninstall.
func runCoreUninstall(ctx context.Context, cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cliError{code: 64, msg: "core uninstall: <lang> is required"}
	}
	lang := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return cliError{code: 1, msg: "getwd: " + err.Error()}
	}
	workDir, err := coreinstall.FindWorkDir(cwd)
	if err != nil {
		return cliError{code: 1, msg: "locate mekami source tree: " + err.Error() +
			"\nhint: run core uninstall from inside the mekami checkout, where go.work lives."}
	}

	out, err := coreinstall.Uninstall(workDir, lang)
	if err != nil {
		return cliError{code: 1, msg: "core uninstall: " + err.Error()}
	}

	if out.NotPresent {
		fmt.Printf("core uninstall: %s not in %s; no changes\n", out.Language, out.ConfigPath)
		return nil
	}
	fmt.Printf("core uninstall: %s removed from %s\n", out.Language, out.ConfigPath)
	if out.AllGenPath != "" {
		fmt.Printf("  regenerated: %s\n", out.AllGenPath)
		fmt.Fprintf(os.Stderr,
			"note: rebuild the mekami binary to drop the frontend (e.g. ./build.sh)\n")
	} else {
		fmt.Fprintf(os.Stderr,
			"note: all_gen.go was not in the work tree (production install?); only config.json was updated.\n")
	}
	return nil
}

// runCoreList prints the indexer set requested by the project's
// .mekami/config.json and the frontends actually registered in
// the running binary. With --json, emits a structured report.
func runCoreList(ctx context.Context, cmd *cobra.Command) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return cliError{code: 1, msg: "load config: " + err.Error()}
	}
	report := coreinstall.List(cfg.Indexers)

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		return printJSON(report)
	}

	if len(report.Indexers) == 0 && len(report.Builtins) == 0 {
		fmt.Fprintln(os.Stderr, "core list: no languages installed. Run `mekami core install <lang>`.")
		return nil
	}

	width := nameWidth(append(listEntryNames(report.Indexers), report.Builtins...))
	fmt.Printf("%-*s  %-9s  %s\n", width, "LANGUAGE", "STATE", "VERSION")
	for _, ix := range report.Indexers {
		state := "loaded"
		if ix.Missing {
			state = "missing"
		}
		v := ix.Version
		if v == "" {
			v = "-"
		}
		fmt.Printf("%-*s  %-9s  %s\n", width, ix.Name, state, v)
	}
	for _, name := range report.Builtins {
		fmt.Printf("%-*s  %-9s  %s\n", width, name, "builtin", "-")
	}
	if len(report.Missing) > 0 {
		fmt.Fprintf(os.Stderr,
			"hint: languages marked 'missing' have no blank import in all_gen.go;\n"+
				"  rebuild the mekami binary or run `core install %s` to fix.\n",
			report.Missing[0])
	}
	return nil
}

// runCoreStatus is the runner for `mekami core status`. It
// prints the same table as `core list` plus a one-line summary
// of how many indexers are configured, loaded, and missing.
// This is the command to reach for when something looks wrong:
// if the list shows a language as "missing", the binary cannot
// ingest files in that language until it is rebuilt.
func runCoreStatus(ctx context.Context, cmd *cobra.Command) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return cliError{code: 1, msg: "load config: " + err.Error()}
	}
	report := coreinstall.List(cfg.Indexers)

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		return printJSON(report)
	}

	if len(report.Indexers) == 0 && len(report.Builtins) == 0 {
		fmt.Fprintln(os.Stderr, "core status: no languages installed. Run `mekami core install <lang>`.")
		return nil
	}

	width := nameWidth(append(listEntryNames(report.Indexers), report.Builtins...))
	fmt.Printf("%-*s  %-9s  %s\n", width, "LANGUAGE", "STATE", "VERSION")
	for _, ix := range report.Indexers {
		state := "loaded"
		if ix.Missing {
			state = "missing"
		}
		v := ix.Version
		if v == "" {
			v = "-"
		}
		fmt.Printf("%-*s  %-9s  %s\n", width, ix.Name, state, v)
	}
	for _, name := range report.Builtins {
		fmt.Printf("%-*s  %-9s  %s\n", width, name, "builtin", "-")
	}

	configured := len(report.Indexers)
	missing := len(report.Missing)
	loaded := 0
	for _, ix := range report.Indexers {
		if ix.Loaded {
			loaded++
		}
	}
	fmt.Fprintf(os.Stdout, "\nconfigured: %d, loaded: %d, missing: %d\n",
		configured, loaded, missing)
	if missing > 0 {
		fmt.Fprintf(os.Stderr,
			"hint: rebuild the mekami binary (./build.sh) to load the missing frontends.\n")
	}
	return nil
}

// listEntryNames returns the .Name field of each entry.
func listEntryNames(xs []coreinstall.ListEntry) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.Name
	}
	return out
}

// nameWidth returns the max length of any name, with a floor of
// 8 (the "LANGUAGE" header length).
func nameWidth(names []string) int {
	w := len("LANGUAGE")
	for _, n := range names {
		if len(n) > w {
			w = len(n)
		}
	}
	// sort isn't strictly necessary for width, but stable input
	// makes tests easier.
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return w
}

// indexAt returns the byte index of the first '@' in s, or -1.
func indexAt(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return i
		}
	}
	return -1
}

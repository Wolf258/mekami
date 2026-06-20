// Package mekami is the CLI entry point. See root.go for the
// top-level layout; this file implements the per-command runners
// the root dispatch table calls.
package mekami

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/install"
	mcpserver "github.com/Wolf258/mekami-cli/internal/mcp"
	"github.com/Wolf258/mekami-cli/internal/supervisor"
	"github.com/Wolf258/mekami-cli/internal/watch"
	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/ingest"
	"github.com/Wolf258/mekami-core/queries"
	"github.com/Wolf258/mekami-core/store"
	"github.com/spf13/cobra"
)

// ─── Lifecycle ─────────────────────────────────────────────────

func runServe(ctx context.Context, cmd *cobra.Command) error {
	path, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	root, _ := filepath.Abs(".")
	wc := watch.NewMCPClient(root)
	srv, err := mcpserver.NewServerWithWatcher(path, wc)
	if err != nil {
		return err
	}
	defer srv.Close()
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func runBuild(ctx context.Context, cmd *cobra.Command) error {
	rootFlag, _ := cmd.Flags().GetString("root")
	langRaw, _ := cmd.Flags().GetString("lang")
	clean, _ := cmd.Flags().GetBool("clean")
	quiet, _ := cmd.Flags().GetBool("quiet")
	jobs, _ := cmd.Flags().GetInt("jobs")
	if rootFlag == "" {
		rootFlag = "."
	}
	if jobs < 0 {
		return cliError{code: 64, msg: "build: --jobs must be >= 0"}
	}
	// resolveLang distinguishes "user passed --lang X" from
	// "user omitted --lang" by checking the Changed bit. When
	// the flag is omitted, we apply the config-driven rules
	// (zero indexers: error; single indexer: use it; multiple:
	// --lang required).
	var explicit string
	if cmd.Flags().Changed("lang") {
		explicit = langRaw
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return cliError{code: 1, msg: "load config: " + err.Error()}
	}
	resolved, err := resolveLang(cfg, explicit)
	if err != nil {
		return err
	}
	// .mekami/config.json is the source of truth for which
	// languages the project tracks. Compute the tracking set
	// from the config's indexers; if --lang added a new one,
	// extend the config so the new state is durable. The
	// build's AllowedLangs then drives the cross-language
	// cleanup before ingest.
	tracking := indexerNames(cfg.Indexers)
	if explicit != "" && !hasIndexer(cfg.Indexers, explicit) {
		if cfg.Indexers == nil {
			cfg.Indexers = make(map[string]string)
		}
		// The new entry has no version yet; `core install` will
		// fill it in next time it runs for this language.
		cfg.Indexers[explicit] = ""
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("validate: %w", err)
		}
		if err := config.Save(cfg, config.DefaultPath()); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		tracking = append(tracking, explicit)
		sort.Strings(tracking)
		names := strings.Join(tracking, ", ")
		fmt.Fprintf(os.Stderr, "build: adding new indexer %q to config.json (no version yet — run `mekami core install %s` to register it). tracking now: %s\n", explicit, explicit, names)
	}
	opts := ingest.BuildOptions{
		Root:         rootFlag,
		DBPath:       defaultDBPath(dbPath),
		Lang:         resolved,
		Clean:        clean,
		Quiet:        quiet,
		Jobs:         jobs,
		AllowedLangs: tracking,
	}
	stats, err := ingest.Build(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("scanned=%d ingested=%d skipped=%d symbols=%d refs=%d duration=%s\n",
		stats.FilesScanned, stats.FilesIngested, stats.FilesSkipped,
		stats.SymbolsAdded, stats.RefsAdded, stats.Duration.Round(time.Millisecond))
	return nil
}

// indexerNames returns the sorted list of language identifiers
// in cfg.Indexers. Used to build ingest.BuildOptions.AllowedLangs
// and the daemons' indexer env var.
func indexerNames(in map[string]string) []string {
	out := make([]string, 0, len(in))
	for name := range in {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// hasIndexer reports whether in already contains the given
// language. Used by runBuild to decide whether --lang adds a
// new tracker to the config.
func hasIndexer(in map[string]string, name string) bool {
	_, ok := in[name]
	return ok
}

// loadConfigAndResolveLang loads .mekami/config.json and delegates
// to resolveLang. It is the thin shim the runners use; resolveLang
// itself takes a config value so it can be tested in isolation.
func loadConfigAndResolveLang(explicit string) (string, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return "", cliError{code: 1, msg: "load config: " + err.Error()}
	}
	return resolveLang(cfg, explicit)
}

// resolveLang maps the value of `mekami build --lang` (or empty
// when the flag is omitted) to the language identifier the build
// pipeline should use. Rules:
//
//   - When --lang is set, it must be either a name listed in
//     .mekami/config.json indexers[] or a frontend registered in
//     the running binary (api.Global.Names()). Unknown values
//     produce a clear error that suggests core install.
//
//   - When --lang is omitted and exactly one indexer is listed,
//     that indexer is used (and must be registered in the binary).
//     When zero are listed, the error says no cores are installed
//     and points at core install. When two or more are listed,
//     --lang becomes required and the error points at the ambiguity.
//
// The CLI's `ingest` package never sees this; resolveLang is the
// single place that knows about the config layer.
func resolveLang(cfg config.Config, explicit string) (string, error) {
	loaded := map[string]bool{}
	for _, n := range api.Global.Names() {
		loaded[n] = true
	}
	known := func(name string) bool { return hasIndexer(cfg.Indexers, name) || loaded[name] }

	if explicit != "" {
		if known(explicit) {
			return explicit, nil
		}
		return "", cliError{code: 64, msg: fmt.Sprintf(
			"build: --lang %q is not installed for this project.\n"+
				"hint: run `mekami core install %s` to register it.", explicit, explicit)}
	}
	switch len(cfg.Indexers) {
	case 0:
		return "", cliError{code: 64, msg: "build: no cores installed for this project.\n" +
			"hint: run `mekami core install <lang>` to register a frontend."}
	case 1:
		name := onlyKey(cfg.Indexers)
		if !loaded[name] {
			return "", cliError{code: 64, msg: fmt.Sprintf(
				"build: indexer %q is configured but not registered in the current binary.\n"+
					"hint: rebuild mekami after `core install` to link the frontend.", name)}
		}
		return name, nil
	default:
		return "", cliError{code: 64, msg: fmt.Sprintf(
			"build: --lang is required (multiple indexers configured: %s).",
			strings.Join(indexerNames(cfg.Indexers), ", "))}
	}
}

// onlyKey returns the single key of a one-entry map. The caller
// is expected to have already verified len(m) == 1; any other
// input returns "" and is a programmer error.
func onlyKey(m map[string]string) string {
	for k := range m {
		return k
	}
	return ""
}

func runStats(ctx context.Context, cmd *cobra.Command) error {
	path, err := resolveDBPath(dbPath)
	if err != nil {
		return err
	}
	s, err := store.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	counts, err := queries.Stats(ctx, s)
	if err != nil {
		return err
	}
	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		out := map[string]any{"db": path, "counts": counts}
		if root, err := queries.LastRoot(ctx, s); err == nil {
			out["last_root"] = root
		} else if !errors.Is(err, store.ErrNoLastRoot) {
			fmt.Fprintf(os.Stderr, "warning: last_root lookup failed: %v\n", err)
		}
		return printJSON(out)
	}
	fmt.Printf("db: %s\n", path)
	for _, k := range queries.StatsTables {
		fmt.Printf("  %-10s %d\n", k+":", counts[k])
	}
	if root, err := queries.LastRoot(ctx, s); err == nil {
		fmt.Printf("  last_root: %s\n", root)
	} else if !errors.Is(err, store.ErrNoLastRoot) {
		fmt.Fprintf(os.Stderr, "warning: last_root lookup failed: %v\n", err)
	}
	return nil
}

// runInit writes .mekami/config.json (selecting language cores
// along the way), runs an initial build so .mekami/graph.db is
// ready, and (optionally) starts the watcher daemon. The
// --daemon flag is tri-state:
//
//	auto  (default) prompt in a TTY, no in non-interactive
//	yes            start the daemon
//	no             don't start
func runInit(ctx context.Context, cmd *cobra.Command, _ []string) error {
	daemonMode, _ := cmd.Flags().GetString("daemon")
	yes, _ := cmd.Flags().GetBool("yes")
	verbose, _ := cmd.Flags().GetBool("verbose")
	requested, _ := cmd.Flags().GetStringSlice("lang")
	if daemonMode == "" {
		daemonMode = "auto"
	}
	switch daemonMode {
	case "auto", "yes", "no":
	default:
		return cliError{code: 64, msg: "init: --daemon must be auto|yes|no"}
	}

	available := api.Global.Names()
	chosen, err := resolveInitLangs(requested, available)
	if err != nil {
		if cliErr, ok := err.(cliError); ok {
			return cliErr
		}
		return err
	}

	cfgPath := config.DefaultPath()
	absCfg, _ := filepath.Abs(cfgPath)
	exists := true
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			exists = false
		} else {
			return fmt.Errorf("stat %s: %w", cfgPath, err)
		}
	}

	if !exists {
		cfg := config.Default()
		cfg.Indexers = indexersFromNames(chosen)
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("validate: %w", err)
		}
		if err := config.Save(cfg, cfgPath); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		fmt.Fprintf(os.Stderr, "init: wrote %s\n", absCfg)
	} else {
		existing, loadErr := config.Load(cfgPath)
		if loadErr != nil {
			return fmt.Errorf("reload config: %w", loadErr)
		}
		merged := mergeIndexers(existing.Indexers, indexersFromNames(chosen), len(requested) > 0)
		if !indexerMapsEqual(existing.Indexers, merged) {
			existing.Indexers = merged
			if err := existing.Validate(); err != nil {
				return fmt.Errorf("validate: %w", err)
			}
			if err := config.Save(existing, cfgPath); err != nil {
				return fmt.Errorf("save: %w", err)
			}
			fmt.Fprintf(os.Stderr, "init: updated %s\n", absCfg)
		} else {
			fmt.Fprintf(os.Stderr, "init: %s already exists\n", absCfg)
		}
	}

	if len(available) == 1 && len(requested) == 0 {
		fmt.Fprintf(os.Stderr, "init: using available core: %s\n", available[0])
	} else if len(requested) > 0 {
		// Distinguish cores that have a version (came from
		// core install or a previous config) from those that
		// init just added (no version yet). The user
		// benefits from knowing which entries they should run
		// `core install` for.
		savedCfg, loadErr := config.Load(cfgPath)
		parts := make([]string, 0, len(chosen))
		for _, name := range chosen {
			v := ""
			if loadErr == nil {
				v = savedCfg.Indexers[name]
			}
			if v == "" {
				parts = append(parts, name+" (no version)")
			} else {
				parts = append(parts, name+" "+v)
			}
		}
		fmt.Fprintf(os.Stderr, "init: using requested core(s): %s\n", strings.Join(parts, ", "))
	}

	wantDaemon := false
	switch daemonMode {
	case "yes":
		wantDaemon = true
	case "no":
		wantDaemon = false
	default:
		if yes || !isInteractive() {
			wantDaemon = false
		} else {
			wantDaemon = confirm("Start the watcher daemon now? [y/N] ")
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	root, _ := filepath.Abs(".")
	dbPath := defaultDBPath(dbPath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("mkdir db dir: %w", err)
	}

	// Run the initial build only when a single language can be
	// resolved. With multiple indexers configured and no explicit
	// --lang we cannot pick one here; the user (or the daemon
	// below) will pick on the next build.
	lang, langErr := loadConfigAndResolveLang("")
	canBuild := langErr == nil
	if !canBuild {
		if _, ok := langErr.(cliError); !ok {
			return langErr
		}
		fmt.Fprintln(os.Stderr,
			"init: skipping initial build (multiple indexers configured; pass --lang to `mekami build` once you've picked one).")
	}
	if canBuild {
		buildOpts := ingest.BuildOptions{
			Root:         root,
			DBPath:       dbPath,
			Lang:         lang,
			Clean:        false,
			Quiet:        !verbose,
			Jobs:         cfg.Build.Jobs,
			ForceRoot:    cfg.Build.ForceRoot,
			AllowedLangs: indexerNames(cfg.Indexers),
		}
		buildStats, buildErr := ingest.Build(ctx, buildOpts)
		if buildErr != nil {
			return fmt.Errorf("init: build failed: %w", buildErr)
		}
		fmt.Fprintf(os.Stderr,
			"init: build complete (scanned=%d ingested=%d skipped=%d symbols=%d refs=%d)\n",
			buildStats.FilesScanned, buildStats.FilesIngested, buildStats.FilesSkipped,
			buildStats.SymbolsAdded, buildStats.RefsAdded)
	}

	if !wantDaemon {
		fmt.Fprintln(os.Stderr, "init: watcher not started. Run 'mekami start' later to begin watching this project.")
		return nil
	}

	// The daemon also takes a single lang. If we couldn't resolve
	// one above, we don't start the daemon — the user has to pick
	// a language first via `core install` + re-init or by editing
	// the config down to a single indexer.
	if !canBuild {
		return cliError{code: 1, msg: "init: cannot start daemon with multiple indexers and no --lang; reduce indexers or pass --lang."}
	}

	if _, err := startDaemonForRoot(root, dbPath, lang, cfg); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	fmt.Fprintln(os.Stderr, "init: watcher daemon started.")
	return nil
}

// resolveInitLangs decides which language cores init selects.
// available comes from api.Global.Names(); requested is what the
// user passed via --lang (possibly empty).
//
// Rules:
//   - available is empty: error, no cores registered in the binary.
//   - requested is empty: use available as-is (sorted).
//   - requested is non-empty: every entry must be in available,
//     otherwise error pointing at core install + ./build.sh.
//   - duplicates in requested are de-duplicated; order is not
//     preserved (output is sorted for determinism).
func resolveInitLangs(requested, available []string) ([]string, error) {
	if len(available) == 0 {
		return nil, cliError{code: 64, msg: "init: no language cores registered in this binary.\n" +
			"hint: run `./build.sh` (or `mekami core install <lang>` + rebuild) to load at least one frontend."}
	}
	known := make(map[string]bool, len(available))
	for _, n := range available {
		known[n] = true
	}
	if len(requested) == 0 {
		out := append([]string(nil), available...)
		sort.Strings(out)
		return out, nil
	}
	seen := make(map[string]bool, len(requested))
	out := make([]string, 0, len(requested))
	for _, n := range requested {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if seen[n] {
			continue
		}
		if !known[n] {
			return nil, cliError{code: 64, msg: fmt.Sprintf(
				"init: --lang %q is not registered in this binary.\n"+
					"hint: run `./build.sh` after `mekami core install %s` to link the frontend.",
				n, n)}
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, cliError{code: 64, msg: "init: --lang was provided but resolved to an empty list."}
	}
	sort.Strings(out)
	return out, nil
}

// indexersFromNames builds a map[string]string from a list of
// language names, all with empty version. Used when init has no
// prior config to consult; core install fills the version later.
func indexersFromNames(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = ""
	}
	return out
}

// mergeIndexers reconciles the indexers already in the config
// with the set init just selected. explicit=true means the user
// passed --lang, so the selection fully replaces the existing
// list (preserving the version of any name that was already
// there). explicit=false preserves existing entries the user did
// not mention — it just unions the new names in (also a no-op
// when they're already present).
func mergeIndexers(existing, selected map[string]string, explicit bool) map[string]string {
	if explicit {
		out := make(map[string]string, len(selected))
		for name, version := range selected {
			if prev, ok := existing[name]; ok && version == "" {
				// Don't downgrade a real version to "" when
				// the user re-runs init with --lang and
				// didn't supply a version.
				out[name] = prev
				continue
			}
			out[name] = version
		}
		return out
	}
	out := make(map[string]string, len(existing)+len(selected))
	for name, version := range existing {
		out[name] = version
	}
	for name, version := range selected {
		if _, ok := out[name]; ok {
			continue
		}
		out[name] = version
	}
	return out
}

// indexerMapsEqual reports whether two indexer maps hold the
// same names with the same versions. Used by init to decide
// whether the merged config differs from what was on disk.
func indexerMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// ─── Daemon (top-level: start/stop/status/restart/reload/logs) ─

func runDaemon(name string, ctx context.Context, cmd *cobra.Command, _ []string) error {
	root, _ := filepath.Abs(".")
	cli := supervisor.NewClient()
	switch name {
	case "start":
		cfg, err := loadWatchConfig()
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		dbPath := defaultDBPath(dbPath)
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return fmt.Errorf("mkdir db dir: %w", err)
		}
		if !cli.Ping(ctx) {
			if err := ensureSupervisor(); err != nil {
				return err
			}
			cli = supervisor.NewClient()
		}
		abs, _ := filepath.Abs(root)
		lang, langErr := loadConfigAndResolveLang("")
		if langErr != nil {
			if cliErr, ok := langErr.(cliError); ok {
				return cliErr
			}
			return langErr
		}
		names := make([]string, 0, len(cfg.Indexers))
		for name := range cfg.Indexers {
			names = append(names, name)
		}
		sort.Strings(names)
		v, err := cli.Start(ctx, supervisor.StartPayload{
			Root:          abs,
			Lang:          lang,
			DBPath:        dbPath,
			RestartPolicy: "on-crash",
			IndexerNames:  names,
		})
		if err != nil {
			if err.Error() == "supervisor: daemon already running" {
				fmt.Fprintln(os.Stderr, "start: daemon already running")
				return nil
			}
			return err
		}
		fmt.Fprintf(os.Stderr, "start: daemon started (pid=%d)\n", v.PID)
		return nil
	case "stop":
		if !cli.Ping(ctx) {
			fmt.Fprintln(os.Stderr, "stop: no daemon running")
			return nil
		}
		if err := cli.Stop(ctx, root, false); err != nil {
			if err.Error() == "supervisor: not running" {
				fmt.Fprintln(os.Stderr, "stop: no daemon running")
				return nil
			}
			return err
		}
		fmt.Fprintln(os.Stderr, "stop: daemon stopped")
		return nil
	case "status":
		jsonMode, _ := cmd.Flags().GetBool("json")
		if !cli.Ping(ctx) {
			if jsonMode {
				fmt.Fprintln(os.Stdout, `{"running":false}`)
				return nil
			}
			fmt.Fprintln(os.Stderr, "status: no daemon running")
			os.Exit(1)
		}
		views, err := cli.Status(ctx, root)
		if err != nil {
			return err
		}
		if len(views) == 0 {
			if jsonMode {
				fmt.Fprintln(os.Stdout, `{"running":false}`)
				return nil
			}
			fmt.Fprintln(os.Stderr, "status: no daemon for this project")
			os.Exit(1)
		}
		if jsonMode {
			return printJSON(views[0])
		}
		v := views[0]
		pid := v.PID
		uptime := time.Duration(v.UptimeS) * time.Second
		fmt.Printf("  pid:        %d\n", pid)
		fmt.Printf("  state:      %s\n", v.State)
		fmt.Printf("  uptime:     %s\n", formatDuration(uptime))
		fmt.Printf("  source:     %s\n", orDefault(v.Source, "?"))
		fmt.Printf("  batches:    %d\n", v.Batches)
		fmt.Printf("  ingested:   %d\n", v.FilesIngested)
		fmt.Printf("  removed:    %d\n", v.FilesRemoved)
		fmt.Printf("  full:       %d\n", v.FullRebuilds)
		fmt.Printf("  errors:     %d\n", v.Errors)
		if v.LastBatchUnix > 0 {
			last := time.Unix(v.LastBatchUnix, 0)
			fmt.Printf("  last batch: %s (%s ago)\n",
				last.Format(time.RFC3339),
				formatDuration(time.Since(last)))
		}
		return nil
	case "restart":
		if !cli.Ping(ctx) {
			fmt.Fprintln(os.Stderr, "restart: no daemon running")
			return nil
		}
		v, err := cli.Restart(ctx, root)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "restart: restarted (pid=%d)\n", v.PID)
		return nil
	case "reload":
		if !cli.Ping(ctx) {
			fmt.Fprintln(os.Stderr, "reload: no daemon running")
			return nil
		}
		if err := cli.Reload(ctx, root); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "reload: reloaded")
		return nil
	case "logs":
		return followFile(supervisor.DaemonLogPath(root))
	}
	return fmt.Errorf("unknown daemon command %q", name)
}

// ─── Service (install/uninstall the supervisor) ────────────────
//
// runServiceInstall / runServiceUninstall are the wrappers that the
// cobra dispatch in runner.go calls. They are thin shims around the
// per-platform serviceInstall / serviceUninstall functions defined
// in service_linux.go / service_darwin.go / service_other.go; the
// split exists so the platform-specific code (systemd unit writing,
// launchctl plist writing) can live in build-tag-gated files
// without dragging the cobra plumbing along with it.

func runServiceInstall() error {
	return serviceInstall()
}

func runServiceUninstall() error {
	return serviceUninstall()
}

// ─── MCP integration (mcp install / mcp uninstall) ─────────────

func runMCPInstall(ctx context.Context, _ []string, flags namingArgMap) error {
	_ = ctx
	oc := &install.OpenCodeClient{
		BinaryPath: flags.GetString("binary", ""),
		ServerName: flags.GetString("name", "mekami"),
	}
	envList := flags.GetStringSlice("env", nil)
	if len(envList) > 0 {
		env, err := parseEnvSlice(envList)
		if err != nil {
			return cliError{code: 64, msg: err.Error()}
		}
		oc.Environment = env
	}
	if flags.GetBool("disable", false) {
		f := false
		oc.Enabled = &f
	}
	path, err := oc.ConfigPath()
	if err != nil {
		return err
	}
	entry, err := oc.Entry()
	if err != nil {
		return err
	}
	name := oc.Name()
	res, err := install.Register(install.RegisterOptions{
		ConfigPath: path,
		Name:       name,
		Entry:      entry,
	})
	if err != nil {
		return err
	}
	fmt.Printf("registered MCP server %q in %s\n", name, path)
	fmt.Printf("  entry: %s\n", entry.String())
	if res.BackupPath != "" {
		fmt.Printf("  backup: %s\n", res.BackupPath)
	}
	if !res.Changed && res.Existed {
		fmt.Println("  (no changes; entry was already up to date)")
	}
	return nil
}

func runMCPUninstall(ctx context.Context, _ []string) error {
	_ = ctx
	oc := &install.OpenCodeClient{ServerName: "mekami"}
	path, err := oc.ConfigPath()
	if err != nil {
		return err
	}
	res, err := install.Unregister(install.RegisterOptions{
		ConfigPath: path,
		Name:       oc.Name(),
	})
	if err != nil {
		return err
	}
	if !res.Existed {
		fmt.Printf("no MCP entry %q in %s; nothing to do\n", oc.Name(), path)
		return nil
	}
	fmt.Printf("removed MCP server %q from %s\n", oc.Name(), path)
	if res.BackupPath != "" {
		fmt.Printf("  backup: %s\n", res.BackupPath)
	}
	return nil
}

// parseEnvSlice turns ["FOO=bar", "BAZ=1"] into a map. Malformed
// entries (no '=') are rejected so the user does not silently lose
// settings.
func parseEnvSlice(in []string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for _, kv := range in {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			return nil, fmt.Errorf("mcp install: bad env entry %q (want KEY=VALUE)", kv)
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out, nil
}

// ─── Hidden / supervisor control ───────────────────────────────

func runSupervise(ctx context.Context, args []string) error {
	cli := supervisor.NewClient()
	if len(args) == 0 {
		return fmt.Errorf("'supervise' requires a subcommand: start, stop, status, list, reload, restart, _run")
	}
	switch args[0] {
	case "start":
		if cli.Ping(ctx) {
			fmt.Fprintln(os.Stderr, "supervise: already running")
			return nil
		}
		if err := startSupervisorProcess(); err != nil {
			return err
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if cli.Ping(ctx) {
				fmt.Fprintln(os.Stderr, "supervise: started")
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return fmt.Errorf("supervise: did not start within 3s")
	case "stop":
		if err := cli.Quit(ctx); err != nil {
			if isNotRunningErr(err) {
				fmt.Fprintln(os.Stderr, "supervise: not running")
				return nil
			}
			return err
		}
		fmt.Fprintln(os.Stderr, "supervise: stopped")
		return nil
	case "status":
		views, err := cli.Status(ctx, "")
		if err != nil {
			if isNotRunningErr(err) {
				fmt.Fprintln(os.Stderr, "supervise: not running")
				os.Exit(1)
			}
			return err
		}
		fmt.Fprintf(os.Stderr, "supervise: %d daemon(s)\n", len(views))
		for _, v := range views {
			fmt.Printf("  %s  state=%s pid=%d uptime=%ds source=%s batches=%d errors=%d\n",
				v.Root, v.State, v.PID, v.UptimeS, v.Source, v.Batches, v.Errors)
		}
		return nil
	case "list":
		roots, err := cli.List(ctx)
		if err != nil {
			if isNotRunningErr(err) {
				fmt.Fprintln(os.Stderr, "supervise: not running")
				os.Exit(1)
			}
			return err
		}
		for _, r := range roots {
			fmt.Println(r)
		}
		return nil
	case "reload":
		root := ""
		if len(args) > 1 {
			abs, err := filepath.Abs(args[1])
			if err != nil {
				return err
			}
			root = abs
		}
		if err := cli.Reload(ctx, root); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "supervise: reloaded")
		return nil
	case "restart":
		if len(args) < 2 {
			return fmt.Errorf("restart requires a root path")
		}
		abs, err := filepath.Abs(args[1])
		if err != nil {
			return err
		}
		v, err := cli.Restart(ctx, abs)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "supervise: restarted %s (pid=%d)\n", v.Root, v.PID)
		return nil
	case "_run":
		return runSupervisorMain(ctx)
	case "_watchdog":
		return runWatchdogMain(ctx)
	}
	return fmt.Errorf("supervise: unknown subcommand %q", args[0])
}

func isNotRunningErr(err error) bool {
	if err == nil {
		return false
	}
	return err == supervisor.ErrSupervisorNotRunning ||
		err.Error() == supervisor.ErrSupervisorNotRunning.Error() ||
		(strings.Contains(err.Error(), "supervisor") && strings.Contains(err.Error(), "not running"))
}

// runDaemonEntry is the body of `mekami _daemon` (re-execed by
// the supervisor with _MEKAMI_DAEMON=1).
func runDaemonEntry(cmd *cobra.Command) error {
	if os.Getenv("_MEKAMI_DAEMON") != "1" {
		return fmt.Errorf("the _daemon subcommand is for the watcher daemon only")
	}
	return watch.DaemonEntryPoint(cmd.Context())
}

// runSupervisorMain is the body of `mekami supervise _run`.
func runSupervisorMain(ctx context.Context) error {
	if os.Getenv("_MEKAMI_SUPERVISOR") != "1" {
		return fmt.Errorf("the _run subcommand is for the supervisor only")
	}
	// Clear any leftover stop sentinel from a previous
	// (failed) uninstall. The sentinel is a stop signal,
	// not persistent state, so a missing file is the
	// "no stop requested" case. The watchdog also clears
	// the sentinel on its way out, so this is mostly a
	// belt-and-suspenders against a crash between the
	// sentinel write and the supervisor's exit.
	if err := supervisor.ClearSentinel(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not clear stop sentinel: %v\n", err)
	}
	// Write our PID to supervisor.pid so the watchdog (and
	// any other observer) can find us. The file is removed
	// on shutdown via defer; if the supervisor crashes
	// hard and the file is left behind, the next start
	// will overwrite it. The watchdog's "gone" detection
	// is robust against a stale supervisor.pid: it does
	// signal 0 to verify the PID is alive before deciding
	// to take any action.
	pidPath := filepath.Join(supervisor.StateDir(), "supervisor.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		// Non-fatal: the supervisor still works, the
		// watchdog just falls back to "socket is
		// missing" as its liveness signal.
		fmt.Fprintf(os.Stderr, "warning: could not write supervisor.pid: %v\n", err)
	}
	defer os.Remove(pidPath)
	// Spawn the watchdog BEFORE the supervisor's IPC
	// server comes up: the watchdog's first health
	// check is on a 5-second tick, so the supervisor
	// has time to bind the socket before the watchdog
	// starts polling. The watchdog is the
	// supervisor's auto-restart safety net: if the
	// supervisor ever wedges, the watchdog will kill
	// and re-spawn it. The watchdog itself runs in
	// its own session (setsid) and exits when the
	// supervisor is gone.
	//
	// We spawn the watchdog here, on the canonical
	// supervisor-startup path, rather than only in
	// startSupervisorProcess, so the systemd-managed
	// path (which goes directly to `supervise _run`
	// without going through startSupervisorProcess)
	// also gets a watchdog. This was a real bug:
	// before this change, the systemd unit ran the
	// supervisor with no watchdog, so a wedged
	// supervisor was only restartable by the user
	// logging in and re-running `mekami status`.
	if err := startWatchdogProcess(); err != nil {
		// Best-effort: if the watchdog fails to
		// start, the supervisor still works, we
		// just lose the auto-restart safety net.
		fmt.Fprintf(os.Stderr, "warning: could not start supervisor watchdog: %v\n", err)
	}
	s, err := supervisor.NewSupervisor()
	if err != nil {
		return err
	}
	_ = s.LoadFromRegistry()
	ipcSrv, err := supervisor.StartIPCServer(s)
	if err != nil {
		return err
	}
	defer ipcSrv.Shutdown()
	return s.Run(ctx)
}

// startSupervisorProcess re-execs the current binary with the
// hidden "supervise _run" subcommand and _MEKAMI_SUPERVISOR=1.
// The child detaches via setsid so it survives the parent's exit.
//
// In addition, a small watchdog process is spawned alongside
// the supervisor. The watchdog polls the supervisor's IPC
// socket every few seconds; if the supervisor stops
// responding, the watchdog kills it and re-spawns it. This
// is the cross-platform "auto-restart the supervisor"
// mechanism, and complements the systemd --user / LaunchAgent
// units (which only restart the *supervisor* if the
// supervisor exits; they do nothing for a wedged supervisor
// that is alive but unresponsive). On platforms where
// service install is not implemented, the watchdog is the
// only safety net.
//
// The watchdog itself is also short-lived: it exits when
// its parent supervisor exits cleanly. systemd/launchd
// will then restart the whole pair (supervisor first, then
// the watchdog that the supervisor forks on its own).
func startSupervisorProcess() error {
	if err := supervisor.EnsureStateDir(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	cmd := exec.Command(self, "supervise", "_run")
	cmd.Env = append(os.Environ(), "_MEKAMI_SUPERVISOR=1")
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// startWatchdogProcess re-execs the current binary as the
// hidden "supervise _watchdog" subcommand, detached and in
// its own session. The watchdog reads the supervisor's PID
// from .mekami/supervisor/supervisor.pid and polls its
// socket; if the supervisor stays unresponsive for more
// than a few ticks, the watchdog kills it and re-spawns
// it via startSupervisorProcess (which in turn spawns
// another watchdog).
func startWatchdogProcess() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	cmd := exec.Command(self, "supervise", "_watchdog")
	cmd.Env = append(os.Environ(), "_MEKAMI_WATCHDOG=1")
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork watchdog: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// runWatchdogMain is the body of `mekami supervise _watchdog`.
// The function is a thin CLI wrapper around
// supervisor.WatchdogRun: the actual health checks live
// in the supervisor package so they are unit-testable
// without the CLI in the loop. The CLI's only job here
// is to:
//   - write the watchdog's PID to
//     $XDG_CONFIG_HOME/mekami/supervisor/watchdog.pid
//     so `service uninstall` can find us without
//     scanning the process table;
//   - install a SIGTERM handler that cancels ctx
//     (the supervisor does not propagate signals to
//     the watchdog because they are in different
//     sessions);
//   - pass a respawn callback (startSupervisorProcess)
//     and the canonical state directory.
//
// On exit, the watchdog's PID file is removed so a
// future `service uninstall` does not signal a stale
// PID. The sentinel file (if any) is left alone: the
// supervisor clears it on the next startup.
//
// The watchdog exits when:
//   - the stop sentinel is observed (set by
//     `service uninstall` via HandleQuitAll);
//   - the supervisor's PID disappears AND the socket
//     is gone (clean shutdown: systemd will restart
//     the supervisor, which will spawn a new
//     watchdog); or
//   - the supervisor is wedged and the watchdog has
//     re-spawned it (the new supervisor spawns its
//     own watchdog); or
//   - SIGTERM is delivered to the watchdog (e.g. the
//     `service uninstall` flow signals the PID
//     directly as a fast path); or
//   - ctx is cancelled (test only).
func runWatchdogMain(ctx context.Context) error {
	if os.Getenv("_MEKAMI_WATCHDOG") != "1" {
		return fmt.Errorf("the _watchdog subcommand is for the supervisor watchdog only")
	}
	if err := supervisor.WriteWatchdogPID(os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write watchdog.pid: %v\n", err)
	}
	defer func() { _ = supervisor.RemoveWatchdogPID() }()
	// SIGTERM handler. The watchdog runs in its own
	// session (Setsid), so SIGTERM from the parent
	// shell is not inherited; this handler is here
	// for the explicit `pkill -TERM mekami` path
	// (e.g. `service uninstall`'s fast path) and for
	// tests that cancel the watchdog via signal.
	sigCtx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer cancel()
	return supervisor.WatchdogRun(sigCtx, supervisor.StateDir(), startSupervisorProcess)
}

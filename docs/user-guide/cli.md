# CLI reference

Every command is a top-level verb. There are no `query` / `watch` / `mcp` parent groups — discover the surface by reading `mekami --help` once.

!!! tip "Unified vocabulary"

    Every CLI command has a matching MCP tool. The CLI uses kebab-case (`who-calls`); MCP uses snake_case (`who_calls`). They are declared once in `internal/naming.Specs` and rendered into both surfaces automatically.

## Global flags

| Flag | Description |
| --- | --- |
| `--db /path/to/graph.db` | Override the default database path (`.mekami/graph.db`). Accepted by every subcommand. |
| `--json` | Emit machine-readable JSON instead of human text. Accepted by every read command. |

## Lifecycle

| Command | Description |
| --- | --- |
| `mekami init` | Create `.mekami/config.json` and (optionally) start the watcher daemon. |
| `mekami serve` | Run the MCP server on stdio. |
| `mekami build` | Build the code graph database. |
| `mekami stats` | Show per-table counts and the last build's root. `--json` for machine output. |

### `mekami init` flags

| Flag | Description |
| --- | --- |
| `--lang <list>` | Comma-separated list of language cores to enable (default: every core registered in the running binary — "all-available"). Repeatable: `--lang go --lang rust`. |
| `--daemon auto\|yes\|no` | Start the watcher daemon after init. `auto` (default) prompts in a TTY and skips in non-interactive shells. |
| `--yes` | Assume "no" to the daemon prompt (equivalent to non-interactive auto). |
| `--verbose` | Show full `mekami build` progress instead of the one-line summary. |

`init` writes `.mekami/config.json`, persists the chosen cores to `indexers`, runs an initial `mekami build` (skipped when more than one core is configured and no `--lang` was passed), and then optionally starts the watcher. The build's `AllowedLangs` come from the just-written `indexers`, so any data in `.mekami/graph.db` whose `lang` is no longer tracked is removed.

Re-running `init` is idempotent: without `--lang` it unions the existing `indexers` with whatever the binary now registers; with `--lang` the explicit list replaces what was there.

### `mekami build` flags

| Flag | Description |
| --- | --- |
| `--root <path>` | Source root (default: cwd). |
| `--lang <lang>` | Language to ingest (default `go`; the binary ships with the Go frontend). |
| `--clean` | Delete the existing DB and rebuild from scratch. |
| `--quiet` | Suppress per-file progress. |
| `--jobs <n>` | Parse workers (`0` = `NumCPU`). |

`.mekami/config.json` is the source of truth for which languages the project tracks. Before every build, the `indexers` list is reconciled against the rows in `.mekami/graph.db`: any file whose `lang` is no longer in the set is deleted, with one log line per removed language:

```text
build: removing data for disabled language(s): rust (12 files, 230 symbols, 1144 refs)
```

Passing `--lang <x>` where `<x>` is not yet in `indexers` extends the list in place and logs the change:

```text
build: adding new indexer "rust" to config.json. tracking now: go, rust
```

## Graph reads

Every MCP tool is also a CLI command. The matching MCP tool is the snake_case form of the same name.

| CLI command | MCP tool | Description |
| --- | --- | --- |
| `mekami show <qn>` | `get_symbol` | A symbol's definition. Default returns the header; pass `--body` to get the numbered source body. |
| `mekami who-calls <qn>` | `who_calls` | Incoming references (callers, type uses, value reads, embeds, imports). |
| `mekami what-calls <qn>` | `what_calls` | Distinct outgoing references. |
| `mekami list-file <path>` | `list_file` | Top-level symbols in a file. |
| `mekami trace <from> <to>` | `trace_calls` | Shortest call path between two symbols. |
| `mekami list-files [prefix]` | `list_files` | Project file tree. |
| `mekami list-package <import>` | `list_package` | All symbols in a package. |
| `mekami list-importers <import>` | `list_importers` | Packages that import the given one. |
| `mekami list-modules` | `list_modules` | Indexed modules. |
| `mekami show-modules` | `show_modules` | Per-module package summary. |
| `mekami show-changes` | `show_changes` | Files added/modified/removed since the last build. |
| `mekami index-status` | `index_status` | Snapshot of the index (`last_root`, `last_build_at`, counts). |
| `mekami find-symbols <query>` | `find_symbols` | Substring match against declared symbol names. Narrows with `--kind` and `--path-prefix`. |
| `mekami circular-imports` | `circular_imports` | Cycles in the package import graph (project packages only). |
| `mekami unused` | `unused` | Exported symbols with no incoming references (dead-code candidates, with entry-point filter). |
| `mekami type-hierarchy <type>` | `type_hierarchy` | Members of a type, or types that implement an interface (`--mode=members\|implementers\|all`). |
| `mekami dependents <target>` | `dependents` | Tree of symbols/packages/modules affected by a change to `<target>` (`--level=symbol\|package\|module`). |

All read commands accept `--json` to emit machine-readable JSON to stdout (a non-zero exit code on a real error; `0` with an empty result on a no-hits query).

## Daemon controls

| Command | Description |
| --- | --- |
| `mekami start` | Spawn a watcher daemon for the current project (idempotent). |
| `mekami stop` | Stop the daemon for the current project. |
| `mekami status` | Daemon's PID, uptime, batch counters, source. Use `--json`. |
| `mekami restart` | Stop + start. |
| `mekami reload` | Re-read `.mekami/config.json`; hot-only changes are pushed, cold changes trigger restart. |
| `mekami logs` | Tail the daemon log. |
| `mekami service install` | Register the supervisor as a system service (systemd --user on Linux, LaunchAgent on macOS). |
| `mekami service uninstall` | Tear the service down. |
| `mekami service status` | Show whether the supervisor is registered, enabled, and active. |

See [Watch mode](watch-mode.md) for the full supervisor / watchdog / orphan-adoption story.

## MCP integration

| Command | Description |
| --- | --- |
| `mekami mcp install` | Register the mekami MCP server in the host client (OpenCode today). |
| `mekami mcp uninstall` | Remove the entry. |
| `mekami mcp test` | Spawn the server as a subprocess and call a sample of tools (smoke test). |

`mcp install` accepts:

| Flag | Description |
| --- | --- |
| `--binary /abs/path/mekami` | Pin the entry to a specific binary (useful for dev builds). |
| `--name <other>` | Register under a different server name. |
| `--disable` | Register with `enabled: false`. |
| `--env KEY=VALUE` | Inject an environment variable. Repeatable. |

## Core (language indexers)

| Command | Description |
| --- | --- |
| `mekami core install <lang>[@<version>]` | Register a language indexer for this project. |
| `mekami core list` | List configured and loaded cores. |
| `mekami core uninstall <lang>` | Remove a language indexer from this project. |
| `mekami core status` | Show configured vs loaded cores with a missing/loaded summary. |

`core install` resolves the version via the Go module proxy (`go list -m -versions`), writes the entry to `indexers` in `.mekami/config.json`, and regenerates `mekami-cli/internal/core/frontend/all_gen/all_gen.go` with a fresh blank import. See [The `all_gen` mechanism](../extending/all-gen.md) for the full dev-vs-prod story.

## Hidden commands

A handful of commands are deliberately hidden from `--help` because they are internal:

| Command | Description |
| --- | --- |
| `supervise _run` | Internal: the supervisor process entrypoint. |
| `supervise _watchdog` | Internal: the supervisor watchdog. |
| `serve <flags...>` | Internal: the `start` command re-execs the same binary with hidden env vars. |

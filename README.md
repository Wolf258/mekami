# Mekami

A SQLite-backed Go code graph for humans and LLM agents, exposed
over the [Model Context Protocol](https://modelcontextprotocol.io).

Mekami walks a Go project, parses every file with `go/parser`, and
persists symbols, definitions, signatures, and reference edges
into a single SQLite database. It runs as an MCP server so an
agent (Claude, OpenCode, etc.) can ask structural questions —
*who calls `X`? where is `X` defined? what's the call path between
`A` and `B`?* — instead of grepping the source tree. The same
graph is also queryable from the shell: every MCP tool is also a
top-level `mekami` command.

Mekami is **not** a code search engine. It indexes symbol names
and reference edges only; it does not index raw source text. For
substring search inside function bodies, comments, log strings, or
any arbitrary text, use `mekami find-text` (or the MCP
`find_text` tool) or your editor's read tool.

## Repository layout

The whole `mekami` binary lives in this repo as a single Go
module under `mekami-cli/`. The indexing pipeline that used to
ship as a separate `github.com/Wolf258/mekami-core` module now
lives at `mekami-cli/internal/core/`. The two external
repositories that are still consumed by version are:

- [`Wolf258/mekami-api`](https://github.com/Wolf258/mekami-api) — the `api.Frontend` interface contract that every language core implements.
- [`Wolf258/mekami-core-go`](https://github.com/Wolf258/mekami-core-go) — the Go-language frontend, registered as a blank import via the generated `mekami-cli/internal/core/frontend/all_gen/all_gen.go`.

## Features

- **Incremental indexing** — files are fingerprinted with
  `sha256`; unchanged files are skipped on rebuild.
- **Parallel ingest** — parsing runs on `runtime.NumCPU()` workers;
  writes are serialized through a single SQLite transaction.
- **Workspace-aware** — detects `go.work` and indexes every
  `use`d module from the workspace root, or just the current module
  when run from a sub-module.
- **MCP server** — 17 tools over stdio covering symbol search,
  callers/callees, call-path BFS, file/package/module outlines,
  source ranges, filesystem text search, and an index snapshot.
  Tool names are snake_case (`who_calls`, `find_symbol`); the
  matching CLI commands are kebab-case (`who-calls`, `find`).
- **Unified vocabulary** — both surfaces are declared in one place
  (`internal/naming.Specs`). Change a name once, change it on both
  sides.
- **Watch mode** — `mekami start` re-indexes edited files in place
  via `fsnotify` (with a poller fallback on NFS/SMB/FUSE),
  debouncing, and structural-change detection that promotes
  `go.mod` / `go.work` / `go.sum` edits to a full rebuild. Managed
  by a per-user supervisor that handles restarts, config
  reloads, orphan adoption after crashes, and the global
  inotify watch budget across all your projects. The
  supervisor is itself watched by a small watchdog so a
  wedged supervisor is re-spawned in under 30 seconds.
- **Pure Go** — no CGo. Single static binary backed by
  `modernc.org/sqlite`.

## Install

Mekami ships through the AUR. There is no bootstrap installer —
the AUR package places the binary at `/usr/bin/mekami` directly.

### Arch / Manjaro

```bash
yay -S mekami-bin    # prebuilt binary from GitHub Releases
# or
yay -S mekami        # builds from source (requires go >= 1.26)
```

The two packages `provide` and `conflict` with each other, so
installing one removes the other. See `.aur/README.md` for bump
instructions.

Verify the result:

```bash
mekami --version
```

The version is stamped at build time via
`-ldflags "-X ...install.version=..."`. Untouched builds report
`dev`.

### Wire mekami into an MCP client

```bash
mekami mcp install
```

This writes an `mcp.mekami` entry into the user's `opencode.json`
(respecting `$XDG_CONFIG_HOME`) with the portable form:

```json
{
  "mcp": {
    "mekami": {
      "type": "local",
      "command": ["mekami", "serve"],
      "enabled": true
    }
  }
}
```

The original file is backed up to `opencode.json.bak` before any
change. Pass `--binary /abs/path/mekami` to pin the entry to a
specific binary, `--name <other>` to register under a different
server name, `--disable` to register with `enabled: false`, or
`--env KEY=VALUE` (repeatable) to inject environment variables.

To remove the entry:

```bash
mekami mcp uninstall
```

## Quick start

```bash
# 1. Install via the AUR.
yay -S mekami-bin

# 2. Wire it into your MCP client.
mekami mcp install

# 3. In your Go project: create .mekami/, build the index, and
#    (optionally) install the watcher as a system service so the
#    graph stays in sync with your edits even when no agent is
#    running.
cd /your/go/project
mekami init --daemon=yes   # writes config, builds, starts daemon
# or, for one-off use:
mekami init                 # writes config; `mekami start` later
mekami build

# 4. Restart OpenCode so it picks up the new MCP server. The
#    agent now has 17 tools available automatically.
```

To run tests:

```bash
cd mekami-cli && go test ./...
```

The default database path is `./.mekami/graph.db`. Override it
with the global `--db /path/to/graph.db` flag (accepted by every
subcommand).

## CLI reference

Every command is a top-level verb. There are no `query` /
`watch` / `mcp` parent groups — discover the surface by reading
`mekami --help` once.

### Lifecycle

| Command | Description |
| --- | --- |
| `mekami init` | Create `.mekami/config.json` and (optionally) start the watcher daemon. |
| `mekami serve` | Run the MCP server on stdio. |
| `mekami build` | Build the code graph database. |
| `mekami stats` | Show per-table counts and the last build's root. Use `--json` for machine output. |

### Graph reads (every MCP tool is also a CLI command)

| Command | MCP tool | Description |
| --- | --- | --- |
| `mekami find <q>` | `find_symbol` | Substring search over symbol names. |
| `mekami show <qn>` | `get_symbol` | A symbol's definition. Use `--body` or `--header` to constrain the output. |
| `mekami show-body <qn>` | `show_body` | A symbol's source body (numbered lines). |
| `mekami show-lines <path> <start> [end]` | `show_lines` | A range of lines from a file. |
| `mekami who-calls <qn>` | `who_calls` | Incoming references (callers, type uses, value reads, embeds, imports). |
| `mekami what-calls <qn>` | `what_calls` | Distinct outgoing references. |
| `mekami list-file <path>` | `list_file` | Top-level symbols in a file. |
| `mekami trace <from> <to>` | `trace_calls` | Shortest call path between two symbols. |
| `mekami list-files [prefix]` | `list_files` | Project file tree. |
| `mekami list-package <import>` | `list_package` | All symbols in a package. |
| `mekami list-package-symbols <import>` | `list_package_symbols` | Top-level symbols in a package (JSON). |
| `mekami list-importers <import>` | `list_importers` | Packages that import the given one. |
| `mekami list-modules` | `list_modules` | Indexed modules. |
| `mekami show-modules` | `show_modules` | Per-module package summary. |
| `mekami show-changes` | `show_changes` | Files added/modified/removed since the last build. |
| `mekami find-text <pattern>` | `find_text` | Server-side regex search across source files. |
| `mekami index-status` | `index_status` | Snapshot of the index (last_root, last_build_at, counts). |

All read commands accept `--json` to emit machine-readable JSON to
stdout (a non-zero exit code on a real error; 0 with an empty
result on a no-hits query).

### Daemon controls

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

### MCP integration

| Command | Description |
| --- | --- |
| `mekami mcp install` | Register the mekami MCP server in the host client (OpenCode today). |
| `mekami mcp uninstall` | Remove the entry. |
| `mekami mcp test` | Spawn the server as a subprocess and call a sample of tools (smoke test). |

### Core (language indexers)

| Command | Description |
| --- | --- |
| `mekami core install <lang>[@<version>]` | Register a language indexer for this project. |
| `mekami core list` | List configured and loaded cores. |
| `mekami core uninstall <lang>` | Remove a language indexer from this project. |
| `mekami core status` | Show configured vs loaded cores with a missing/loaded summary. |

### `mekami build` flags

| Flag | Description |
| --- | --- |
| `--root <path>` | Source root (default: cwd). |
| `--lang <lang>` | Language to ingest (default `go`; the binary ships with the Go frontend). |
| `--clean` | Delete the existing DB and rebuild from scratch. |
| `--quiet` | Suppress per-file progress. |
| `--jobs <n>` | Parse workers (0 = `NumCPU`). |

`.mekami/config.json` is the source of truth for which languages
the project tracks. Before every build, the `indexers` list is
reconciled against the rows in `.mekami/graph.db`: any file whose
`lang` is no longer in the set is deleted, with one log line per
removed language:

```
build: removing data for disabled language(s): rust (12 files, 230 symbols, 1144 refs)
```

Passing `--lang <x>` where `<x>` is not yet in `indexers` extends
the list in place and logs the change:

```
build: adding new indexer "rust" to config.json. tracking now: go, rust
```

### `mekami init` flags

| Flag | Description |
| --- | --- |
| `--lang <list>` | Comma-separated list of language cores to enable (default: every core registered in the running binary — "all-available"). Repeatable: `--lang go --lang rust`. |
| `--daemon auto\|yes\|no` | Start the watcher daemon after init. `auto` (default) prompts in a TTY and skips in non-interactive shells. |
| `--yes` | Assume "no" to the daemon prompt (equivalent to non-interactive auto). |
| `--verbose` | Show full `mekami build` progress instead of the one-line summary. |

`init` writes `.mekami/config.json`, persists the chosen cores to
`indexers`, runs an initial `mekami build` (skipped when more than
one core is configured and no `--lang` was passed), and then
optionally starts the watcher. The build's AllowedLangs come
from the just-written `indexers`, so any data in `.mekami/graph.db`
whose `lang` is no longer tracked is removed with the same
`build: removing data for disabled language(s): ...` line that
`mekami build` emits. Re-running `init` is idempotent: without
`--lang` it unions the existing `indexers` with whatever the
binary now registers; with `--lang` the explicit list replaces
what was there. If the binary has no cores registered yet
(fresh checkout), `init` errors out and points at `./build.sh`
or `mekami core install <lang>`.

## MCP tools

All tools return text content (JSON or formatted text) over MCP.
Below is a quick reference; full descriptions are embedded in the
server (the LLM reads them on every call).

| Tool | Purpose |
| --- | --- |
| `find_symbol` | Substring search over symbol names. |
| `get_symbol` | A symbol's definition (formatted text, human-readable). |
| `show_body` | A symbol's source body (numbered lines). |
| `show_lines` | An arbitrary line range from a file. |
| `who_calls` | Incoming references (call, type-use, value, field, embed, import). |
| `what_calls` | Distinct outgoing references. |
| `list_file` | Top-level symbols in a file. |
| `list_package` | All symbols in a package. |
| `show_modules` | High-level summary of indexed modules and their packages. |
| `list_modules` | Indexed modules (JSON). |
| `list_package_symbols` | Top-level symbols declared in a given package. |
| `list_importers` | Packages that import a given package. |
| `list_files` | Project file tree from the indexed snapshot. |
| `trace_calls` | BFS to find a call path between two qualified names. |
| `show_changes` | Files added/modified/removed since the last `mekami build`. |
| `find_text` | Server-side regex search across source files. |
| `index_status` | Snapshot of the index (last_root, last_build_at, counts). |

Several tools accept filters:

- `kind` (`func`, `type`, `method`, `var`, `const`) on `find_symbol`
  — filters symbol kinds.
- `ref_kind` (`call`, `type-use`, `value`, `field`, `embed`, `import`)
  on `who_calls` — filters reference edge kinds.
- `path_prefix` on most listing tools — restricts to files whose
  path starts with the given prefix.

## Supported layouts

- **Single Go module** — point `--root` at the directory
  containing `go.mod`.
- **`go.work` workspace** — point `--root` at the directory
  containing `go.work`. Mekami indexes every `use`d module.
- **Sub-module of a workspace** — point `--root` at a single
  module inside a workspace; sibling modules are skipped to avoid
  cross-contaminating the graph.

Only `--lang go` is implemented in the binary. Passing any other
value returns an error listing the registered frontends. Adding a
new language is documented in
`internal/graph/ingest/frontend/README.md`.

## How indexing works

1. The walker enumerates every `*.go` file under the source root,
   skipping `.git`, `.mekami`, `node_modules`, `vendor`, `_dev`,
   and `*_test.go`.
2. For each file: read the bytes, hash with `sha256`, and compare
   against the stored hash. Unchanged files are skipped without
   re-parsing.
3. Changed files are parsed with `go/parser`. The collector walks
   the AST and emits:
   - **Symbols** — `func`, `method`, `type`, `var`, `const`, plus
     a synthetic `__imports__` anchor for the import block. Each
     symbol carries its `qualified_name` (e.g.
     `graph.queries.SearchSymbols`), line range, signature, and
     export status.
   - **Refs** — `call`, `type-use`, `value`, and `import` edges,
     each tagged with the source line. A lightweight
     intra-procedural type resolver maps local variables to their
     declared types so that `m := recv.Field` can resolve `Field`
     to `pkg.Type.Field` even when the receiver's type is
     inferred. Anonymous function literals at file scope — the
     typical `&cobra.Command{ RunE: func(...) error { ... } }`
     shape — get a synthetic owner symbol (kind `funclit`,
     qualified name `pkg.__lit__<file>_<line>__`) so every call
     inside the closure stays visible in `callers_of` and
     `path_between`.
4. All results are written inside a single SQLite transaction
   (WAL mode, `synchronous=NORMAL`, `foreign_keys=ON`). Files that
   disappeared since the last build are removed in the same pass.

## Architecture

The repo is split across three Go modules. A workspace
(`go.work`) ties them together for local development; the AUR
PKGBUILD does the equivalent at build time.

```
github.com/Wolf258/mekami-core         Indexer, queries, SQLite store
├── api/v1/                           Public Frontend contract (vendored
│   │                                 by every language indexer)
├── model/                            DB rows + DTOs
├── store/                            SQLite store (open/close, tx, scan)
├── walk/                             FS walker + fingerprint helper
├── modlayout/                        go.mod / go.work resolution
├── ingest/                           Build orchestration + incremental
├── frontend/all_gen/                 Generated blank-imports (rewritten
│   │                                 by `mekami core install`)
├── queries/, path/, diff/, grep/     Read-side helpers
└── testutil/                         Shared test fixtures

github.com/Wolf258/mekami-core-go      Go language indexer (lives in
│                                     its own module since phase 2)
├── parser.go, collector.go, ...      Frontend implementation
└── external_test/                    Cross-package tests of the public
                                      signature helpers

github.com/Wolf258/mekami-core         CLI / MCP / supervisor / daemon
├── main.go                           blank-imports core/frontend/all_gen
├── cmd/mekami/                       Cobra entrypoint
│   ├── root.go                       Specs -> cobra loop
│   ├── runner.go                     dispatch + --json + exit codes
│   ├── commands.go                   lifecycle / daemon / mcp runners
│   ├── coreinstall.go                `core install` / `core list` /
│   │                                 `core uninstall` / `core status`
│   ├── mcptest.go                    `mekami mcp test` smoke runner
│   ├── util.go                       printJSON, supervisor helpers
│   ├── service_*.go                  platform-specific service install
│   ├── service_status.go             `service status` runner
│   └── dbpath.go                     --db flag plumbing
├── internal/
│   ├── config/                       .mekami/config.json schema + Load
│   │                                 (parses the `indexers` list)
│   ├── coreinstall/                  `core install` resolver + gen +
│   │                                 uninstall + list implementation
│   ├── naming/                       single source of truth for the
│   │                                 user-facing surface (Spec, Flag, Specs)
│   ├── handlers/read.go              shared read implementations (CLI+MCP)
│   ├── mcp/server.go                 MCP server, tool registry from Specs
│   ├── format/format.go              human-readable text formatters
│   ├── install/                      MCP client registration (opencode)
│   ├── watch/                        watcher daemon
│   └── supervisor/                   per-user daemon supervisor
└── tests/                            black-box tests
```
```

```
github.com/Wolf258/mekami-cli       CLI / MCP / supervisor / daemon
├── main.go                        blank-imports core/frontend/all_gen
├── cmd/mekami/                    Cobra entrypoint
│   ├── root.go                    Specs -> cobra loop
│   ├── runner.go                  dispatch + --json + exit codes
│   ├── commands.go                lifecycle / daemon / mcp runners
│   ├── mcptest.go                 `mekami mcp test` smoke runner
│   ├── util.go                    printJSON, supervisor helpers
│   ├── service_*.go               platform-specific service install
│   ├── service_status.go          `service status` runner
│   └── dbpath.go                  --db flag plumbing
├── internal/
│   ├── config/                    .mekami/config.json schema + Load
│   │                              (now also parses the `indexers` list)
│   ├── naming/                    single source of truth for the
│   │                              user-facing surface (Spec, Flag, Specs)
│   ├── handlers/read.go           shared read implementations (CLI+MCP)
│   ├── mcp/server.go              MCP server, tool registry from Specs
│   ├── format/format.go           human-readable text formatters
│   ├── install/                   MCP client registration (opencode)
│   ├── watch/                     watcher daemon
│   └── supervisor/                per-user daemon supervisor
└── tests/                         black-box tests
```

- `cmd/mekami` is the only package that depends on `cobra`. The
  rest is a pure library.
- `internal/naming` is the single source of truth: every CLI
  command and every MCP tool is declared as a `Spec` in
  `specs.go`. The CLI and the MCP server each walk the slice and
  register their side; renaming a tool or adding a flag is a
  one-line change.
- `internal/handlers` contains the read-side logic shared by the
  CLI runner and the MCP server. Both call the same functions;
  the only thing that differs is the wire format.
- `core/store` is the only package that talks to SQLite.
  `core/queries`, `core/path`, and `core/diff` issue their own
  SELECTs through `Store.DB()` for read paths and share the
  row-decoding helpers in `store/scan.go`.
- `core/ingest` is split into `build.go` (orchestration:
  workspace discovery, parallelism, deletes), `incremental.go`
  (re-ingest a set of paths without re-walking the tree),
  `write.go` (language-agnostic `WriteParseResult`), and the
  `frontend/` subpackage. The build pipeline resolves an
  `api.Frontend` once per `Build` and calls its `ParseFile` from
  a worker pool. The `api/v1` package is the public surface
  external indexers implement.
- `cli/watch` runs an `fsnotify` reader goroutine, debounces
  events through an internal coalescer, and dispatches to
  `BuildIncremental` for files handled by the active frontend or
  `Build` when a structural file is touched (Go: `go.mod` /
  `go.work` / `go.sum`; configurable per frontend via
  `Frontend.StructuralFiles()`). A `Source` abstraction lets the
  daemon swap `fsnotify` for a polling source on filesystems
  where inotify is unreliable (NFS, SMB, FUSE). The daemon mode
  re-execs the same binary with hidden env vars so the same code
  path serves both modes.

### Phase 2: per-language indexer repos

Each language indexer is its own Go module. The Go indexer lives
in [`mekami-core-go`](https://github.com/Wolf258/mekami-core-go)
and is **not** bundled by default. After cloning, run
`mekami core install go` to add it to your `.mekami/config.json`
and rebuild. Additional languages — Rust, C, etc. — follow the
same shape: a standalone repo at
`github.com/Wolf258/mekami-core-<lang>` that depends only on
`github.com/Wolf258/mekami-api/api/v1` to register itself.

Project-wide installation is driven by `.mekami/config.json`:

```json
{
  "version": 1,
  "indexers": {
    "go": "v0.1.0",
    "rust": "v0.1.0"
  }
}
```

`indexers` is a map from language name to the version
`core install` resolved. An empty value (`"rust": ""`) means the
language was added by `mekami init` but `core install` hasn't run
for it yet — the build still tracks it, and a later
`mekami build --lang rust` (or `core install rust`) fills the
version. `mekami core install <lang>[@<version>]` resolves the
version via the Go module proxy (`go list -m -versions`),
writes the entry to `indexers`, and regenerates
`mekami-cli/internal/core/frontend/all_gen/all_gen.go` with a
fresh blank import. `mekami core list` and `mekami core status` show the
indexer set requested by the config versus what the running
binary has registered (frontends that are listed but whose blank
import is missing are reported as `missing`).

## Watch mode

`mekami start` keeps the index in sync with the source tree
while you edit. The watcher is a long-lived daemon owned by a
per-user **supervisor** process. There is at most one supervisor
per user, and it manages every Mekami daemon across every project
you have initialised.

```bash
mekami init --daemon=yes          # create config, build, start daemon
# or, manually:
mekami start                      # ask the supervisor to spawn a daemon
mekami status                     # one-line summary
mekami logs                       # tail the daemon log
mekami stop                       # ask the supervisor to stop the daemon
mekami restart                    # stop + start
mekami reload                     # re-read .mekami/config.json
```

`mekami service install` registers the supervisor as a system
service (per-user, single instance) so it starts automatically
when you log in and rehydrates every daemon from `daemons.json`:

- **Linux**: writes a single `systemd --user` unit
  (`mekami-supervisor.service`).
- **macOS**: writes a single `~/Library/LaunchAgents` plist
  (`dev.mekami.supervisor`).
- Other platforms: not implemented (you can still run
  the supervisor manually from your shell rc). The
  watchdog still works in this mode: it spawns
  alongside the supervisor and gives you auto-restart
  of the supervisor for free, even without a service
  manager.

### The supervisor

The supervisor is the per-user process that owns all watcher
daemons. It:

- starts/stops daemons on demand (`init --daemon=yes`, `start`,
  `stop`, `restart`),
- monitors each daemon and restarts it on crash (with backoff),
- re-reads `daemons.json` on startup and rehydrates every daemon
  that was active before the supervisor stopped,
- **adopts orphaned daemons** that survived a supervisor crash
  (PID + socket + ping) instead of double-forking,
- tracks the global inotify watch budget and degrades the noisiest
  daemons to the poller when the budget gets tight,
- is itself supervised by a tiny **watchdog** process that
  re-spawns the supervisor when it is wedged (PID alive but
  unresponsive).

State lives in `$XDG_CONFIG_HOME/mekami/supervisor/`:

- `daemons.json` — the registered daemons and their last known
  state.
- `supervisor.sock` — the Unix socket the CLI talks to.
- `supervisor.pid` — the supervisor's PID (single-instance).
- `supervisor.log` — the supervisor's own log.

You rarely invoke the supervisor directly; the daemon commands do
it for you. The supervisor is what `init --daemon=yes` and
`start` start on first use.

### Orphan adoption

When the supervisor starts up (or when a user runs
`mekami start` manually), it checks every project in
`daemons.json` whose last known state was `running`,
`starting`, `reloading`, or `crashed`. For each, it asks:

1. Is `.mekami/watcher.pid` present and parseable?
2. Is the recorded PID alive (`kill -0`)?
3. Does `.mekami/watcher.sock` exist?
4. Does that socket answer a `ping`?

If all four answers are yes, the existing daemon is
**adopted**: the supervisor records its PID in its in-memory
table and skips the re-spawn. This is what makes
`kill -9 mekami-supervisor` safe — the watcher keeps
running, the next supervisor invocation finds it, and you
do not end up with two daemons fighting over the same
project socket.

If the PID file is stale (the recorded process is gone) but
the socket is still there, `Start` cleans up `.mekami/`
(pids/socket/heartbeat) before forking a fresh daemon. The
cleanup is best-effort: a leftover file that cannot be
removed is reported as a normal spawn error.

If the heartbeat file is present but stale at adoption time
(more than 30s since the last write), the supervisor logs a
warning to `supervisor.log` but still adopts the daemon.
A PID that responds to `kill -0` and answers a ping is, by
definition, alive; the heartbeat may just be lagging.

### The supervisor watchdog

A daemon that lives only as long as its supervisor is
fragile: if the supervisor ever wedges (alive in the
process table, but not responding to its IPC socket),
nothing on the system will restart it. `systemd --user`
and `LaunchAgents` only restart a process that has exited;
they cannot tell that a process is stuck.

To close this gap, the supervisor is launched together
with a tiny sibling: the **watchdog**. The watchdog polls
the supervisor's PID and Unix socket every 5 seconds.
After 6 consecutive failed health checks (30 seconds of
unresponsiveness), the watchdog:

1. Sends `SIGKILL` to the supervisor's PID.
2. Removes the stale `supervisor.sock` so the new
   supervisor can bind it.
3. Re-spawns the supervisor (`supervise _run`), which in
   turn re-spawns its own watchdog.

The watchdog is best-effort:

- If the supervisor exits cleanly, the watchdog notices
  the missing PID file and exits; the service manager
  (`systemd --user` / `LaunchAgent`) restarts the whole
  pair. The watchdog is not a replacement for the service
  install; it is a complement that catches the "wedged
  but alive" case the service manager cannot.
- If you do not run `mekami service install`, the
  watchdog still works: it is launched automatically the
  first time any `mekami` command needs the supervisor.
  The watchdog is what keeps the supervisor alive across
  reboots on platforms without a service manager.

You never invoke the watchdog directly. It is the hidden
`supervise _watchdog` subcommand and runs in its own
session (`setsid`) so it survives the parent shell
exiting. On startup the watchdog writes its own PID to
`$XDG_CONFIG_HOME/mekami/supervisor/watchdog.pid` and
removes the file on exit, so `service uninstall` can find
and signal it without scanning the process table.

The watchdog also watches for a **stop sentinel** at
`$XDG_CONFIG_HOME/mekami/supervisor/stop`. When the
file is present, the watchdog exits on its next tick
(immediately if the sentinel is already there on
startup) regardless of supervisor state. The sentinel
is what `service uninstall` uses to make the watchdog
exit deterministically rather than waiting for the
next health-check tick to discover the supervisor is
gone. The supervisor clears the sentinel on the next
startup so a leftover file from a previous uninstall
does not cascade into the new run.

### Daemon health and orphan recovery

Each watcher daemon writes a heartbeat to
`.mekami/heartbeat` every 5 seconds. The heartbeat is a
single line containing the unix-nano timestamp of the
write. The supervisor uses it as a secondary liveness
signal: a daemon that answers `kill -0` and pings but has
not refreshed its heartbeat in 30 seconds is logged as
"stale heartbeat" on adoption, so a future maintainer can
see whether a previously-frozen process was picked up.

The daemon also carries a copy of the supervisor's PID
(the `_MEKAMI_DAEMON_SUPERVISOR_PID` env var). It pings
that PID every 5 seconds; if the supervisor becomes
unreachable, the daemon logs
`"warning: supervisor pid=N unreachable, running
standalone"` once a minute. By default the daemon keeps
running — losing the supervisor is not a reason to lose
the index.

If you want the daemon to give up after being orphaned
for a while (for example, in CI containers that come and
go), set `watch.self_terminate_on_orphan` in
`.mekami/config.json`:

```json
{
  "watch": {
    "self_terminate_on_orphan": "10m"
  }
}
```

The value is a `time.ParseDuration` string (`30s`, `5m`,
`1h`, ...). The empty string (the default) means "never
self-terminate", which is the right default for
developers who want the watcher to keep the index fresh
even when no supervisor is around.

### Uninstalling the service

`mekami service uninstall` is the symmetric counterpart
to `service install`. On Linux and macOS it:

1. Sends a `quit-all` IPC request to the running
   supervisor. The supervisor stops every registered
   daemon (graceful IPC stop → `SIGTERM` →
   `SIGKILL` on timeout), writes the stop sentinel,
   and signals the watchdog's PID file so the
   watchdog exits immediately on its next tick.
2. Sends `SIGTERM` via the service manager
   (`systemctl --user disable --now` on Linux,
   `launchctl unload -w` on macOS) as a safety net
   for the case where the supervisor was not
   running or its IPC socket was unreachable.
3. Removes the runtime state files from
   `$XDG_CONFIG_HOME/mekami/supervisor/`:
   `supervisor.pid`, `supervisor.sock`,
   `supervisor.log`, `watchdog.pid`, and the stop
   sentinel. A missing file is not an error; a
   permission error is logged but does not abort
   the uninstall.
4. Removes the unit file (`mekami-supervisor.service`
   on Linux, `dev.mekami.supervisor.plist` on
   macOS) and tells the service manager to reload.

The per-project `.mekami/` directories and the
`daemons.json` registry are **preserved**. A
subsequent `mekami service install` will rehydrate
the same set of daemons from the registry, so the
user's intent ("watch these projects") survives the
uninstall. The result is what we call a **hard
uninstall**: the supervisor, watchdog, and all
daemon children are gone, but the registry and
per-project state are intact. A future install
brings everything back as it was.

If you also want the registry and per-project state
removed, the user can do it manually (`rm -rf
$XD_CONFIG_HOME/mekami` and the `.mekami/`
directories inside each project). Adding a
`--purge` flag to `service uninstall` is a
deliberate non-feature: deleting user data without
an explicit, separate opt-in is too easy to do by
accident.

The watchdog is reachable via
`$XDG_CONFIG_HOME/mekami/supervisor/watchdog.pid`,
so `service uninstall` does not have to scan the
process table to find it. If the PID file is
missing (e.g. the user manually killed the
watchdog) the uninstall falls through to the
service-manager unload, which is the same safety
net the supervisor relies on.

The inotify budget is enforced on Linux. Each fsnotify watcher
registers one watch per directory; with thousands of directories
across many projects, the per-user limit
(`/proc/sys/fs/inotify/max_user_watches`, typically 8192 by
default) gets tight. The supervisor measures consumption; once it
crosses 80% it flips the noisiest daemons to the poller
(`fallback: "poll"`) automatically. If you want to raise the
limit:

```bash
# Raise the per-user watch budget to 524288.
sudo sysctl fs.inotify.max_user_watches=524288
```

### Configuration

The watcher reads its settings from `.mekami/config.json`. The
file is optional; absent → sensible defaults. Schema:

```json
{
  "version": 1,
  "watch": {
    "enabled": true,
    "debounce_ms": 250,
    "ignore": ["*.tmp", "*.swp", ".DS_Store"],
    "on_start": "build",
    "log": "info",
    "fallback": "auto",
    "poll_interval_s": 30,
    "log_level": "resumen",
    "self_terminate_on_orphan": ""
  },
  "build": {
    "jobs": 0
  }
}
```

- `debounce_ms` is the quiet window the coalescer waits after the
  last filesystem event before firing a rebuild. 0 disables the
  debounce (each event rebuilds immediately). 250ms is a good
  default for editors that emit multiple events per save.
- `on_start` is what the watcher does once before entering the
  event loop: `build` (full `Build`, the default), `incremental`
  (re-ingest the existing set of files), or `skip` (assume the DB
  is fresh).
- `ignore` is a list of basename globs to drop on top of the
  build walker's built-in exclusions (`.git`, `.mekami`, `vendor`,
  `node_modules`, `_dev`). Use it for editor temp files, swap
  files, etc.
- `log` is one of `info` (one line per batch), `debug` (per-event
  logging), or `quiet` (errors only). Applies to the foreground
  CLI.
- `fallback` selects the event source. `auto` (default) uses
  `fsnotify` and falls back to a poller if the FS is detected as
  unreliable (NFS, SMB, FUSE). Set to `fsnotify` to force the
  inotify path, or `poll` to force the poller. The poller runs
  every `poll_interval_s` seconds.
- `log_level` controls the daemon's persisted log
  (`.mekami/watcher.log`). `resumen` (default) writes one line
  per batch plus errors and lifecycle events; `verbose` writes
  per-event lines. The log is rotated at 1 MiB with three
  backups.
- `self_terminate_on_orphan` is the maximum time the daemon
  will run standalone after losing contact with its
  supervisor. Parsed by `time.ParseDuration`, so values
  like `"30s"`, `"10m"`, `"1h"` are accepted. The empty
  string (default) means "never self-terminate": the
  daemon keeps running and the user gets a chance to
  investigate. Set it explicitly only when you want the
  daemon to give up after N minutes without a supervisor
  (e.g. ephemeral CI containers). See [Daemon health and
  orphan recovery](#daemon-health-and-orphan-recovery) for
  the full mechanics.

Structural changes (any of `go.mod`, `go.work`, `go.sum`)
automatically promote the current batch to a full rebuild — the
watcher's incremental path is for ordinary Go source edits, where
re-walking the tree is wasted work. If the watcher cannot find
`last_root` in the DB (e.g. you ran `start` without ever
building), it falls back to a one-shot full build.

The watcher daemon shuts down cleanly on `SIGINT` / `SIGTERM`
(sent via `mekami stop`) and writes a final summary line with
batch / file / error counters to its log.

## Limitations

- **One shipped language frontend (Go).** The architecture
  supports additional languages (see
  `internal/core/frontend/README.md`), but no frontend is
  bundled in the binary by default. Add a frontend by publishing a
  new module at `github.com/Wolf258/mekami-core-<lang>`, depending
  on `github.com/Wolf258/mekami-api/api/v1`, and registering via
  `mekami core install <lang>`.
- **No body text in the index.** Mekami only indexes symbol
  names and reference edges. For substring search inside function
  bodies, comments, log strings, or TODOs, use `mekami find-text`
  (server-side regex over the source tree) or your editor's read
  tool.
- **Intra-procedural type resolution only.** The local-variable
  type resolver understands function parameters, short variable
  declarations, plain assignments, `range` clauses, and
  same-package constructor calls. It does not chase through
  cross-package calls — that would require `go/types` on the full
  package, which is out of scope for now.
- **Workspace vs. sub-module builds.** Building from a workspace
  root indexes every `use`d module; building from a sub-module
  skips siblings. Switching between the two without `--clean` is
  rejected to avoid leaving stale paths in the DB.
- **No background daemon in `serve`.** `serve` runs a single
  stdio session per invocation; it reads the database but never
  writes to it. Long-running reindexing is triggered explicitly
  via `build`, or in the background via the watcher started by
  `init --daemon=yes` or `start`. Multiple `serve` instances on
  the same project share the same daemon.

## Development

Run the test suite from the `mekami-cli` module:

```bash
cd mekami-cli && go test ./...
```

The CI workflow at `.github/workflows/mekami.yml` runs on every
push to `main` and on every pull request:

1. `go test ./...` in `mekami-cli/`.
2. `./build.sh` to produce the binary.
3. `./mekami build` to index the Mekami codebase itself
   (self-hosting test).
4. `./mekami mcp test` to verify the MCP wire works end-to-end
   against the freshly built graph.

## Status

Early-stage. The schema, ingest pipeline, MCP server, CLI, and
test suite are in place. Expect breaking changes as the toolset
expands and the type resolver grows.

# Mekami

> A SQLite-backed code graph for Go projects, queryable from the CLI or by LLM agents over the [Model Context Protocol](https://modelcontextprotocol.io).

Mekami walks a Go project, parses every file with `go/parser`, and persists symbols, definitions, signatures, and reference edges into a single SQLite database. It runs as an MCP server so an agent (Claude, OpenCode, etc.) can ask structural questions — *who calls `X`? where is `X` defined? what's the call path between `A` and `B`?* — instead of grepping the source tree.

Mekami is not a code search engine. It indexes symbol names and reference edges only; for substring search inside function bodies, comments, or log strings, use `mekami find-text` (or the MCP `find_text` tool) or your editor's read tool.

## Features

- **Language-agnostic by design** — Go is supported out of the box; the public `api/v1` package lets you plug in new language frontends (Rust, TS, etc.) without forking the core.
- **LLM-native** — a structured graph query returns exactly the symbols and edges an agent needs, instead of the unstructured, token-heavy dump `grep` produces. The same data is exposed on both the CLI and the MCP surface.
- **Fast and predictable** — pure Go, no CGo, single static binary backed by `modernc.org/sqlite`. Performance stays constant on medium and large codebases.
- **Incremental + watch** — files are fingerprinted with `sha256`; `mekami start` re-indexes edited files in place via `fsnotify` (with a poller fallback on NFS/SMB/FUSE), with debouncing and structural-change detection that promotes edits to `go.mod` / `go.work` / `go.sum` to a full rebuild. A per-user supervisor manages the daemon across all your projects.
- **Dev-friendly monorepo** — `mekami`, `mekami-api`, and `mekami-core-go` live in the same workspace, with a unit + integration test suite (`go test ./...`, `-tags integration`) and a one-shot `./build.sh` that produces the dev binary in the repo root.

## Quick start

```bash
# Arch (and derivatives)
yay -S mekami-bin

# Index a Go project and start the daemon
cd /path/to/your/project
mekami init
mekami start
```

Wire `mekami` into your MCP client (Claude Desktop, OpenCode, etc.) and ask structural questions about your code. Every MCP tool is also a top-level `mekami` command.

The `mekami` binary is produced by `./build.sh`. The AUR package `mekami-bin` installs it to `/usr/bin/mekami`. Verify with `mekami --version`.

## Where to go next

- [Installation](https://wolf258.github.io/mekami/getting-started/installation/)
- [Quick start](https://wolf258.github.io/mekami/getting-started/quickstart/)
- [CLI reference](https://wolf258.github.io/mekami/user-guide/cli/)
- [MCP tools](https://wolf258.github.io/mekami/user-guide/mcp-tools/)
- [How indexing works](https://wolf258.github.io/mekami/user-guide/how-it-works/)
- [Architecture](https://wolf258.github.io/mekami/architecture/)
- [Writing a new language frontend](https://wolf258.github.io/mekami/extending/writing-a-frontend/)
- [Contributing & testing](https://wolf258.github.io/mekami/development/contributing/)
- [Releasing](https://wolf258.github.io/mekami/development/releasing/)
- [AUR packaging](https://wolf258.github.io/mekami/build/aur/)
- [License (MIT)](https://wolf258.github.io/mekami/license/)

For the source code layout, see the [architecture overview](https://wolf258.github.io/mekami/architecture/) in the docs.

# Quick start

This walkthrough assumes you have the `mekami` binary on your `$PATH` (see [installation](installation.md) if you don't).

## 1. Initialize a workspace

Inside a Go project, run:

```bash
mekami init
```

This creates a `.mekami/` directory with a default `config.json` and a `graph.db` placeholder. The configuration is committed-by-convention; the database is per-user and should be added to `.gitignore`.

If your project uses `go.work` and you want every `use`d module indexed as one graph, run from the workspace root. If you want only the current module indexed, run from inside the module directory — Mekami auto-detects both.

## 2. Build the index

A one-shot build:

```bash
mekami build
```

The first build walks every Go file under the workspace, parses it with `go/parser`, and persists symbols, definitions, signatures, and reference edges into `.mekami/graph.db`. Subsequent runs only re-ingest files whose content or import set has changed.

For large repos, the build is parallel across `runtime.NumCPU()` workers; writes are serialized through a single SQLite transaction.

## 3. Ask a question

Mekami's CLI commands and MCP tools share a vocabulary — every query is a single command.

```bash
# What does Foo look like?
mekami find Foo

# Who calls Bar?
mekami who-calls Bar

# What's the call path between A and B?
mekami trace A B

# Outlines
mekami file-outline ./cmd/...
mekami package-outline ./internal/foo
```

All commands read from the same `.mekami/graph.db` the build produced. The CLI renders results as human-readable text; the same calls over MCP return JSON.

## 4. Stay in sync with the daemon

For an edit-driven workflow, run the watch daemon instead of `mekami build`:

```bash
mekami start
```

This spawns a per-project daemon that watches for file changes, debounces them, detects structural changes (e.g. a `go.mod` edit), and re-indexes incrementally. Stop it with `mekami stop`.

If you want this to survive shell sessions, install the supervisor as a user service:

```bash
mekami service install --start
```

See the [watch mode](../user-guide/watch-mode.md) page for the full supervisor / watchdog / orphan-adoption story.

## 5. Use it from your agent

If you wired Mekami into an MCP client with `mekami mcp install`, you can now ask the agent things like:

> Who calls `connectToServer` and what is the call path from `main` to it?

The agent will dispatch the `who_calls` and `call_path` tools and synthesize an answer from the graph. See [MCP tools](../user-guide/mcp-tools.md) for the full tool surface.

## Next step

- Read the [CLI reference](../user-guide/cli.md) to see what else is available.
- Read [how indexing works](../user-guide/how-it-works.md) to understand what gets persisted.
- If you want to add support for a new language, see [extending Mekami](../extending/index.md).

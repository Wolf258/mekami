---
title: Mekami
slug: /
hide_title: false
hide_table_of_contents: true
---

import CardGrid, { Card } from '@site/src/components/CardGrid';

# Mekami

A SQLite-backed Go code graph for humans and LLM agents, exposed over the [Model Context Protocol](https://modelcontextprotocol.io).

Mekami walks a Go project, parses every file with `go/parser`, and persists symbols, definitions, signatures, and reference edges into a single SQLite database. It runs as an MCP server so an agent (Claude, OpenCode, etc.) can ask structural questions — *who calls `X`? where is `X` defined? what's the call path between `A` and `B`?* — instead of grepping the source tree. The same graph is also queryable from the shell: every MCP tool is also a top-level `mekami` command.

Mekami is **not** a code search engine. It indexes symbol names and reference edges only; it does not index raw source text. For substring search inside function bodies, comments, log strings, or any arbitrary text, use `mekami find-text` (or the MCP `find_text` tool) or your editor's read tool.

## At a glance

- **Incremental indexing** — files are fingerprinted with `sha256`; unchanged files are skipped on rebuild.
- **Parallel ingest** — parsing runs on `runtime.NumCPU()` workers; writes are serialized through a single SQLite transaction.
- **Workspace-aware** — detects `go.work` and indexes every `use`d module from the workspace root, or just the current module when run from a sub-module.
- **MCP server** — 17 tools over stdio covering symbol search, callers/callees, call-path tracing, file/package/module outlines, source ranges, filesystem text search, and an index snapshot.
- **Unified vocabulary** — both CLI and MCP surfaces are declared in one place (`internal/naming.Specs`). Change a name once, change it on both sides.
- **Watch mode** — `mekami start` re-indexes edited files in place via `fsnotify` (with a poller fallback on NFS/SMB/FUSE), debouncing, and structural-change detection that promotes `go.mod` / `go.work` / `go.sum` edits to a full rebuild. Managed by a per-user supervisor that handles restarts, config reloads, orphan adoption after crashes, and the global inotify watch budget across all your projects.
- **Pure Go** — no CGo. Single static binary backed by `modernc.org/sqlite`.

## Where to go next

<CardGrid>
  <Card
    icon="🚀"
    title="Getting started"
    description="Install Mekami from the AUR, wire it into your MCP client, and run your first query."
    to="/getting-started/installation"
  />
  <Card
    icon="🖥️"
    title="CLI reference"
    description="Every command Mekami exposes, grouped by purpose: lifecycle, graph reads, daemon controls, service manager, MCP, core management."
    to="/user-guide/cli"
  />
  <Card
    icon="⚙️"
    title="How indexing works"
    description="Walk the data flow from source files to the SQLite graph: fingerprint, AST collector, type resolver, writer."
    to="/user-guide/how-it-works"
  />
  <Card
    icon="🛠️"
    title="Extend Mekami"
    description="Add a new language frontend by implementing the `api.Frontend` interface. Full walkthrough using Rust as an example."
    to="/extending/writing-a-frontend"
  />
</CardGrid>

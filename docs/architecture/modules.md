# Modules

The umbrella repo is **one Go module** (`github.com/Wolf258/mekami-cli`). The public `api/v1` contract and the language cores are external Go modules pulled from the module proxy by version.

## `github.com/Wolf258/mekami-cli` (this repo)

The single Go module that compiles to the `mekami` binary. Contains:

- The cobra CLI runner.
- The MCP server.
- The supervisor + watchdog pair.
- The indexing pipeline (fused in as `internal/core/`).
- The test suite.

The repo root carries a `go.work` file that lists `./mekami-cli` so build and test commands from the root resolve the module. No `replace` directives are required: the external modules are fetched from the proxy by version.

### Tree

```text
mekami-cli/
├── main.go                              # blank-imports all_gen, calls cmd/mekami.Execute()
├── go.mod
├── cmd/mekami/                          # cobra entrypoint
│   ├── root.go                          # Specs -> cobra loop
│   ├── runner.go                        # dispatch + --json + exit codes
│   ├── commands.go                      # lifecycle / daemon / mcp runners
│   ├── coreinstall.go                   # core install / list / uninstall / status
│   ├── mcptest.go                       # mekami mcp test smoke runner
│   ├── util.go                          # printJSON, supervisor helpers
│   ├── service_linux.go                 # systemd --user
│   ├── service_darwin.go                # LaunchAgent
│   ├── service_other.go                 # stub
│   ├── service_status.go                # service status runner
│   └── dbpath.go                        # --db flag plumbing
├── internal/
│   ├── config/                          # .mekami/config.json schema + Load
│   ├── coreinstall/                     # core install / list / uninstall
│   ├── naming/                          # single source of truth (Spec, Specs)
│   ├── handlers/                        # shared read implementations
│   ├── mcp/                             # MCP server, tool registry from Specs
│   ├── format/                          # human-readable text formatters
│   ├── install/                         # MCP client registration (opencode)
│   ├── watch/                           # watcher daemon + supervisor watchdog
│   ├── supervisor/                      # per-user daemon supervisor
│   ├── socktestutil/                    # cross-package test helpers (socket paths)
│   ├── core/                            # the indexing pipeline
│   │   ├── ingest/                      # build / incremental / write
│   │   ├── store/                       # SQLite store
│   │   ├── queries/, path/, diff/, grep/  # read-side helpers
│   │   ├── walk/                        # FS walker + fingerprint
│   │   ├── modlayout/                   # go.mod / go.work resolution
│   │   ├── model/                       # DB rows + DTOs
│   │   ├── frontend/
│   │   │   ├── all_gen/                 # generated blank imports
│   │   │   └── README.md                # how to write a new indexer
│   │   ├── integration_test/            # e2e tests, build tag "integration"
│   │   └── testutil/                    # ingest/build test fixtures
└── tests/                               # black-box tests
```

`cmd/mekami` is the only package that depends on `cobra`. The rest is a pure library.

## `github.com/Wolf258/mekami-api` (external)

Tiny pure-stdlib module that defines the `api.Frontend` interface every language indexer implements. Zero internal dependencies — external frontends only need to depend on this.

Public types and functions:

- `Workspace`, `FileMeta`, `ModuleInfo`
- `ParseResult`, `Symbol`, `SymbolKind`
- `Ref`, `RefKind`
- `ModuleEntry`
- `Frontend` interface
- `Registry` and the global `api.Global` registry
- `Register`, `Get`, `Names`, `All`, `IsStructural`, `DefaultStructuralFiles`

For the full reference, see the [Frontend API reference](../api-reference/frontend-api.md).

## `github.com/Wolf258/mekami-core-go` (external)

The Go-language frontend module. Implements `api.Frontend`. Lives in its own repo so other languages can follow the same shape (`mekami-core-rust`, `mekami-core-c`, …).

Files:

- `parser.go` — entry point and `ParseFile`.
- `collector.go` — top-level walker.
- `visitor.go` — `ast.Visitor` that emits `Symbol` and `Ref` rows.
- `walkexpr.go` — intra-procedural type resolver.
- `imports.go` — import-block handling and synthetic `__imports__` anchor.
- `resolve.go` — package and import resolution.
- `astutil.go` — small AST helpers (signature rendering, qualified-name assembly, funclit synth).

## Layered dependency graph

```text
                  ┌─────────────────────┐
                  │      cmd/mekami     │
                  └──────────┬──────────┘
                             │
       ┌─────────────────────┼─────────────────────┐
       ▼                     ▼                     ▼
┌─────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  internal/  │    │   internal/mcp   │    │   internal/     │
│  watch      │    │                  │    │   supervisor    │
└──────┬──────┘    └────────┬────────┘    └────────┬────────┘
       │                    │                      │
       └────────┬───────────┴──────────────────────┘
                ▼
        ┌──────────────────┐
        │  internal/       │
        │  handlers        │
        └────────┬─────────┘
                 ▼
        ┌──────────────────┐         ┌────────────────────┐
        │  internal/core/  │ ◀──────▶│ mekami-api/api/v1  │
        │  ingest / store  │         └────────────────────┘
        └──────────────────┘
                 ▲
                 │ (blank import via all_gen)
                 │
        ┌────────┴─────────┐
        │ mekami-core-go   │
        │ (and future lang)│
        └──────────────────┘
```

Solid arrows are Go imports; the dashed line is a blank import driven by `all_gen.go`. The whole tree is one Go module, except for `mekami-api` and the language cores which are pulled by version from the module proxy.

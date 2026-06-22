# Limitations

- **One shipped language frontend (Go).** The architecture supports additional languages (see [Writing a frontend](extending/writing-a-frontend.md)), but no frontend is bundled in the binary by default. Add a frontend by publishing a new module at `github.com/Wolf258/mekami-core-<lang>`, depending on `github.com/Wolf258/mekami-api/api/v1`, and registering via `mekami core install <lang>`.
- **No body text in the index.** Mekami only indexes symbol names and reference edges. For substring search inside function bodies, comments, log strings, or TODOs, use `rg` (ripgrep) or your editor's read tool. The narrow exception is `find-symbols` (CLI) / `find_symbols` (MCP), which matches symbol *declarations* by name substring.
- **Intra-procedural type resolution only.** The local-variable type resolver understands function parameters, short variable declarations, plain assignments, `range` clauses, and same-package constructor calls. It does not chase through cross-package calls — that would require `go/types` on the full package, which is out of scope for now.
- **Workspace vs. sub-module builds.** Building from a workspace root indexes every `use`d module; building from a sub-module skips siblings. Switching between the two without `--clean` is rejected to avoid leaving stale paths in the DB.
- **No background daemon in `serve`.** `serve` runs a single stdio session per invocation; it reads the database but never writes to it. Long-running reindexing is triggered explicitly via `build`, or in the background via the watcher started by `init --daemon=yes` or `start`. Multiple `serve` instances on the same project share the same daemon.

## Status

Early-stage. The schema, ingest pipeline, MCP server, CLI, and test suite are in place. Expect breaking changes as the toolset expands and the type resolver grows.

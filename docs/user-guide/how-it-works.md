# How indexing works

When you run `mekami build`, the following happens, in order.

## 1. Walk

The walker enumerates every `*.go` file under the source root, skipping `.git`, `.mekami`, `node_modules`, `vendor`, `_dev`, and `*_test.go` by default. Additional exclusions come from `watch.ignore` in `.mekami/config.json`.

## 2. Fingerprint

For each file: read the bytes, hash with `sha256`, and compare against the stored hash. Unchanged files are skipped without re-parsing. This is the basis for incremental builds.

## 3. Parse

Changed files are parsed with `go/parser`. The collector walks the AST and emits:

- **Symbols** — `func`, `method`, `type`, `var`, `const`, plus a synthetic `__imports__` anchor for the import block. Each symbol carries its `qualified_name` (e.g. `graph.queries.SearchSymbols`), line range, signature, and export status.
- **Refs** — `call`, `type-use`, `value`, and `import` edges, each tagged with the source line.

A lightweight intra-procedural type resolver maps local variables to their declared types so that `m := recv.Field` can resolve `Field` to `pkg.Type.Field` even when the receiver's type is inferred. Anonymous function literals at file scope — the typical `&cobra.Command{ RunE: func(...) error { ... } }` shape — get a synthetic owner symbol (kind `funclit`, qualified name `pkg.__lit__<file>_<line>__`) so every call inside the closure stays visible in `who-calls` and `trace`.

## 4. Write

All results are written inside a single SQLite transaction (WAL mode, `synchronous=NORMAL`, `foreign_keys=ON`). Files that disappeared since the last build are removed in the same pass.

## Parallelism

Parsing runs on `runtime.NumCPU()` workers. Each worker is a `go/parser.ParseFile` call against its own file; the results are streamed through a channel into a single writer that owns the SQLite transaction. This gives you near-linear speedup on large repos while keeping the database writes serialized.

## The data model

The store is intentionally narrow: every row is keyed on a `qualified_name` (for symbols) or `(file, line, kind)` (for refs). The schema is owned by `core/store/schema.go`; the row types and DTOs by `core/model/`. For the precise types, see the [API reference](../api-reference/frontend-api.md).

## What gets *not* indexed

- **Source body text.** The graph does not index file bodies. Use `rg` (ripgrep) or your editor's read tool for substring search inside function bodies, comments, or log strings. See [Limitations](../limitations.md).
- **Cross-package type resolution.** The local-variable resolver understands function parameters, short variable declarations, plain assignments, `range` clauses, and same-package constructor calls. It does not chase through cross-package calls — that would require `go/types` on the full package, which is out of scope for now.
- **Test files.** `*_test.go` is excluded by default. If you want them indexed, point `--root` at a directory that does not exclude them or extend the walker's `IsStructuralFiles` / exclude list.

## Incremental reindexing

The watcher calls a different path: `BuildIncremental(paths)`. It loads the current file set from the DB, computes the diff against the new set, and re-parses only the changed or added files. Removed files are deleted in the same pass. A structural file change (any of `go.mod` / `go.work` / `go.sum`) promotes the batch to a full `Build` instead.

The structural-file set is frontend-specific and exposed via `Frontend.StructuralFiles()` (see [the frontend contract](../extending/frontend-contract.md)).

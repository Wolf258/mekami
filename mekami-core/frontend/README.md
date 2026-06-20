# Language frontends

The ingest pipeline speaks a small, language-agnostic interface
defined in [`api/v1`](../../api/v1/api.go). A **frontend** is a
self-registering package that implements `api.Frontend` and
knows how to:

1. Identify the files it claims (`Extensions()`).
2. Resolve the workspace layout for the build root
   (`ResolveLayout()`, returning a `*api.Workspace`).
3. Map a file to its module/package identifiers
   (`ResolveFile()`).
4. Parse a single file into the generic `api.ParseResult` shape
   (`ParseFile()`).
5. List the basenames whose edit invalidates the whole index
   (`StructuralFiles()`).
6. Skip language-specific file kinds (e.g. `_test.go` in Go) from
   the walk (`IsIndexable()`).
7. Return the canonical root module identifier for the build root
   (`RootModule()`).

Since phase 2, individual frontends live in their own Go modules
(e.g. `github.com/Wolf258/mekami-core-go` for the Go language).
The bundled `all_gen` package is generated from
`.mekami/config.json` indexers[] by `mekami core-install`; adding
a language is now a separate-repo concern plus one CLI command.

## Writing a new language indexer

The recommended home for a new language indexer is its own
repository at `github.com/Wolf258/mekami-core-<lang>`, with a
single Go module that depends only on
`github.com/Wolf258/mekami-api/api/v1`. The shape of a frontend
is roughly 100-300 lines plus the parser itself. The recommended
strategy for the parser is to bind to
[tree-sitter](https://tree-sitter.github.io/tree-sitter/) (a
single CGo-free Go binding handles all grammars).

### 1. Create a new module

```
mekami-core-<lang>/
    go.mod
    frontend.go     # Frontend implementation
    parser.go       # the tree-sitter glue
    helpers.go      # symbol extraction, ref collection
```

The go.mod should require only `mekami-core/api/v1` and
`mekami-core/modlayout` (the latter for go.mod-style workspaces;
non-Go languages can omit it).

### 2. Implement the `api.Frontend` interface

```go
package mylang

import (
    "github.com/Wolf258/mekami-api/api/v1"
)

type Frontend struct{}

func (Frontend) Name() string                          { return "mylang" }
func (Frontend) Extensions() []string                  { return []string{".ml"} }
func (Frontend) StructuralFiles() []string             { return []string{"mylang.toml"} }
func (Frontend) IsIndexable(rel string) bool           { return true }
func (Frontend) ResolveLayout(root string) (*api.Workspace, error) {
    return &api.Workspace{}, nil
}
func (Frontend) RootModule(root string) (string, error) { return "", nil }
func (Frontend) ResolveFile(root, abs string) (api.FileMeta, error) {
    // Look up the project / package identifiers for abs.
}
func (Frontend) ParseFile(root, rel, abs string, hash string, mtime, size int64) (api.ParseResult, error) {
    // Read abs, parse it, return symbols + refs.
    // `Refs[i].FromSymbol` is the 0-based index into the returned
    // `Symbols` slice; the writer resolves it to a real id.
}

func init() { api.Register(Frontend{}) }
```

### 3. Register it for a project

From inside the mekami source tree (where `go.work` lives):

```
mekami core-install mylang@v0.1.0
```

This appends `{ "name": "mylang", "version": "v0.1.0" }` to
`.mekami/config.json` indexers[], regenerates
`frontend/all_gen/all_gen.go` with a fresh blank import for the
new module path, and prints a hint to rebuild the binary.

## Contract guarantees

A frontend **MUST**:

- Be safe for concurrent `ParseFile` / `ResolveFile` calls (the
  pipeline runs N workers).
- Return a non-nil `Symbols` and `Refs` slice (empty is fine) —
  never `nil`. The writer indexes them directly.
- Set `ParseResult.Lang` to a stable lowercase identifier. The
  pipeline uses it to short-circuit re-ingest when the file's
  language changes (e.g. a `.go` file is renamed to `.py`).
- Set `Refs[i].FromSymbol` to the 0-based index of the
  originating symbol in `Symbols`. The writer translates that
  to a real DB id after the symbols are inserted. The Go
  frontend uses a 0-based index; do the same in yours.

A frontend **MAY**:

- Return an empty `Workspace` from `ResolveLayout` if the
  language has no workspace concept.
- Return `""` from `RootModule` if the language has no canonical
  root module.
- Return an empty `StructuralFiles` if every edit should be
  handled by the incremental path.

A frontend **SHOULD NOT**:

- Touch the database directly. Persist via `api.ParseResult` and
  let `ingest.WriteParseResult` handle the DB write.
- Block on I/O outside its own file. Workers run in parallel
  and one slow frontend would stall the pool.

## Schema compatibility

The store, queries, MCP tools and DTOs are language-agnostic.
The `packages` table uses two identifier columns:

- `module_id` — a string naming the build's module
  (Go: `github.com/foo/bar`; Python: project name from
  `pyproject.toml`; Rust: crate name).
- `package_id` — a string identifying the package within the
  module (Go: full import path; Python: dotted module path;
  Rust: `crate::module`).

Frontends that have no concept of a sub-package can use the
`module_id` as the `package_id` and the directory basename as
the `name` column.

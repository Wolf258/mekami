# Writing a frontend

This walkthrough adds a hypothetical `mylang` indexer. The shape of a frontend is roughly 100-300 lines plus the parser itself. The recommended strategy for the parser is to bind to [tree-sitter](https://tree-sitter.github.io/tree-sitter/) (a single CGo-free Go binding handles all grammars).

## 1. Create a new module

```text
mekami-core-mylang/
    go.mod
    frontend.go     # Frontend implementation
    parser.go       # the tree-sitter glue
    helpers.go      # symbol extraction, ref collection
```

The `go.mod` should require only `github.com/Wolf258/mekami-api` and (for Go-style workspaces) `github.com/Wolf258/mekami-core-go/modlayout` — non-Go languages can omit the latter.

```bash
mkdir mekami-core-mylang && cd mekami-core-mylang
go mod init github.com/Wolf258/mekami-core-mylang
go get github.com/Wolf258/mekami-api@v0.1.0
```

## 2. Implement the `api.Frontend` interface

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
func (Frontend) ResolveModules(root string) ([]api.ModuleInfo, error) {
    return []api.ModuleInfo{{Dir: root, ModuleID: "mylang-root"}}, nil
}
func (Frontend) RootModule(root string) (string, error) { return "mylang-root", nil }
func (Frontend) ResolveFile(root, abs string) (api.FileMeta, error) {
    // Look up the project / package identifiers for abs.
}
func (Frontend) ParseFile(root, rel, abs string, hash string, mtime, size int64) (api.ParseResult, error) {
    // Read abs, parse it, return symbols + refs.
    // `Refs[i].FromSymbol` is the 0-based index into the returned
    // `Symbols` slice; the writer resolves it to a real id.
}
```

See the [frontend contract](frontend-contract.md) for the full method-by-method spec.

## 3. Self-register at `init()`

```go
func init() { api.Register(Frontend{}) }
```

The `api.Global` registry panics on duplicate names, so a typo in one frontend is caught at startup.

## 4. Tag the first release

```bash
git tag v0.1.0
git push origin main v0.1.0
```

## 5. Register the indexer for a project

From inside the mekami source tree (where `go.work` lives):

```bash
mekami core install mylang@v0.1.0
```

This writes `{ "mylang": "v0.1.0" }` to `.mekami/config.json indexers`, regenerates `mekami-cli/internal/core/frontend/all_gen/all_gen.go` with a fresh blank import, and prints a hint to rebuild the binary.

In production (AUR install), the binary is read-only and the user needs to update the package to pick up newly installed cores. In dev, run `./build.sh` to recompile with the new blank import.

## 6. Verify

```bash
./build.sh
./mekami core list        # should now show "mylang"
./mekami build --lang mylang
./mekami find-symbol Foo
```

The `core list` and `core status` commands are your first sanity check. `core status` reports frontends that are listed in the config but whose blank import is missing as `missing`.

## Concrete walkthrough: `mekami-core-rust`

Suppose you're adding `mekami-core-rust`.

1. **Create the repo:**
    ```bash
    gh repo create Wolf258/mekami-core-rust --public
    ```

2. **Inside the new repo, init the Go module and pull in `mekami-api`:**
    ```bash
    go mod init github.com/Wolf258/mekami-core-rust
    go get github.com/Wolf258/mekami-api@v0.1.0
    ```

3. **Implement `api.Frontend` from `github.com/Wolf258/mekami-api/api/v1`.** The interface is small — check `mekami-core-go` (`parser.go`) for a reference implementation. Every method has a docstring explaining the contract.

4. **Add a blank import in the core's entry file so it self-registers via `init()`:**
    ```go
    package rustfrontend

    import _ "github.com/Wolf258/mekami-api/api/v1"

    func init() { v1.Register(Frontend{}) }
    ```

5. **Tag the first release:**
    ```bash
    git tag v0.1.0
    git push origin main v0.1.0
    ```

6. **In the `Mekami` umbrella repo, `coreinstall` picks it up automatically** — `ModulePath("rust")` returns `github.com/Wolf258/mekami-core-rust` and the resolver fetches it from the proxy by version. No code change is needed in `mekami-cli/internal/coreinstall/lang.go`.

7. **Test:**
    ```bash
    go test ./...
    ./build.sh
    ./mekami core install rust
    ./mekami core list    # should now show "rust"
    ```

## Common pitfalls

- **Forgetting to rebuild the binary.** The blank-import manifest is read at compile time. A new `core install` does not take effect until `./build.sh` (or a fresh AUR package) is run.
- **Returning `nil` for `Symbols` or `Refs`.** The writer indexes them directly; `nil` will panic during the bulk insert. Always return `make([]X, 0)` when the file has no entries.
- **Non-deterministic qualified names.** The Go frontend derives `qualified_name` from the file's package path + the symbol's local name. If your names are not stable across rebuilds, `list_package` will return duplicate hits for the same declaration.
- **Blocking I/O inside `ParseFile`.** The pipeline runs `runtime.NumCPU()` workers. One slow frontend stalls the whole pool. Parse one file, do not call out to network resources, and let the worker pool's queue absorb the rest.

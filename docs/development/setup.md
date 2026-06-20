# Setup

## Prerequisites

- **Go 1.26+** (matches the version in `mekami-cli/go.mod`).
- **git**.
- **sqlite3** CLI optional, only for poking at the `.mekami/*.db` files by hand.
- A C toolchain is **not** required — Mekami uses `modernc.org/sqlite` (pure Go) and the Go toolchain only.

## Repository layout

Mekami is one umbrella repo with one Go module:

```text
Wolf258/Mekami             ← umbrella: mekami-cli/ (one Go module) + go.work
```

The module lives at `mekami-cli/`. The indexing pipeline that used to live in a separate `mekami-core` repo is fused in as `internal/core/`. A committed `go.work` file at the repo root lists `./mekami-cli` so build commands from the root resolve the module.

External Go modules pulled from the proxy:

```text
github.com/Wolf258/mekami-api           ← api/v1/ (the Frontend interface contract)
github.com/Wolf258/mekami-core-go       ← Go language frontend
```

The `mekami-core-go` blank import is generated into `mekami-cli/internal/core/frontend/all_gen/all_gen.go` by `mekami core install` (or `./build.sh` in dev) so the running binary registers the Go frontend in `api.Global`.

All modules are published under `github.com/Wolf258/...`. The `mekami/...` prefix is not used because the GitHub org of that name is owned by someone else.

## Basic setup

This is what a contributor who just wants to fix a CLI bug would do. No language core or core dev setup needed.

```bash
git clone https://github.com/Wolf258/Mekami
cd Mekami
go version                      # must be 1.26+

# Test the whole binary.
go test -short ./mekami-cli/...

# Build the binary.
./build.sh
./mekami --version
```

The committed `go.work` at the repo root pulls in `./mekami-cli` so commands from the root stay in scope. `go test -short ./...` from the repo root is **not** valid: the root is not itself a module, only `./mekami-cli` is. Pass the path explicitly.

`./build.sh` runs the dev-allgen script, regenerates `mekami-cli/internal/core/frontend/all_gen/all_gen.go` with the dev builtin set (so local edits to a registered frontend take effect), and produces a `mekami` binary in the repo root.

## Common commands

```bash
# Run the short test suite from the repo root.
go test -short ./mekami-cli/...

# Run a single package.
( cd mekami-cli && go test ./internal/supervisor/... )

# Regenerate the all_gen.go blank-import manifest.
( cd mekami-cli && go run ./internal/core/scripts/dev-allgen )

# Build the CLI binary.
./build.sh

# Rebuild after changing a core.
./build.sh && ./mekami core list
```

## Troubleshooting

### `pattern ./... matches no packages`

You're running `go test ./...` from a directory that has no `go.mod` and is not part of the workspace. The repo root is not a module on its own; only `./mekami-cli` is. Run with an explicit path:

```bash
go test -short ./mekami-cli/...
```

### Accidentally broke the workspace

The committed `go.work` lists only `./mekami-cli` and is meant to be tracked. If you edited it (or generated a `go.work.sum` against a local-only layout) and want to restore the committed content:

```bash
git checkout -- go.work
rm -f go.work.sum
```

If you are pointing the workspace at local clones of `mekami-api` or `mekami-core-go` for e2e work, prefer a `replace` directive in `mekami-cli/go.mod` to mutating `go.work`, so the change is local to your checkout and disappears with the working tree.

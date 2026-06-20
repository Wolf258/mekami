# Mekami — Development Guide

This document is for people hacking on Mekami itself: the
maintainer, contributors adding a new language core, and future
you when you've forgotten how the workspace is wired up.

The public README is the user-facing manual. This file is the
"how do I work on this thing" manual.

## Prerequisites

- **Go 1.26+** (matches the version in `mekami-cli/go.mod`).
- **git**.
- **sqlite3** CLI optional, only for poking at the `.mekami/*.db`
  files by hand.
- A C toolchain is **not** required — Mekami uses
  `modernc.org/sqlite` (pure Go) and the Go toolchain only.

## Repo layout

Mekami is split across three public repositories so each
component can be consumed, versioned, and tested independently:

```
Wolf258/mekami-api         ← api/v1/ (the Frontend interface contract)
Wolf258/Mekami             ← umbrella: mekami-cli + mekami-core
Wolf258/mekami-core-go     ← Go language frontend
```

`mekami-core` lives inside the `Mekami` umbrella repo (as a
sub-module) and is consumed by the CLI and any language core via
its sub-path: `github.com/Wolf258/Mekami/mekami-core`. The CLI
also blank-imports `mekami-core-go` from the generated
`all_gen.go` to register the Go frontend in `api.Global`.

All modules are published under `github.com/Wolf258/...`. The
`mekami/...` prefix is not used because the GitHub org of that
name is owned by someone else.

### What lives where

- **`mekami-api`** — pure stdlib, no internal deps. Just the
  `api.Frontend` interface and the shared data shapes
  (`ParseResult`, `Symbol`, `Ref`, `Workspace`, `ModuleInfo`,
  `ModuleEntry`). Bumping this is a major version for every
  downstream consumer.
- **`mekami-core`** — language-agnostic indexing pipeline:
  ingest, store, queries, walker, diff, grep. Imports
  `mekami-api` for the contract. Does **not** know about Go,
  Rust, etc. directly. Its only language-specific assumption is
  that any frontend can answer `ResolveLayout`,
  `ResolveModules`, `RootModule`, `ResolveFile`, `ParseFile`.
- **`mekami-cli`** — the binary. Imports `mekami-core` and
  blank-imports the language cores the user has installed
  (`core-install go` etc.).
- **`mekami-core-go`** — the Go language frontend. Implements
  `api.Frontend` and self-registers at `init()`. Imports
  `mekami-api` for the contract; does **not** import
  `mekami-core` (which keeps the module graph acyclic).

## Basic setup — CLI + core only

This is what a contributor who just wants to fix a CLI bug or
work on `mekami-core` would do. No language core needed.

```bash
git clone https://github.com/Wolf258/Mekami
cd Mekami
go version                      # must be 1.26+

# Test the CLI in isolation.
( cd mekami-cli   && go test ./... )
( cd mekami-core  && go test ./... )

# Build the binary.
./build.sh
./mekami --version
```

`./build.sh` runs the dev-allgen script, regenerates
`mekami-core/frontend/all_gen/all_gen.go` with whatever cores
are currently resolvable, and produces a `mekami` binary in the
repo root.

The CLI depends on `github.com/Wolf258/mekami-core-go` (via
`go.mod`) so the watch tests can register the Go frontend. The
module is fetched from the Go proxy at `v0.1.1`. No `replace`
directive is required.

## Local dev with multiple modules

If you want to develop `mekami-cli`, `mekami-core`, and a core
like `mekami-core-go` simultaneously and have your local edits
take effect without publishing tags, use a local `go.work` file.

### Steps

1. Clone the umbrella repo:
   ```bash
   git clone https://github.com/Wolf258/Mekami
   cd Mekami
   ```

2. Clone any core(s) you want to develop as **sibling
   directories**:
   ```bash
   git clone https://github.com/Wolf258/mekami-core-go
   ```

3. Create a `go.work` file in the repo root. **This file is
   gitignored** — it's yours, not the project's:
   ```bash
   cat > go.work <<'EOF'
   go 1.26.3

   use (
       ./mekami-cli
       ./mekami-core
   )
   EOF
   ```
   (The sibling `mekami-core-go` is consumed by version from the
   proxy unless you also want to develop it locally; in that
   case add `./mekami-core-go` to the `use` block.)

4. Verify the workspace resolves:
   ```bash
   go work edit -print
   ```

5. Test everything at once (uses the workspace):
   ```bash
   go test ./...
   ```

6. Build:
   ```bash
   ./build.sh
   ```

### Why `go.work` is gitignored

- A fresh clone of `Wolf258/Mekami` should compile and test
  against published module versions, not against whatever
  happens to be sitting in a sibling directory on your machine.
- Each contributor's `go.work` may differ (different cores, Go
  version, etc.). It's a local concern.

The CI side handles the multi-module case with a matrix
strategy that tests each module independently — see
`.github/workflows/mekami.yml`. `mekami-core-go` has its own
CI in its own repository.

### Useful `go work` commands

```bash
# Add a new local module to the workspace.
go work use ./mekami-core-rust

# Show the current workspace definition.
go work edit -print

# Sync the workspace after editing go.mod files.
go work sync

# Remove a module from the workspace.
go work edit -dropreplace=./mekami-core-rust
```

## Common commands

```bash
# Run all tests across the workspace (uses go.work if present).
go test ./...

# Test a single module.
( cd mekami-cli       && go test ./... )
( cd mekami-core      && go test ./... )

# Run only the matching tests, e.g. supervisor.
( cd mekami-cli   && go test ./internal/supervisor/... )

# Regenerate the all_gen.go blank-import manifest.
( cd mekami-core  && go run ./scripts/dev-allgen )

# Build the CLI binary.
./build.sh

# Rebuild after changing a core.
./build.sh && ./mekami core-list
```

## Adding a new language core

Suppose you're adding `mekami-core-rust`.

1. Create the repo (`github.com/Wolf258/mekami-core-rust`):
   ```bash
   gh repo create Wolf258/mekami-core-rust --public
   ```

2. Inside the new repo, init the Go module and pull in
   `mekami-api`:
   ```bash
   go mod init github.com/Wolf258/mekami-core-rust
   go get github.com/Wolf258/mekami-api@v0.1.0
   ```

3. Implement `api.Frontend` from
   `github.com/Wolf258/mekami-api/api/v1`. The interface is
   small — check `mekami-core-go` (parser.go) for a reference
   implementation. Every method has a docstring explaining the
   contract.

4. Add a blank import in the core's entry file so it
   self-registers via `init()`:
   ```go
   package rustfrontend

   import _ "github.com/Wolf258/mekami-api/api/v1"

   func init() { v1.Register(Frontend{}) }
   ```

5. Tag the first release:
   ```bash
   git tag v0.1.0
   git push origin main v0.1.0
   ```

6. In the `Mekami` umbrella repo, the CLI's `coreinstall` will
   pick it up automatically — `ModulePath("rust")` returns
   `github.com/Wolf258/mekami-core-rust` and the resolver
   fetches it from the proxy by version. No code change is
   needed in `mekami-cli/internal/coreinstall/lang.go`.

7. Test:
   ```bash
   go test ./...
   ./build.sh
   ./mekami core-install rust
   ./mekami core-list          # should now show "rust"
   ```

## Integration tests in mekami-core

`mekami-core` has two test suites:

- **Default (`go test ./...`)**: unit tests that use a stub
  frontend (`ingest_test/stub_frontend_test.go`). The stub
  parses Go files with `go/parser` but only extracts package
  name + top-level decls (no imports, no refs, no call edges).
  This keeps the default CI fast and free of language-specific
  dependencies.

- **Integration (`go test -tags=integration ./integration_test/...`)**:
  full end-to-end tests that require `mekami-core-go` as a
  test-only dependency. The integration test suite is the
  authoritative coverage of the ingest pipeline + real Go
  frontend interaction. Run it locally with:
  ```bash
  ( cd mekami-core && go test -tags=integration ./integration_test/... )
  ```

## Releasing a new version

1. Bump the version in the affected module(s):
   - `mekami-cli` (binary, AUR): tag `v0.2.0` on the umbrella
     repo's `main` branch.
   - `mekami-core`: tag `v0.2.0` on the umbrella repo's `main`
     branch at the same commit (the CLI and core move in
     lockstep because the core is a sub-module of the same
     repo).
   - `mekami-core-<lang>`: tag `v0.2.0` on its own repo.
2. Bump the `require` lines in downstream `go.mod` files to
   match:
   ```bash
   go get github.com/Wolf258/Mekami/mekami-core@v0.2.0
   go get github.com/Wolf258/mekami-core-go@v0.2.0
   go mod tidy
   ```
3. Commit the `go.mod` / `go.sum` updates and push.

SemVer rules: tag all three repos in lockstep. A bump in
`mekami-api`'s `api/v1` interface is a major bump for every
consumer.

## Troubleshooting

### `pattern ./... matches no packages`

You're running `go test ./...` from the repo root, but the
default `go.work`-less mode treats each `go.mod` as a separate
module and the root has no `go.mod`. Either:

- `cd mekami-cli && go test ./...` (etc., one module at a time).
- Set up a local `go.work` (see "Local dev with multiple
  modules" above) to make `go test ./...` cover the whole tree.

### A core is installed in `config.json` but not in the running binary

The `coreinstall` system writes to
`mekami-core/frontend/all_gen/all_gen.go` (a generated file
with blank imports), but the **binary** must be rebuilt for
new blank imports to take effect. Run:

```bash
./build.sh
```

In production (AUR install), the binary is read-only and the
user needs to update the package to pick up newly installed
cores.

### `go.work` got committed by accident

```bash
git rm --cached go.work go.work.sum
git commit -m "untrack go.work (local-only)"
```

The files are already in `.gitignore`, so this only happens
once.

## See also

- `README.md` — user-facing documentation.
- `github.com/Wolf258/mekami-api` — the `Frontend` interface
  contract every core must implement.
- `mekami-cli/internal/coreinstall/doc.go` — how `core-install`
  and the generated `all_gen.go` fit together.
- `mekami-core/scripts/dev-allgen/` — the script that
  regenerates `all_gen.go`.

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
external component can be consumed, versioned, and tested
independently. The indexing pipeline that used to live in a
separate `mekami-core` repo is now fused into the umbrella as
`mekami-cli/internal/core/`:

```
Wolf258/mekami-api         ← api/v1/ (the Frontend interface contract)
Wolf258/Mekami             ← umbrella: mekami-cli (with internal/core) + go.work
Wolf258/mekami-core-go     ← Go language frontend
```

The `Mekami` umbrella repo contains the whole binary as a
single Go module at `mekami-cli/`, with the former `mekami-core`
tree living under `internal/core/`. A committed `go.work` file
at the repo root points at `mekami-cli` so build commands from
the root keep working. The CLI blank-imports `mekami-core-go`
from the generated `all_gen.go` to register the Go frontend
in `api.Global`.

`mekami-api` and `mekami-core-go` remain external repositories.
They are pulled from the Go module proxy by version.

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
  (`core install go` etc.).
- **`mekami-core-go`** — the Go language frontend. Implements
  `api.Frontend` and self-registers at `init()`. Imports
  `mekami-api` for the contract; does **not** import
  `mekami-core` (which keeps the module graph acyclic).

## Basic setup — CLI only

This is what a contributor who just wants to fix a CLI bug
would do. No language core or core dev setup needed.

```bash
git clone https://github.com/Wolf258/Mekami
cd Mekami
go version                      # must be 1.26+

# Test everything in the workspace (cli + core).
go test ./...

# Build the binary.
./build.sh
./mekami --version
```

The committed `go.work` at the repo root pulls in
`./mekami-cli` so `go test ./mekami-cli/...` from the root
covers the whole binary. No manual workspace setup is needed
for the common case.

`./build.sh` runs the dev-allgen script, regenerates
`mekami-cli/internal/core/frontend/all_gen/all_gen.go` with
whatever cores are currently resolvable, and produces a
`mekami` binary in the
repo root.

The CLI depends on `github.com/Wolf258/mekami-core`,
`github.com/Wolf258/mekami-api`, and `github.com/Wolf258/mekami-core-go`
(via `go.mod`). All three are fetched from the Go proxy by
version. No `replace` directive is required.

## Local dev with multiple modules

If you want to develop `mekami-cli` together with local edits
to either `mekami-api` or `mekami-core-go` so those take effect
without publishing a tag, replace the relevant `require` in
`mekami-cli/go.mod` with a `replace ... => ../<sibling>`
directive, then re-run `go mod tidy`. The committed `go.work`
in this repo is no longer used for that — the binary is one
module now.

### Useful `go work` commands

```bash
# Add a new local module to the workspace.
go work use ../mekami-core-rust

# Show the current workspace definition.
go work edit -print

# Sync the workspace after editing go.mod files.
go work sync

# Remove a module from the workspace.
go work edit -dropreplace=../mekami-core-rust
```

## Common commands

```bash
# Run all tests across the workspace (uses the committed go.work).
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
./build.sh && ./mekami core list
```

## Test tiers

The repo has two test tiers, distinguished by the `integration`
build tag:

- **Default (`go test ./...`)** — fast unit tests with stubs.
  Does not require the e2e workspace. Does not load
  `mekami-core-go` into the test binary. Safe to run on every
  commit; finishes in seconds.
- **`-tags integration`** — end-to-end tests that need a real
  language frontend registered in the running test binary.
  Today that means:
  - `mekami-cli/internal/core/integration_test/...` — ingest
    pipeline against real Go source.
  - `mekami-cli/cmd/mekami/service_integration_test.go` —
    supervisor / watchdog / service install lifecycle
    (requires `systemd --user`).
  - `mekami-cli/internal/watch/integration_test.go` — full
    fsnotify / poller → build → DB propagation.

  Run them locally from the `mekami-cli/` directory:

  ```bash
  go test -tags integration ./internal/core/integration_test/...
  go test -tags integration ./internal/watch/...
  go test -tags integration ./cmd/mekami/ -run ServiceLifecycle
  ```

  The `integration` tag is the same tag the `service_lifecycle`
  tests already used, so this is a unification rather than a
  new convention. The rule is simple: any test that needs
  `mekami-api` or `mekami-core-go` actually present in the
  test binary gets the `integration` tag and stays out of
  `go test ./...` by default.

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
   ./mekami core install rust
   ./mekami core list          # should now show "rust"
   ```

## Integration tests in mekami-core

`mekami-core` has two test suites:

- **Default (`go test ./...`)**: unit tests that use a stub
  frontend (`ingest_test/stub_frontend_test.go`). The stub
  parses Go files with `go/parser` but only extracts package
  name + top-level decls (no imports, no refs, no call edges).
  This keeps the default CI fast and free of language-specific
  dependencies.

- **Integration (`go test -tags=integration ./...`)**:
  full end-to-end tests that require `mekami-core-go` as a
  test-only dependency. The integration test suite is the
  authoritative coverage of the ingest pipeline + real Go
  frontend interaction. Run it locally from `mekami-cli/`:
  ```bash
  go test -tags=integration ./internal/core/integration_test/...
  ```

## Releasing a new version

1. Bump the version in the affected module(s):
   - `mekami-cli` (binary, AUR): tag `v0.2.0` on the umbrella
     repo's `main` branch.
   - `mekami-core`: tag `v0.2.0` on its own repo.
   - `mekami-core-<lang>`: tag `v0.2.0` on its own repo.
2. Bump the `require` lines in downstream `go.mod` files to
   match:
   ```bash
   go get github.com/Wolf258/mekami-core@v0.2.0
   go get github.com/Wolf258/mekami-core-go@v0.2.0
   go mod tidy
   ```
3. Commit the `go.mod` / `go.sum` updates and push.

SemVer rules: tag all three repos in lockstep. A bump in
`mekami-api`'s `api/v1` interface is a major bump for every
consumer.

## Troubleshooting

### `pattern ./... matches no packages`

You're running `go test ./...` from a directory that has no
`go.mod` and is not part of the workspace. Make sure you're at
the repo root (where `go.work` lives) and that the file is
intact. To run a single module in isolation:

```bash
( cd mekami-cli  && go test ./... )
( cd mekami-core && go test ./... )
```

### A core is installed in `config.json` but not in the running binary

The `coreinstall` system writes to
`mekami-cli/internal/core/frontend/all_gen/all_gen.go` (a
generated file with blank imports), but the **binary** must be
rebuilt for new blank imports to take effect. Run:

```bash
./build.sh
```

In production (AUR install), the binary is read-only and the
user needs to update the package to pick up newly installed
cores.

### Accidentally broke the workspace

The committed `go.work` lists only `./mekami-cli` and is
meant to be tracked. If you edited it (or generated a
`go.work.sum` against a local-only layout) and want to
restore the committed content:

```bash
git checkout -- go.work
rm -f go.work.sum
```

If you are pointing the workspace at local clones of
`mekami-api` or `mekami-core-go` for e2e work, prefer a
`replace` directive in `mekami-cli/go.mod` to mutating
`go.work`, so the change is local to your checkout and
disappears with the working tree.

## See also

- `README.md` — user-facing documentation.
- `github.com/Wolf258/mekami-api` — the `Frontend` interface
  contract every core must implement.
- `mekami-cli/internal/coreinstall/doc.go` — how `core install`
  and the generated `all_gen.go` fit together.
- `mekami-cli/internal/core/scripts/dev-allgen/` — the script
  that regenerates `all_gen.go`.

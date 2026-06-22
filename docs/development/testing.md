# Testing

How the test suite is organised, how to run it, and the conventions contributors must follow when adding new tests.

## At a glance

- 50 `*_test.go` files, 314 test functions, no external test-framework dependencies (no `testify`, no `ginkgo`).
- Two levels: **unit** (default `go test`) and **integration** (build tag `integration`).
- Go modules in this repo:
    - `mekami-cli/` (in this workspace, primary; contains `internal/core/` which used to be the standalone `mekami-core` module)
    - `mekami-api/` (external, pulled from module proxy)
    - `mekami-core-go/` (external, pulled from module proxy)

## Running the suite

### Unit tests (default)

From the repo root, with the committed `go.work`:

```bash
go env GOWORK
go test -short ./mekami-cli/...
```

`go test -short ./...` from the repo root is rejected by Go because the root directory is not itself a module — the workspace only lists `./mekami-cli` as a module. Pass the pattern explicitly.

This is what CI runs and what the AUR `check()` runs. It is fast (seconds), hermetic, and exercises every package except those gated behind the `integration` build tag.

To run a single package:

```bash
go test -count=1 ./mekami-cli/cmd/mekami/
go test -count=1 -run '^TestResolveLang$' ./mekami-cli/cmd/mekami/
```

To run with the race detector:

```bash
go test -race ./mekami-cli/...
```

### Integration tests

Integration tests live behind the `integration` build tag. The default `go test` does not compile them.

For most integration tests you do not need a local clone of the external cores — they are pure Go and run against `mekami-core-go` via the module proxy:

```bash
go test -tags integration ./mekami-cli/internal/core/integration_test/...
go test -tags integration ./mekami-cli/internal/watch/...
```

The service-manager round-trip in `service_integration_test.go` also lives in the same module and uses the same `integration` build tag.

## Build tags

| Tag | Files | Purpose |
|---|---|---|
| `integration` | 20 in `mekami-cli/internal/core/integration_test/`, 1 in `mekami-cli/internal/watch/integration_test.go`, 1 in `mekami-cli/cmd/mekami/service_integration_test.go` | End-to-end tests that need a real `mekami-core-go` parser, the file system, or a live user bus. |
| `integration && linux` | `mekami-cli/cmd/mekami/service_integration_test.go` | Service-manager round-trip; depends on a live systemd user bus, so it is Linux-only. |
| `!integration` | `mekami-cli/internal/core/ingest_test/{setup,stub_frontend}_test.go` | The opposite of `integration`. Wires the stub Go frontend so the unit tests can run without the real `mekami-core-go` package. |

The build-tag form is the modern `//go:build` (Go 1.17+). We do not keep the legacy `// +build` form; the project targets Go 1.26 and there is no compatibility reason to.

## Test matrix

| Module | Unit | Integration | Notes |
|---|---|---|---|
| `mekami-cli/internal/core/store` | yes | — | Upsert / upsert-parent round-trips. |
| `mekami-cli/internal/core/queries` | yes | — | Stats query helper. |
| `mekami-cli/internal/core/path` | yes | — | Error-wrap table tests. |
| `mekami-cli/internal/core/ingest_test` | yes (`!integration`) | — | Stub frontend, hermetic. |
| `mekami-cli/internal/core/integration_test` | — | yes (20) | Real `mekami-core-go`, full build graph, prune, refs, mcp polish, etc. |
| `mekami-cli/internal/core/scripts/dev-allgen` | yes | — | `all_gen.go` regenerator. |
| `mekami-cli/cmd/mekami` | yes | yes (`integration && linux`) | resolveLang, resolveInitLangs, mergeIndexers, runInit, runBuild, service commands. |
| `mekami-cli/internal/config` | yes | — | Default, Load, Validate, OnStartAction, ShouldLog, Indexers. |
| `mekami-cli/internal/coreinstall` | yes | — | SplitLangRef, IsValidLang, NormalizeVersion, HighestVersion, List, Gen. |
| `mekami-cli/internal/handlers` | yes | — | Read handlers (get_symbol, show_changes, list_package, who_calls, trace_calls). |
| `mekami-cli/internal/supervisor` | yes | — | supervisor state machine, spawn, registry, ipc, inotify budget, adopt, sentinel. The watchdog lives in `internal/watch` and is exercised by the supervisor tests. |
| `mekami-cli/internal/watch` | yes | yes (1) | Filter, Coalescer, Translate, poller, paths, plus a real fsnotify integration. |
| `mekami-cli/tests/internal/install` | yes | — | Black-box MCP client registration. |
| `mekami-cli/tests/cmd/mekami` | yes | — | Black-box smoke for the `mcp-test` truncation helper. |
| `mekami-core-go` | yes (2) | — | `imports_test.go` + `external_test/func_signature_test.go`. |
| `mekami-api` | — | — | No tests. |

## Conventions

These are the rules the suite follows; new tests should follow them too.

- **Standard `testing` only.** Use `t.Errorf` / `t.Fatalf` for assertions. Do not introduce `testify` or `ginkgo`.
- **Subtests via `t.Run`** for groups of related cases. Use snake_case subtest names that read as a path (`ok`, `multiple_indexers_explicit_picks_requested`).
- **Table-driven when there are ≥3 similar cases.** Define a `cases` slice of anonymous structs (or a map when the input is a natural key); each case carries a `name` for the subtest.
- **Hermetic state.** Use `t.TempDir()` for filesystem state, `t.Setenv()` for env, `t.Cleanup()` for everything else. Never reach for a global `os.Setenv` / `os.Chdir` directly.
- **`t.Helper()`** at the top of every test helper that calls `t.Errorf` / `t.Fatalf`.
- **No `t.Parallel()`.** Tests are fast and depend on shared state in places (the `api.Global` registry, the supervisor state). Adding parallelism is a deliberate decision, not a default.
- **Skip, don't fail, on environment-only gaps.** Use `t.Skip("reason")` when the test cannot run because of a missing platform prerequisite (no systemd user bus, no `/proc`, etc.) and add a comment explaining how to enable it.
- **`TestMain` is rare.** Only two test files declare one: the stub-frontend registrar in `ingest_test/setup_test.go` (build tag `!integration`) and the empty integration-test bootstrap in `integration_test/setup_test.go`.

## Helpers and stubs

Helpers that are reused across packages live in:

- `mekami-cli/internal/core/testutil/helpers.go` (production package, not `_test.go`). Exposes `MustMkdir`, `MustWrite`, `WriteModuleFiles`, `OpenStoreForTest`, `QueriesStatsForTest`. Black-box tests import it the same way production code does.
- `mekami-cli/internal/supervisor/testhelpers_test.go` and `mekami-cli/internal/watch/testhelpers_test.go` for package-local helpers (fsnotify shim, fake daemons, stub IPC servers, and a thin wrapper around `socktestutil.ShortSockDir` described below).
- `mekami-cli/internal/core/integration_test/bridge_test.go:buildTestGraph` is the canonical "build a graph from a Go source blob" helper used by most integration tests.

Tests that bind a Unix socket must use `ShortSockDir(t)` from `mekami-cli/internal/socktestutil/sockdir.go` (re-exported as `shortSockDir(t)` from the per-package test helpers) instead of `t.TempDir()` as the parent of the socket path. On macOS the runtime temp dir lives under `/var/folders/.../T/<name><digits>/<digits>/`, and once you append `.mekami/watcher.sock` the full path exceeds the 104-byte `sun_path` limit and `bind()` returns `invalid argument`. The helper is a no-op on Linux/Windows (it just returns `t.TempDir()`) and on macOS parks the dir under `/tmp/ms-<short-name>-XXXX` with a name truncated to 16 chars so the resulting socket path stays well under the limit.

There are three stubs of `api.Frontend` in the suite:

- `mekami-cli/internal/core/ingest_test/stub_frontend_test.go` — full `go/parser`-backed stub that returns package name and top-level declarations only (no imports, refs, or calls). Registered automatically in `TestMain` under the `!integration` tag.
- `mekami-cli/cmd/mekami/commands_test.go:fakeFrontend` — minimal in-package stub for the `resolveLang` / `resolveInitLangs` / `runInit` tests.
- `mekami-cli/internal/coreinstall/list_test.go:testFrontend` — minimal stub for the `List` tests.

They are intentionally small and not consolidated — each stub covers only the surface the tests in its package need.

## CI and packaging

- **CI** (`.github/workflows/mekami.yml`): runs from the repo root so the committed `go.work` is in scope. The `test` job runs `go test -short ./...` against the workspace (covers `mekami-cli` and `mekami-core`) on Go 1.26 across `ubuntu-latest`, `macos-latest`, and `windows-latest`. No `-tags integration`, so the integration suite is not exercised in CI. The `build` job runs `./build.sh` on Linux/macOS and a plain `go build ./...` on Windows.
- **AUR** (`.aur/mekami/PKGBUILD:check()`): runs from the repo root, calls `go work sync` (idempotent; regenerates the gitignored `go.work.sum` if missing) and then `go test -short ./...`. The workspace activation matters because `mekami-cli/go.mod` requires `mekami-core`, and on a clean AUR build that module is only resolvable as a local one through the workspace — not from the module proxy.
- **No Makefile.** `build.sh` is a developer-only build script and does not run tests.

## Adding a new test

1. Pick the package the test belongs to. Prefer `package <name>_test` (black-box) when the test exercises the public surface; `package <name>` (white-box) only when you need access to unexported state.
2. If the test needs a real `mekami-core-go` parser, a real filesystem watcher, or a live user bus, gate it behind `//go:build integration`. If it depends on Linux systemd, add `&& linux`.
3. Use the conventions above: `t.TempDir()`, `t.Setenv()`, `t.Cleanup()`, `t.Helper()`, table-driven with `t.Run`.
4. Place shared helpers in `testutil/` (for cross-package helpers) or `<pkg>/testhelpers_test.go` (for package-local ones).
5. Run the suite locally:
    ```bash
    go test ./...
    go test -tags integration ./...
    gofmt -l .   # must be empty
    go vet ./...
    ```
6. CI does not run the integration suite. If your change depends on integration tests passing, run them locally before opening a PR.

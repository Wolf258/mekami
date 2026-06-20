# Releasing

Mekami is one umbrella Go module (`github.com/Wolf258/mekami-cli`) plus external language cores fetched from the Go module proxy. Releases must keep the umbrella binary, the public API contract, and every language core in compatible versions.

## SemVer rules

- **Major** (e.g. `v1.0.0` → `v2.0.0`) — any breaking change in `github.com/Wolf258/mekami-api/api/v1` or in the CLI / MCP surface.
- **Minor** (e.g. `v0.2.0` → `v0.3.0`) — new features, new tools, new commands, new config keys, all backwards compatible.
- **Patch** (e.g. `v0.2.3` → `v0.2.4`) — bug fixes only, no surface changes.

A bump in `mekami-api`'s `api/v1` interface is a **major** bump for every consumer.

## What gets tagged

| Tag | Repo | What it does |
| --- | --- | --- |
| `v<version>` | `Wolf258/Mekami` (this umbrella repo) | The single source the AUR `mekami` package builds from. Contains `mekami-cli/` as the one Go module, with the indexing pipeline fused in as `internal/core/`. |
| `v<version>` | `Wolf258/mekami-core-<lang>` | Per-language frontends. Each is its own Go module that depends on `mekami-api` and registers itself via blank import. The umbrella does not embed these — they are pulled from the Go module proxy by `mekami core install <lang>@<version>`. |

`Wolf258/mekami-api` is a pure-stdlib contract package. It has no per-release coordination: any breaking change there is a major bump for the umbrella and a major bump for every language core that depends on it.

## Bump procedure

1. **Tag the umbrella repo.**
    ```bash
    git tag v0.2.0
    git push origin main v0.2.0
    ```

2. **Bump the `require` line in `mekami-cli/go.mod` for any frontend that changed version.**
    ```bash
    go get github.com/Wolf258/mekami-core-go@v0.2.0
    go mod tidy
    ```

3. **Commit the `go.mod` / `go.sum` updates and push.**

4. **Tag the language-core repo(s) you bumped.** Each `mekami-core-<lang>` is independent; the umbrella only consumes it by version.

## AUR bump

See the full procedure at [AUR packaging](../build/aur.md). The short version:

1. Tag upstream: `git tag v0.2.0 && git push origin v0.2.0`.
2. Upload release tarballs named exactly `mekami_0.2.0_linux_x86_64.tar.gz` and `mekami_0.2.0_linux_aarch64.tar.gz`.
3. Compute `sha256sum` for each.
4. Update `.aur/mekami-bin/PKGBUILD` (`pkgver` + checksums).
5. Update `.aur/mekami/PKGBUILD` (`pkgver` only).
6. Regenerate both `.SRCINFO` files with `makepkg --printsrcinfo > .SRCINFO`.
7. Commit + push.
8. Push to AUR.

## Version stamping

The version is stamped at build time via `-ldflags "-X ...install.version=..."`. The `-ldflags` expression is inlined in two places, and they must be kept in lockstep:

- `build.sh` (manual dev builds)
- `.aur/mekami/PKGBUILD` (AUR from-source package)

If the install package ever moves the `version` variable, both files must be updated together.

## What to communicate

A release commit or PR description should call out:

- Which repos were tagged and at what version.
- Any `api/v1` changes (and therefore which consumers need a major bump).
- Any AUR package changes (PKGBUILD, .SRCINFO).
- Anything that requires a user action (rebuild the binary, edit config, run `core install`, etc.).

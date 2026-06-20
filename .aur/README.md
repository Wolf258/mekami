# AUR packages for mekami

This directory holds PKGBUILDs for the [AUR](https://aur.archlinux.org).
The two packages target the same `mekami` binary, but pull it from
different sources:

| Package | Source | Who it's for |
| --- | --- | --- |
| `mekami-bin` | Prebuilt release tarball from GitHub Releases. | Arch users who do not want a Go toolchain. **Start here.** |
| `mekami` | Builds the CLI from the upstream git tag. | Users who prefer to compile from source, or want the latest unreleased changes. Requires `go>=1.26`. |

The two packages conflict with each other and both `provide` `mekami`,
so installing one automatically removes the other. This matches the
usual AUR convention for `-bin` siblings.

## Bumping a release

The two PKGBUILDs intentionally use the same `pkgver` and the same
upstream tag (`v<pkgver>`). The bump procedure:

1. **Tag upstream.** From the repo root:
   ```bash
   git tag v0.2.0
   git push origin v0.2.0
   ```
2. **Upload release tarballs.** The release workflow should publish
   the assets with these exact names — the PKGBUILD downloads them
   by name, so any deviation breaks the build:
   ```
   mekami_0.2.0_linux_x86_64.tar.gz
   mekami_0.2.0_linux_aarch64.tar.gz
   ```
   Each tarball expands to a `mekami` binary (and optionally a
   `LICENSE` file at the root of the archive).
3. **Compute checksums.**
   ```bash
   sha256sum mekami_0.2.0_linux_x86_64.tar.gz \
             mekami_0.2.0_linux_aarch64.tar.gz
   ```
4. **Update `.aur/mekami-bin/PKGBUILD`.**
   - Bump `pkgver` to `0.2.0`.
   - Replace the two `sha256sums_*` placeholders with the values
     from step 3.
5. **Update `.aur/mekami/PKGBUILD`.**
   - Bump `pkgver` to `0.2.0` (the `pkgver()` function will pick up
     the same value from the git tag at build time; the literal line
     is only used as a fallback for the `makepkg` UI).
6. **Regenerate both `.SRCINFO` files.** `makepkg` requires this and
   the AUR will reject submissions with stale SRCINFO entries.
   ```bash
   cd .aur/mekami     && makepkg --printsrcinfo > .SRCINFO
   cd .aur/mekami-bin && makepkg --printsrcinfo > .SRCINFO
   ```
7. **Commit + push.** Commit `PKGBUILD` and `.SRCINFO` together; do
   not touch the binary tarballs from this repo (they live on the
   GitHub release, not here).
8. **Push to AUR.** Clone the [aur](https://gitlab.archlinux.org/archlinux/aur)-
   side repos (one per package) and push the matching subtree. The
   standard AUR workflow is:
   ```bash
   git clone ssh://aur@aur.archlinux.org/mekami-bin.git
   cp -r .aur/mekami-bin/* mekami-bin/
   cd mekami-bin && makepkg --printsrcinfo > .SRCINFO
   git add -A && git commit -m "mekami-bin: bump to 0.2.0" && git push
   ```

## Local sanity check

Before pushing to AUR, verify the PKGBUILDs build locally:

```bash
# from the repo root
cd .aur/mekami-bin && makepkg -si    # prebuilt, fast
cd .aur/mekami     && makepkg -si    # from source, slower
```

`makepkg` will report any missing dependencies, broken checksums, or
syntax errors in the PKGBUILD itself.

## Why does `build.sh` and the PKGBUILD share the same `-ldflags`?

The `-ldflags` expression that stamps the version into the binary
lives in two places:

- `build.sh` (manual dev builds — produces `./mekami` in the repo root)
- `.aur/mekami/PKGBUILD` (AUR from-source package)

Both inline the expression rather than sharing a helper, so each is
a self-contained script the AUR tooling can parse without our repo
layout. The bootstrap installer (`scripts/install.sh`) was removed:
AUR users install via `yay -S mekami-bin` (or `yay -S mekami`) and
get the binary on PATH directly. **If the install package ever moves
the `version` variable, both files must be updated in lockstep.**

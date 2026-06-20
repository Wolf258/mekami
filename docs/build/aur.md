# AUR packaging

Mekami ships through the [AUR](https://aur.archlinux.org). The two packages target the same `mekami` binary, but pull it from different sources.

| Package | Source | Who it's for |
| --- | --- | --- |
| `mekami-bin` | Prebuilt release tarball from GitHub Releases. | Arch users who do not want a Go toolchain. **Start here.** |
| `mekami` | Builds the CLI from the upstream git tag (single umbrella repo). | Users who prefer to compile from source, or want the latest unreleased changes. Requires `go>=1.26`. |

The two packages conflict with each other and both `provide` `mekami`, so installing one automatically removes the other. This matches the usual AUR convention for `-bin` siblings.

## Installing

```bash
yay -S mekami-bin    # prebuilt binary
# or
yay -S mekami        # builds from source
```

Verify with `mekami --version`. The version is stamped at build time via `-ldflags "-X ...install.version=..."`.

## Bumping a release

The two PKGBUILDs intentionally use the same `pkgver` and the same upstream tag (`v<pkgver>`). The bump procedure:

1. **Tag upstream.** From the umbrella repo root:
    ```bash
    git tag v0.2.0
    git push origin v0.2.0
    ```

2. **Upload release tarballs.** The release workflow should publish the assets with these exact names — the `mekami-bin` PKGBUILD downloads them by name, so any deviation breaks the build:
    ```text
    mekami_0.2.0_linux_x86_64.tar.gz
    mekami_0.2.0_linux_aarch64.tar.gz
    ```
    Each tarball expands to a `mekami` binary (and optionally a `LICENSE` file at the root of the archive).

3. **Compute checksums.**
    ```bash
    sha256sum mekami_0.2.0_linux_x86_64.tar.gz \
              mekami_0.2.0_linux_aarch64.tar.gz
    ```

4. **Update `.aur/mekami-bin/PKGBUILD`.**
    - Bump `pkgver` to `0.2.0`.
    - Replace the two `sha256sums_*` placeholders with the values from step 3.

5. **Update `.aur/mekami/PKGBUILD`.**
    - Bump `pkgver` to `0.2.0`. The PKGBUILD pulls a single source from the umbrella tag, so the version is the only field that needs to change.

6. **Regenerate both `.SRCINFO` files.** `makepkg` requires this and the AUR will reject submissions with stale SRCINFO entries.
    ```bash
    cd .aur/mekami     && makepkg --printsrcinfo > .SRCINFO
    cd .aur/mekami-bin && makepkg --printsrcinfo > .SRCINFO
    ```

7. **Commit + push.** Commit `PKGBUILD` and `.SRCINFO` together; do not touch the binary tarballs from this repo (they live on the GitHub release, not here).

8. **Push to AUR.** Clone the aur-side repos (one per package) and push the matching subtree. The standard AUR workflow is:
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

`makepkg` will report any missing dependencies, broken checksums, or syntax errors in the PKGBUILD itself.

## Version stamping

The `-ldflags` expression that stamps the version into the binary lives in two places, and they must be kept in lockstep:

- `build.sh` (manual dev builds — produces `./mekami` in the repo root)
- `.aur/mekami/PKGBUILD` (AUR from-source package)

Both inline the expression rather than sharing a helper so each file is self-contained and the AUR tooling can parse it without our repo layout. **If the install package ever moves the `version` variable, both files must be updated in lockstep.**

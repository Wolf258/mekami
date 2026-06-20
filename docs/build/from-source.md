# Building from source

`./build.sh` is a developer-only build script. It does **not** run tests and does **not** produce an installable package; for that, see [AUR packaging](aur.md).

## What it does

```bash
./build.sh
```

1. Checks that Go ≥ 1.26 is installed.
2. Regenerates `mekami-cli/internal/core/frontend/all_gen/all_gen.go` with the dev builtin set (so local edits to `mekami-core-go` and friends take effect).
3. Stamps the version via `-ldflags "-X ...install.version=..."`.
4. Produces `./mekami` in the repo root.

The script is idempotent: re-running it is the canonical "I changed a core, give me a fresh binary" command.

## Requirements

- Go 1.26+.
- A C toolchain is **not** required.
- `git` is required (the `dev-allgen` script reads your module cache).

## Why does `build.sh` and the PKGBUILD share the same `-ldflags`?

The `-ldflags` expression that stamps the version into the binary lives in two places:

- `build.sh` (manual dev builds — produces `./mekami` in the repo root)
- `.aur/mekami/PKGBUILD` (AUR from-source package)

Both inline the expression rather than sharing a helper, so each is a self-contained script the AUR tooling can parse without our repo layout.

**If the install package ever moves the `version` variable, both files must be updated in lockstep.**

## Manual build (without the script)

If you want the bare build, you can do it by hand:

```bash
( cd mekami-cli && go build -o ../mekami . )
```

You will be using the *production* `all_gen.go` (the file as it was last generated in the source tree), not the dev set. Use `./build.sh` when you have edited a core locally.

## Windows

The `build.sh` script uses bash. On Windows, build with:

```powershell
cd mekami-cli
go build -o ..\mekami.exe .\cmd\mekami
```

The CI workflow at `.github/workflows/mekami.yml` does the equivalent on `windows-latest`.

## Cross-compilation

Mekami is a single Go binary. Cross-compiling is the standard `GOOS` / `GOARCH` dance:

```bash
GOOS=linux  GOARCH=amd64 go build -o mekami-linux-amd64   ./mekami-cli/cmd/mekami
GOOS=linux  GOARCH=arm64 go build -o mekami-linux-arm64   ./mekami-cli/cmd/mekami
GOOS=darwin GOARCH=arm64 go build -o mekami-darwin-arm64  ./mekami-cli/cmd/mekami
```

The release tarball naming convention used by the AUR `-bin` package is `mekami_<pkgver>_linux_{x86_64,aarch64}.tar.gz`.

## Sanity check

After building:

```bash
./mekami --version
./mekami stats         # .mekami/config.json does not exist yet — this just prints the version
./mekami init          # in a test repo, not your real code
./mekami build
./mekami find-symbol Foo
```

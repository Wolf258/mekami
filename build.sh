#!/usr/bin/env bash
# build.sh — local development build of the mekami binary.
#
# This is a DEV-ONLY tool. It produces ./mekami in the repo root for
# iteration, self-hosting, and CI. End users install mekami from the
# AUR (yay -S mekami-bin), not by running this script.
#
# Usage:
#   ./build.sh           # stamps "dev"
#   ./build.sh v0.2.0    # stamps a real version
#
# The -ldflags expression is inlined (not shared with a helper) so
# the script stays self-contained. The same expression lives in
# .aur/mekami/PKGBUILD; keep them in lockstep when changing the
# install.version variable path.
#
# Dev-only behavior: this script regenerates
# mekami-core/frontend/all_gen/all_gen.go with the dev builtin set
# (today: mekami-core-go) before compiling, so the resulting binary
# can index a Go project without `mekami core-install go` first.
# The original all_gen.go is restored on exit (success or failure)
# via an EXIT trap, so the working tree stays clean. If you are
# editing all_gen.go by hand, do not run this script in parallel.

set -euo pipefail

cd "$(dirname "$0")"

# Minimum Go version required to build mekami. Bump both fields when
# the toolchain floor changes; the AUR PKGBUILD's makedepends is
# left as a comment cross-reference.
required_major=1
required_minor=26

if ! command -v go >/dev/null 2>&1; then
	echo "error: 'go' is not on PATH" >&2
	exit 1
fi

current=$(go version | awk '{print $3}' | sed 's/^go//')
current_major=$(echo "$current" | cut -d. -f1)
current_minor=$(echo "$current" | cut -d. -f2)
if [ "$current_major" -lt "$required_major" ] \
	|| { [ "$current_major" -eq "$required_major" ] && [ "$current_minor" -lt "$required_minor" ]; }; then
	echo "error: go >= ${required_major}.${required_minor} required, found $current" >&2
	exit 1
fi

# Regenerate all_gen.go with the dev builtin set. The original
# file is backed up in a mktemp directory and restored by the
# EXIT trap below, so the working tree is left untouched whether
# the build succeeds or fails.
all_gen="mekami-core/frontend/all_gen/all_gen.go"
backup_dir=$(mktemp -d)
cp "$all_gen" "$backup_dir/orig"
trap 'cp "$backup_dir/orig" "$all_gen"; rm -rf "$backup_dir"' EXIT

go run ./mekami-core/scripts/dev-allgen "$all_gen"

version="${1:-dev}"

# Stamp the version into the binary. The variable path lives in
# github.com/mekami/mekami-cli/internal/install (see
# mekami-cli/internal/install/install.go). The same expression is
# inlined in .aur/mekami/PKGBUILD; keep them in lockstep.
ldflags="-X github.com/mekami/mekami-cli/internal/install.version=${version}"

go build -ldflags "$ldflags" -o mekami ./mekami-cli

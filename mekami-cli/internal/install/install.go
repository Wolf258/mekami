// Package install implements the `mekami mcp install` and
// `mekami mcp uninstall` workflows: wiring the MCP server into
// supported agent clients (OpenCode today) and tearing that down
// cleanly.
//
// The binary install/uninstall path is intentionally absent from
// this package: end users install mekami from the AUR
// (yay -S mekami-bin), which places the binary at
// /usr/bin/mekami directly. The package is library-shaped so a
// future installer (Homebrew formula, deb package, etc.) can
// reuse the same MCP-registration primitives.
package install

import (
	"strings"
)

// Version returns the runtime version of the mekami binary. The
// value is set at build time via -ldflags
// "-X github.com/Wolf258/mekami-cli/internal/install.version=...".
// "dev" is returned for an unset var (e.g. `go run`).
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	return "dev"
}

// version is set via -ldflags "-X github.com/Wolf258/mekami-cli/internal/install.version=..."
// by the build script. Storing it in this package (rather than
// main) lets the CLI import it without a cyclic dependency: the
// main package also imports this same variable to expose it on
// `mekami --version`.
var version = ""

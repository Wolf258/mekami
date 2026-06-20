//go:build windows

package watch

// detachStdio on Windows is a no-op. The daemon path is
// Unix-only; the _daemon subcommand is hidden on Windows.
func detachStdio() error { return nil }

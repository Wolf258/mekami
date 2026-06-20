//go:build windows

package supervisor

import "os"

// devNullFile opens os.DevNull (NUL on Windows) in read-only
// mode. Used by spawn.go to detach a forked daemon's stdin /
// stdout from the supervisor's console. See devnull_unix.go for
// the full rationale.
func devNullFile() *os.File {
	f, _ := os.Open(os.DevNull)
	return f
}

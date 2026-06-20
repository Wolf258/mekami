//go:build !windows

package supervisor

import "os"

// devNullFile is a small helper around os.DevNull so the spawn
// code does not have to repeat the open call. Used to detach
// a forked daemon's stdin and stdout from the supervisor's
// tty; stderr is captured separately (see spawn.go).
//
// The returned *os.File stays open until the spawned child closes
// its end of the handle. The supervisor does not own the file
// after Start() returns, so the fd leak is bounded by the
// child's lifetime, not the supervisor's.
func devNullFile() *os.File {
	f, _ := os.Open(os.DevNull)
	return f
}

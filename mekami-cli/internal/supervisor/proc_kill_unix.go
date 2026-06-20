//go:build !windows

package supervisor

import "syscall"

// killProcess sends sig to pid. On Unix this is a thin wrapper
// around syscall.Kill; on Windows (see proc_kill_windows.go) it
// uses TerminateProcess because Unix-style signals do not exist
// for arbitrary processes.
//
// The function returns the underlying OS error. ESRCH ("no such
// process") is returned verbatim so callers can distinguish
// "process already gone" from "permission denied" / "OS error".
func killProcess(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

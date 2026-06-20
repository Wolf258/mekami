//go:build !windows

package supervisor

import "syscall"

// detachSysProcAttr returns the *syscall.SysProcAttr used to detach
// a child process from the parent's controlling terminal and
// session. On Unix this is a new session (Setsid); on Windows the
// equivalent is set in proc_detach_windows.go.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

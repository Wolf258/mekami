//go:build !windows

package supervisor

import (
	"os"
	"syscall"
)

// processAlive returns true if pid refers to a live process. On
// Unix this is signal 0 to the pid: the kernel reports ESRCH
// when the process is gone, EPERM when we lack permission to
// signal it (which still implies the process exists).
//
// pid <= 0 is treated as "not alive" to keep callers from
// accidentally passing an unset PID.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

//go:build !windows

package watch

import (
	"os"
	"syscall"
)

// checkProcessAlive returns nil if pid is alive, an error
// otherwise. On Unix this is signal 0. The error is preserved so
// the caller can decide what to do (e.g. log it, count a miss).
func checkProcessAlive(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.Signal(0))
}

// terminateSelf sends an interrupt to the current process so the
// signal handler installed by DaemonEntryPoint can trigger the
// normal teardown. On Unix we use SIGTERM (the signal we already
// install a handler for); on Windows we use os.Interrupt, which
// is the only signal os/signal can deliver there.
func terminateSelf() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

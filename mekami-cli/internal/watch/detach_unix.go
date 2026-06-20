//go:build !windows

package watch

import (
	"fmt"
	"os"
	"syscall"
)

// detachStdio redirects the process's stdin/stdout/stderr to
// /dev/null so the daemon does not write to the parent's tty.
// Implementation uses syscall.Dup2 to atomically replace the
// underlying file descriptors.
func detachStdio() error {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open devnull: %w", err)
	}
	_ = syscall.Dup2(int(devnull.Fd()), 0)
	_ = syscall.Dup2(int(devnull.Fd()), 1)
	_ = syscall.Dup2(int(devnull.Fd()), 2)
	_ = devnull.Close()
	return nil
}

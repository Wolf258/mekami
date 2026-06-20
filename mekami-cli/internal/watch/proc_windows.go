//go:build windows

package watch

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// checkProcessAlive returns nil if pid is alive, an error
// otherwise. On Windows we use OpenProcess + GetExitCodeProcess
// because os.FindProcess always succeeds (it only returns the
// handle; it does not verify the process exists) and signal 0 is
// not a meaningful probe.
//
// The function returns nil when the process is alive (still
// running). Returns an error otherwise; windows.ERROR_INVALID_PARAMETER
// (process not found) and windows.ERROR_ACCESS_DENIED (process
// exited between OpenProcess and GetExitCodeProcess) both map
// to a non-nil error so the caller's "alive?" check sees a
// consistent "not alive" result.
func checkProcessAlive(pid int) error {
	if pid <= 0 {
		return errors.New("watch: invalid pid")
	}
	const queryLimited = windows.PROCESS_QUERY_LIMITED_INFORMATION
	h, err := windows.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return err
	}
	if code == 259 { // STILL_ACTIVE
		return nil
	}
	return errors.New("watch: process not running")
}

// terminateSelf sends a polite stop to the current process. On
// Windows the only portable signal os/signal can deliver to a
// detached process is os.Interrupt, which we install a handler
// for in DaemonEntryPoint.
func terminateSelf() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(os.Interrupt)
}

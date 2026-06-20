//go:build windows

package supervisor

import (
	"golang.org/x/sys/windows"
)

// processAlive returns true if pid refers to a live process. On
// Windows the standard idiom ("signal 0 to the pid") is not
// available because:
//
//   - os.FindProcess always succeeds on Windows: it only
//     allocates a Process struct, it does not check whether the
//     underlying handle refers to a real process.
//   - syscall.Signal(0) is not meaningful on Windows; the
//     runtime's Signal implementation for Windows only handles
//     os.Kill (TerminateProcess).
//
// The portable Windows probe is OpenProcess +
// GetExitCodeProcess: OpenProcess fails when the pid has been
// recycled or never existed; GetExitCodeProcess returns
// STILL_ACTIVE (259) when the process is still running.
//
// pid <= 0 is treated as "not alive" to keep callers from
// accidentally passing an unset PID.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const queryLimited = windows.PROCESS_QUERY_LIMITED_INFORMATION
	h, err := windows.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

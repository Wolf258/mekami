//go:build windows

package supervisor

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// killProcess terminates the process identified by pid. On Windows
// there is no Unix-style signal delivery for arbitrary processes:
// SIGTERM does not exist as a concept, and processes cannot be
// politely asked to exit via signal. The closest portable
// equivalent is TerminateProcess, which is unconditional: the
// target exits with the supplied exit code, no cleanup runs.
//
// We ignore the `sig` argument because there is no meaningful
// "polite stop" path on Windows for processes the supervisor
// does not own hooks into. Callers that need a graceful stop
// must use the IPC channel (e.g. send {"cmd":"stop"} over the
// named pipe) before falling back to killProcess.
//
// The function returns nil when the process was successfully
// terminated, or an error otherwise. ESRCH semantics (process
// already gone) are preserved by mapping
// windows.ERROR_INVALID_PARAMETER (raised when OpenProcess is
// given a pid that has been recycled or never existed) to
// syscall.ESRCH so callers that do errors.Is(err, syscall.ESRCH)
// keep working.
func killProcess(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	const terminate = windows.PROCESS_TERMINATE
	h, err := windows.OpenProcess(terminate, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER (87) and ERROR_ACCESS_DENIED
		// (5) can both indicate "process gone" depending on
		// timing. Map them to ESRCH so the caller's
		// errors.Is(err, syscall.ESRCH) check still works.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return syscall.ESRCH
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	return nil
}

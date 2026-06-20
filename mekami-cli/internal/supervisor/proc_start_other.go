//go:build windows || darwin

package supervisor

import "time"

// readProcStartTime is a stub on non-Linux platforms: we
// do not have a portable way to ask the kernel for a
// process start time without spawning a tool, and the
// adoption path is best-effort. Returning (zero, false)
// makes the caller fall back to time.Now(), which gives
// a slightly wrong uptime for adopted orphans but is
// harmless for the supervisor's bookkeeping.
func readProcStartTime(pid int) (time.Time, bool) {
	return time.Time{}, false
}

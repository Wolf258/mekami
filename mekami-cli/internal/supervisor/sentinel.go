package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// SentinelPath returns the path of the global-stop
// sentinel file. When the file exists, the watchdog (and
// any other observer that cares) treats it as a "shut
// down now" signal and exits without waiting for the
// next health-check tick.
//
// The sentinel is what makes `mekami service uninstall`
// fast: instead of waiting up to 5 seconds for the
// watchdog to notice on its own that the supervisor is
// gone, the uninstall flow writes the sentinel, sends
// SIGTERM to the watchdog PID, and the watchdog exits
// within milliseconds. The sentinel is also useful as
// a "deadman switch" for tests: a test that wants the
// watchdog to exit immediately just touches the file.
//
// The sentinel is intentionally a file (not a Unix
// signal) because the watchdog runs in its own session
// (setsid) and signals targeted at the watchdog's
// process group can be lost in some session-leader
// scenarios. A file is observable from any process
// with read access to the state dir.
func SentinelPath() string {
	return filepath.Join(StateDir(), "stop")
}

// WatchdogPIDPath returns the canonical path to the
// watchdog's PID file. The watchdog writes its PID
// here on startup and removes it on exit, so
// `service uninstall` can find and signal the watchdog
// without scanning the process table.
func WatchdogPIDPath() string {
	return filepath.Join(StateDir(), "watchdog.pid")
}

// SetSentinel writes a sentinel file at SentinelPath.
// The file's content is the wall-clock timestamp at
// which the sentinel was set, so a post-mortem can tell
// when the shutdown was requested. The function is
// best-effort: errors are returned to the caller, who
// is expected to fall back to other stop mechanisms
// (e.g. SIGTERM the watchdog PID directly).
//
// The sentinel is a stop signal, not a state file: it
// is removed on the next supervisor start. This avoids
// surprising the user with a stuck "stop" state after a
// reboot.
func SetSentinel() error {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	return os.WriteFile(SentinelPath(), []byte(ts+"\n"), 0o644)
}

// ClearSentinel removes the sentinel file if it exists.
// It is called by the supervisor on startup so the
// sentinel from a previous (failed) shutdown does not
// cascade into the new run. The function is
// best-effort: a missing sentinel is not an error.
func ClearSentinel() error {
	err := os.Remove(SentinelPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SentinelSet reports whether the sentinel file exists
// at SentinelPath. The boolean is true if the file is
// present (regardless of its content); a missing file
// means "no stop requested". The function does not
// read the file, so it is cheap to call on every
// watchdog tick.
func SentinelSet() bool {
	_, err := os.Stat(SentinelPath())
	return err == nil
}

// ReadWatchdogPID returns the PID stored in the
// watchdog's PID file, or (0, nil) if the file is
// missing. A malformed file returns an error. Callers
// should treat (0, nil) as "watchdog not running".
func ReadWatchdogPID() (int, error) {
	return readWatchdogPID(WatchdogPIDPath())
}

// WriteWatchdogPID writes pid to the watchdog's PID
// file. The file is created with 0644 perms so the
// user can cat it from another shell. Used by the
// watchdog itself on startup; the corresponding
// RemoveWatchdogPID is called on shutdown. The
// function creates the parent directory if it does
// not exist so a fresh install (where the supervisor
// state dir was never created) does not fail at the
// first watchdog start.
func WriteWatchdogPID(pid int) error {
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(WatchdogPIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// RemoveWatchdogPID removes the watchdog's PID file.
// A missing file is not an error. Called on
// shutdown so a stale PID from a previous run does
// not confuse a future `service uninstall`.
func RemoveWatchdogPID() error {
	err := os.Remove(WatchdogPIDPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SignalWatchdog sends SIGTERM to the watchdog's PID
// (read from the PID file) and returns true if the
// signal was delivered. The boolean is false if the
// PID file is missing (no watchdog to signal) or the
// PID is not alive. A successful signal delivery is
// not the same as a confirmed exit: the watchdog may
// take a few milliseconds to handle SIGTERM and remove
// its PID file. Callers that need a hard "watchdog is
// gone" guarantee should poll ReadWatchdogPID after
// sending the signal.
//
// This is the cross-platform equivalent of
// `pkill -TERM -F WatchdogPIDPath`. We use the PID
// file rather than a process name match because
// several `mekami` processes (supervisor, daemons)
// share the binary name and we want to target one
// specific process.
func SignalWatchdog() bool {
	pid, err := ReadWatchdogPID()
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix this delivers SIGTERM (polite stop). On Windows
	// os.Kill maps to TerminateProcess, which is unconditional;
	// the watchdog path there is the IPC stop channel anyway.
	if err := proc.Signal(os.Kill); err != nil {
		// ESRCH means the process is already
		// gone, which is what we want anyway.
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		return false
	}
	return true
}

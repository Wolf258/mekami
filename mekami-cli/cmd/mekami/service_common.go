//go:build linux || darwin

package mekami

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Wolf258/mekami-cli/internal/supervisor"
)

// stopSupervisorAndDaemons asks the running supervisor
// (if any) to perform a hard stop: every registered
// daemon is shut down, the stop sentinel is written,
// and the watchdog is signalled to exit. The
// supervisor itself is expected to exit shortly
// after.
//
// The function is best-effort: every error is
// logged to stderr and the caller continues. The
// rationale is that `service-uninstall` is the last
// step of a teardown: even if the supervisor is
// wedged or its socket is missing, the follow-up
// `disable --now` (Linux) or `launchctl unload`
// (macOS) will still get the job done.
//
// The 5-second timeout is chosen so a wedged
// supervisor cannot stall the CLI indefinitely. A
// graceful quit-all normally returns in well under
// a second (each daemon's IPC stop is ~50ms, the
// rest is bookkeeping).
func stopSupervisorAndDaemons() {
	cli := supervisor.NewClient()
	cli.Timeout = 5 * time.Second
	if !cli.Ping(context.Background()) {
		// Supervisor not running: nothing to do.
		// The service-manager unload below will
		// still succeed.
		return
	}
	if err := cli.QuitAll(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: quit-all: %v\n", err)
		return
	}
	// Best-effort: wait a short while for the
	// supervisor to finish its bookkeeping (registry
	// save, socket close) before we let the
	// service-manager unload take over. If the
	// supervisor is hung, the unload will eventually
	// SIGKILL it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !cli.Ping(context.Background()) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// cleanupSupervisorRuntimeState removes the
// supervisor's runtime files in the per-user state
// directory. The intent is to leave a clean slate for
// a future `service-install` (which would otherwise
// find a stale supervisor.pid pointing at a dead
// process, or a stale sentinel from a previous
// uninstall that was interrupted before the sentinel
// was cleared).
//
// The function is best-effort: a missing file is
// not an error, and a permission error is logged
// but does not abort the cleanup. The state
// directory itself is preserved because the
// per-user `daemons.json` lives there; the next
// `service-install` will use the same registry
// (and therefore the same set of daemons) as
// before the uninstall. This is the "preserve
// user intent" property that distinguishes a
// hard uninstall from a destructive purge.
func cleanupSupervisorRuntimeState() {
	stateDir := supervisor.StateDir()
	for _, name := range []string{
		"supervisor.pid",  // supervisor's own PID file
		"supervisor.sock", // IPC socket
		"supervisor.log",  // supervisor's own log
		"watchdog.pid",    // watchdog's PID file
		"stop",            // stop sentinel
	} {
		p := filepath.Join(stateDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", p, err)
		}
	}
}

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WatchdogInterval is how often the supervisor watchdog
// polls the supervisor's PID + socket. Five seconds is a
// good balance: it catches a wedged supervisor in at
// most 5s, and the wakeup rate is negligible.
const WatchdogInterval = 5 * time.Second

// WatchdogMisses is the number of consecutive failed
// health checks before the watchdog considers the
// supervisor dead. With WatchdogInterval = 5s, that is
// 30 seconds of unresponsiveness, well above any
// reasonable supervisor startup time and well below a
// user-perceived "the watcher is frozen" interval.
const WatchdogMisses = 6

// WatchdogHealth checks whether the supervisor is alive
// AND responsive. The first return value is true if
// everything is fine; the second is true if the
// supervisor PID is missing (clean shutdown, watchdog
// should exit) so the caller can distinguish "wedged"
// from "gone".
//
// The function lives in the supervisor package so it can
// be unit-tested with a fake listener; the CLI's
// `mekami supervise _watchdog` is a thin wrapper that
// calls it in a loop.
func WatchdogHealth(pidPath, sockPath string) (healthy bool, gone bool) {
	pid, err := readWatchdogPID(pidPath)
	if err != nil || pid <= 0 {
		return false, true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, true
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, true
	}
	// PID is alive; check the socket. We use a short
	// timeout because a wedged supervisor may accept
	// the connection but never reply.
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		// Socket is the more reliable liveness
		// signal: if the PID is alive but the socket
		// is gone/wedged, the supervisor is broken.
		return false, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(`{"cmd":"ping"}` + "\n")); err != nil {
		return false, false
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false, false
	}
	return true, false
}

func readWatchdogPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing file is "no watchdog",
			// which the public ReadWatchdogPID
			// contract documents as (0, nil).
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// WatchdogRun is the body of `mekami supervise _watchdog`.
// It is a thin loop around WatchdogHealth: every
// WatchdogInterval it checks the supervisor; after
// WatchdogMisses consecutive failures it kills the
// supervisor (if still alive), removes the stale
// socket, and re-launches the supervisor via the
// caller-supplied respawn function. The function
// returns nil when the supervisor is gone entirely
// (so the service manager can restart the whole pair).
//
// In addition, the loop checks the stop sentinel on
// every iteration: when `mekami service-uninstall`
// runs, it writes the sentinel before signalling the
// supervisor, so the watchdog exits within one tick
// (≤5s in production, much less in tests) rather than
// waiting for the supervisor's PID to disappear.
//
// ctx is the watchdog's own context; respawn is the
// function the CLI passes in (it knows how to fork a
// new supervisor process). Keeping the respawn
// callback in the supervisor package would create a
// circular import (the supervisor would have to call
// back into the CLI), so we keep this small package
// free of fork logic and let the CLI supply the
// respawn implementation.
func WatchdogRun(ctx context.Context, stateDir string, respawn func() error) error {
	return watchdogRunTuned(ctx, stateDir, respawn, WatchdogInterval, WatchdogMisses)
}

// WatchdogRunForTest is the test-only entry point that
// lets the caller shrink the polling interval and the
// miss threshold. The production code path uses
// WatchdogRun; the test variant exists so the suite can
// exercise the respawn trigger without sleeping for
// 30s. We do not gate it behind a build tag because the
// CLI never calls it.
func WatchdogRunForTest(ctx context.Context, stateDir string, respawn func() error, interval time.Duration, maxMisses int) error {
	return watchdogRunTuned(ctx, stateDir, respawn, interval, maxMisses)
}

func watchdogRunTuned(ctx context.Context, stateDir string, respawn func() error, interval time.Duration, maxMisses int) error {
	if respawn == nil {
		return errors.New("supervisor: watchdog requires a respawn function")
	}
	pidPath := filepath.Join(stateDir, "supervisor.pid")
	sockPath := filepath.Join(stateDir, "supervisor.sock")
	// Honor an external stop signal before we even
	// start polling: a test or a previous uninstall
	// may have left the sentinel behind. The supervisor
	// clears the sentinel on startup, but a crashed
	// uninstall path may not have that luxury.
	if SentinelSet() {
		return nil
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			// Stop sentinel: the most explicit
			// "shut down now" signal we have.
			// The supervisor writes this before
			// asking the daemon children to quit,
			// so by the time we see the sentinel
			// the supervisor is on its way out.
			// We exit immediately, without
			// probing the supervisor again, so
			// the user-visible shutdown latency
			// is one tick (5s in production,
			// much less in tests).
			if SentinelSet() {
				return nil
			}
			ok, gone := WatchdogHealth(pidPath, sockPath)
			if ok {
				misses = 0
				continue
			}
			// If the supervisor is gone entirely
			// (PID file missing or PID dead), exit
			// and let the service manager restart
			// the pair. The (ok=false, gone=false)
			// case means "PID alive, socket wedged":
			// that is the case we want to escalate
			// via the misses counter, not exit.
			if gone {
				return nil
			}
			misses++
			if misses < maxMisses {
				continue
			}
			// Threshold reached: kill the supervisor
			// and re-spawn it. The re-spawn is
			// blocking-ish; we exit after success
			// so the new supervisor spawns its own
			// watchdog.
			pid, _ := readWatchdogPID(pidPath)
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
			_ = os.Remove(sockPath)
			if err := respawn(); err != nil {
				return fmt.Errorf("watchdog: respawn: %w", err)
			}
			return nil
		}
	}
}

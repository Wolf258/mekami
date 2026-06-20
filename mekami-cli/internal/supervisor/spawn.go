package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// SpawnSpec is the input to SpawnDaemon. The supervisor fills it
// in from the registry, the user CLI, and the on-disk config.
type SpawnSpec struct {
	Root   string
	DBPath string
	Lang   string
	Watch  config.WatchConfig
	Build  config.BuildConfig
	// IndexerNames is the set of language identifiers the project
	// tracks (from .mekami/config.json's indexers). The supervisor
	// passes them to the daemon as _MEKAMI_DAEMON_INDEXERS so the
	// cross-language cleanup runs on every full build the
	// watcher triggers. Empty means "no cross-language cleanup".
	IndexerNames []string
	// FallbackOverride, if non-empty, is written into the
	// daemon's env instead of cfg.Fallback. The supervisor uses
	// this to flip a daemon to the poller when the inotify
	// budget is tight.
	FallbackOverride string
}

// ErrAlreadyRunning is returned by SpawnDaemon if the daemon for
// this root is already up.
var ErrAlreadyRunning = errors.New("supervisor: daemon already running")

// SpawnDaemon fork+execs a new watcher daemon for the given
// spec. The caller (the supervisor) is responsible for recording
// the resulting PID in its daemon table and for waiting on
// status. The spawned child writes its own PID to
// <root>/.mekami/watcher.pid and listens on its own Unix socket.
//
// The function does not block: the supervisor continues while
// the daemon initialises. Use ProbeDaemonReady to wait for the
// daemon's socket to appear.
func SpawnDaemon(spec SpawnSpec) (pid int, err error) {
	absRoot, err := filepath.Abs(spec.Root)
	if err != nil {
		return 0, fmt.Errorf("abs root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absRoot, ".mekami"), 0o700); err != nil {
		return 0, fmt.Errorf("mkdir state: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locate self: %w", err)
	}

	cfgJSON, err := json.Marshal(spec.Watch)
	if err != nil {
		return 0, fmt.Errorf("marshal watch config: %w", err)
	}
	bcfgJSON, err := json.Marshal(spec.Build)
	if err != nil {
		return 0, fmt.Errorf("marshal build config: %w", err)
	}
	idxJSON, err := json.Marshal(spec.IndexerNames)
	if err != nil {
		return 0, fmt.Errorf("marshal indexer names: %w", err)
	}

	cmd := exec.Command(self, "_daemon")
	cmd.Env = append(os.Environ(),
		"_MEKAMI_DAEMON=1",
		"_MEKAMI_DAEMON_ROOT="+absRoot,
		"_MEKAMI_DAEMON_DB="+spec.DBPath,
		"_MEKAMI_DAEMON_LANG="+spec.Lang,
		"_MEKAMI_DAEMON_CONFIG="+string(cfgJSON),
		"_MEKAMI_DAEMON_BCONFIG="+string(bcfgJSON),
		"_MEKAMI_DAEMON_INDEXERS="+string(idxJSON),
		"_MEKAMI_DAEMON_SUPERVISED=1",
		// _MEKAMI_DAEMON_SUPERVISOR_PID lets the daemon
		// detect when its supervisor has died (orphan
		// detection). The daemon uses this to log a warning
		// and, if the user configured self-terminate,
		// gracefully shut down.
		"_MEKAMI_DAEMON_SUPERVISOR_PID="+strconv.Itoa(os.Getpid()),
		// _MEKAMI_DAEMON_SELF_TERM carries the parsed
		// self_terminate_on_orphan value from config so
		// the daemon can decide when (if ever) to
		// shut itself down while orphaned. Empty means
		// "never".
		"_MEKAMI_DAEMON_SELF_TERM="+spec.Watch.SelfTerminateOnOrphan,
	)
	if spec.FallbackOverride != "" {
		// Pass through as an override; the daemon honours
		// _MEKAMI_DAEMON_FALLBACK when set.
		cmd.Env = append(cmd.Env, "_MEKAMI_DAEMON_FALLBACK="+spec.FallbackOverride)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	// Persist the daemon child's stderr so a crash during
	// startup (e.g. unknown subcommand, missing env, panic
	// before the file logger is up) leaves a forensic trace
	// rather than dying silently when ProbeDaemonReady
	// expires. Uses .mekami/watcher.err.log: separate from
	// watcher.log so startup errors aren't interleaved with
	// the watcher's runtime output.
	errLogPath := filepath.Join(absRoot, ".mekami", "watcher.err.log")
	errLog, err := os.OpenFile(errLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open daemon err log: %w", err)
	}
	defer errLog.Close()
	cmd.Stderr = errLog

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("fork: %w", err)
	}
	pid = cmd.Process.Pid
	// Detach: the daemon outlives the supervisor.
	_ = cmd.Process.Release()
	return pid, nil
}

// DaemonSocketPath is the canonical socket for a daemon rooted
// at root. We re-export the watch package's convention.
func DaemonSocketPath(root string) string {
	return filepath.Join(root, ".mekami", "watcher.sock")
}

// DaemonPIDPath is the canonical PID file for the daemon.
func DaemonPIDPath(root string) string {
	return filepath.Join(root, ".mekami", "watcher.pid")
}

// DaemonLogPath is the canonical log file for the daemon.
func DaemonLogPath(root string) string {
	return filepath.Join(root, ".mekami", "watcher.log")
}

// ProbeDaemonReady polls the daemon's socket for up to timeout.
// Returns true as soon as the socket is reachable. Used by the
// supervisor after spawn to decide when the daemon is "running"
// (vs. "starting").
func ProbeDaemonReady(root string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(DaemonSocketPath(root)); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

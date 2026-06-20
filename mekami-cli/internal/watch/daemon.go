package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// DaemonEntryPoint is the re-execed child invoked by the
// supervisor (internal/supervisor/spawn.go). It is started
// when _MEKAMI_DAEMON=1 is in the env. The function does not
// return under normal operation: it installs signal handlers,
// writes the PID, opens the socket, and enters the event loop.
//
// Lifecycle:
//  1. Parse env vars (_MEKAMI_DAEMON_ROOT/DB/LANG/CONFIG/BCONFIG).
//  2. Write PID to .mekami/watcher.pid and start the IPC server.
//  3. Run RunLoop until ctx is cancelled or "stop" IPC arrives.
//  4. On exit: remove PID file, close socket, flush log, exit 0.
//
// The supervisor owns the daemon's lifecycle: it spawns it,
// restarts it on crash, and rehydrates it on supervisor restart.
// The daemon itself only knows how to run the loop.
// normal operation: it installs signal handlers, writes the PID,
// opens the socket, and enters the event loop.
func DaemonEntryPoint(ctx context.Context) error {
	root := os.Getenv("_MEKAMI_DAEMON_ROOT")
	if root == "" {
		return errors.New("daemon: _MEKAMI_DAEMON_ROOT not set")
	}
	dbPath := os.Getenv("_MEKAMI_DAEMON_DB")
	if dbPath == "" {
		return errors.New("daemon: _MEKAMI_DAEMON_DB not set")
	}
	lang := os.Getenv("_MEKAMI_DAEMON_LANG")

	// Detach stdio. The CLI may have been launched from a
	// terminal; we must not write to the parent's tty. The
	// implementation is platform-specific.
	if err := detachStdio(); err != nil {
		return err
	}

	var cfg config.WatchConfig
	if raw := os.Getenv("_MEKAMI_DAEMON_CONFIG"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("daemon: parse config: %w", err)
		}
	} else {
		cfg = config.DefaultWatch()
	}
	var bcfg config.BuildConfig
	if raw := os.Getenv("_MEKAMI_DAEMON_BCONFIG"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &bcfg); err != nil {
			return fmt.Errorf("daemon: parse config: %w", err)
		}
	}
	// _MEKAMI_DAEMON_FALLBACK overrides the fallback mode for
	// this daemon only. The supervisor sets it when degrading
	// a daemon to the poller due to the inotify budget.
	if override := os.Getenv("_MEKAMI_DAEMON_FALLBACK"); override != "" {
		cfg.Fallback = override
	}

	if err := WritePID(root, os.Getpid()); err != nil {
		return fmt.Errorf("write pid: %w", err)
	}
	defer RemovePID(root)

	logFile, err := newFileLogger(LogPath(root), 1<<20) // 1 MiB
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close()
	verbose := cfg.LogLevel == "verbose"

	// Install signal handlers.
	sigCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	stats := &Stats{}

	// The IPC server gets a reference to the current config so
	// reload handlers can re-read it. We capture by closure;
	// the daemon updates currentCfg via the reload hook.
	currentCfg := cfg
	var cfgMu sync.Mutex
	stopped := make(chan struct{})
	ipc, err := startIPCServer(SocketPath(root), root, stats, func() {
		cancel()
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}, func() (config.WatchConfig, error) {
		cfgMu.Lock()
		defer cfgMu.Unlock()
		return currentCfg, nil
	}, func(newCfg config.WatchConfig) error {
		cfgMu.Lock()
		old := currentCfg
		currentCfg = newCfg
		cfgMu.Unlock()
		logFile.writeLine(fmt.Sprintf("reload: on_start=%s debounce_ms=%d fallback=%s",
			newCfg.OnStart, newCfg.DebounceMs, newCfg.Fallback))
		_ = old
		return nil
	})
	if err != nil {
		logFile.writeLine("error: ipc: " + err.Error())
		return err
	}
	defer ipc.Shutdown()

	logFile.writeLine(fmt.Sprintf("started root=%s db=%s", root, dbPath))

	mode, _ := ParseFallbackMode(currentCfg.Fallback)
	src := NewSource(root, mode, currentCfg.PollInterval(), StdLogger{W: nil})
	stats.LastSourceName = src.Name()
	logFile.writeLine("source=" + src.Name())

	// Background: heartbeat + supervisor watchdog. Both
	// goroutines share the same sigCtx and exit on shutdown.
	startHeartbeatAndWatchdog(sigCtx, root, logFile)

	opts := Options{
		Root:         root,
		DBPath:       dbPath,
		Config:       currentCfg,
		BuildConfig:  bcfg,
		Lang:         lang,
		Logger:       logWriter{fl: logFile, verbose: verbose},
		Quiet:        !verbose,
		Source:       src,
		AllowedLangs: indexerNamesFromEnv(),
	}
	if err := RunLoop(sigCtx, src, opts, stats); err != nil {
		logFile.writeLine("error: loop: " + err.Error())
	}

	<-stopped
	logFile.writeLine(fmt.Sprintf("stopped batches=%d ingested=%d removed=%d full_rebuilds=%d errors=%d",
		stats.Batches.Load(), stats.FilesIngested.Load(),
		stats.FilesRemoved.Load(), stats.FullRebuilds.Load(),
		stats.Errors.Load()))
	return nil
}

// logWriter adapts fileLogger to the watch.Logger interface so we
// can pass it to RunLoop. In "resumen" mode only errors are
// persisted; in "verbose" mode info+debug are too.
type logWriter struct {
	fl      *fileLogger
	verbose bool
}

func (w logWriter) Info(format string, args ...any) {
	if w.verbose {
		_ = w.fl.writeLine("info: " + fmt.Sprintf(format, args...))
	}
}

func (w logWriter) Debug(format string, args ...any) {
	if w.verbose {
		_ = w.fl.writeLine("debug: " + fmt.Sprintf(format, args...))
	}
}

func (w logWriter) Error(format string, args ...any) {
	_ = w.fl.writeLine("error: " + fmt.Sprintf(format, args...))
}

// indexerNamesFromEnv reads the set of language identifiers the
// parent CLI serialised into _MEKAMI_DAEMON_INDEXERS before
// forking the daemon. The set comes from the project's
// .mekami/config.json indexers field; it's the tracking set the
// cross-language cleanup uses. Missing env var means "no
// cross-language cleanup", which matches the legacy behaviour
// for callers that haven't been updated yet.
func indexerNamesFromEnv() []string {
	raw := os.Getenv("_MEKAMI_DAEMON_INDEXERS")
	if raw == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	return names
}

// supervisorPIDFromEnv reads the supervisor's PID from the
// _MEKAMI_SUPERVISOR_PID env var. The supervisor passes its
// own PID at spawn time so the daemon can detect when it
// has been orphaned (the supervisor crashed, was killed
// with SIGKILL, or the user's session ended). Returns 0 if
// the env var is missing or invalid, which means "no
// supervisor to watch for" (e.g. foreground mekami watch).
func supervisorPIDFromEnv() int {
	raw := os.Getenv("_MEKAMI_SUPERVISOR_PID")
	if raw == "" {
		return 0
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return pid
}

// startHeartbeatAndWatchdog launches the two background
// goroutines every daemon needs:
//
//  1. heartbeat: rewrites .mekami/heartbeat every
//     HeartbeatInterval so a supervising process can detect
//     a frozen-but-alive daemon.
//
//  2. supervisor watchdog: every WatchdogInterval, checks
//     whether the supervisor PID (passed at spawn time) is
//     still alive. If the supervisor has been unreachable
//     for WatchdogOrphanThreshold consecutive checks, the
//     daemon logs a warning and (if configured) shuts down.
//
// The two goroutines share a single ticker to keep the
// wakeup count at one syscall per interval. Both exit when
// sigCtx is cancelled.
func startHeartbeatAndWatchdog(sigCtx context.Context, root string, logFile *fileLogger) {
	supPID := supervisorPIDFromEnv()
	orphanChecks := 0
	// Pick the coarser interval as the tick. Heartbeat is
	// every HeartbeatInterval; the watchdog is just "is
	// supPID still alive" so it can share the same tick.
	tick := time.NewTicker(HeartbeatInterval)
	// First heartbeat is written immediately so a fresh
	// adopt-by-supervisor on the same tick succeeds without
	// waiting HeartbeatInterval.
	writeHeartbeat(root)
	go func() {
		defer tick.Stop()
		for {
			select {
			case <-sigCtx.Done():
				return
			case <-tick.C:
				writeHeartbeat(root)
				if supPID <= 0 {
					continue
				}
				if err := syscall.Kill(supPID, syscall.Signal(0)); err != nil {
					orphanChecks++
					if orphanChecks == 1 || orphanChecks%orphanLogEvery == 0 {
						logFile.writeLine(fmt.Sprintf(
							"warning: supervisor pid=%d unreachable (%v), running standalone (check %d)",
							supPID, err, orphanChecks))
					}
					if cfg := readSelfTerminateOnOrphan(); cfg > 0 && time.Duration(orphanChecks)*HeartbeatInterval >= cfg {
						logFile.writeLine(fmt.Sprintf(
							"orphan: self-terminate after %s without supervisor", cfg))
						// Reuse the daemon's stop path: send
						// SIGTERM to our own PID. The signal
						// handler in DaemonEntryPoint will
						// trigger the normal teardown.
						_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
						return
					}
				} else {
					if orphanChecks > 0 {
						logFile.writeLine(fmt.Sprintf(
							"recovered: supervisor pid=%d reachable again", supPID))
						orphanChecks = 0
					}
				}
			}
		}
	}()
}

// orphanLogEvery caps how often we write the "running
// standalone" warning to the log. Once a minute is enough;
// the supervisor is gone or it isn't.
const orphanLogEvery = 12 // 12 * 5s = 60s

// readSelfTerminateOnOrphan reads the optional
// _MEKAMI_DAEMON_SELF_TERM env var. The CLI sets it from
// the project's .mekami/config.json (watch.self_terminate_on_orphan)
// so the user can opt into "kill me if my supervisor
// disappears for N minutes". Zero means "never self-terminate",
// which is the safe default: the daemon keeps running
// standalone and the user gets a chance to investigate.
func readSelfTerminateOnOrphan() time.Duration {
	raw := os.Getenv("_MEKAMI_DAEMON_SELF_TERM")
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}

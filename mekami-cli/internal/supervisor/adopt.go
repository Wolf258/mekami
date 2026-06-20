package supervisor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HeartbeatPath is the canonical path to a daemon's heartbeat
// file. Re-exported from the watch package so the supervisor
// can read it without importing watch directly (which would
// risk a circular import via spawn.go).
func HeartbeatPath(root string) string {
	return filepath.Join(root, ".mekami", "heartbeat")
}

// HeartbeatStale is the maximum age a daemon's heartbeat may
// have before the supervisor considers the daemon frozen
// (alive by PID, dead by liveness). It is set well above
// HeartbeatInterval so a single missed write does not
// trigger a false positive.
const HeartbeatStale = 30 * time.Second

// AdoptResult is the outcome of a successful adopt attempt.
// It is returned to LoadFromRegistry so the caller can wire
// the orphan into its in-memory state without going through
// the IPC layer.
type AdoptResult struct {
	PID  int
	Root string
	// State is always StateRunning on success; reserved for
	// future states (e.g. StateDegraded) that might also
	// warrant adoption.
	State State
	// StartedAt is the wall-clock time we observed. The
	// supervisor records this as the daemon's
	// StartedAt so its uptime counter is not reset.
	StartedAt time.Time
}

// ErrNotAnOrphan is returned by adoptDaemon when the on-disk
// state for the project is not consistent with a live
// process: the PID file is missing or stale, the socket
// is gone, or the daemon does not answer a ping. Callers
// treat this as "spawn fresh".
var ErrNotAnOrphan = errors.New("supervisor: no live orphan to adopt")

// adoptDaemon checks whether a watcher daemon for root is
// already running independent of this supervisor. It is the
// core of the "orphan adoption" path: when the supervisor
// starts after a crash that left a daemon alive, we want to
// register the existing process instead of starting a new
// one (which would either fail to bind the socket or, worse,
// create a second watcher competing for the same project).
//
// The check is intentionally strict: any one of the four
// preconditions failing returns ErrNotAnOrphan so the
// caller falls back to a fresh spawn. The four checks are:
//
//  1. .mekami/watcher.pid exists and parses as an integer.
//  2. signal 0 to that PID succeeds (the process is alive).
//  3. .mekami/watcher.sock exists (the daemon bound it).
//  4. The socket answers a "ping" with Ok=true.
//
// In addition, if the heartbeat file is present and not
// stale, we treat the daemon as "fresh"; if the heartbeat
// is stale (or missing), we still adopt but the caller
// can decide whether to log a warning.
//
// The function does not touch the registry or the in-memory
// daemon table: it only reports what is on disk. Callers
// (LoadFromRegistry) are responsible for wiring the result
// into the supervisor's state.
func adoptDaemon(root string) (AdoptResult, error) {
	pidPath := filepath.Join(root, ".mekami", "watcher.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return AdoptResult{}, ErrNotAnOrphan
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return AdoptResult{}, ErrNotAnOrphan
	}
	if !processAlive(pid) {
		return AdoptResult{}, ErrNotAnOrphan
	}
	sockPath := filepath.Join(root, ".mekami", "watcher.sock")
	if _, err := os.Stat(sockPath); err != nil {
		return AdoptResult{}, ErrNotAnOrphan
	}
	// Ping the daemon over its Unix socket. The wire
	// format is the same one the watch.Client uses
	// (line-delimited JSON, "ping" command, "ok" reply).
	conn, err := dialIPC(sockPath, 2*time.Second)
	if err != nil {
		return AdoptResult{}, ErrNotAnOrphan
	}
	defer conn.Close()
	if err := pingOverConn(conn, 2*time.Second); err != nil {
		return AdoptResult{}, ErrNotAnOrphan
	}
	return AdoptResult{
		PID:       pid,
		Root:      root,
		State:     StateRunning,
		StartedAt: readStartedAtBestEffort(root, pid),
	}, nil
}

// readStartedAtBestEffort is a tiny helper that tries to
// recover the daemon's started-at timestamp. If the file
// is missing or unparseable, it falls back to the process
// start time as reported by the kernel (Linux only) or,
// finally, to "now". The resulting uptime is slightly
// off in the fallback case, but the adoption still
// succeeds. The function never returns an error: the
// caller is adoption, which is best-effort by design.
func readStartedAtBestEffort(root string, pid int) time.Time {
	if t, ok := readHeartbeatFile(root); ok {
		return t
	}
	if t, ok := readProcStartTime(pid); ok {
		return t
	}
	return time.Now()
}

// readHeartbeatFile is a small wrapper around the on-disk
// heartbeat. Returns (zero, false) if the file is missing
// or unparseable.
func readHeartbeatFile(root string) (time.Time, bool) {
	data, err := os.ReadFile(HeartbeatPath(root))
	if err != nil {
		return time.Time{}, false
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, ns), true
}

// pingOverConn sends a "ping" request and reads one line
// back. It is a tiny stand-in for what the watch.Client
// does in normal operation; the supervisor uses it only
// during adoption, so the function is intentionally
// minimal and lives here rather than pulling the watch
// package into the supervisor's import set.
func pingOverConn(conn net.Conn, timeout time.Duration) error {
	type pingReq struct {
		Cmd string `json:"cmd"`
	}
	data, err := json.Marshal(pingReq{Cmd: "ping"})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	type pingResp struct {
		OK bool `json:"ok"`
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 4096)
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if !scanner.Scan() {
		if serr := scanner.Err(); serr != nil {
			return serr
		}
		return errors.New("adopt: empty response")
	}
	var resp pingResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("adopt: decode: %w", err)
	}
	if !resp.OK {
		return errors.New("adopt: daemon refused ping")
	}
	return nil
}

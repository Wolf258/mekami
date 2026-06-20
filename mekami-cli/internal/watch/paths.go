package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// StatePath returns the canonical path to the watcher's state
// directory for the given root. We keep it inside .mekami/ so the
// state goes with the project: copying the project also copies the
// watcher's state, and removing .mekami/ removes the daemon.
func StatePath(root string) string {
	return filepath.Join(root, ".mekami")
}

// SocketPath returns the path to the watcher's Unix domain socket.
// Two daemons on the same project would conflict, so the file's
// presence is the "is a daemon running" probe.
func SocketPath(root string) string {
	return filepath.Join(StatePath(root), "watcher.sock")
}

// PIDPath returns the path to the watcher's PID file. The file
// contains the daemon's PID as a string followed by a newline.
func PIDPath(root string) string {
	return filepath.Join(StatePath(root), "watcher.pid")
}

// LogPath returns the path to the watcher's log file. Daemons
// always log here regardless of `Log` (which is for the
// foreground CLI). Rotation is handled by the daemon itself.
func LogPath(root string) string {
	return filepath.Join(StatePath(root), "watcher.log")
}

// HeartbeatPath returns the path to the watcher's heartbeat
// file. The daemon rewrites it every HeartbeatInterval with
// the current unix-nano timestamp; the supervisor (or any
// other observer) uses it to detect a frozen-but-alive
// process (PID responds to signal 0 but the timestamp is
// stale). The file is best-effort: a missing or malformed
// file simply means "unknown liveness", not "definitely dead".
func HeartbeatPath(root string) string {
	return filepath.Join(StatePath(root), "heartbeat")
}

// HeartbeatInterval is how often the daemon rewrites the
// heartbeat file. The supervisor treats a heartbeat older
// than HeartbeatStale as "stale" and falls back to PID
// liveness alone. Five seconds strikes a balance between
// quick failure detection and reasonable write traffic
// (1 syscall per daemon per 5s).
const HeartbeatInterval = 5 * time.Second

// HeartbeatStale is the maximum age a heartbeat may have
// before the supervisor considers the daemon frozen. It is
// set to 6× HeartbeatInterval so a single missed write does
// not trigger a false positive.
const HeartbeatStale = 6 * HeartbeatInterval

// EnsureStateDir creates the .mekami directory under root with
// 0700 perms. We use 0700 because the socket lives there and
// should not be world-accessible.
func EnsureStateDir(root string) error {
	return os.MkdirAll(StatePath(root), 0o700)
}

// ReadPID returns the PID stored in root's PID file, or 0 if the
// file is missing or invalid. The function does not check whether
// the process is alive; callers should do that with ProcessSignal
// or syscall.Kill(pid, 0).
func ReadPID(root string) (int, error) {
	data, err := os.ReadFile(PIDPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("pid file: %w", err)
	}
	return pid, nil
}

// WritePID writes pid to the PID file. The file is created with
// 0644 perms so the user can `cat` it from another shell.
func WritePID(root string, pid int) error {
	return os.WriteFile(PIDPath(root), []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// RemovePID removes the PID file. Missing file is not an error.
func RemovePID(root string) error {
	err := os.Remove(PIDPath(root))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IPC request types are declared in ipc_server.go alongside
// the dispatcher. The wire format is line-delimited JSON: one
// request per line, one response per line. Fields are stable;
// adding new fields is fine, renaming/typing changes is a
// breaking change.

// Request is the wire format for a client -> daemon message. Cmd
// is required; Payload is optional and command-specific (unused
// in the MVP).
type Request struct {
	Cmd     string          `json:"cmd"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is the wire format for a daemon -> client message. Ok
// =false means the request failed; Error is human-readable.
type Response struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Status fields. Populated only by `status`. We inline them
	// rather than nest a sub-struct so the JSON shape stays flat
	// and easy to grep.
	UptimeS       int64  `json:"uptime_s,omitempty"`
	LastBatchUnix int64  `json:"last_batch_unix,omitempty"`
	Batches       int64  `json:"batches,omitempty"`
	FilesIngested int64  `json:"files_ingested,omitempty"`
	FilesRemoved  int64  `json:"files_removed,omitempty"`
	FullRebuilds  int64  `json:"full_rebuilds,omitempty"`
	Errors        int64  `json:"errors,omitempty"`
	Source        string `json:"source,omitempty"`
	Root          string `json:"root,omitempty"`
}

// Encode serialises a Response to a single line (no trailing
// newline). The caller appends "\n" before writing to the socket.
func (r Response) Encode() ([]byte, error) {
	return json.Marshal(r)
}

// DecodeRequest parses a single line into a Request. Empty lines
// and lines starting with '#' are skipped by the server loop; this
// function returns an error for malformed JSON.
func DecodeRequest(line []byte) (Request, error) {
	var r Request
	if err := json.Unmarshal(line, &r); err != nil {
		return r, fmt.Errorf("decode request: %w", err)
	}
	if r.Cmd == "" {
		return r, errors.New("decode request: empty cmd")
	}
	return r, nil
}

// FormatUptime returns a human-readable "1d 2h 3m 4s" string.
// Used by `watch status` for the CLI summary.
func FormatUptime(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)
	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	return strings.Join(parts, "")
}

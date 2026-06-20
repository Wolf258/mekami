package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// writeHeartbeat writes the current unix-nano timestamp to
// root's heartbeat file. The file is best-effort: errors are
// silently dropped because losing one tick is harmless (the
// supervisor tolerates 6× HeartbeatInterval of staleness before
// treating the daemon as frozen). The function takes root
// instead of a full path so the package surface stays
// symmetric with PIDPath/HeartbeatPath/LogPath.
//
// Writes are atomic at the POSIX level: we write to a
// sibling temp file, fsync, and rename. This avoids leaving
// a half-written integer in the file if the daemon is killed
// mid-write.
func writeHeartbeat(root string) {
	p := HeartbeatPath(root)
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, "heartbeat-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, p)
}

// readHeartbeat returns the timestamp stored in the heartbeat
// file at root, or (zero, false) if the file is missing,
// unreadable, or contains garbage. The boolean is false in
// every case where the caller cannot trust the value.
func readHeartbeat(root string) (time.Time, bool) {
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

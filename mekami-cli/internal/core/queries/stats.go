package queries

import (
	"context"

	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// StatsTables is the ordered list of tables counted by Stats.
// Exported so the CLI's `stats` command can iterate the same set.
var StatsTables = []string{"files", "modules", "packages", "symbols", "refs"}

// Stats returns a row count for every table in StatsTables. The query
// is one round-trip; each scalar subquery is independent and cheap
// with the row counts we expect (hundreds of symbols, not millions).
func Stats(ctx context.Context, s *store.Store) (map[string]int64, error) {
	out := make(map[string]int64, len(StatsTables))
	row := s.DB().QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM files),
		(SELECT COUNT(*) FROM modules),
		(SELECT COUNT(*) FROM packages),
		(SELECT COUNT(*) FROM symbols),
		(SELECT COUNT(*) FROM refs)`)
	var counts = make([]int64, len(StatsTables))
	if err := row.Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4]); err != nil {
		return nil, err
	}
	for i, tbl := range StatsTables {
		out[tbl] = counts[i]
	}
	return out, nil
}

// Status is the payload returned by the MCP `status` tool. It is a
// structured snapshot the LLM can read to decide whether the index is
// usable and how stale it is.
type Status struct {
	LastRoot    string           `json:"last_root"`
	LastBuildAt string           `json:"last_build_at,omitempty"` // RFC3339 UTC, empty if never built
	IsWorkspace bool             `json:"is_workspace"`
	RootModule  string           `json:"root_module,omitempty"`
	Counts      map[string]int64 `json:"counts"`
	// Watcher is non-nil only if the caller asked the daemon for
	// its status and got a response. When the daemon is not
	// running, Watcher is nil and the rest of the status is
	// authoritative. When the daemon IS running, Watcher
	// describes its state.
	Watcher *WatcherStatus `json:"watcher,omitempty"`
}

// WatcherStatus is the watcher-side metadata attached to Status.
// We re-declare the fields here (rather than importing the
// internal/watch package) so the queries package has no
// dependency on the watcher — the server layer fills this in
// from an injected client.
type WatcherStatus struct {
	Running      bool   `json:"running"`
	PID          int    `json:"pid,omitempty"`
	UptimeS      int64  `json:"uptime_s,omitempty"`
	LastBatchUnix int64 `json:"last_batch_unix,omitempty"`
	Batches      int64  `json:"batches,omitempty"`
	Source       string `json:"source,omitempty"`
	Errors       int64  `json:"errors,omitempty"`
}

// IndexStatus returns a high-level snapshot of the graph DB. If
// `last_root` is unset (no build has ever run) it returns
// store.ErrNoLastRoot so the MCP handler can surface a clear message.
func IndexStatus(ctx context.Context, s *store.Store) (Status, error) {
	var st Status
	root, err := s.GetMeta(ctx, store.MetaLastRoot)
	if err != nil {
		return st, err
	}
	st.LastRoot = root
	if ts, err := s.GetMeta(ctx, store.MetaLastBuildAt); err == nil {
		st.LastBuildAt = ts
	}
	if ws, err := s.GetMeta(ctx, store.MetaIsWorkspace); err == nil {
		st.IsWorkspace = ws == "1"
	}
	if rm, err := s.GetMeta(ctx, store.MetaRootModule); err == nil {
		st.RootModule = rm
	}
	counts, err := Stats(ctx, s)
	if err != nil {
		return st, err
	}
	st.Counts = counts
	return st, nil
}

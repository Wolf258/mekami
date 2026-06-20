package supervisor

import (
	"os"
	"sort"
	"sync"
)

// BudgetLevel describes how close we are to the per-user
// inotify watch limit.
type BudgetLevel int

const (
	BudgetUnknown BudgetLevel = iota
	BudgetOK
	BudgetWarning
	BudgetDegraded
	BudgetCritical
)

// InotifyBudget tracks the per-user inotify watch usage. The
// supervisor probes /proc for the real limit at construction
// time; on non-Linux or when /proc is unreadable the limit is
// -1 and Level() reports BudgetUnknown.
//
// All exported methods are safe for concurrent use; the
// per-daemon map and the cached usage total are guarded by the
// same mutex.
type InotifyBudget struct {
	mu        sync.Mutex
	limit     int64
	usage     int64
	perDaemon map[string]int64
}

// NewInotifyBudget probes the inotify watch limit from the
// kernel and returns a fresh budget tracker.
func NewInotifyBudget() *InotifyBudget {
	limit := probeInotifyLimit()
	return &InotifyBudget{
		limit:     limit,
		perDaemon: make(map[string]int64),
	}
}

// probeInotifyLimit reads /proc/sys/fs/inotify/max_user_watches
// and returns its value. Returns -1 when the file is missing
// (non-Linux) or unreadable.
func probeInotifyLimit() int64 {
	const path = "/proc/sys/fs/inotify/max_user_watches"
	data, err := readFile(path)
	if err != nil {
		return -1
	}
	var v int64
	for _, b := range data {
		if b < '0' || b > '9' {
			break
		}
		v = v*10 + int64(b-'0')
	}
	if v == 0 {
		return -1
	}
	return v
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 32)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	return buf[:n], nil
}

// SetDaemonWatches records the current watch count for a
// daemon. Passing 0 removes the entry. Usage is maintained
// incrementally so callers can read it cheaply.
func (b *InotifyBudget) SetDaemonWatches(root string, watches int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	prev, had := b.perDaemon[root]
	if watches == 0 {
		if had {
			delete(b.perDaemon, root)
			b.usage -= prev
		}
		return
	}
	b.perDaemon[root] = watches
	if had {
		b.usage += watches - prev
	} else {
		b.usage += watches
	}
}

// Usage returns the total number of inotify watches across all
// tracked daemons.
func (b *InotifyBudget) Usage() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

// Level buckets Usage relative to the probed limit. The
// thresholds (60/80/95 percent) are fixed; see supervisor.go
// for the warn/degrade/critical policy knobs.
func (b *InotifyBudget) Level() BudgetLevel {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return BudgetUnknown
	}
	pct := b.usage * 100 / b.limit
	switch {
	case pct < 60:
		return BudgetOK
	case pct < 80:
		return BudgetWarning
	case pct < 95:
		return BudgetDegraded
	default:
		return BudgetCritical
	}
}

// Percent returns the budget usage as a percentage of the
// probed limit, or -1 when the limit is unknown.
func (b *InotifyBudget) Percent() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return -1
	}
	return b.usage * 100 / b.limit
}

// RootUsage is a (root, watches) pair returned by
// SuggestPollingTargets.
type RootUsage struct {
	Root    string
	Watches int64
}

// SuggestPollingTargets returns the top-N daemons by watch
// count, in descending order. limit<=0 means "all of them".
func (b *InotifyBudget) SuggestPollingTargets(limit int) []RootUsage {
	b.mu.Lock()
	out := make([]RootUsage, 0, len(b.perDaemon))
	for r, w := range b.perDaemon {
		out = append(out, RootUsage{Root: r, Watches: w})
	}
	b.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Watches != out[j].Watches {
			return out[i].Watches > out[j].Watches
		}
		return out[i].Root < out[j].Root
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

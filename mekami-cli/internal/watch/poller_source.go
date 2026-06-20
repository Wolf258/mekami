package watch

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PollerSource implements Source by periodically walking the root
// and comparing each file's (size, mtime) against the previous
// snapshot. It is the fallback for file systems where fsnotify is
// unreliable (NFS, SMB, some FUSE mounts).
//
// The poller is intentionally simple: it does not attempt to detect
// renames. A file that disappears and reappears with a new name is
// reported as Remove(old) + Create(new) in the same tick. The
// coalescer handles the rest.
//
// Performance: a tick walks the entire tree under root. To bound
// the cost, the poller skips the same directories the build walker
// and fsnotify source skip (.git, .mekami, node_modules, vendor,
// _dev). User-supplied ignore patterns from Filter are not applied
// here — the filter is checked again in RunLoop.
type PollerSource struct {
	root     string
	interval time.Duration
	log      Logger

	// seen is the previous snapshot: rel-path -> (size, mtime).
	// A nil map means "no previous snapshot" — the next tick
	// builds one without emitting events.
	mu   sync.Mutex
	seen map[string]fileStat

	out  chan Event
	stop chan struct{}
	done chan struct{}
}

type fileStat struct {
	size  int64
	mtime int64 // unix nanos
}

// NewPollerSource creates a poller with the given interval. A
// non-positive interval defaults to 30s. The first tick is
// scheduled immediately, before the first sleep.
func NewPollerSource(root string, interval time.Duration, log Logger) *PollerSource {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s := &PollerSource{
		root:     root,
		interval: interval,
		log:      log,
		seen:     nil,
		out:      make(chan Event, 1024),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.run()
	return s
}

// Name implements Source.
func (s *PollerSource) Name() string { return "poller" }

// Events implements Source.
func (s *PollerSource) Events() <-chan Event { return s.out }

// Stop implements Source.
func (s *PollerSource) Stop() error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
	return nil
}

// run is the poller goroutine. It emits no events on the first
// tick (it only builds the snapshot); subsequent ticks diff against
// the snapshot and emit Create/Write/Remove.
func (s *PollerSource) run() {
	defer close(s.done)
	defer close(s.out)

	tick := time.NewTicker(s.interval)
	defer tick.Stop()

	// Take the initial snapshot synchronously so the first
	// interval tick has something to diff against.
	s.takeSnapshot()

	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			s.diffAndEmit()
		}
	}
}

// takeSnapshot walks the root and stores the (size, mtime) of every
// file. Errors per-file are logged and skipped.
func (s *PollerSource) takeSnapshot() {
	snap := map[string]fileStat{}
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.Type().IsRegular() {
			if d.IsDir() {
				base := filepath.Base(path)
				if base == ".git" || base == ".mekami" || base == "node_modules" ||
					base == "vendor" || base == "_dev" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		snap[rel] = fileStat{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	s.mu.Lock()
	s.seen = snap
	s.mu.Unlock()
}

// diffAndEmit walks the root, compares against the snapshot, and
// emits Create/Write/Remove events. Then it replaces the snapshot.
func (s *PollerSource) diffAndEmit() {
	cur := map[string]fileStat{}
	_ = filepath.WalkDir(s.root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.Type().IsRegular() {
			if d.IsDir() {
				base := filepath.Base(path)
				if base == ".git" || base == ".mekami" || base == "node_modules" ||
					base == "vendor" || base == "_dev" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		cur[rel] = fileStat{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})

	s.mu.Lock()
	prev := s.seen
	s.seen = cur
	s.mu.Unlock()

	if prev == nil {
		// Should not happen because takeSnapshot runs at start,
		// but be defensive.
		return
	}

	// Diff: emit events for added, changed, removed files.
	for rel, st := range cur {
		p, ok := prev[rel]
		switch {
		case !ok:
			s.tryEmit(Event{Path: rel, Kind: EventCreate, Time: time.Now()})
		case p != st:
			s.tryEmit(Event{Path: rel, Kind: EventWrite, Time: time.Now()})
		}
	}
	for rel := range prev {
		if _, ok := cur[rel]; !ok {
			s.tryEmit(Event{Path: rel, Kind: EventRemove, Time: time.Now()})
		}
	}
}

func (s *PollerSource) tryEmit(e Event) {
	select {
	case s.out <- e:
	case <-s.stop:
	default:
		// Out channel full: drop and log. Same policy as the
		// coalescer overflow path.
		if s.log != nil {
			s.log.Error("poller: dropped event %s (buffer full)", e.Path)
		}
	}
}

// Compile-time guard.
var _ Source = (*PollerSource)(nil)

// isLikelyNetworkFS inspects path's filesystem type and returns true
// for known network or unreliable types. The result is best-effort;
// the `auto` policy uses it as a hint, not a hard rule.
//
// Implementation notes:
//   - Linux: parse /proc/mounts (or /etc/mtab) for the longest
//     matching mount point. fstype is the 3rd whitespace-separated
//     field.
//   - macOS: statfs with type MNT_NFS or MNT_SMBFS (we use the
//     magic numbers because Go's syscall package exposes them
//     inconsistently).
//   - Other Unix: not yet implemented, returns false.
func isLikelyNetworkFS(path string) bool {
	return isLikelyNetworkFSImpl(path)
}

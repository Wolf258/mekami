package watch

import (
	"context"
	"sync/atomic"
	"time"
)

// Source produces normalised Events from the file system. RunLoop
// is the consumer; Sources are interchangeable so the daemon can
// pick the best one (fsnotify on local FS, poller on NFS, etc.).
//
// A Source must:
//   - emit at least one event for any change a user would expect
//     the index to reflect (Create/Write/Remove/Rename/Chmod);
//   - shut down promptly when Stop is called;
//   - close the Events channel on shutdown so RunLoop can detect it.
type Source interface {
	// Events returns a channel of normalised events. The Source
	// closes the channel on shutdown so RunLoop can detect it.
	Events() <-chan Event
	// Stop signals the source to shut down. The implementation
	// must close the Events channel soon after.
	Stop() error
	// Name is a short identifier used in logs and `index_status`
	// ("fsnotify", "poller", "auto:fsnotify", "auto:poller").
	Name() string
}

// Stats accumulates counters the caller can inspect after the loop
// returns. All fields are atomic so a signal handler can read them
// without locking. Stats is returned by pointer because atomic.Int64
// embeds a noCopy lock and cannot be returned by value.
type Stats struct {
	Batches       atomic.Int64
	FilesIngested atomic.Int64
	FilesRemoved  atomic.Int64
	FullRebuilds  atomic.Int64
	Errors        atomic.Int64
	// StartedAt is set once when RunLoop begins. Useful for
	// uptime reporting in `watch status`.
	StartedAt time.Time
	// LastBatchAt is the wall-clock time of the most recent
	// dispatched batch. Zero if no batch has been processed yet.
	LastBatchAt atomic.Int64 // unix nanos
	// LastSourceName is the Name() of the Source that produced
	// events. Set once at startup. Plain string because we
	// assign it before any concurrent reader.
	LastSourceName string
}

// RunLoop drains events from src, feeds them through the coalescer,
// and dispatches batches to handleBatch. It blocks until ctx is
// cancelled or src.Events() closes.
//
// This is the shared core used by:
//   - the foreground `mekami watch` (passes a fsnotify source);
//   - the daemonised watcher (passes whatever Source the auto-detect
//     layer picked).
//
// The function returns nil on graceful shutdown. Per-batch errors
// are reported via opts.Logger and counted in stats.Errors; they do
// not abort the loop.
func RunLoop(ctx context.Context, src Source, opts Options, stats *Stats) error {
	if stats == nil {
		stats = &Stats{}
	}
	stats.StartedAt = time.Now()

	filter := &Filter{IgnorePatterns: opts.Config.SortedIgnore()}
	coalesce := NewCoalescer(opts.Config.Debounce(), 4096)

	// stop is closed when ctx is done OR when the source closes
	// its Events channel (e.g. fsnotify watcher errored out).
	// We use a dedicated goroutine to detect the source close
	// without consuming events.
	stop := make(chan struct{})
	sourceDone := make(chan struct{})
	go func() {
		// We can't range over src.Events() here because that
		// would consume events. Instead, the main reader closes
		// sourceDone when it sees the channel close. We just
		// close stop when that happens.
		<-sourceDone
		close(stop)
	}()

	// readerDone signals when the source reader goroutine has
	// exited. The defer below waits on it to ensure the source
	// is fully torn down before RunLoop returns.
	readerDone := make(chan struct{})

	// Reader: apply the filter, hand events to the coalescer.
	// On channel close, signal sourceDone so the watcher exits.
	go func() {
		defer close(readerDone)
		defer close(sourceDone)
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-src.Events():
				if !ok {
					return
				}
				if !filter.Accept(e.Path) {
					opts.Logger.Debug("filter %s (%s)", e.Path, kindName(e.Kind))
					continue
				}
				if !coalesce.Add(e) {
					opts.Logger.Error("coalesce buffer full, dropped %s", e.Path)
				}
			}
		}
	}()

	defer func() {
		_ = src.Stop()
		<-readerDone
	}()

	for {
		batch, ok := coalesce.Drain(stop)
		if !ok {
			leftover := coalesce.FlushImmediately()
			if len(leftover) > 0 {
				if err := handleBatch(ctx, opts, stats, leftover); err != nil {
					opts.Logger.Error("batch: %v", err)
					stats.Errors.Add(1)
				}
			}
			return nil
		}
		stats.LastBatchAt.Store(time.Now().UnixNano())
		if err := handleBatch(ctx, opts, stats, batch); err != nil {
			opts.Logger.Error("batch: %v", err)
			stats.Errors.Add(1)
		}
	}
}

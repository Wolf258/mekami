package watch

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventKind is a coarse classification of an fsnotify event,
// normalised across platforms. The watcher uses it to filter noise
// (chmod on macOS, IN_IGNORED on Linux, etc.) before debouncing.
type EventKind int

const (
	// EventCreate fires for newly created files. Editors that save
	// with rename-and-create produce a Create followed by a Write
	// on the same path; both collapse into one batch.
	EventCreate EventKind = iota
	// EventWrite fires when an existing file's content is
	// rewritten. Most editor saves (vim, VSCode) emit Write
	// after a Create, which the debouncer merges.
	EventWrite
	// EventRemove fires when a file is deleted. Atomic editor
	// saves (rename) emit a Remove on the temp file; the watcher
	// filters those by path suffix.
	EventRemove
	// EventRename fires when a file is renamed. fsnotify cannot
	// always report the destination, so the watcher treats the
	// old name as Remove and ignores the new one until a
	// subsequent event confirms it.
	EventRename
	// EventChmod fires on permission/metadata changes. Most
	// editors emit at least one Chmod per save. The watcher
	// collapses Chmod with co-temporal Create/Write so it does
	// not cause spurious rebuilds.
	EventChmod
)

// Event is a normalised, deduplicated FS event ready for the
// debouncer. Path is always relative to the watcher root and uses
// forward slashes.
type Event struct {
	Path string
	Kind EventKind
	Time time.Time
}

// Coalescer buffers events and emits a deduplicated batch once a
// quiet window of `debounce` has elapsed since the last event. It is
// safe for concurrent use: producers send via Add, the consumer
// calls Drain in a single goroutine. Drain is blocking: it returns
// when stop is closed or when a batch is ready.
//
// Behaviour:
//   - Repeated events on the same path collapse into one (latest
//     kind wins, with promotion: Remove > Rename > Create > Write
//     > Chmod).
//   - The first event in a window starts a timer. Each new event
//     resets the timer. When the timer fires, the accumulated set
//     is flushed via the wakeup channel.
//   - FlushImmediately drains without waiting for the timer. Used
//     on shutdown so callers don't have to wait.
type Coalescer struct {
	debounce time.Duration
	buffered int

	mu     sync.Mutex
	events map[string]Event
	timer  *time.Timer
	wakeup chan struct{}
}

func NewCoalescer(debounce time.Duration, buffered int) *Coalescer {
	if buffered <= 0 {
		buffered = 1024
	}
	return &Coalescer{
		debounce: debounce,
		buffered: buffered,
		events:   make(map[string]Event),
		wakeup:   make(chan struct{}, 1),
	}
}

// signal posts a non-blocking wake to the consumer. The channel is
// buffered 1, so a second signal coalesces with the first.
func (c *Coalescer) signal() {
	select {
	case c.wakeup <- struct{}{}:
	default:
	}
}

// Add records an event. The path is normalised to forward slashes
// before being used as the dedup key. Returns false if the internal
// buffer is full and the event was dropped.
func (c *Coalescer) Add(e Event) bool {
	if e.Path == "" {
		return true
	}
	e.Path = filepath.ToSlash(e.Path)
	e.Time = time.Now()
	c.mu.Lock()
	if len(c.events) >= c.buffered {
		c.mu.Unlock()
		return false
	}
	prev, ok := c.events[e.Path]
	if !ok || beats(e.Kind, prev.Kind) {
		c.events[e.Path] = e
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(c.debounce, c.flush)
	c.mu.Unlock()
	c.signal()
	return true
}

// flush is the timer callback. It signals the wakeup channel
// when there is at least one pending event. Runs in the timer
// goroutine.
func (c *Coalescer) flush() {
	c.mu.Lock()
	hasEvents := len(c.events) > 0
	c.mu.Unlock()
	if hasEvents {
		c.signal()
	}
}

// FlushImmediately drains pending events synchronously. It does not
// signal the wakeup channel — the next Drain call will pick up the
// drained batch because the map is empty. Used by the watcher on
// shutdown.
func (c *Coalescer) FlushImmediately() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drainLocked()
}

// Drain blocks until either a batch is ready or stop is closed. On
// success it returns the accumulated events and true. On stop it
// returns the leftover batch (if any) and false. Each call returns
// at most one batch.
//
// Semantics: stop is strictly dominant. If stop is closed at any
// point during the call — including before Drain is called, while
// holding the lock, or after a wakeup races with the close — Drain
// returns ok=false. The leftover batch is still returned so the
// caller can decide whether to handle it.
func (c *Coalescer) Drain(stop <-chan struct{}) ([]Event, bool) {
	for {
		// Non-blocking peek: is stop already closed? If so, return
		// immediately with whatever's pending.
		select {
		case <-stop:
			c.mu.Lock()
			leftover := c.drainLocked()
			c.mu.Unlock()
			return leftover, false
		default:
		}
		// Block waiting for either a wakeup or stop.
		select {
		case <-stop:
			c.mu.Lock()
			leftover := c.drainLocked()
			c.mu.Unlock()
			return leftover, false
		case <-c.wakeup:
			c.mu.Lock()
			batch := c.drainLocked()
			c.mu.Unlock()
			// stop may have closed while we were draining.
			// Re-check under the assumption that it has, so
			// the caller sees ok=false and exits cleanly.
			select {
			case <-stop:
				return batch, false
			default:
				if len(batch) == 0 {
					// Spurious wake. Loop and wait.
					continue
				}
				return batch, true
			}
		}
	}
}

// drainLocked returns and clears the current event set. Caller
// must hold c.mu.
func (c *Coalescer) drainLocked() []Event {
	if len(c.events) == 0 {
		return nil
	}
	out := make([]Event, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, e)
	}
	c.events = map[string]Event{}
	return out
}

// beats returns true if a's kind should override b's kind for the
// same path. Promotion order: Remove > Rename > Create > Write >
// Chmod.
func beats(a, b EventKind) bool {
	return rank(a) > rank(b)
}

func rank(k EventKind) int {
	switch k {
	case EventRemove:
		return 5
	case EventRename:
		return 4
	case EventCreate:
		return 3
	case EventWrite:
		return 2
	case EventChmod:
		return 1
	}
	return 0
}

// Translate converts a raw fsnotify event into our normalised form,
// filtering out operations we never care about. The path is made
// relative to root and converted to forward slashes.
func Translate(root string, ev fsnotify.Event) (Event, bool) {
	if ev.Name == "" {
		return Event{}, false
	}
	rel, err := filepath.Rel(root, ev.Name)
	if err != nil {
		return Event{}, false
	}
	if strings.HasPrefix(rel, "..") {
		return Event{}, false
	}
	rel = filepath.ToSlash(rel)

	var kind EventKind
	switch {
	case ev.Op&fsnotify.Create == fsnotify.Create:
		kind = EventCreate
	case ev.Op&fsnotify.Write == fsnotify.Write:
		kind = EventWrite
	case ev.Op&fsnotify.Remove == fsnotify.Remove:
		kind = EventRemove
	case ev.Op&fsnotify.Rename == fsnotify.Rename:
		kind = EventRename
	case ev.Op&fsnotify.Chmod == fsnotify.Chmod:
		kind = EventChmod
	default:
		return Event{}, false
	}
	return Event{Path: rel, Kind: kind, Time: time.Now()}, true
}

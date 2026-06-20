package watch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// fileLogger writes "watch: <line>\n" to a file with simple
// size-based rotation. Rotation keeps three backups (.log, .1, .2,
// .3) and resets the active log to zero on each rotation. The
// logger is safe for concurrent use.
//
// Rotation policy:
//   - When the active log exceeds MaxBytes, the file is rotated:
//     .2 -> .3, .1 -> .2, .log -> .1, new empty .log.
//   - Old .3 is removed.
//   - Rotation is best-effort: if it fails, the new write is
//     dropped with a one-line stderr note.
//
// The MVP only needs "resumen" (one line per batch + errors +
// lifecycle), but the logger accepts any string and lets the
// caller decide what to print.
type fileLogger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	w        io.WriteCloser
	bytes    int64
}

// newFileLogger opens path for append. If the file already exists
// and exceeds maxBytes, it is rotated once on open so the daemon
// does not start with a multi-megabyte log.
func newFileLogger(path string, maxBytes int64) (*fileLogger, error) {
	l := &fileLogger{path: path, maxBytes: maxBytes, backups: 3}
	// Pre-rotate if the existing file is too big.
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		if err := l.rotate(); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	l.w = f
	if info, err := f.Stat(); err == nil {
		l.bytes = info.Size()
	}
	return l, nil
}

// writeLine writes a single line to the active log, rotating
// first if the line would push us over maxBytes. The caller is
// responsible for NOT embedding newlines.
func (l *fileLogger) writeLine(s string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := s + "\n"
	if l.maxBytes > 0 && l.bytes+int64(len(line)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			// Best-effort: drop the line and note on stderr.
			fmt.Fprintf(os.Stderr, "watch: log rotate failed: %v\n", err)
			return err
		}
	}
	n, err := io.WriteString(l.w, line)
	l.bytes += int64(n)
	return err
}

func (l *fileLogger) rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateLocked()
}

// rotateLocked is the inner part of rotation. Caller holds l.mu.
func (l *fileLogger) rotateLocked() error {
	if l.w != nil {
		_ = l.w.Close()
		l.w = nil
	}
	// Shift backups: .N-1 -> .N, .N-2 -> .N-1, ..., .1 -> .2.
	for i := l.backups - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", l.path, i)
		next := fmt.Sprintf("%s.%d", l.path, i+1)
		// Best-effort rename; ignore "not exist".
		if _, err := os.Stat(old); err == nil {
			_ = os.Rename(old, next)
		}
	}
	// .log -> .1
	if _, err := os.Stat(l.path); err == nil {
		_ = os.Rename(l.path, l.path+".1")
	}
	// Reopen.
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.w = f
	l.bytes = 0
	return nil
}

func (l *fileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return nil
	}
	err := l.w.Close()
	l.w = nil
	return err
}

// Compile-time guard.
var _ io.Closer = (*fileLogger)(nil)

// _ keeps filepath in the import set even if rotation helpers
// are not used by the test (avoids goimports churn when this file
// is added or removed).
var _ = filepath.Base

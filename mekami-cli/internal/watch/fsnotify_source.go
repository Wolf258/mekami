package watch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// FsnotifySource implements Source using fsnotify. The
// implementation walks the root once to register every directory
// (fsnotify is not recursive), then translates raw fsnotify events
// into the normalised Event type.
//
// Lifecycle:
//   - NewFsnotifySource starts a goroutine that reads from the
//     fsnotify watcher; the same goroutine owns the Events channel.
//   - Stop closes the underlying fsnotify watcher, which unblocks
//     the reader and causes Events to be closed.
type FsnotifySource struct {
	root string
	log  Logger

	fsw   *fsnotify.Watcher
	out   chan Event
	stop  chan struct{}
	done  chan struct{}
	errCh chan error
}

// NewFsnotifySource walks root and starts an fsnotify watcher. It
// returns an error if the watcher cannot be created. Errors during
// the directory walk are logged and the affected directory is
// skipped, matching the build walker's behaviour.
func NewFsnotifySource(root string, log Logger) *FsnotifySource {
	s := &FsnotifySource{
		root:  root,
		log:   log,
		out:   make(chan Event, 1024),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		errCh: make(chan error, 1),
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		s.errCh <- fmt.Errorf("fsnotify: %w", err)
		close(s.out)
		close(s.done)
		return s
	}
	s.fsw = fsw

	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if log != nil {
				log.Error("walk %s: %v", path, walkErr)
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			base := filepath.Base(path)
			if base == ".git" || base == ".mekami" || base == "node_modules" ||
				base == "vendor" || base == "_dev" {
				return filepath.SkipDir
			}
		}
		if d.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		if err := fsw.Add(path); err != nil {
			if log != nil {
				log.Error("watch add %s: %v", path, err)
			}
		}
		return nil
	}); err != nil {
		s.errCh <- fmt.Errorf("walk root: %w", err)
		_ = fsw.Close()
		close(s.out)
		close(s.done)
		return s
	}

	go s.run()
	return s
}

// Name implements Source.
func (s *FsnotifySource) Name() string { return "fsnotify" }

// Events implements Source. The channel is closed when the
// underlying fsnotify watcher shuts down.
func (s *FsnotifySource) Events() <-chan Event { return s.out }

// Stop implements Source. It closes the underlying fsnotify watcher
// and waits for the reader goroutine to exit. Stop is idempotent.
func (s *FsnotifySource) Stop() error {
	if s.fsw != nil {
		_ = s.fsw.Close()
	}
	select {
	case <-s.done:
	default:
		// Signal stop and wait briefly.
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
		<-s.done
	}
	return nil
}

// InitError returns the error from construction (if any) without
// blocking. Returns nil if the source was created successfully.
func (s *FsnotifySource) InitError() error {
	select {
	case err := <-s.errCh:
		return err
	default:
		return nil
	}
}

// run is the reader goroutine. It translates fsnotify events into
// our normalised Event type and forwards them on s.out. The
// goroutine exits when:
//   - the fsnotify watcher closes (Stop was called);
//   - s.stop is closed (defensive).
func (s *FsnotifySource) run() {
	defer close(s.done)
	defer close(s.out)
	for {
		select {
		case <-s.stop:
			return
		case ev, ok := <-s.fsw.Events:
			if !ok {
				return
			}
			e, accepted := Translate(s.root, ev)
			if !accepted {
				continue
			}
			select {
			case s.out <- e:
			case <-s.stop:
				return
			}
		case err, ok := <-s.fsw.Errors:
			if !ok {
				return
			}
			if s.log != nil {
				s.log.Error("fsnotify: %v", err)
			}
		}
	}
}

// Compile-time guard.
var _ Source = (*FsnotifySource)(nil)

// ErrSourceInit is the error type returned by Source implementations
// when construction fails. Callers can use errors.Is to detect it.
var ErrSourceInit = errors.New("watch: source init failed")

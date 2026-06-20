// Package watch implements a file-system watcher that triggers
// incremental re-indexing of the code graph.
//
// Architecture:
//
//	Source (fsnotify / poller) ──► RunLoop ──► handleBatch ──► ingest.BuildIncremental
//	                                                                  │
//	                                                  ErrStructural? ─► ingest.Build
//
// The watcher is intentionally a single goroutine (RunLoop's main
// loop) plus one reader goroutine per Source. There is no shared
// state beyond the Coalescer's internal map, so concurrency
// hazards are localised.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/core/ingest"
	"github.com/Wolf258/mekami-api/api/v1"
	_ "github.com/Wolf258/mekami-cli/internal/core/frontend/all_gen"
)

// Logger is the minimum sink the watcher needs. The CLI supplies a
// Progress-style helper; tests pass a bytes.Buffer wrapper.
type Logger interface {
	Info(format string, args ...any)
	Debug(format string, args ...any)
	Error(format string, args ...any)
}

// StdLogger wraps an io.Writer with the standard `level: msg`
// format. It is the default Logger when the caller does not
// provide one.
type StdLogger struct {
	W       io.Writer
	Verbose bool
}

func (l StdLogger) Info(format string, args ...any) {
	if l.W == nil {
		return
	}
	fmt.Fprintf(l.W, "watch: "+format+"\n", args...)
}

func (l StdLogger) Debug(format string, args ...any) {
	if !l.Verbose || l.W == nil {
		return
	}
	fmt.Fprintf(l.W, "watch: "+format+"\n", args...)
}

func (l StdLogger) Error(format string, args ...any) {
	if l.W == nil {
		return
	}
	fmt.Fprintf(l.W, "watch: error: "+format+"\n", args...)
}

// Options bundles the dependencies and knobs Run needs. The CLI
// builds this from the parsed config + CLI flags; tests build it
// directly with a tmp dir and a bytes.Buffer logger.
type Options struct {
	// Root is the source tree to watch. Required. Walked once
	// before the FS watcher starts so directories are registered
	// with fsnotify (which is not recursive).
	Root string
	// DBPath is the SQLite file the build pipeline reads/writes.
	// Required.
	DBPath string
	// Config controls debounce, ignore patterns, on-start
	// behaviour, and verbosity. Use config.Default() if you
	// have nothing specific to set.
	Config config.WatchConfig
	// BuildConfig is the build-time defaults (jobs, force-root).
	// 0 jobs means runtime.NumCPU().
	BuildConfig config.BuildConfig
	// Lang selects the language frontend for the build. Empty
	// defaults to "go" inside the ingest package; the watcher
	// forwards it as-is.
	Lang string
	// AllowedLangs is the set of language identifiers the
	// project tracks, derived from .mekami/config.json's
	// indexers. The watcher forwards it to ingest.BuildOptions
	// so the cross-language cleanup (prune) runs before every
	// full build. Empty means "no cross-language cleanup",
	// matching the legacy single-lang behaviour.
	AllowedLangs []string
	// Logger is invoked for Info/Debug/Error messages. Use
	// StdLogger{W: os.Stderr} for production.
	Logger Logger
	// Quiet suppresses all non-error output. Overrides
	// Config.Log.
	Quiet bool
	// Once runs a single batch then exits. Used by `mekami watch
	// --once` for scripting/CI. Honours on_start the same as the
	// long-running mode.
	Once bool
	// Source overrides the auto-detect layer. When nil, Run
	// picks fsnotify (foreground mode) or whatever AutoSource
	// recommends (daemon mode). Tests use this to inject
	// deterministic Sources.
	Source Source
}

// Run blocks until ctx is cancelled or --once finishes its on-start
// pass. It returns nil on graceful shutdown (ctx done) and a non-nil
// error only on fatal init failures (e.g. fsnotify cannot start).
// Per-batch errors are reported via Options.Logger and counted in
// stats.Errors; they do not abort the loop.
//
// Options.Once means "run the on-start pass and exit": useful for
// scripting where the user wants a one-shot build using the same
// config-driven on_start semantics as the long-running watcher.
// Once does not enter the event loop.
func Run(ctx context.Context, opts Options) (*Stats, error) {
	stats := &Stats{}
	if opts.Root == "" {
		return stats, errors.New("watch: Root is required")
	}
	if opts.DBPath == "" {
		return stats, errors.New("watch: DBPath is required")
	}
	if opts.Logger == nil {
		opts.Logger = StdLogger{W: os.Stderr, Verbose: opts.Config.Log == "debug"}
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return stats, fmt.Errorf("abs root: %w", err)
	}
	opts.Root = absRoot

	// OnStart: optionally do a first build before entering the
	// event loop. The user can ask to skip (assume the DB is
	// fresh) or run an incremental pass over the existing set of
	// files.
	if err := runOnStart(ctx, opts, stats); err != nil {
		opts.Logger.Error("on-start: %v", err)
		stats.Errors.Add(1)
	}
	if opts.Once {
		// --once exits after the on-start pass. We don't even
		// bother starting the event loop.
		return stats, nil
	}

	src := opts.Source
	if src == nil {
		src = NewFsnotifySource(opts.Root, opts.Logger)
	}
	stats.LastSourceName = src.Name()

	if err := RunLoop(ctx, src, opts, stats); err != nil {
		return stats, err
	}
	return stats, nil
}

// runOnStart executes the configured on-start action. It is a
// separate function so the main loop reads top-down.
func runOnStart(ctx context.Context, opts Options, stats *Stats) error {
	switch opts.Config.OnStartAction() {
	case config.OnStartSkip:
		opts.Logger.Debug("on-start: skip (assuming fresh DB)")
		return nil
	case config.OnStartIncremental:
		opts.Logger.Info("on-start: incremental pass over current files")
		buildOpts := buildOptsFromConfig(opts)
		s, err := ingest.Build(ctx, buildOpts)
		if err != nil {
			return err
		}
		stats.Batches.Add(1)
		stats.FilesIngested.Add(int64(s.FilesIngested))
		stats.FilesRemoved.Add(int64(s.FilesRemoved))
		if s.Mode == "full" {
			stats.FullRebuilds.Add(1)
		}
		return nil
	default:
		opts.Logger.Info("on-start: full build")
		buildOpts := buildOptsFromConfig(opts)
		s, err := ingest.Build(ctx, buildOpts)
		if err != nil {
			return err
		}
		stats.Batches.Add(1)
		stats.FilesIngested.Add(int64(s.FilesIngested))
		stats.FilesRemoved.Add(int64(s.FilesRemoved))
		stats.FullRebuilds.Add(1)
		return nil
	}
}

// handleBatch classifies a coalesced batch and dispatches it to
// BuildIncremental or Build. A "structural" batch (one that touches
// go.mod/go.work/go.sum) or a non-Go path forces a full rebuild.
// We log the promotion explicitly so the user can see why a full
// rebuild happened.
func handleBatch(ctx context.Context, opts Options, stats *Stats, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	stats.Batches.Add(1)
	if !opts.Quiet && opts.Config.ShouldLog("info") {
		// Compact one-line summary: kinds with counts.
		counts := map[EventKind]int{}
		for _, e := range batch {
			counts[e.Kind]++
		}
		parts := make([]string, 0, len(counts))
		for k, n := range counts {
			parts = append(parts, fmt.Sprintf("%s:%d", kindName(k), n))
		}
		opts.Logger.Info("batch[%d events: %s]", len(batch), strings.Join(parts, " "))
	}
	paths := make([]string, 0, len(batch))
	structural := false
	for _, e := range batch {
		if ingest.IsStructural(e.Path) {
			structural = true
			continue
		}
		// Files that the active frontend cannot index slipped past
		// the filter (e.g. a freshly created .md in the project
		// root). Treat as a signal for a full rebuild: the project
		// layout may have changed in a way the walker cannot
		// enumerate incrementally. We use the default set of file
		// extensions across all registered frontends here; the
		// incremental builder re-checks per-frontend when it
		// actually classifies the path.
		if !hasKnownExt(e.Path) {
			structural = true
			continue
		}
		paths = append(paths, e.Path)
	}
	if structural {
		if !opts.Quiet {
			opts.Logger.Info("structural change detected; full rebuild")
		}
		buildOpts := buildOptsFromConfig(opts)
		s, err := ingest.Build(ctx, buildOpts)
		if err != nil {
			return err
		}
		stats.FilesIngested.Add(int64(s.FilesIngested))
		stats.FilesRemoved.Add(int64(s.FilesRemoved))
		stats.FullRebuilds.Add(1)
		return nil
	}
	if len(paths) == 0 {
		// All events were test files or otherwise filtered.
		return nil
	}
	buildOpts := buildOptsFromConfig(opts)
	s, err := ingest.BuildIncremental(ctx, buildOpts, paths)
	if err != nil {
		if errors.Is(err, ingest.ErrStructuralChange) {
			// Defensive: BuildIncremental also returns this if a
			// structural file was hiding in the batch. Promote.
			opts.Logger.Info("incremental refused: full rebuild")
			full, ferr := ingest.Build(ctx, buildOpts)
			if ferr != nil {
				return ferr
			}
			stats.FilesIngested.Add(int64(full.FilesIngested))
			stats.FilesRemoved.Add(int64(full.FilesRemoved))
			stats.FullRebuilds.Add(1)
			return nil
		}
		if errors.Is(err, ingest.ErrNoLastRoot) {
			// The DB has never been built. Treat as a one-shot
			// full build so the user can recover by re-running.
			opts.Logger.Info("no last_root; running full build")
			full, ferr := ingest.Build(ctx, buildOpts)
			if ferr != nil {
				return ferr
			}
			stats.FilesIngested.Add(int64(full.FilesIngested))
			stats.FilesRemoved.Add(int64(full.FilesRemoved))
			stats.FullRebuilds.Add(1)
			return nil
		}
		return err
	}
	stats.FilesIngested.Add(int64(s.FilesIngested))
	stats.FilesRemoved.Add(int64(s.FilesRemoved))
	if !opts.Quiet && opts.Config.ShouldLog("info") && s.FilesIngested+s.FilesRemoved > 0 {
		opts.Logger.Info("incremental: ingested=%d removed=%d duration=%s",
			s.FilesIngested, s.FilesRemoved, s.Duration.Round(time.Millisecond))
	}
	return nil
}

// buildOptsFromConfig adapts Options to ingest.BuildOptions. The
// Jobs field is forwarded as-is: 0 means runtime.NumCPU() inside
// the ingest package, which is the documented behaviour.
// AllowedLangs is forwarded so the cross-language cleanup runs
// before every full build the watcher triggers.
func buildOptsFromConfig(opts Options) ingest.BuildOptions {
	return ingest.BuildOptions{
		Root:         opts.Root,
		DBPath:       opts.DBPath,
		Lang:         opts.Lang,
		Quiet:        opts.Quiet,
		Jobs:         opts.BuildConfig.Jobs,
		ForceRoot:    opts.BuildConfig.ForceRoot,
		AllowedLangs: opts.AllowedLangs,
	}
}

func kindName(k EventKind) string {
	switch k {
	case EventCreate:
		return "create"
	case EventWrite:
		return "write"
	case EventRemove:
		return "remove"
	case EventRename:
		return "rename"
	case EventChmod:
		return "chmod"
	}
	return "?"
}

// hasKnownExt returns true if path's extension matches the union of
// all registered frontend Extensions(). It is a coarse pre-filter in
// the watcher; the incremental builder does the authoritative check
// per active frontend.
func hasKnownExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	for _, f := range api.All() {
		for _, e := range f.Extensions() {
			if strings.EqualFold(e, ext) {
				return true
			}
		}
	}
	return false
}

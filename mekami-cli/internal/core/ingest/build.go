package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/modlayout"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/core/walk"
)

// BuildOptions configures a single Build invocation.
type BuildOptions struct {
	Root   string
	DBPath string
	Lang   string // language identifier (defaults to "go" when empty)
	Clean  bool
	Quiet  bool
	// Jobs is the number of parse workers used during ingest. 0 picks
	// runtime.NumCPU(). Values < 1 are treated as 1.
	Jobs int
	// ForceRoot allows updating last_root to a different absolute path
	// without rebuilding the database from scratch. When false (the
	// default), Build refuses a root change and returns an error,
	// because file paths stored in the DB would no longer resolve
	// against the new root, breaking DiffSinceLastBuild and SourceSlice.
	ForceRoot bool
	// AllowedLangs is the set of language identifiers the project
	// currently tracks. Rows in `files` whose lang is not in this
	// set are removed before ingest starts, and the removal is
	// surfaced in BuildStats.RemovedLangs so the caller can log
	// it. Empty means "no cross-language cleanup"; legacy single-
	// lang callers (and tests that don't care about it) leave it
	// nil. The CLI's runBuild populates it from the project's
	// .mekami/config.json indexers, plus the frontend selected by
	// --lang when --lang adds a new one.
	AllowedLangs []string
}

// BuildStats are the per-build counters returned by Build.
type BuildStats struct {
	Mode           string // "full" or "incremental"
	FilesScanned   int
	FilesIngested  int
	FilesSkipped   int
	FilesRemoved   int
	SymbolsAdded   int64
	RefsAdded      int64
	Duration       time.Duration
	StructuralFull bool // true if incremental promoted to full rebuild
	// RemovedLangs holds per-language removal counts from the
	// cross-language cleanup that runs when AllowedLangs is set.
	// Each entry is lang → files deleted. Symbols and refs per
	// language are not reported here because they cascade through
	// the FK and the caller can derive them from Stats if needed.
	// Nil when no cleanup ran.
	RemovedLangs map[string]int64
	// SkippedByReason groups FilesSkipped by error reason. The
	// ingest loop accumulates one entry per distinct reason
	// string and the caller renders the top entries as a
	// summary instead of N per-file log lines. Nil when no
	// files were skipped.
	SkippedByReason map[string]int64
}

// Build walks the source tree under opts.Root, parses every file via
// the registered frontend for opts.Lang, and persists the resulting
// symbols / refs into a SQLite database at opts.DBPath. The pipeline
// is incremental: a file whose sha256 matches the stored hash is
// skipped without re-parsing.
func Build(ctx context.Context, opts BuildOptions) (BuildStats, error) {
	stats := BuildStats{Mode: "full"}
	start := time.Now()

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	if opts.Lang == "" {
		opts.Lang = "go"
	}
	if abs, err := filepath.Abs(opts.Root); err == nil {
		opts.Root = abs
	} else {
		return stats, fmt.Errorf("abs root: %w", err)
	}
	fe, err := api.Get(opts.Lang)
	if err != nil {
		return stats, err
	}

	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o755); err != nil {
		return stats, err
	}

	if opts.Clean {
		_ = os.Remove(opts.DBPath)
		_ = os.Remove(opts.DBPath + "-wal")
		_ = os.Remove(opts.DBPath + "-shm")
	}

	s, err := store.Open(opts.DBPath)
	if err != nil {
		return stats, err
	}
	defer s.Close()

	ws, rootIsWorkspaceRoot, err := prepareBuildContext(ctx, s, fe, opts)
	if err != nil {
		return stats, err
	}

	var langFiles []string
	seen := map[string]struct{}{}
	err = walk.MatchingFiles(opts.Root, ws, rootIsWorkspaceRoot, fe.Extensions(), func(rel string) error {
		if !fe.IsIndexable(rel) {
			return nil
		}
		langFiles = append(langFiles, rel)
		seen[rel] = struct{}{}
		return nil
	})
	if err != nil {
		return stats, err
	}

	stats.FilesScanned = len(langFiles)

	tx, err := s.Begin(ctx)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()

	// Cross-language cleanup. .mekami/config.json is the source
	// of truth for which languages the project tracks; rows in
	// the DB whose lang is no longer in that set are deleted
	// before ingest. The deletion runs inside the build's
	// transaction so a later failure rolls the prune back too.
	if len(opts.AllowedLangs) > 0 {
		prune, err := pruneDisabledLangs(ctx, tx, opts.AllowedLangs)
		if err != nil {
			return stats, err
		}
		if prune != nil {
			stats.RemovedLangs = prune.FilesRemoved
			if !opts.Quiet {
				if msg := formatPruneLog(prune); msg != "" {
					fmt.Fprintln(os.Stderr, msg)
				}
			}
		}
	}

	prevFiles, err := queries.AllFiles(ctx, s)
	if err != nil {
		return stats, err
	}
	prevByPath := map[string]model.File{}
	for _, f := range prevFiles {
		prevByPath[f.Path] = f
	}

	beforeStats, err := queries.Stats(ctx, s)
	if err != nil {
		return stats, err
	}

	prog := NewProgress(ctx, os.Stderr, opts.Quiet)
	defer prog.Done()

	if err := ingestFilesParallel(ctx, tx, opts.Root, langFiles, prevByPath, fe, prog, &stats, opts.Jobs); err != nil {
		return stats, err
	}

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	for _, prev := range prevFiles {
		if _, ok := seen[prev.Path]; ok {
			continue
		}
		// When AllowedLangs is set, the cross-language cleanup
		// (prune) at the start of the build is responsible for
		// removing disabled-language rows, and the active
		// frontend's walk is responsible for refreshing the
		// active language. We don't sweep arbitrary unseen files
		// here because that would delete rows that belong to
		// another (still-allowed) frontend's tracked files, or
		// rows that the watcher is about to re-create from an
		// out-of-band write. The legacy single-language
		// behaviour (AllowedLangs empty) keeps the original
		// "remove everything we didn't see" semantics.
		if len(opts.AllowedLangs) > 0 {
			continue
		}
		prog.Event("delete", prev.Path)
		if err := tx.RemoveFile(prev.ID); err != nil {
			return stats, err
		}
		stats.FilesRemoved++
	}

	if err := tx.Commit(); err != nil {
		return stats, err
	}

	afterStats, err := queries.Stats(ctx, s)
	if err != nil {
		return stats, err
	}
	stats.SymbolsAdded = afterStats["symbols"] - beforeStats["symbols"]
	stats.RefsAdded = afterStats["refs"] - beforeStats["refs"]
	stats.Duration = time.Since(start)

	if !opts.Quiet && stats.FilesSkipped > 0 {
		PrintSkippedSummary(os.Stderr, stats, opts.Clean)
	}

	return stats, nil
}

// prepareBuildContext is the shared preamble: open the store, write
// schema, validate root transition, stamp last_root / last_build_at,
// discover the workspace, and persist workspace metadata. Both full
// and incremental builds start with this so the meta table is always
// consistent.
func prepareBuildContext(ctx context.Context, s *store.Store, fe api.Frontend, opts BuildOptions) (*api.Workspace, bool, error) {
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, false, fmt.Errorf("abs root: %w", err)
	}
	prevRoot, getErr := s.GetMeta(ctx, store.MetaLastRoot)
	if getErr == nil && prevRoot != "" && !modlayout.SamePath(prevRoot, absRoot) {
		if !opts.Clean && !opts.ForceRoot {
			return nil, false, fmt.Errorf(
				"last_root is %q, requested %q; "+
					"file paths in the DB would no longer resolve. "+
					"Use --clean to rebuild from scratch, or pass --force-root to relocate (the DB will be inconsistent until rebuilt)",
				prevRoot, absRoot)
		}
	}
	if err := s.SetMeta(ctx, store.MetaLastRoot, absRoot); err != nil {
		return nil, false, err
	}
	if err := s.SetMeta(ctx, store.MetaLastBuildAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, false, err
	}
	opts.Root = absRoot

	ws, err := fe.ResolveLayout(opts.Root)
	if err != nil {
		return nil, false, fmt.Errorf("workspace: %w", err)
	}
	rootIsWorkspaceRoot := ws.IsWorkspace && modlayout.SamePath(ws.WorkspaceDir, opts.Root)
	wsFlag := "0"
	if rootIsWorkspaceRoot {
		wsFlag = "1"
	}
	if err := s.SetMeta(ctx, store.MetaIsWorkspace, wsFlag); err != nil {
		return nil, false, err
	}
	if rootIsWorkspaceRoot {
		// Enumerate modules via the frontend (language-agnostic).
		// The frontend returns the canonical module id for each
		// module dir; the core just persists the (dir, path)
		// pairs as JSON lines.
		mods, merr := fe.ResolveModules(opts.Root)
		if merr != nil {
			return nil, false, fmt.Errorf("resolve modules: %w", merr)
		}
		relMods := make([]string, 0, len(mods))
		for _, m := range mods {
			r, e := filepath.Rel(opts.Root, m.Dir)
			if e != nil {
				r = m.Dir
			}
			rel := filepath.ToSlash(r)
			if m.ModuleID == "" {
				relMods = append(relMods, rel)
				continue
			}
			entry := modlayout.ModuleEntry{Dir: rel, Path: m.ModuleID}
			b, jerr := json.Marshal(entry)
			if jerr != nil {
				relMods = append(relMods, rel)
				continue
			}
			relMods = append(relMods, string(b))
		}
		if err := s.SetMeta(ctx, store.MetaWorkspaceMods, strings.Join(relMods, "\n")); err != nil {
			return nil, false, err
		}
		if err := s.SetMeta(ctx, store.MetaPrimaryModule, ws.PrimaryModPath); err != nil {
			return nil, false, err
		}
	} else if ws.IsWorkspace {
		if err := s.SetMeta(ctx, store.MetaWorkspaceMods, ""); err != nil {
			return nil, false, err
		}
		if err := s.SetMeta(ctx, store.MetaPrimaryModule, ""); err != nil {
			return nil, false, err
		}
	}

	// The root module is the canonical module identifier for the
	// build root. Frontends that have no concept of a single root
	// module return an empty string and the meta key stays unset.
	if rm, rerr := fe.RootModule(opts.Root); rerr == nil && rm != "" {
		if err := s.SetMeta(ctx, store.MetaRootModule, rm); err != nil {
			return nil, false, err
		}
	}

	return ws, rootIsWorkspaceRoot, nil
}

// loadPrevFiles returns the previously indexed files, sorted by path
// for stable diffs and tests. Shared between Build and
// BuildIncremental.
func loadPrevFiles(ctx context.Context, s *store.Store) ([]model.File, map[string]model.File, error) {
	prev, err := queries.AllFiles(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	byPath := map[string]model.File{}
	for _, f := range prev {
		byPath[f.Path] = f
	}
	return prev, byPath, nil
}

// ingestFilesParallel parses langFiles concurrently with up to jobs
// workers, then writes the results serially in the original file
// order. Parsing is the CPU-bound part of ingest; the write phase
// must stay serial because the underlying SQLite transaction is
// not safe for concurrent use.
func ingestFilesParallel(
	ctx context.Context,
	tx *store.Tx,
	root string,
	langFiles []string,
	prevByPath map[string]model.File,
	fe api.Frontend,
	prog *Progress,
	stats *BuildStats,
	jobs int,
) error {
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	if jobs > len(langFiles) {
		jobs = len(langFiles)
	}
	if jobs < 1 {
		jobs = 1
	}

	type job struct {
		order int
		rel   string
	}
	type parsed struct {
		order  int
		result api.ParseResult
		err    error
	}

	jobsCh := make(chan job, jobs)
	results := make(chan parsed, jobs)

	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				if err := ctx.Err(); err != nil {
					results <- parsed{order: j.order, err: err}
					continue
				}
				abs := filepath.Join(root, j.rel)
				hash, mtime, size, err := walk.Fingerprint(abs)
				if err != nil {
					results <- parsed{order: j.order, err: fmt.Errorf("%s: %w", j.rel, err)}
					continue
				}
				if prev, ok := prevByPath[j.rel]; ok && prev.Hash == hash && prev.Lang == fe.Name() {
					results <- parsed{order: j.order}
					continue
				}
				res, err := fe.ParseFile(root, j.rel, abs, hash, mtime, size)
				results <- parsed{order: j.order, result: res, err: err}
			}
		}()
	}

	go func() {
		for i, rel := range langFiles {
			jobsCh <- job{order: i, rel: rel}
		}
		close(jobsCh)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	ordered := make([]parsed, len(langFiles))
	for r := range results {
		if r.order < 0 || r.order >= len(ordered) {
			return fmt.Errorf("internal: out-of-range result order %d", r.order)
		}
		ordered[r.order] = r
	}

	for i, p := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := langFiles[i]
		if p.err != nil {
			if errors.Is(p.err, context.Canceled) || errors.Is(p.err, context.DeadlineExceeded) {
				return p.err
			}
			reason := skipReason(p.err)
			if stats.SkippedByReason == nil {
				stats.SkippedByReason = map[string]int64{}
			}
			stats.SkippedByReason[reason]++
			stats.FilesSkipped++
			continue
		}
		if p.result.RelPath == "" {
			continue
		}
		prog.Event("ingest", rel)
		if err := WriteParseResult(tx, p.result); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		stats.FilesIngested++
	}
	return nil
}

// skipReason normalises a per-file parse error into a short
// string suitable for grouping in the SkippedByReason map. We
// keep the type prefix so identical errors from different files
// collapse, but drop the wrapping file path that Build inserts
// (e.g. "api/client.go: Rel: ...") so the summary line stays
// readable. The colon between the type and the message is what
// the original "Rel: can't make X relative to Y" error naturally
// produces, so the result reads as a single line.
func skipReason(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	// Trim any "<path>: " prefix the caller may have added; we
	// only want the underlying reason in the summary bucket.
	if i := strings.LastIndex(s, ": "); i > 0 {
		// Heuristic: a path-like prefix has a slash or a dot
		// before the colon. "Rel: ..." has neither. Accept
		// the cut only when the left side looks like a path.
		left := s[:i]
		if strings.ContainsAny(left, "/\\.") {
			s = s[i+2:]
		}
	}
	return s
}

// PrintSkippedSummary writes a human-readable summary of the
// files Build skipped during ingest, grouped by reason. The top
// 5 reasons are listed in descending count; the rest are
// aggregated under "...". When `clean` is true the headline
// emphasises the data-loss risk so the operator notices. Output
// goes to w; callers route it to stderr.
func PrintSkippedSummary(w io.Writer, stats BuildStats, clean bool) {
	if stats.FilesSkipped == 0 || len(stats.SkippedByReason) == 0 {
		return
	}
	type kv struct {
		reason string
		count  int64
	}
	all := make([]kv, 0, len(stats.SkippedByReason))
	for r, c := range stats.SkippedByReason {
		all = append(all, kv{r, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].reason < all[j].reason
	})
	head := all
	if len(head) > 5 {
		head = head[:5]
	}
	if clean {
		fmt.Fprintf(w, "build: --clean skipped %d files. Top reasons:\n", stats.FilesSkipped)
	} else {
		fmt.Fprintf(w, "build: skipped %d files. Top reasons:\n", stats.FilesSkipped)
	}
	for _, e := range head {
		line := e.reason
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		fmt.Fprintf(w, "  %4d  %s\n", e.count, line)
	}
	if len(all) > len(head) {
		fmt.Fprintf(w, "  ...  %d more distinct reasons\n", len(all)-len(head))
	}
}

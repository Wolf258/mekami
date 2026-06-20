package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// PruneStats summarises what pruneDisabledLangs removed. The
// per-language counts are returned to the caller so the build
// can log a single, human-readable line and surface the data
// in BuildStats. The Langs field is the sorted list of language
// identifiers that were actually pruned; the maps are keyed by
// the same identifiers.
type PruneStats struct {
	Langs        []string
	FilesRemoved map[string]int64
	Symbols      map[string]int64
	Refs         map[string]int64
}

// pruneDisabledLangs removes every row in `files` whose lang is
// not in `allowed`. The operation runs inside the supplied
// transaction so the build's Commit / Rollback semantics apply:
// if the build later fails, the rollback also undoes the
// removals, and the DB stays consistent.
//
// `allowed` may be empty; in that case the function is a no-op
// and the caller is expected to skip the cross-language cleanup
// entirely. `allowed` is the set of language identifiers the
// project currently tracks — typically the names listed in
// .mekami/config.json's indexers, possibly extended with the
// frontend selected for the current build (--lang).
func pruneDisabledLangs(ctx context.Context, tx *store.Tx, allowed []string) (*PruneStats, error) {
	if len(allowed) == 0 {
		return nil, nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}

	// First pass: which langs are about to be removed and how
	// much data do they carry? We need the per-language counts
	// for the log line, so we read them before the DELETE.
	rows, err := tx.QueryContext(ctx,
		`SELECT lang, COUNT(*) FROM files WHERE lang IS NOT NULL GROUP BY lang`)
	if err != nil {
		return nil, fmt.Errorf("prune: list langs: %w", err)
	}
	defer rows.Close()

	var toPrune []string
	for rows.Next() {
		var lang string
		var count int64
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("prune: scan lang row: %w", err)
		}
		if !allowedSet[lang] {
			toPrune = append(toPrune, lang)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prune: iter langs: %w", err)
	}
	if len(toPrune) == 0 {
		return nil, nil
	}
	sort.Strings(toPrune)

	out := &PruneStats{
		Langs:        toPrune,
		FilesRemoved: make(map[string]int64, len(toPrune)),
		Symbols:      make(map[string]int64, len(toPrune)),
		Refs:         make(map[string]int64, len(toPrune)),
	}
	for _, lang := range toPrune {
		syms, err := tx.CountSymbolsForLang(lang)
		if err != nil {
			return nil, fmt.Errorf("prune: count symbols for %q: %w", lang, err)
		}
		refs, err := tx.CountRefsForLang(lang)
		if err != nil {
			return nil, fmt.Errorf("prune: count refs for %q: %w", lang, err)
		}
		n, err := tx.RemoveFilesByLang(lang)
		if err != nil {
			return nil, fmt.Errorf("prune: remove %q: %w", lang, err)
		}
		out.FilesRemoved[lang] = n
		out.Symbols[lang] = syms
		out.Refs[lang] = refs
	}
	return out, nil
}

// formatPruneLog renders the one-line summary the build prints
// to stderr when pruneDisabledLangs removed something. The line
// is intentionally human-readable: build scripts that need the
// counts in machine form should read BuildStats.RemovedLangs
// (populated by the caller of pruneDisabledLangs).
func formatPruneLog(p *PruneStats) string {
	if p == nil || len(p.Langs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.Langs))
	for _, lang := range p.Langs {
		parts = append(parts, fmt.Sprintf(
			"%s (%d files, %d symbols, %d refs)",
			lang, p.FilesRemoved[lang], p.Symbols[lang], p.Refs[lang]))
	}
	return "build: removing data for disabled language(s): " + strings.Join(parts, ", ")
}

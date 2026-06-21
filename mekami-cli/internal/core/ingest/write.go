package ingest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// WriteParseResult persists a parsed file's module, package, file
// row, and its symbols/refs using the provided transaction. It must
// be called serially — the underlying tx is not safe for concurrent
// use.
//
// The function is language-agnostic: it consumes a api.ParseResult
// (produced by a Frontend.ParseFile) and writes the generic
// symbols/refs shape. Any frontend that produces a ParseResult
// conforming to the contract in the frontend package can use this
// without changes.
func WriteParseResult(tx *store.Tx, r api.ParseResult) error {
	if err := tx.UpsertModule(r.ModuleID); err != nil {
		return fmt.Errorf("upsert module: %w", err)
	}
	pkgID, err := tx.UpsertPackage(model.Package{
		ModuleID:  r.ModuleID,
		PackageID: r.PackageID,
		Name:      coalescePkgName(r.PackageName, r.RelPath),
		Dir:       r.DirRel,
	})
	if err != nil {
		return fmt.Errorf("upsert package: %w", err)
	}

	fileID, err := tx.UpsertFile(model.File{
		Path:  filepath.ToSlash(r.RelPath),
		Hash:  r.Hash,
		Mtime: r.Mtime,
		Size:  r.Size,
		Lang:  r.Lang,
	})
	if err != nil {
		return fmt.Errorf("upsert file: %w", err)
	}
	if err := tx.DeleteFileContent(fileID); err != nil {
		return fmt.Errorf("delete content: %w", err)
	}

	for i := range r.Symbols {
		r.Symbols[i].FileID = fileID
		r.Symbols[i].PackageID = pkgID
	}

	symIDs := make([]int64, len(r.Symbols))
	// pendingParents records, for every emitted method whose
	// parent type is declared in the same file, the mapping from
	// "real id of the method row" to "real id of the parent row".
	// We resolve these after the first insert pass so the
	// parent_symbol FK can point at a row that already exists.
	type parentLink struct {
		childID int64
		parentID int64
	}
	var pendingParents []parentLink
	for i, s := range r.Symbols {
		s.FileID = fileID
		s.PackageID = pkgID
		// Defer the parent FK: the row the parent points at may
		// not be inserted yet (Go allows forward references to
		// types within a file when the collector encounters the
		// method before the type).
		var parentIdx *int64
		if s.ParentSymbol != nil {
			p := *s.ParentSymbol
			parentIdx = &p
		}
		id, err := tx.InsertSymbol(model.Symbol{
			Kind:          string(s.Kind),
			Name:          s.Name,
			QualifiedName: s.QualifiedName,
			StartLine:     s.StartLine,
			EndLine:       s.EndLine,
			Exported:      s.Exported,
			Signature:     s.Signature,
			FileID:        s.FileID,
			PackageID:     s.PackageID,
		})
		if err != nil {
			return fmt.Errorf("insert symbol %q: %w", s.QualifiedName, err)
		}
		symIDs[i] = id
		if parentIdx != nil {
			pendingParents = append(pendingParents, parentLink{
				childID:  id,
				parentID: symIDs[*parentIdx],
			})
		}
	}
	for _, p := range pendingParents {
		if err := tx.SetSymbolParent(p.childID, p.parentID); err != nil {
			return fmt.Errorf("link parent %d -> child %d: %w", p.parentID, p.childID, err)
		}
	}

	// Refs come out of the frontend with FromSymbol set to the 0-based
	// index of the source symbol within r.Symbols. We translate it
	// to a real symbol id before persisting.
	for _, ref := range r.Refs {
		idx := int(ref.FromSymbol)
		if idx < 0 || idx >= len(symIDs) {
			// The frontend should never produce an out-of-range
			// FromSymbol; if it does, dropping the ref would silently
			// thin the graph. Surface it so the next person to debug
			// the index has a breadcrumb.
			ingestWarning("%s: dropping ref to %q at line %d: FromSymbol %d out of range (syms=%d)",
				r.RelPath, ref.ToQualified, ref.Line, ref.FromSymbol, len(symIDs))
			continue
		}
		fromID := symIDs[idx]
		if fromID == 0 {
			continue
		}
		if err := tx.InsertRef(model.Ref{
			FromSymbol:  fromID,
			ToQualified: ref.ToQualified,
			Kind:        string(ref.Kind),
			Line:        ref.Line,
		}); err != nil {
			return fmt.Errorf("insert ref %q: %w", ref.ToQualified, err)
		}
	}

	return nil
}

// coalescePkgName returns the package name to stamp into the
// packages.name column. Frontends that resolve the declared
// package name from the source (Go: `package foo`) provide it
// in api.ParseResult.PackageName; this is the value of record
// because it reflects what the source actually says, which can
// differ from the directory basename (e.g. `package main` in
// `cmd/mekami/...`). When the frontend does not resolve it,
// the function falls back to the directory basename so the
// packages table stays populated for languages that do not
// surface a name through the contract.
func coalescePkgName(declared, relPath string) string {
	if declared != "" {
		return declared
	}
	return packageNameFromPath(relPath)
}

// packageNameFromPath returns a sensible default `name` column value
// for the packages table. It is the cross-language fallback used
// when a frontend does not provide a declared package name through
// api.ParseResult.PackageName (Go: `package foo`; Python: __init__
// package; Rust: crate name; etc. all surface it through the same
// field).
func packageNameFromPath(relPath string) string {
	if relPath == "" {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "." || dir == "/" {
		return ""
	}
	parts := []rune{}
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' {
			break
		}
		parts = append([]rune{rune(dir[i])}, parts...)
	}
	return string(parts)
}

// ingestWarning is emitted to stderr when the writer drops a ref
// because its FromSymbol is out of range. A real ref-loss is a bug;
// surfacing the message at least gives the user a hint instead of a
// silently partial graph.
func ingestWarning(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mekami: %s\n", fmt.Sprintf(format, args...))
}

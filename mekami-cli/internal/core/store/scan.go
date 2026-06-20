package store

import (
	"database/sql"

	"github.com/Wolf258/mekami-cli/internal/core/model"
)

// SymbolWithFileSelect is the canonical column list for the
// (symbols JOIN files) subquery used by SearchSymbols,
// SymbolByQName, FileOutline, PackageOutline, RefsTo and the
// path reconstructor. Centralizing it keeps the row->struct
// mapping in ScanSymbolWithFile in sync with the actual SELECT.
// Exported so sibling packages (queries, path) can issue the
// same SELECT shape.
const SymbolWithFileSelect = `s.id,s.file_id,s.package_id,s.kind,s.name,s.qualified_name,
		       s.start_line,s.end_line,s.exported,COALESCE(s.signature,''),s.parent_symbol,f.path`

// ScanSymbolWithFile decodes a row produced by SymbolWithFileSelect.
// It handles the exported-int-to-bool and the nullable parent_symbol
// mapping once, so callers don't repeat the same dance.
func ScanSymbolWithFile(rows *sql.Rows) (model.SymbolWithFile, error) {
	var swf model.SymbolWithFile
	var exported int
	var parent sql.NullInt64
	var fpath string
	if err := rows.Scan(&swf.ID, &swf.FileID, &swf.PackageID, &swf.Kind, &swf.Name, &swf.QualifiedName,
		&swf.StartLine, &swf.EndLine, &exported, &swf.Signature, &parent, &fpath); err != nil {
		return model.SymbolWithFile{}, err
	}
	swf.Exported = exported != 0
	swf.FilePath = fpath
	if parent.Valid {
		v := parent.Int64
		swf.ParentSymbol = &v
	}
	return swf, nil
}

// ScanRefSite decodes a row that has the SymbolWithFileSelect
// columns followed by r.line and r.kind, in a single Scan.
// Caller is responsible for setting rs.ToQName (which is not
// stored in the row).
func ScanRefSite(rows *sql.Rows) (model.RefSite, error) {
	var rs model.RefSite
	var exported int
	var parent sql.NullInt64
	if err := rows.Scan(&rs.FromSymbol.ID, &rs.FromSymbol.FileID, &rs.FromSymbol.PackageID,
		&rs.FromSymbol.Kind, &rs.FromSymbol.Name, &rs.FromSymbol.QualifiedName,
		&rs.FromSymbol.StartLine, &rs.FromSymbol.EndLine, &exported,
		&rs.FromSymbol.Signature, &parent, &rs.FromSymbol.FilePath,
		&rs.Line, &rs.Kind); err != nil {
		return model.RefSite{}, err
	}
	rs.FromSymbol.Exported = exported != 0
	if parent.Valid {
		v := parent.Int64
		rs.FromSymbol.ParentSymbol = &v
	}
	return rs, nil
}

// ScanRefFromSymbolAt decodes one row of the batched query in
// path.Reconstruct. The query SELECTs (r.to_qualified,
// MIN(r.line), r.kind, `+SymbolWithFileSelect+`). The three
// leading scalars are written through the pointer args; the
// trailing symbolWithFileSelect block is decoded into a
// SymbolWithFile.
func ScanRefFromSymbolAt(rows *sql.Rows, tqn *string, line *int, kind *string) (model.SymbolWithFile, error) {
	var swf model.SymbolWithFile
	var exported int
	var parent sql.NullInt64
	if err := rows.Scan(tqn, line, kind,
		&swf.ID, &swf.FileID, &swf.PackageID,
		&swf.Kind, &swf.Name, &swf.QualifiedName,
		&swf.StartLine, &swf.EndLine, &exported,
		&swf.Signature, &parent, &swf.FilePath); err != nil {
		return model.SymbolWithFile{}, err
	}
	swf.Exported = exported != 0
	if parent.Valid {
		v := parent.Int64
		swf.ParentSymbol = &v
	}
	return swf, nil
}

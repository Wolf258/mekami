// Package model holds the data types stored in the SQLite graph
// and the DTOs returned to callers. The struct shapes are
// language-agnostic: every language indexer writes the same fields.
package model

// File is one indexed source file. ID is assigned by the store on
// insert; Path is the slash-form relative path inside the build
// root.
type File struct {
	ID    int64
	Path  string
	Hash  string
	Mtime int64
	Size  int64
	Lang  string
}

// Package identifies a sub-package inside a module. ModuleID is
// the module path (Go) / project name (Python) / crate name
// (Rust); PackageID is the fully qualified intra-module address
// (Go: import path; Python: dotted module path; Rust:
// crate::module).
type Package struct {
	ID        int64  `json:"-"`
	ModuleID  string
	PackageID string
	Name      string
	Dir       string
}

// Symbol is a single declaration in a file. The Kind field is one
// of the api.SymbolKind constants in string form (e.g. "func",
// "method", "type"); the constants live in api/v1 because they
// are part of the public indexer contract.
type Symbol struct {
	ID            int64  `json:"-"`
	FileID        int64  `json:"-"`
	PackageID     int64  `json:"-"`
	Kind          string
	Name          string
	QualifiedName string
	StartLine     int
	EndLine       int
	Exported      bool
	Signature     string
	ParentSymbol  *int64 `json:"-"`
}

// Ref is a single reference edge. Kind is one of the api.RefKind
// constants in string form.
type Ref struct {
	ID          int64
	FromSymbol  int64
	ToQualified string
	Kind        string
	Line        int
}

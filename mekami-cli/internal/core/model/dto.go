package model

// SymbolWithFile is a Symbol joined with the file path it lives in.
// It is the canonical "this is a definition" row returned by the
// read-side queries.
type SymbolWithFile struct {
	Symbol
	FilePath string
}

// RefSite is a Ref enriched with the source symbol's metadata and
// the file path. Returned by who-calls and used by trace_calls to
// render the call path.
type RefSite struct {
	FromSymbol SymbolWithFile
	ToQName    string
	Kind       string
	Line       int
}

// FileNode is one node in the file-tree returned by list_files.
// Type is "file" or "dir". Children is nil for files.
type FileNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Type     string      `json:"type"`
	Size     int64       `json:"size,omitempty"`
	Lang     string      `json:"lang,omitempty"`
	Children []*FileNode `json:"children,omitempty"`
}

// SourceLine is one line of a file as returned by show_lines /
// show_body. Line is 1-based.
type SourceLine struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// PackageSummary is the per-package rollup returned by
// show_modules.
type PackageSummary struct {
	ModuleID  string `json:"module_id"`
	PackageID string `json:"package_id"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Files     int    `json:"files"`
	Symbols   int    `json:"symbols"`
	Exported  int    `json:"exported"`
}

// ModuleSummary is one module's worth of PackageSummary entries.
type ModuleSummary struct {
	ModuleID string           `json:"module_id"`
	Dir      string           `json:"dir"`
	Packages []PackageSummary `json:"packages"`
}

// FileDiff is the result of diff.SinceLastBuild. Added / Modified /
// Removed are read from the filesystem-vs-DB comparison;
// Inaccessible holds files that disappeared or errored.
type FileDiff struct {
	Added        []string `json:"added"`
	Modified     []string `json:"modified"`
	Removed      []string `json:"removed"`
	Inaccessible []string `json:"inaccessible,omitempty"`
}

// ModuleInfo describes a module in the indexed graph.
type ModuleInfo struct {
	Path        string `json:"path"`
	Dir         string `json:"dir"` // relative to last_root, or "" for single-module
	IsWorkspace bool   `json:"is_workspace"`
	Primary     bool   `json:"primary"` // true for the primary module of a workspace
}

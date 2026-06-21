# Frontend API

The `github.com/Wolf258/mekami-api/api/v1` package is the public contract every language indexer implements. The package only depends on the Go standard library.

This page lists every exported type and function in the package. For the contract guarantees and the data shapes, see the [Frontend contract](../extending/frontend-contract.md) page.

## Types

### `Workspace`

```go
type Workspace struct {
    IsWorkspace      bool
    WorkFile         string
    WorkspaceDir     string
    WorkspaceMods    []string
    PrimaryModPath   string
    PrimaryModuleDir string
}
```

### `FileMeta`

```go
type FileMeta struct {
    ModuleID  string
    PackageID string
    DirRel    string
}
```

### `ModuleInfo`

```go
type ModuleInfo struct {
    Dir      string
    ModuleID string
}
```

### `ParseResult`

```go
type ParseResult struct {
    RelPath    string
    Lang       string
    ModuleID   string
    PackageID  string
    DirRel     string
    Hash       string
    Mtime      int64
    Size       int64
    Symbols    []Symbol
    Refs       []Ref
}
```

### `Symbol`

```go
type Symbol struct {
    ID            int64
    FileID        int64
    PackageID     int64
    Kind          SymbolKind
    Name          string
    QualifiedName string
    StartLine     int
    EndLine       int
    Exported      bool
    Signature     string
    ParentSymbol  *int64
}
```

### `Ref`

```go
type Ref struct {
    ID          int64
    FromSymbol  int64
    ToQualified string
    Kind        RefKind
    Line        int
}
```

### `ModuleEntry`

```go
type ModuleEntry struct {
    Dir  string `json:"dir"`
    Path string `json:"path"`
}
```

### `Frontend` (interface)

```go
type Frontend interface {
    Name() string
    Extensions() []string
    ResolveLayout(root string) (*Workspace, error)
    ResolveModules(root string) ([]ModuleInfo, error)
    RootModule(root string) (string, error)
    ResolveFile(root, absPath string) (FileMeta, error)
    ParseFile(root, relPath, absPath string, hash string, mtime, size int64) (ParseResult, error)
    StructuralFiles() []string
    IsIndexable(relPath string) bool
}
```

See the [Frontend contract](../extending/frontend-contract.md) page for the full method-by-method spec.

## Constants

### `SymbolKind`

```go
const (
    KindFunc      SymbolKind = "func"
    KindMethod    SymbolKind = "method"
    KindType      SymbolKind = "type"
    KindVar       SymbolKind = "var"
    KindConst     SymbolKind = "const"
    KindImports   SymbolKind = "imports"
    KindFuncLit   SymbolKind = "funclit"
)
```

### `RefKind`

```go
const (
    RefCall    RefKind = "call"
    RefTypeUse RefKind = "type-use"
    RefImport  RefKind = "import"
    RefValue   RefKind = "value"
)
```

## Registry

### `Registry` and `Global`

```go
var Global = NewRegistry()

func NewRegistry() *Registry
func Register(f Frontend)            // shorthand for Global.Register
func Get(name string) (Frontend, error)  // shorthand for Global.Get
func Names() []string                // shorthand for Global.Names
func All() []Frontend                // shorthand for Global.All
```

### `Registry` methods

```go
func (r *Registry) Register(f Frontend)
func (r *Registry) Get(name string) (Frontend, error)
func (r *Registry) Names() []string
func (r *Registry) All() []Frontend
func (r *Registry) IsStructural(rel string) bool
func (r *Registry) DefaultStructuralFiles() []string
```

### Package-level helpers

```go
func IsStructural(rel string) bool
func DefaultStructuralFiles() []string
```

`Register` panics on duplicate names so a typo in one frontend is caught at startup.

## Source

The package source lives in the `mekami-api` module: [`api.go`](https://github.com/Wolf258/mekami-api/blob/main/api/v1/api.go).

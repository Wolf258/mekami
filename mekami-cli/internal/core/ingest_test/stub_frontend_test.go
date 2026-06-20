//go:build !integration

package ingest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"

	"github.com/Wolf258/mekami-api/api/v1"
)

// stubFrontend is a minimal Go frontend used by the ingest_test
// suite when the build tag `integration` is NOT set. It exercises
// the api.Frontend contract end-to-end against real Go files but
// only extracts the package name and the names of top-level
// declarations (funcs, types, vars, consts). It is intentionally
// simpler than mekami-core-go: there are no imports, no refs, no
// call edges, no type-use resolution.
//
// Tests that assert on richer graph content (cross-file refs,
// imports, call paths) live in the integration_test/ directory
// and require the `integration` build tag plus mekami-core-go as
// a test dependency.
type stubFrontend struct{}

func (stubFrontend) Name() string         { return "go" }
func (stubFrontend) Extensions() []string { return []string{".go"} }
func (stubFrontend) StructuralFiles() []string {
	return []string{"go.mod", "go.sum", "go.work"}
}
func (stubFrontend) IsIndexable(rel string) bool {
	return !strings.HasSuffix(rel, "_test.go")
}
func (stubFrontend) ResolveLayout(root string) (*api.Workspace, error) {
	return &api.Workspace{}, nil
}
func (stubFrontend) RootModule(root string) (string, error) {
	return "stub", nil
}
func (stubFrontend) ResolveModules(root string) ([]api.ModuleInfo, error) {
	return []api.ModuleInfo{{Dir: root, ModuleID: "stub"}}, nil
}
func (stubFrontend) ResolveFile(root, abs string) (api.FileMeta, error) {
	dir := strings.TrimPrefix(abs, root)
	dir = strings.TrimPrefix(dir, "/")
	dir = strings.TrimSuffix(dir, ".go")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[:i]
	} else {
		dir = ""
	}
	return api.FileMeta{
		ModuleID:  "stub",
		PackageID: "stub/" + dir,
		DirRel:    dir,
	}, nil
}

func (stubFrontend) ParseFile(root, relPath, absPath, hash string, mtime, size int64) (api.ParseResult, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return api.ParseResult{}, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, data, parser.ParseComments)
	if err != nil {
		return api.ParseResult{}, err
	}
	meta, _ := stubFrontend{}.ResolveFile(root, absPath)
	res := api.ParseResult{
		RelPath:   relPath,
		Lang:      "go",
		ModuleID:  meta.ModuleID,
		PackageID: meta.PackageID,
		DirRel:    meta.DirRel,
		Hash:      hash,
		Mtime:     mtime,
		Size:      size,
		Symbols:   extractTopLevel(file, fset, file.Name.Name),
	}
	return res, nil
}

func extractTopLevel(file *ast.File, fset *token.FileSet, pkgName string) []api.Symbol {
	var out []api.Symbol
	for _, d := range file.Decls {
		pos := fset.Position(d.Pos())
		end := fset.Position(d.End())
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				out = append(out, api.Symbol{
					Kind:          api.KindFunc,
					Name:          decl.Name.Name,
					QualifiedName: pkgName + "." + decl.Name.Name,
					StartLine:     pos.Line,
					EndLine:       end.Line,
					Exported:      ast.IsExported(decl.Name.Name),
				})
			} else {
				recv := receiverName(decl.Recv)
				out = append(out, api.Symbol{
					Kind:          api.KindMethod,
					Name:          decl.Name.Name,
					QualifiedName: pkgName + "." + recv + "." + decl.Name.Name,
					StartLine:     pos.Line,
					EndLine:       end.Line,
					Exported:      ast.IsExported(decl.Name.Name),
				})
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out = append(out, api.Symbol{
						Kind:          api.KindType,
						Name:          s.Name.Name,
						QualifiedName: pkgName + "." + s.Name.Name,
						StartLine:     pos.Line,
						EndLine:       end.Line,
						Exported:      ast.IsExported(s.Name.Name),
					})
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out = append(out, api.Symbol{
							Kind:          kindFromToken(decl.Tok),
							Name:          n.Name,
							QualifiedName: pkgName + "." + n.Name,
							StartLine:     pos.Line,
							EndLine:       end.Line,
							Exported:      ast.IsExported(n.Name),
						})
					}
				}
			}
		}
	}
	return out
}

func kindFromToken(tok token.Token) api.SymbolKind {
	switch tok {
	case token.CONST:
		return api.KindConst
	}
	return api.KindVar
}

func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

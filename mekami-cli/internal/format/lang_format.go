// Package format exposes a small per-language extension point so a
// future indexer (Rust, Python, TypeScript) can override how its
// symbols, references, and packages are rendered. Today only the Go
// formatter is registered, but the dispatch via Lang is live: any
// symbol carrying Lang=="go" is rendered with GoFormatter, and any
// other (or empty) Lang falls back to the Go defaults until a
// language-specific formatter is added.
package format

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/model"
)

// LangFormatter is the per-language extension point. The default
// implementations in this file render the Go conventions; indexers
// for other languages can implement this interface and register
// themselves at init time.
//
// The contract is intentionally narrow: each method takes the
// already-fetched data and returns a single rendered line (no
// trailing newline). The format package wraps the per-line calls
// in the right list/header/footer so the formatter only has to
// care about "what does one row look like".
type LangFormatter interface {
	// Name returns the language identifier (matches api.Frontend.Name).
	Name() string

	// FormatSymbol renders a single symbol definition line, e.g.
	//   "func coreinstall.NewResolver() *Resolver  resolver.go:19"
	// for Go, or
	//   "pub fn new_resolver() -> Resolver  resolver.rs:19"
	// for Rust (hypothetical). The line must NOT include a trailing
	// newline; the caller appends one.
	FormatSymbol(s model.SymbolWithFile) string

	// FormatRef renders a single reference site (the call site of
	// some symbol), e.g. for Go:
	//   "coreinstall.NewResolver  cmd/mekami/root.go:42  [call]"
	FormatRef(r model.RefSite) string

	// FormatPackage renders a single package row, e.g. for Go:
	//   "github.com/Wolf258/mekami-cli/internal/format"
	// The lang argument is the language the package belongs to
	// (derived from its module's files). model.Package itself
	// does not carry a Lang field because the packages table has
	// no per-package language column; the caller joins it.
	FormatPackage(p model.Package, lang string) string
}

// Registry holds the formatters known to the running binary. The
// production binary registers Go at init; tests can register a
// stub to verify dispatch.
type Registry struct {
	byName map[string]LangFormatter
}

func newRegistry() *Registry { return &Registry{byName: map[string]LangFormatter{}} }

var globalRegistry = newRegistry()

// Register adds a formatter. Duplicate names panic so a typo in a
// formatter init is caught at startup, matching the
// api.Registry.Register convention.
func Register(f LangFormatter) {
	if f == nil {
		panic("format.Register: nil formatter")
	}
	if f.Name() == "" {
		panic("format.Register: empty Name()")
	}
	if _, exists := globalRegistry.byName[f.Name()]; exists {
		panic(fmt.Sprintf("format.Register: duplicate formatter %q", f.Name()))
	}
	globalRegistry.byName[f.Name()] = f
}

// formatterFor returns the formatter registered under lang, or
// the default (Go) formatter if no match is found. The empty
// string also resolves to the default so symbols that did not
// carry a Lang (e.g. legacy rows) still render.
func formatterFor(lang string) LangFormatter {
	if f, ok := globalRegistry.byName[lang]; ok {
		return f
	}
	if f, ok := globalRegistry.byName[defaultLang]; ok {
		return f
	}
	// Last-resort fallback: a brand-new GoFormatter built on the fly.
	// This keeps the package working even if Register was never
	// called (e.g. in a test that doesn't blank-import format).
	return GoFormatter{}
}

const defaultLang = "go"

// ─── GoFormatter ────────────────────────────────────────────────

// GoFormatter is the default. It implements the Go conventions:
// qname first, signature inlined, file:line, kind tag, and
// "exported" inferred from the case of the leading character
// (so the field is omitted from the output to save bytes — the
// caller can re-derive it).
type GoFormatter struct{}

// Name implements LangFormatter.
func (GoFormatter) Name() string { return "go" }

// FormatSymbol renders a Go symbol definition as:
//
//	<qualified_name>  [<kind>]  <file>:<line>  <signature>
//
// Example:
//
//	coreinstall.NewResolver  [func]  resolver.go:19  func NewResolver() *Resolver
//
// The Exported field is intentionally omitted: in Go, a symbol is
// exported iff its name starts with an upper-case letter, which
// the LLM can already see in the qname.
func (GoFormatter) FormatSymbol(s model.SymbolWithFile) string {
	var b strings.Builder
	b.WriteString(s.QualifiedName)
	b.WriteString("  [")
	b.WriteString(s.Kind)
	b.WriteString("]")
	if s.StartLine == s.EndLine {
		fmt.Fprintf(&b, "  %s:%d", s.FilePath, s.StartLine)
	} else {
		fmt.Fprintf(&b, "  %s:%d-%d", s.FilePath, s.StartLine, s.EndLine)
	}
	if s.Signature != "" && s.Signature != " " {
		b.WriteString("  ")
		b.WriteString(s.Signature)
	}
	return b.String()
}

// FormatRef renders a Go reference site as:
//
//	<from_qname>  <file>:<line>  [<ref_kind>]  <to_qname>
//
// Example:
//
//	cmd/mekami/root.go:42  [call]  cobra.Command
func (GoFormatter) FormatRef(r model.RefSite) string {
	var b strings.Builder
	b.WriteString(r.FromSymbol.QualifiedName)
	fmt.Fprintf(&b, "  %s:%d", r.FromSymbol.FilePath, r.Line)
	b.WriteString("  [")
	b.WriteString(r.Kind)
	b.WriteString("]")
	b.WriteString("  ")
	b.WriteString(r.ToQName)
	return b.String()
}

// FormatPackage renders a Go package as its canonical import path.
// The lang argument is ignored for Go — Go's package identity IS
// the import path, so there is no per-language variation.
func (GoFormatter) FormatPackage(p model.Package, lang string) string {
	return p.PackageID
}

// ─── Tree formatter (language-agnostic, but lives here) ───────

// FileTreeText renders a *model.FileNode tree in the same shape
// as the `tree(1)` command: top-down, with box-drawing
// connectors and indentation. Directories get a trailing "/".
// The output has no header or footer; callers compose it with
// their own.
//
// The traversal is iterative to avoid recursion limits on deep
// trees. The sort is stable: directories come before files at
// each level (so the LLM sees the structure before the leaves).
func FileTreeText(root *model.FileNode) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *model.FileNode, prefix, connector string, last bool)
	walk = func(n *model.FileNode, prefix, connector string, last bool) {
		if n != root {
			b.WriteString(prefix)
			b.WriteString(connector)
			b.WriteString(n.Name)
			if n.Type == "dir" {
				b.WriteString("/")
			}
			b.WriteByte('\n')
		}
		if n.Type != "dir" {
			return
		}
		// Sort children: directories first, then files, both
		// alphabetically. We do not mutate the input slice; we
		// operate on a copy.
		kids := append([]*model.FileNode(nil), n.Children...)
		sort.SliceStable(kids, func(i, j int) bool {
			if kids[i].Type != kids[j].Type {
				return kids[i].Type == "dir"
			}
			return kids[i].Name < kids[j].Name
		})
		for i, c := range kids {
			isLast := i == len(kids)-1
			next := prefix
			if n != root {
				if last {
					next += "    "
				} else {
					next += "│   "
				}
			}
			conn := "├── "
			if isLast {
				conn = "└── "
			}
			walk(c, next, conn, isLast)
		}
	}
	// Root: print the name without indentation and without a
	// connector (so the first line is a clean "src/" or ".")
	if root.Type == "dir" {
		b.WriteString(root.Name)
		b.WriteString("/\n")
	} else {
		b.WriteString(root.Name)
		b.WriteByte('\n')
	}
	walk(root, "", "", true)
	return b.String()
}

// ─── init ──────────────────────────────────────────────────────

func init() {
	Register(GoFormatter{})
}

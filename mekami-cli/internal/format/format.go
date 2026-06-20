package format

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-core/model"
)

// JSON encodes v as an indented JSON string. If v is already a string
// (typical for human-readable formatters like format.Symbol), it is
// returned verbatim. Any encoding error is returned as a string
// instead of an error so callers can pass the result to wire formats
// (CLI stdout, MCP TextContent) without losing the payload.
func JSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("format.JSON: marshal failed: %v", err)
	}
	return string(out)
}

func exportMark(s model.SymbolWithFile) string {
	if s.Exported {
		return " exported"
	}
	return ""
}

func symLine(s model.SymbolWithFile) string {
	sig := s.Signature
	if sig != "" {
		sig = "  " + sig
	}
	return fmt.Sprintf("  %4d: %-30s  [%-6s]%s%s",
		s.StartLine, s.QualifiedName, s.Kind, exportMark(s), sig)
}

// FileOutline: list of a file's symbols ordered by line.
func FileOutline(syms []model.SymbolWithFile) string {
	if len(syms) == 0 {
		return "(no symbols)"
	}
	byPath := map[string][]model.SymbolWithFile{}
	order := []string{}
	for _, s := range syms {
		if _, ok := byPath[s.FilePath]; !ok {
			order = append(order, s.FilePath)
		}
		byPath[s.FilePath] = append(byPath[s.FilePath], s)
	}
	sort.Strings(order)
	var b strings.Builder
	for _, p := range order {
		fmt.Fprintf(&b, "%s\n", p)
		for _, s := range byPath[p] {
			b.WriteString(symLine(s))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// PackageOutline: same shape as FileOutline, with a package header.
func PackageOutline(importPath string, syms []model.SymbolWithFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s  (%d symbols)\n", importPath, len(syms))
	b.WriteString(FileOutline(syms))
	return b.String()
}

// RefsTo: formats incoming references (callers / uses).
func RefsTo(target string, refs []model.RefSite) string {
	if len(refs) == 0 {
		return fmt.Sprintf("no references to %q", target)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "references to %q  (%d sites)\n", target, len(refs))
	for _, r := range refs {
		fmt.Fprintf(&b, "  %s  %s:%d  [%s]\n",
			r.FromSymbol.QualifiedName, r.FromSymbol.FilePath, r.Line, r.Kind)
	}
	return b.String()
}

// RefsFrom: formats outgoing references (callees).
func RefsFrom(source string, qnames []string) string {
	if len(qnames) == 0 {
		return fmt.Sprintf("%q has no outgoing references", source)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "outgoing references from %q  (%d)\n", source, len(qnames))
	for _, q := range qnames {
		fmt.Fprintf(&b, "  %s\n", q)
	}
	return b.String()
}

// ModuleOverview: compact table per module and package.
func ModuleOverview(mods []model.ModuleSummary) string {
	if len(mods) == 0 {
		return "(no modules)"
	}
	var b strings.Builder
	b.WriteString("module overview\n")
	for _, m := range mods {
		fmt.Fprintf(&b, "\n%s", m.ModuleID)
		if m.Dir != "" {
			fmt.Fprintf(&b, "  (dir=%s)", m.Dir)
		}
		b.WriteString("\n")
		if len(m.Packages) == 0 {
			b.WriteString("  (no packages)\n")
			continue
		}
		for _, p := range m.Packages {
			fmt.Fprintf(&b, "  %-50s  files=%-3d  syms=%-4d  exported=%d\n",
				p.PackageID, p.Files, p.Symbols, p.Exported)
		}
	}
	return b.String()
}

// Symbol: formats the definition of a symbol.
func Symbol(syms []model.SymbolWithFile) string {
	var b strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&b, "%s  [%s]%s\n", s.QualifiedName, s.Kind, exportMark(s))
		fmt.Fprintf(&b, "  %s:%d-%d\n", s.FilePath, s.StartLine, s.EndLine)
		if s.Signature != "" {
			fmt.Fprintf(&b, "  signature: %s\n", s.Signature)
		}
	}
	return b.String()
}

// SymbolBody: header + numbered source lines.
func SymbolBody(sym model.SymbolWithFile, lines []model.SourceLine, maxLines int) string {
	exp := ""
	if sym.Exported {
		exp = " exported"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d-%d  [%s]%s\n", sym.FilePath, sym.StartLine, sym.EndLine, sym.Kind, exp)
	if sym.Signature != "" {
		fmt.Fprintf(&b, "  signature: %s\n", sym.Signature)
	}
	maxLine := sym.EndLine
	if maxLines > 0 && sym.EndLine-sym.StartLine+1 > maxLines {
		maxLine = sym.StartLine + maxLines - 1
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "  %4d: %s\n", l.Line, l.Content)
	}
	if maxLine < sym.EndLine {
		fmt.Fprintf(&b, "  ... truncated at line %d (max_lines=%d); symbol ends at line %d\n",
			maxLine, maxLines, sym.EndLine)
	}
	return b.String()
}

// FileRange: numbered lines with path:start-end header. No signature is
// included because the range is arbitrary (it may cross symbols).
func FileRange(path string, startLine, endLine int, lines []model.SourceLine, maxLines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d-%d\n", path, startLine, endLine)
	maxLine := endLine
	if maxLines > 0 && len(lines) > maxLines {
		maxLine = startLine + maxLines - 1
	}
	for _, l := range lines {
		fmt.Fprintf(&b, "  %4d: %s\n", l.Line, l.Content)
	}
	if maxLine < endLine {
		fmt.Fprintf(&b, "  ... truncated at line %d (max_lines=%d); range ends at line %d\n",
			maxLine, maxLines, endLine)
	}
	return b.String()
}

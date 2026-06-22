package format

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/model"
)

// maxChangesPathsPerBucket is the per-bucket path cap for
// TextChanges when the diff is long. The CLI default (--head 30)
// already constrains the union; this constant is the per-bucket
// slice for the human-readable rendering.
const maxChangesPathsPerBucket = 10


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

// Cap is the truncation metadata emitted alongside any list-shaped
// formatter when the result was longer than the visible cap. The
// fields are populated only when there is something to report; the
// JSON tag omitempty keeps short responses identical to the
// pre-cap shape.
type Cap struct {
	// Total is the number of items the underlying query produced
	// before the cap was applied. When Truncated is false, Total
	// equals Shown.
	Total int `json:"total,omitempty"`
	// Shown is the number of items actually included in the output.
	Shown int `json:"shown,omitempty"`
	// Truncated is true when Shown < Total. Consumers can use it as
	// a fast-path "was the cap hit" check.
	Truncated bool `json:"truncated,omitempty"`
	// Hint is a one-line suggestion telling the caller how to
	// re-narrow the query (e.g. "use --ref-kind=call or
	// --path-prefix=<subdir>"). Empty when the result was not
	// truncated.
	Hint string `json:"hint,omitempty"`
}

// ListKind is a small enum of "what kind of list is this" so the
// header/footer copy can mention the right noun without each
// formatter having to hardcode it.
type ListKind string

const (
	KindRefs     ListKind = "references"
	KindSymbols  ListKind = "symbols"
	KindFiles    ListKind = "files"
	KindModules  ListKind = "modules"
	KindPackages ListKind = "packages"
	KindImporters ListKind = "importers"
	KindChanges  ListKind = "changes"
	KindSites    ListKind = "sites"
	KindOutgoing ListKind = "outgoing references"
	KindCycles   ListKind = "cycles"
	KindDependents ListKind = "dependents"
)

// headerNoun returns the singular/plural noun used in the header
// "N noun found" line.
func headerNoun(k ListKind) string {
	switch k {
	case KindRefs:
		return "reference"
	case KindSymbols:
		return "symbol"
	case KindFiles:
		return "file"
	case KindModules:
		return "module"
	case KindPackages:
		return "package"
	case KindImporters:
		return "importer"
	case KindChanges:
		return "change"
	case KindSites:
		return "site"
	case KindOutgoing:
		return "outgoing reference"
	case KindCycles:
		return "cycle"
	case KindDependents:
		return "dependent"
	}
	return "item"
}

// HintFor returns the user-facing hint string for a given list kind.
// It is the footer copy printed (and JSON-serialized) when the
// output was truncated. Empty for kinds that do not have a useful
// narrowing suggestion.
func HintFor(k ListKind) string {
	switch k {
	case KindRefs, KindSites:
		return "tip: re-run with --ref-kind=<call|type-use|value|import> or --path-prefix=<subdir> to narrow the result."
	case KindSymbols:
		return "tip: re-run with --kind=<func|type|var|const> or --path-prefix=<subdir> to narrow the result."
	case KindFiles:
		return "tip: re-run with --prefix=<subdir> or --include=<go,md> to narrow the result."
	case KindModules:
		return "tip: this list is exhaustive by design; --head 0 disables the cap."
	case KindPackages:
		return "tip: re-run with --kinds=<func,type> to narrow the symbol set, or pass the canonical package_id."
	case KindImporters:
		return "tip: pass the canonical import path (not the bare last segment) to disambiguate."
	case KindChanges:
		return "tip: re-run `mekami build` to refresh the index, then re-query."
	case KindOutgoing:
		return "tip: re-run with --path-prefix=<subdir> to narrow the result."
	case KindCycles:
		return "tip: cycles are listed in stable order; break the smallest one to make the biggest dent."
	case KindDependents:
		return "tip: re-run with --direction=callees, --level=package, or --ref-kind=call to pivot the view."
	}
	return ""
}

// MaybeHeader returns the "N references found — showing first M of N"
// line when cap.Truncated is true, else "". It is intended to be
// prepended to the formatted list. Pluralization is automatic.
func MaybeHeader(k ListKind, cap Cap) string {
	if !cap.Truncated || cap.Total <= 0 {
		return ""
	}
	noun := headerNoun(k)
	if cap.Total == 1 {
		return fmt.Sprintf("1 %s found.\n", noun)
	}
	return fmt.Sprintf("%d %ss found — showing first %d of %d.\n",
		cap.Total, noun, cap.Shown, cap.Total)
}

// MaybeFooter returns the hint line when cap.Truncated is true, else
// "". Indented with two spaces so it sits under the list without
// looking like another row.
func MaybeFooter(cap Cap) string {
	if !cap.Truncated || cap.Hint == "" {
		return ""
	}
	return "  " + cap.Hint + "\n"
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

// FileOutline: list of a file's symbols ordered by line. When cap
// is truncated, items past Shown are dropped from the output and a
// header/footer is printed. The order of the input slice is
// preserved (caller is expected to sort).
func FileOutline(syms []model.SymbolWithFile, cap Cap) string {
	if len(syms) == 0 {
		return "(no symbols)"
	}
	// Truncate before formatting so the per-file grouping sees a
	// already-sized slice; this keeps the byPath map small.
	items := syms
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	byPath := map[string][]model.SymbolWithFile{}
	order := []string{}
	for _, s := range items {
		if _, ok := byPath[s.FilePath]; !ok {
			order = append(order, s.FilePath)
		}
		byPath[s.FilePath] = append(byPath[s.FilePath], s)
	}
	sort.Strings(order)
	var b strings.Builder
	b.WriteString(MaybeHeader(KindSymbols, cap))
	for _, p := range order {
		fmt.Fprintf(&b, "%s\n", p)
		for _, s := range byPath[p] {
			b.WriteString(symLine(s))
			b.WriteString("\n")
		}
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// PackageOutline: same shape as FileOutline, with a package header.
func PackageOutline(importPath string, syms []model.SymbolWithFile, cap Cap) string {
	items := syms
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindSymbols, cap))
	fmt.Fprintf(&b, "package %s  (%d symbols)\n", importPath, len(items))
	b.WriteString(FileOutline(items, formatZero(cap, len(items))))
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// formatZero returns a Cap with Truncated=false and Total/Shown set
// to n. Used internally to recurse into FileOutline without
// double-counting the header.
func formatZero(in Cap, n int) Cap {
	return Cap{Total: n, Shown: n, Truncated: false, Hint: in.Hint}
}

// RefsTo: formats incoming references (callers / uses). When cap is
// truncated, only the first Shown refs are printed.
func RefsTo(target string, refs []model.RefSite, cap Cap) string {
	items := refs
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	if len(items) == 0 {
		return fmt.Sprintf("no references to %q", target)
	}
	b.WriteString(MaybeHeader(KindRefs, cap))
	fmt.Fprintf(&b, "references to %q  (%d sites)\n", target, len(items))
	for _, r := range items {
		fmt.Fprintf(&b, "  %s  %s:%d  [%s]\n",
			r.FromSymbol.QualifiedName, r.FromSymbol.FilePath, r.Line, r.Kind)
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// RefsFrom: formats outgoing references (callees). When cap is
// truncated, only the first Shown qnames are printed.
func RefsFrom(source string, qnames []string, cap Cap) string {
	items := qnames
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	if len(items) == 0 {
		return fmt.Sprintf("%q has no outgoing references", source)
	}
	b.WriteString(MaybeHeader(KindOutgoing, cap))
	fmt.Fprintf(&b, "outgoing references from %q  (%d)\n", source, len(items))
	for _, q := range items {
		fmt.Fprintf(&b, "  %s\n", q)
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// ModuleOverview: compact table per module and package.
func ModuleOverview(mods []model.ModuleSummary, cap Cap) string {
	if len(mods) == 0 {
		return "(no modules)"
	}
	items := mods
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindModules, cap))
	b.WriteString("module overview\n")
	for _, m := range items {
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
	b.WriteString(MaybeFooter(cap))
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

// ─── Compact text formatters (default CLI output) ────────────────
//
// These produce the byte-minimal line-per-item output that competes
// head-to-head with `rg` / `git grep` / `find`. They are the
// default for every graph-read command on the CLI; the JSON path
// (--json) is reserved for clients that need to parse fields the
// LLM cannot read off a single line.
//
// Each formatter:
//
//   - prepends the same MaybeHeader/MaybeFooter pair the existing
//     RefsTo/FileOutline formatters use, so the truncation contract
//     is identical across modes;
//   - delegates per-row rendering to the LangFormatter registered
//     for the symbol's Lang, so a future Rust/Python indexer can
//     override the row shape without touching the list scaffold;
//   - returns "" or "(none)" / "no matches" for empty input so the
//     LLM can distinguish "query was empty" from "no results".

// SymbolList: compact list of symbol definitions. The order of the
// input slice is preserved (callers are expected to sort by qname
// or line). Truncation drops the tail before formatting, so the
// Shown count always matches the row count.
func SymbolList(syms []model.SymbolWithFile, cap Cap) string {
	if len(syms) == 0 {
		return "no symbols"
	}
	items := syms
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindSymbols, cap))
	for _, s := range items {
		// Pick a per-row formatter. Mixed-language rows in a
		// single list (rare but possible when the project tracks
		// multiple cores) dispatch by Lang.
		b.WriteString(formatterFor(s.Lang).FormatSymbol(s))
		b.WriteByte('\n')
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// RefList: compact list of reference sites (incoming edges). One
// line per site, with the FromSymbol's qname, file:line, and the
// ref kind.
func RefList(target string, refs []model.RefSite, cap Cap) string {
	if len(refs) == 0 {
		return fmt.Sprintf("no references to %q", target)
	}
	items := refs
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindRefs, cap))
	fmt.Fprintf(&b, "references to %q  (%d sites)\n", target, len(items))
	for _, r := range items {
		b.WriteString(formatterFor(r.FromSymbol.Lang).FormatRef(r))
		b.WriteByte('\n')
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// OutgoingList: compact list of outgoing qualified names. Mirrors
// the shape of RefsFrom but uses the LangFormatter convention (one
// qname per line, no extra wrapper). The source is the qname of
// the symbol whose outgoing edges we are listing.
func OutgoingList(source string, qnames []string, cap Cap) string {
	if len(qnames) == 0 {
		return fmt.Sprintf("%q has no outgoing references", source)
	}
	items := qnames
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindOutgoing, cap))
	fmt.Fprintf(&b, "outgoing references from %q  (%d)\n", source, len(items))
	for _, q := range items {
		fmt.Fprintf(&b, "  %s\n", q)
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// PackageList: compact list of package rows. Used by list-importers
// (one row per importer package). The lang argument is the
// language all packages in the list belong to (Go for this
// project); the formatter is free to ignore it. Future
// multi-language projects will pass a per-package lang map.
// An empty lang string resolves to the default formatter.
func PackageList(pkgs []model.Package, lang string, cap Cap) string {
	if len(pkgs) == 0 {
		return "no packages"
	}
	items := pkgs
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindPackages, cap))
	for _, p := range items {
		b.WriteString(formatterFor(lang).FormatPackage(p, lang))
		b.WriteByte('\n')
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// ModuleList: compact list of modules. One line per module, with
// the canonical Path and the on-disk Dir when both are known.
// Distinct from PackageList because modules are not packages; the
// ModuleInfo shape carries no Lang and no qualified path, so the
// formatter is hard-coded.
func ModuleList(mods []model.ModuleInfo, cap Cap) string {
	if len(mods) == 0 {
		return "no modules"
	}
	items := mods
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindModules, cap))
	for _, m := range items {
		if m.Path != "" && m.Dir != "" {
			fmt.Fprintf(&b, "%s  (dir=%s)\n", m.Path, m.Dir)
		} else if m.Path != "" {
			fmt.Fprintf(&b, "%s\n", m.Path)
		} else {
			fmt.Fprintf(&b, "%s\n", m.Dir)
		}
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// IndexSnapshot is the text-friendly mirror of the index status
// payload. The fields are emitted in a stable order so scripts
// that grep for a key keep working across the JSON/--text switch.
// Defined locally to keep `format` from depending on `queries`
// (which already depends on format indirectly).
type IndexSnapshot struct {
	LastRoot    string
	LastBuildAt string
	IsWorkspace bool
	RootModule  string
	Counts      map[string]int64
}

// TextIndexStatus: one-line-per-field rendering of the index
// snapshot. The fields are emitted in a stable order so scripts
// that grep for a key keep working.
func TextIndexStatus(st IndexSnapshot) string {
	var b strings.Builder
	if st.LastRoot != "" {
		fmt.Fprintf(&b, "last_root: %s\n", st.LastRoot)
	}
	if st.LastBuildAt != "" {
		fmt.Fprintf(&b, "last_build_at: %s\n", st.LastBuildAt)
	}
	if st.IsWorkspace {
		fmt.Fprintf(&b, "workspace: yes\n")
	} else {
		fmt.Fprintf(&b, "workspace: no\n")
	}
	if st.RootModule != "" {
		fmt.Fprintf(&b, "root_module: %s\n", st.RootModule)
	}
	for _, k := range []string{"files", "modules", "packages", "symbols", "refs"} {
		if v, ok := st.Counts[k]; ok {
			fmt.Fprintf(&b, "%s: %d\n", k, v)
		}
	}
	return b.String()
}

// TextChanges: compact rendering of a FileDiff as four short
// sections, one per bucket. Each section lists up to
// maxChangesPathsPerBucket paths and reports the rest with
// "and N more" so the LLM can decide whether to re-query with
// --head. The MaybeHeader at the top carries the cap-truncation
// copy and total count, identical to the other list formatters.
//
// When all four buckets are empty the function returns the
// canonical "(no changes)" sentinel so the LLM can distinguish
// "build is fresh" from "no data".
func TextChanges(d model.FileDiff, cap Cap) string {
	if len(d.Added)+len(d.Modified)+len(d.Removed)+len(d.Inaccessible) == 0 {
		return "no changes since last build"
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindChanges, cap))
	writeChangeBucket(&b, "+ added", d.Added)
	writeChangeBucket(&b, "~ modified", d.Modified)
	writeChangeBucket(&b, "- removed", d.Removed)
	writeChangeBucket(&b, "! inaccessible", d.Inaccessible)
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// writeChangeBucket prints a single section header + paths or
// "(none)" when empty. Paths are truncated to the first N
// entries with a trailing "and M more" hint.
func writeChangeBucket(b *strings.Builder, header string, paths []string) {
	if len(paths) == 0 {
		fmt.Fprintf(b, "%s  (none)\n", header)
		return
	}
	fmt.Fprintf(b, "%s  (%d)\n", header, len(paths))
	shown := paths
	if len(shown) > maxChangesPathsPerBucket {
		shown = shown[:maxChangesPathsPerBucket]
	}
	for _, p := range shown {
		fmt.Fprintf(b, "    %s\n", p)
	}
	if rest := len(paths) - len(shown); rest > 0 {
		fmt.Fprintf(b, "    ... and %d more\n", rest)
	}
}

// TextTrace: compact rendering of a call-path between two
// symbols. Each edge is a line "<from> → <to>  (via
// <file>:<line>)" so the LLM can see the step-by-step chain
// at a glance. The path's --head cap (when hit) prepends the
// same MaybeHeader the other list formatters use, and the
// footer hint suggests raising --max-depth.
//
// An empty edge list produces a canonical "no path" message
// so the LLM can distinguish a real path from an empty result.
func TextTrace(edges []model.RefSite, cap Cap) string {
	if len(edges) == 0 {
		return "no path"
	}
	items := edges
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindSites, cap))
	fmt.Fprintf(&b, "call path  (%d edges)\n", len(items))
	for _, e := range items {
		fmt.Fprintf(&b, "  %s → %s  (via %s:%d  [%s])\n",
			e.FromSymbol.QualifiedName, e.ToQName,
			e.FromSymbol.FilePath, e.Line, e.Kind)
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// Cycles renders a list of import cycles, one per line:
//
//	3 cycles detected:
//	  1. A → B → A
//	  2. C → D → E → C
//
// The numbering and "→ A" trailing repeat make the cycle
// visually obvious in plain text. The MaybeHeader/MaybeFooter
// pair keeps the truncation contract consistent with the
// other list formatters.
func Cycles(cycles [][]string, cap Cap) string {
	if len(cycles) == 0 {
		return "no cycles"
	}
	items := cycles
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	var b strings.Builder
	b.WriteString(MaybeHeader(KindCycles, cap))
	fmt.Fprintf(&b, "%d cycle(s) detected\n", len(items))
	for i, c := range items {
		fmt.Fprintf(&b, "  %d. ", i+1)
		for j, pkg := range c {
			if j > 0 {
				b.WriteString(" → ")
			}
			b.WriteString(pkg)
		}
		// Close the cycle visually if the last element is not
		// already equal to the first.
		if len(c) > 0 && c[len(c)-1] != c[0] {
			fmt.Fprintf(&b, " → %s", c[0])
		}
		b.WriteByte('\n')
	}
	b.WriteString(MaybeFooter(cap))
	return b.String()
}

// DependentNode is one node in a DependentTree. It carries the
// resolved display name (a qualified name, package_id, or
// module path) plus an optional list of one-line annotations
// (e.g. the file:line of the call site) so the renderer can
// show context inline. Children are the BFS-discovered
// successors.
type DependentNode struct {
	Name     string
	Detail   string
	Children []*DependentNode
	Depth    int
	// Truncated is set on a node whose subtree was cut off
	// by the BFS cap. The renderer prints a "…" suffix so
	// the LLM sees the boundary.
	Truncated bool
}

// DependentTree renders a dependents tree with two-space
// indentation per depth level. The root line carries a header
// so the LLM knows the BFS parameters that produced the tree:
//
//	dependents of foo.Bar  (symbol, callers, depth=4, nodes=12)
//
// A trailing summary line reports the total node count and
// truncation state. When the BFS was truncated, the matching
// MaybeFooter hint is appended.
func DependentTree(rootLabel string, root *DependentNode, totalNodes int, cap Cap) string {
	if root == nil {
		return "no dependents"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dependents of %s\n", rootLabel)
	var walk func(n *DependentNode, prefix string, last bool)
	walk = func(n *DependentNode, prefix string, last bool) {
		b.WriteString(prefix)
		connector := "├── "
		if last {
			connector = "└── "
		}
		b.WriteString(connector)
		b.WriteString(n.Name)
		if n.Detail != "" {
			fmt.Fprintf(&b, "  %s", n.Detail)
		}
		if n.Truncated {
			b.WriteString("  …")
		}
		b.WriteByte('\n')
		next := prefix
		if last {
			next += "    "
		} else {
			next += "│   "
		}
		for i, c := range n.Children {
			walk(c, next, i == len(n.Children)-1)
		}
	}
	walk(root, "", true)
	fmt.Fprintf(&b, "(%d node(s) total", totalNodes)
	if cap.Truncated {
		fmt.Fprintf(&b, ", truncated: %s", cap.Hint)
	}
	b.WriteString(")\n")
	return b.String()
}

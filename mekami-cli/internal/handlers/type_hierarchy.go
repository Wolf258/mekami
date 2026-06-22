// type_hierarchy.go implements the `type_hierarchy` MCP tool / CLI
// command. It exposes two related views on a Go type:
//
//   --mode=members        Methods and funclits whose parent is
//                          the target type (via parent_symbol FK).
//   --mode=implementers   Types that NAME the target in a type-use
//                          ref. Structural implementers (which
//                          never mention the interface) are NOT
//                          reported — Go interfaces are duck-typed.
//   --mode=all            Both sections in one response.
//
// The handler verifies the target exists and is a type (kind=type)
// before issuing the query. Free funcs and other non-type
// symbols get a friendly "not a type" message.
package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

func typeHierarchy(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	target := args.GetString("type", "")
	if target == "" {
		return AsResult("type is required", nil), nil
	}
	mode := args.GetString("mode", "all")
	switch mode {
	case "members", "implementers", "all":
	default:
		return AsResult(fmt.Sprintf("invalid mode %q; use members|implementers|all", mode), nil), nil
	}

	// Verify the target exists and is a type. We pick the lowest-id
	// row when duplicates exist (defensive — type names should
	// be unique per package).
	syms, err := queries.SymbolByQName(ctx, s, target)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return AsResult(fmt.Sprintf("type %q not found in index — check the qualified name (use find_symbols to look it up)", target), nil), nil
	}
	var targetSym model.SymbolWithFile
	found := false
	for _, s := range syms {
		if s.Kind == "type" {
			targetSym = s
			found = true
			break
		}
	}
	if !found {
		// The symbol exists but is not a type. Tell the user
		// what they hit — useful for "type-hierarchy NewReader"
		// when the user meant the function, not the type.
		kinds := map[string]bool{}
		for _, s := range syms {
			kinds[s.Kind] = true
		}
		kindList := make([]string, 0, len(kinds))
		for k := range kinds {
			kindList = append(kindList, k)
		}
		return AsResult(fmt.Sprintf("%q is not a type (found kind(s): %s)", target, strings.Join(kindList, ", ")), nil), nil
	}

	var members []model.SymbolWithFile
	var implementers []model.SymbolWithFile
	switch mode {
	case "members":
		members, err = queries.TypeMembers(ctx, s, targetSym.QualifiedName)
	case "implementers":
		implementers, err = queries.InterfaceImplementers(ctx, s, targetSym.QualifiedName)
	case "all":
		members, err = queries.TypeMembers(ctx, s, targetSym.QualifiedName)
		if err != nil {
			return nil, err
		}
		implementers, err = queries.InterfaceImplementers(ctx, s, targetSym.QualifiedName)
	}
	if err != nil {
		return nil, err
	}

	// Cap each section independently so neither dominates the
	// output. The total is reported in the header.
	membersCap := capFor(len(members), args, format.KindSymbols)
	implCap := capFor(len(implementers), args, format.KindSymbols)

	text := renderTypeHierarchy(targetSym, mode, members, membersCap, implementers, implCap)

	// Data side: a flat struct so the LLM (or --json) can pull
	// either section without re-parsing the text.
	data := typeHierarchyData{
		Target:       targetSym,
		Members:      members,
		Implementers: implementers,
		Mode:         mode,
	}
	return AsResult(text, data), nil
}

// typeHierarchyData is the JSON shape returned with --json. It
// mirrors the sections rendered by renderTypeHierarchy.
type typeHierarchyData struct {
	Target       model.SymbolWithFile   `json:"target"`
	Mode         string                 `json:"mode"`
	Members      []model.SymbolWithFile `json:"members"`
	Implementers []model.SymbolWithFile `json:"implementers"`
}

func renderTypeHierarchy(target model.SymbolWithFile, mode string, members []model.SymbolWithFile, membersCap format.Cap, implementers []model.SymbolWithFile, implCap format.Cap) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type-hierarchy of %s  [%s]  %s:%d\n",
		target.QualifiedName, target.Kind, target.FilePath, target.StartLine)
	switch mode {
	case "members":
		b.WriteString(renderMembersSection(members, membersCap))
	case "implementers":
		b.WriteString(renderImplementersSection(implementers, implCap))
	case "all":
		b.WriteString(renderMembersSection(members, membersCap))
		b.WriteString("\n")
		b.WriteString(renderImplementersSection(implementers, implCap))
	}
	return b.String()
}

func renderMembersSection(members []model.SymbolWithFile, cap format.Cap) string {
	if len(members) == 0 {
		return "members: (none)\n"
	}
	var b strings.Builder
	// Cap by truncating the slice; reuse the same Cap contract.
	items := members
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	fmt.Fprintf(&b, "members (%d):\n", len(items))
	for _, m := range items {
		fmt.Fprintf(&b, "  %s  [%s]  %s:%d\n",
			m.QualifiedName, m.Kind, m.FilePath, m.StartLine)
	}
	if cap.Truncated {
		fmt.Fprintf(&b, "  ... and %d more\n", len(members)-cap.Shown)
	}
	return b.String()
}

func renderImplementersSection(impls []model.SymbolWithFile, cap format.Cap) string {
	if len(impls) == 0 {
		return "implementers: (none — note: structural implementers are not detected)\n"
	}
	var b strings.Builder
	items := impls
	if cap.Truncated && cap.Shown < len(items) {
		items = items[:cap.Shown]
	}
	fmt.Fprintf(&b, "implementers (%d):\n", len(items))
	for _, m := range items {
		fmt.Fprintf(&b, "  %s  %s:%d\n",
			m.QualifiedName, m.FilePath, m.StartLine)
	}
	if cap.Truncated {
		fmt.Fprintf(&b, "  ... and %d more\n", len(impls)-cap.Shown)
	}
	return b.String()
}

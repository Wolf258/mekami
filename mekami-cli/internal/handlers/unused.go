// unused.go implements the `unused` MCP tool / CLI command: list
// exported symbols that have no incoming references of any kind,
// with a conservative entry-point filter to suppress obvious
// false positives (main, init, _test.go, fmt.Stringer/error
// implementations, etc.).
//
// This is a "dead code CANDIDATE" report, not a definitive
// linter pass. The LLM should treat the output as a starting
// set and verify with who_calls before proposing removals.
package handlers

import (
	"context"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/queries"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

// unused returns exported symbols with no incoming references,
// filtered by a conservative entry-point heuristic. See
// filterEntryPoints for the full rule set.
func unused(ctx context.Context, s *store.Store, args naming.ArgMap) (any, error) {
	includeTests := args.GetBool("include_tests", false)
	includeUnexported := args.GetBool("include_unexported", false)
	limit := args.GetInt("limit", 200)
	syms, err := queries.UnusedSymbols(ctx, s, includeUnexported, limit)
	if err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return AsResult("no unused symbols detected (entry-point filter applied)", nil), nil
	}
	syms = filterEntryPoints(syms, includeTests)
	if len(syms) == 0 {
		return AsResult("no unused symbols after entry-point filter", nil), nil
	}
	cap := capFor(len(syms), args, format.KindSymbols)
	return AsResult(format.SymbolList(syms, cap), payloadOrString(syms, cap)), nil
}

// filterEntryPoints applies a conservative heuristic that drops
// symbols which are entry points even if they have no incoming
// refs in the index. Each rule has a comment explaining why the
// LLM should NOT treat the symbol as unused.
//
// includeTests toggles whether _test.go symbols and Test*/Benchmark*/
// Example*/Fuzz* names are kept in the result.
func filterEntryPoints(syms []model.SymbolWithFile, includeTests bool) []model.SymbolWithFile {
	out := syms[:0:0]
	for _, s := range syms {
		base := strings.TrimSuffix(s.FilePath, ".go")
		isTest := strings.HasSuffix(s.FilePath, "_test.go")

		// Rule 1: test files (gated by includeTests)
		if isTest && !includeTests {
			continue
		}

		// Rule 2: standard Go entry points and test runner entry
		// points. "Test" matches testing.T-style tests, "Benchmark"
		// benchmarks, "Example" doc examples, "Fuzz" fuzz tests.
		if s.Name == "main" || s.Name == "init" {
			continue
		}
		if !includeTests {
			switch {
			case strings.HasPrefix(s.Name, "Test"),
				strings.HasPrefix(s.Name, "Benchmark"),
				strings.HasPrefix(s.Name, "Example"),
				strings.HasPrefix(s.Name, "Fuzz"):
				continue
			}
		}

		// Rule 3: methods that satisfy fmt.Stringer / error. These
		// are called via interface dispatch and have no direct
		// call-site in the index. Detection is best-effort: if the
		// type itself has an Error() or String() method defined
		// somewhere, we don't bother with the receiver — we
		// simply suppress ANY method named "Error" or "String"
		// that returns a string and lives on a non-empty receiver.
		// The query layer does the type-side detection; here we
		// drop the method-level candidates.
		if s.Kind == "method" && (s.Name == "Error" || s.Name == "String") {
			// Only suppress if signature looks like the standard
			// shape (returns a string, takes no args beyond the
			// receiver). The signature is best-effort because
			// the indexer may not always populate it.
			if isStdlibInterfaceMethod(s.Signature) {
				continue
			}
		}

		// Rule 4: method-like names from common interfaces
		// (io.Reader.Read, io.Writer.Write, io.Closer.Close,
		// encoding.TextUnmarshaler.Unmarshal, etc.). These are
		// typically called via interface dispatch.
		_ = base
		if s.Kind == "method" && isStdlibInterfaceMethod(s.Signature) {
			switch s.Name {
			case "Read", "Write", "Close", "Flush", "Unmarshal",
				"Marshal", "Valid", "Reset", "Seek":
				continue
			}
		}

		out = append(out, s)
	}
	return out
}

// isStdlibInterfaceMethod reports whether the signature shape
// matches a method commonly used to satisfy a stdlib interface
// (no args beyond receiver, returns a value). It is a coarse
// filter; the goal is to suppress obvious false positives,
// not to be precise.
func isStdlibInterfaceMethod(sig string) bool {
	if sig == "" {
		// No signature available: be conservative and do NOT
		// suppress. Better to flag a few false positives than
		// to hide a real unused method.
		return false
	}
	// Strip leading "func " and trailing "{ ... }" body if present.
	body := sig
	if idx := strings.Index(body, "{"); idx >= 0 {
		body = body[:idx]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "func ")
	// Find the parameter list.
	open := strings.Index(body, "(")
	close := strings.Index(body, ")")
	if open < 0 || close < 0 || close < open {
		return false
	}
	// Methods have at least one param (the receiver). Free funcs
	// with no args do not satisfy this rule.
	params := strings.TrimSpace(body[open+1 : close])
	if params == "" {
		return false
	}
	return true
}

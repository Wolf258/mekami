package mekami

import (
	"testing"

	"github.com/Wolf258/mekami-cli/internal/naming"
)

// specsWithHead lists the DispatcherKeys of the graph-read specs
// that should expose a --head flag for output capping. The list
// is the source of truth for which commands participate in the
// cap; the test below walks every entry and asserts the flag is
// present and defaulted to "30" so users get a sensible cap
// without having to pass the flag explicitly.
var specsWithHead = []string{
	"who-calls",
	"what-calls",
	"list-file",
	"list-files",
	"list-package",
	"list-importers",
	"list-modules",
	"show-modules",
	"show-changes",
}

func TestSpecsHaveHeadFlag(t *testing.T) {
	for _, key := range specsWithHead {
		spec := naming.LookupByDispatcherKey(key)
		if spec == nil {
			t.Errorf("dispatcher key %q has no spec", key)
			continue
		}
		var found bool
		for _, f := range spec.Flags {
			if f.Name == "head" {
				found = true
				if f.Type != "int" {
					t.Errorf("%s: --head should be int, got %q", key, f.Type)
				}
				if f.Default != "30" {
					t.Errorf("%s: --head default should be 30, got %q", key, f.Default)
				}
				if f.Description == "" {
					t.Errorf("%s: --head description should be set", key)
				}
				break
			}
		}
		if !found {
			t.Errorf("%s: missing --head flag", key)
		}
	}
}

package coreinstall

import (
	"sort"

	"github.com/Wolf258/mekami-api/api/v1"
)

// ListEntry is one row in `mekami core list` and
// `mekami core status`. Loaded is true when the frontend is
// registered in api.Global (i.e. the running binary's blank
// imports included it); Missing is the inverse and means the
// config asks for a language whose blank import is not in the
// current binary.
type ListEntry struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Loaded    bool   `json:"loaded"`
	Missing   bool   `json:"missing"`
	Builtin   bool   `json:"builtin,omitempty"`
}

// ListReport is the result of List(); used by `core list` and
// `core status` to render the table and the JSON form. The
// shape is identical between the two commands — the difference
// is only how the CLI formats the output (status prints an
// extra summary line about missing indexers).
type ListReport struct {
	Indexers []ListEntry   `json:"indexers"`
	Loaded   []string      `json:"loaded"`
	Missing  []string      `json:"missing,omitempty"`
	Builtins []string      `json:"builtins,omitempty"`
}

// List gathers the indexer set requested by the project
// (.mekami/config.json) and the frontends actually loaded in the
// running binary, and joins them. `missing` is true for languages
// listed in the config but not registered in this binary.
func List(cfgIndexers map[string]string) ListReport {
	loaded := api.Global.Names()
	loadedSet := map[string]bool{}
	for _, n := range loaded {
		loadedSet[n] = true
	}
	entries := make([]ListEntry, 0, len(cfgIndexers))
	seen := map[string]bool{}
	for name, version := range cfgIndexers {
		if seen[name] {
			continue
		}
		seen[name] = true
		e := ListEntry{Name: name, Version: version}
		e.Loaded = loadedSet[name]
		e.Missing = !e.Loaded
		entries = append(entries, e)
	}
	// Sort for stable output.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	report := ListReport{Indexers: entries, Loaded: append([]string(nil), loaded...)}
	sort.Strings(report.Loaded)

	// Surface any frontends the binary registered that the user
	// has not added to indexers[] yet, so core-list reports the
	// binary's full frontend set. The shipped CLI registers no
	// frontend by default; this list grows only when the user
	// rebuilds with a populated all_gen.
	report.Builtins = []string{}
	for _, n := range loaded {
		if !seen[n] {
			report.Builtins = append(report.Builtins, n)
		}
	}
	sort.Strings(report.Builtins)

	for _, ix := range entries {
		if ix.Missing {
			report.Missing = append(report.Missing, ix.Name)
		}
	}
	sort.Strings(report.Missing)
	return report
}

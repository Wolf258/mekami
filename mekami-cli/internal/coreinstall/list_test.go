package coreinstall

import (
	"testing"

	"github.com/Wolf258/mekami-api/api/v1"
)

func TestList_EmptyConfigBuiltinGo(t *testing.T) {
	// Save and restore the global registry state so the test is
	// isolated from any other registration that happened in the
	// test binary (none today, but defensive).
	orig := api.Global
	t.Cleanup(func() { api.Global = orig })
	api.Global = api.NewRegistry()
	gof := struct{ Name string }{Name: "go"} // placeholder, see below

	// Register a frontend named "go" through the real API.
	api.Global.Register(testFrontend{name: "go"})

	report := List(nil)
	if len(report.Loaded) != 1 || report.Loaded[0] != "go" {
		t.Errorf("Loaded = %v, want [go]", report.Loaded)
	}
	if len(report.Builtins) != 1 || report.Builtins[0] != "go" {
		t.Errorf("Builtins = %v, want [go]", report.Builtins)
	}
	if len(report.Indexers) != 0 {
		t.Errorf("Indexers = %v, want []", report.Indexers)
	}
	if len(report.Missing) != 0 {
		t.Errorf("Missing = %v, want []", report.Missing)
	}
	_ = gof
}

// testFrontend is a stub implementing api.Frontend for List
// tests. The other methods are not exercised by List.
type testFrontend struct{ name string }

func (t testFrontend) Name() string                                                       { return t.name }
func (t testFrontend) Extensions() []string                                               { return []string{".x"} }
func (t testFrontend) ResolveLayout(string) (*api.Workspace, error)                       { return nil, nil }
func (t testFrontend) ResolveModules(string) ([]api.ModuleInfo, error)                     { return nil, nil }
func (t testFrontend) RootModule(string) (string, error)                                  { return "", nil }
func (t testFrontend) ResolveFile(string, string) (api.FileMeta, error)                   { return api.FileMeta{}, nil }
func (t testFrontend) ParseFile(string, string, string, string, int64, int64) (api.ParseResult, error) {
	return api.ParseResult{}, nil
}
func (t testFrontend) StructuralFiles() []string    { return nil }
func (t testFrontend) IsIndexable(string) bool      { return true }

package main

import (
	"strings"
	"testing"
)

func TestRender_Empty(t *testing.T) {
	got := render(nil)
	if !strings.Contains(got, "import ()\n") {
		t.Errorf("empty render missing `import ()`:\n%s", got)
	}
	if !strings.Contains(got, "package all_gen") {
		t.Errorf("empty render missing package decl:\n%s", got)
	}
}

func TestRender_SingleCore(t *testing.T) {
	got := render([]string{"github.com/Wolf258/mekami-core-go"})
	if !strings.Contains(got, `_ "github.com/Wolf258/mekami-core-go"`) {
		t.Errorf("single render missing blank import:\n%s", got)
	}
	if strings.Contains(got, "import ()") {
		t.Errorf("single render should not have empty import block:\n%s", got)
	}
}

func TestRender_Sorted(t *testing.T) {
	got := render([]string{"github.com/Wolf258/mekami-core-rust", "github.com/Wolf258/mekami-core-go"})
	rustIdx := strings.Index(got, "mekami-core-rust")
	goIdx := strings.Index(got, "mekami-core-go")
	if rustIdx < 0 || goIdx < 0 {
		t.Fatalf("missing import in output:\n%s", got)
	}
	if rustIdx >= goIdx {
		t.Errorf("expected alphabetical (go before rust), got rust at %d, go at %d:\n%s", rustIdx, goIdx, got)
	}
}

func TestRender_HeaderExplainsDevSplit(t *testing.T) {
	got := render(nil)
	if !strings.Contains(got, "dev-allgen") {
		t.Errorf("header should mention dev-allgen:\n%s", got)
	}
	if !strings.Contains(got, "core-install") {
		t.Errorf("header should mention core-install as the prod path:\n%s", got)
	}
}

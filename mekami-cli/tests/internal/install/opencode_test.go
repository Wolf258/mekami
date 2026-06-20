package install_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/install"
)

func TestOpenCodeConfigPathPrefersXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	oc := &install.OpenCodeClient{}
	got, err := oc.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "opencode", "opencode.json")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestOpenCodeConfigPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	oc := &install.OpenCodeClient{}
	got, err := oc.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "opencode", "opencode.json")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestOpenCodeConfigPathRespectsExistingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create an alt layout; the lookup should still pick the
	// canonical opencode.json first.
	canonical := filepath.Join(tmp, "opencode", "opencode.json")
	if err := os.WriteFile(canonical, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	oc := &install.OpenCodeClient{}
	got, err := oc.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != canonical {
		t.Fatalf("expected canonical %s, got %s", canonical, got)
	}
}

func TestOpenCodeName(t *testing.T) {
	if (&install.OpenCodeClient{}).Name() != "mekami" {
		t.Fatal("expected default name 'mekami'")
	}
	if (&install.OpenCodeClient{ServerName: "code"}).Name() != "code" {
		t.Fatal("expected override to win")
	}
}

func TestOpenCodeEntryRejectsMissingBinary(t *testing.T) {
	// Point PATH at an empty temp dir so 'mekami' cannot be found.
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)
	oc := &install.OpenCodeClient{}
	if _, err := oc.Entry(); err == nil {
		t.Fatal("expected error when 'mekami' is not on PATH")
	}
}

func TestOpenCodeEntryUsesAbsolutePath(t *testing.T) {
	oc := &install.OpenCodeClient{BinaryPath: "/abs/path/mekami"}
	entry, err := oc.Entry()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Command[0] != "/abs/path/mekami" {
		t.Fatalf("expected command[0]=/abs/path/mekami, got %v", entry.Command[0])
	}
}

func TestOpenCodeEntryHonoursDisable(t *testing.T) {
	off := false
	oc := &install.OpenCodeClient{BinaryPath: "/abs/path/mekami", Enabled: &off}
	entry, err := oc.Entry()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Enabled == nil || *entry.Enabled {
		t.Fatal("expected enabled=false when client Enabled is set to false")
	}
}

func TestOpenCodeEntryEnvironment(t *testing.T) {
	oc := &install.OpenCodeClient{
		BinaryPath:  "/abs/path/mekami",
		Environment: map[string]string{"FOO": "bar"},
	}
	entry, err := oc.Entry()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Environment["FOO"] != "bar" {
		t.Fatalf("expected env FOO=bar, got %v", entry.Environment)
	}
}

func TestRegisterOpenCodeEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfg := filepath.Join(tmp, "opencode", "opencode.json")

	oc := &install.OpenCodeClient{BinaryPath: "/abs/path/mekami"}
	entry, err := oc.Entry()
	if err != nil {
		t.Fatal(err)
	}
	res, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       oc.Name(),
		Entry:      entry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true on first register")
	}
	data, _ := os.ReadFile(cfg)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	mcp := got["mcp"].(map[string]any)
	entry2 := mcp["mekami"].(map[string]any)
	cmd := entry2["command"].([]any)
	if cmd[0] != "/abs/path/mekami" {
		t.Fatalf("expected command[0]=/abs/path/mekami, got %v", cmd[0])
	}
}

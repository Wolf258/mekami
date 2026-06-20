package install_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/install"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterCreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")

	res, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"mekami", "serve"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath != "" {
		t.Fatal("expected no backup when the source file does not exist")
	}
	if !res.Changed || res.Existed {
		t.Fatalf("expected first write Changed=true Existed=false, got %+v", res)
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	mcp, _ := got["mcp"].(map[string]any)
	if mcp == nil {
		t.Fatalf("expected mcp map, got %v", got)
	}
	entry, _ := mcp["mekami"].(map[string]any)
	if entry == nil {
		t.Fatalf("expected mcp.mekami, got %v", mcp)
	}
	if entry["type"] != "local" {
		t.Fatalf("expected type=local, got %v", entry["type"])
	}
	cmd, _ := entry["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "mekami" || cmd[1] != "serve" {
		t.Fatalf("expected command=[mekami serve], got %v", cmd)
	}
}

func TestRegisterIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	entry := install.MCPConfig{
		Type:    "local",
		Command: []string{"mekami", "serve"},
	}
	first, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg, Name: "mekami", Entry: entry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("first register should report Changed=true")
	}
	second, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg, Name: "mekami", Entry: entry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existed {
		t.Fatal("second register should report Existed=true")
	}
	if second.Changed {
		t.Fatal("second register should be a no-op (Changed=false)")
	}
}

func TestRegisterPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	writeFile(t, cfg, `{
		"$schema": "https://opencode.ai/config.json",
		"autoupdate": false,
		"mcp": {
			"gh_grep": {
				"type": "remote",
				"url": "https://mcp.grep.app",
				"enabled": true
			}
		}
	}`)

	if _, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"mekami", "serve"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["autoupdate"] != false {
		t.Fatal("expected autoupdate to survive")
	}
	mcp := got["mcp"].(map[string]any)
	if _, ok := mcp["gh_grep"]; !ok {
		t.Fatal("expected gh_grep to survive")
	}
	if _, ok := mcp["mekami"]; !ok {
		t.Fatal("expected mekami to be added")
	}
}

func TestRegisterBacksUp(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	writeFile(t, cfg, `{"mcp":{}}`)

	res, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"mekami", "serve"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath == "" {
		t.Fatal("expected backup path")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Fatalf("expected backup file to exist: %v", err)
	}
}

func TestRegisterUpdatesEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	if _, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"old", "serve"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"new", "serve"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Existed {
		t.Fatalf("expected update Changed=true Existed=true, got %+v", res)
	}
	data, _ := os.ReadFile(cfg)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	entry := got["mcp"].(map[string]any)["mekami"].(map[string]any)
	cmd := entry["command"].([]any)
	if cmd[0] != "new" {
		t.Fatalf("expected command[0]=new, got %v", cmd[0])
	}
}

func TestUnregisterRemovesEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	if _, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
		Entry: install.MCPConfig{
			Type:    "local",
			Command: []string{"mekami", "serve"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := install.Unregister(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Existed {
		t.Fatalf("expected removal Changed=true Existed=true, got %+v", res)
	}
	data, _ := os.ReadFile(cfg)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	if m, ok := got["mcp"].(map[string]any); ok {
		if _, present := m["mekami"]; present {
			t.Fatal("expected mekami to be gone")
		}
	}
}

func TestUnregisterMissingEntryIsNoop(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	writeFile(t, cfg, `{"mcp":{"other":{"type":"remote","url":"x"}}}`)

	res, err := install.Unregister(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Existed || res.Changed {
		t.Fatalf("expected no-op, got %+v", res)
	}
}

func TestUnregisterMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "does-not-exist.json")
	if _, err := install.Unregister(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
	}); err != nil {
		t.Fatalf("expected no error when file is missing, got %v", err)
	}
}

func TestUnregisterPreservesSiblings(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	writeFile(t, cfg, `{
		"mcp": {
			"gh_grep": {"type": "remote", "url": "https://mcp.grep.app"},
			"mekami":  {"type": "local", "command": ["mekami", "serve"]}
		}
	}`)
	if _, err := install.Unregister(install.RegisterOptions{
		ConfigPath: cfg,
		Name:       "mekami",
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	var got map[string]any
	_ = json.Unmarshal(data, &got)
	mcp := got["mcp"].(map[string]any)
	if _, ok := mcp["gh_grep"]; !ok {
		t.Fatal("expected gh_grep to survive")
	}
	if _, ok := mcp["mekami"]; ok {
		t.Fatal("expected mekami to be gone")
	}
}

func TestRegisterEmptyEntryRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	if _, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg, Name: "mekami",
	}); err == nil {
		t.Fatal("expected error for empty entry")
	}
}

func TestRegisterEmptyNameRejected(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "opencode.json")
	if _, err := install.Register(install.RegisterOptions{
		ConfigPath: cfg,
		Entry: install.MCPConfig{
			Type: "local", Command: []string{"x"},
		},
	}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

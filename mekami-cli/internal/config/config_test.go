package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Version != 1 {
		t.Fatalf("default version: got %d, want 1", c.Version)
	}
	if !c.Watch.Enabled {
		t.Fatalf("default watch.enabled should be true")
	}
	if c.Watch.DebounceMs != 250 {
		t.Fatalf("default debounce_ms: got %d, want 250", c.Watch.DebounceMs)
	}
	if len(c.Watch.Ignore) == 0 {
		t.Fatalf("default ignore list should be non-empty")
	}
	if c.Watch.OnStart != "build" {
		t.Fatalf("default on_start: got %q, want build", c.Watch.OnStart)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	def := Default()
	if cfg.Watch.DebounceMs != def.Watch.DebounceMs {
		t.Fatalf("missing file should yield defaults, got debounce %d", cfg.Watch.DebounceMs)
	}
}

func TestLoad_Partial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch":{"debounce_ms":500}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load partial: %v", err)
	}
	if cfg.Watch.DebounceMs != 500 {
		t.Fatalf("debounce_ms override: got %d, want 500", cfg.Watch.DebounceMs)
	}
	if !cfg.Watch.Enabled {
		t.Fatalf("partial should not clobber enabled=true default")
	}
	if len(cfg.Watch.Ignore) == 0 {
		t.Fatalf("partial should not clobber default ignore list")
	}
}

func TestLoad_UnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"watch":{"debounce_ms":500,"mystery":42}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("unknown field should produce an error")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("error should mention offending field, got: %v", err)
	}
}

func TestLoad_BadVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("unsupported version should error")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"ok", func(c *Config) {}, ""},
		{"debounce negative", func(c *Config) { c.Watch.DebounceMs = -1 }, "debounce_ms"},
		{"debounce huge", func(c *Config) { c.Watch.DebounceMs = 999999 }, "debounce_ms"},
		{"on_start junk", func(c *Config) { c.Watch.OnStart = "explode" }, "on_start"},
		{"log junk", func(c *Config) { c.Watch.Log = "spam" }, "log"},
		{"empty ignore", func(c *Config) { c.Watch.Ignore = []string{""} }, "ignore"},
		{"jobs negative", func(c *Config) { c.Build.Jobs = -2 }, "jobs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			err := c.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestOnStartAction(t *testing.T) {
	cases := map[string]OnStartAction{
		"":            OnStartBuild,
		"build":       OnStartBuild,
		"skip":        OnStartSkip,
		"incremental": OnStartIncremental,
		"garbage":     OnStartBuild,
	}
	for s, want := range cases {
		if got := (WatchConfig{OnStart: s}).OnStartAction(); got != want {
			t.Fatalf("OnStart %q: got %v, want %v", s, got, want)
		}
	}
}

func TestShouldLog(t *testing.T) {
	cases := []struct {
		log   string
		level string
		want  bool
	}{
		{"", "info", true},
		{"", "debug", false},
		{"info", "info", true},
		{"info", "debug", false},
		{"debug", "info", true},
		{"debug", "debug", true},
		{"quiet", "info", false},
		{"quiet", "error", false},
	}
	for _, tc := range cases {
		got := (WatchConfig{Log: tc.log}).ShouldLog(tc.level)
		if got != tc.want {
			t.Fatalf("log=%q level=%q: got %v, want %v", tc.log, tc.level, got, tc.want)
		}
	}
}

func TestIgnoreListReplaces(t *testing.T) {
	c := Default()
	override := WatchConfig{Ignore: []string{"foo"}}
	got := mergeWatch(c.Watch, override)
	if len(got.Ignore) != 1 || got.Ignore[0] != "foo" {
		t.Fatalf("override should replace ignore list, got %v", got.Ignore)
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{
  "version": 1,
  "watch": {
    "enabled": true,
    "debounce_ms": 300,
    "ignore": ["*.tmp", "vendor/"],
    "on_start": "skip",
    "log": "debug"
  },
  "build": {
    "jobs": 4,
    "force_root": true
  }
}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.DebounceMs != 300 {
		t.Fatalf("debounce: got %d, want 300", cfg.Watch.DebounceMs)
	}
	if cfg.Watch.OnStartAction() != OnStartSkip {
		t.Fatalf("on_start: got %v, want skip", cfg.Watch.OnStartAction())
	}
	if cfg.Build.Jobs != 4 || !cfg.Build.ForceRoot {
		t.Fatalf("build overrides not applied: %+v", cfg.Build)
	}
}

func TestIndexers_Valid(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr string
	}{
		{
			name: "no indexers",
			raw:  `{"version":1}`,
			want: nil,
		},
		{
			name: "empty object",
			raw:  `{"version":1,"indexers":{}}`,
			want: map[string]string{},
		},
		{
			name: "single",
			raw:  `{"version":1,"indexers":{"go":"v0.1.0"}}`,
			want: map[string]string{"go": "v0.1.0"},
		},
		{
			name: "empty version",
			raw:  `{"version":1,"indexers":{"rust":""}}`,
			want: map[string]string{"rust": ""},
		},
		{
			name: "multi",
			raw:  `{"version":1,"indexers":{"go":"","rust":"0.3"}}`,
			want: map[string]string{"go": "", "rust": "0.3"},
		},
		{
			name:    "empty name",
			raw:     `{"version":1,"indexers":{"":"v1"}}`,
			wantErr: "must not have empty key",
		},
		{
			name:    "bad chars",
			raw:     `{"version":1,"indexers":{"Go Lang":""}}`,
			wantErr: "must match",
		},
		{
			name:    "bad version",
			raw:     `{"version":1,"indexers":{"go":"banana"}}`,
			wantErr: "must be like vX.Y.Z",
		},
		{
			name:    "value not a string",
			raw:     `{"version":1,"indexers":{"go":42}}`,
			wantErr: "value must be a string",
		},
		{
			name:    "shape is an array (old form)",
			raw:     `{"version":1,"indexers":[{"name":"go"}]}`,
			wantErr: "must be a map",
		},
		{
			name:    "unknown top-level alongside indexers",
			raw:     `{"version":1,"indexers":{},"bogus":1}`,
			wantErr: "unknown top-level field",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.wantErr != "" {
				// Two error sources: parse-time (shape, unknown
				// fields, malformed JSON) and validate-time
				// (semantic rules). The expected substring is
				// searched in both.
				var got error
				if err != nil {
					got = err
				} else {
					got = cfg.Validate()
				}
				if got == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(got.Error(), tc.wantErr) {
					t.Fatalf("error: got %q, want substring %q", got.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(cfg.Indexers, tc.want) {
				t.Fatalf("indexers: got %+v, want %+v", cfg.Indexers, tc.want)
			}
			if vErr := cfg.Validate(); vErr != nil {
				t.Fatalf("validate: %v", vErr)
			}
		})
	}
}

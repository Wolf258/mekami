package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Config is the on-disk shape of .mekami/config.json. The schema is
// versioned: a future major bump adds a new top-level key. Unknown
// top-level keys are rejected at parse time so the user gets a clear
// "unknown field" error rather than a silently ignored entry.
type Config struct {
	Version int `json:"version"`
	// Indexers is the set of language cores the project tracks,
	// keyed by language name. The value is the semver version
	// `core-install` resolved for that language; an empty string
	// means the language was added by `mekami init` but no
	// `core-install` has run for it yet. The build's
	// cross-language cleanup uses these keys to decide which
	// `files.lang` rows to keep in the graph DB.
	Indexers map[string]string `json:"indexers,omitempty"`
	Watch    WatchConfig       `json:"watch,omitempty"`
	Build    BuildConfig       `json:"build,omitempty"`
}

// WatchConfig controls the `mekami watch` subcommand.
type WatchConfig struct {
	// Enabled is informational: it lets a user disable the watcher
	// from config while keeping the config file around. The CLI flag
	// `mekami watch` is the actual gate that launches the loop.
	Enabled bool `json:"enabled"`
	// DebounceMs is the quiet-window for coalescing file events. The
	// watcher waits this long after the last event in a batch before
	// firing a rebuild. Default 250.
	DebounceMs int `json:"debounce_ms"`
	// Ignore is a list of glob patterns (relative to the watched root)
	// that the watcher should drop. On top of these, the watcher
	// always excludes the same directories as the build walker
	// (.git, .mekami, vendor, node_modules, _dev).
	Ignore []string `json:"ignore"`
	// OnStart is what the watcher should do once before entering the
	// event loop: "build" (full Build), "skip" (assume DB is fresh),
	// or "incremental" (re-ingest the current set of files). Default
	// "build". Unknown values fall back to "build" with a warning.
	OnStart string `json:"on_start"`
	// Log controls per-event log verbosity: "info" (default) prints
	// one line per batch, "debug" prints per-file events, "quiet"
	// suppresses everything except errors.
	Log string `json:"log"`
	// Fallback selects the event source. Values:
	//   - "auto" (default): fsnotify, poller if FS is detected as
	//     unreliable (NFS, SMB, FUSE);
	//   - "fsnotify": always fsnotify;
	//   - "poll": always the poller.
	Fallback string `json:"fallback"`
	// PollIntervalS is the poller's tick interval in seconds. Only
	// relevant when Fallback="poll" or the auto-detect picked the
	// poller. Default 30. 0 means "use default".
	PollIntervalS int `json:"poll_interval_s"`
	// LogLevel controls the daemon's log file verbosity. Values:
	//   - "resumen" (default): one line per batch and on errors;
	//   - "verbose": per-event logging.
	// This is independent of `Log` because `Log` is for the
	// foreground CLI, while `LogLevel` is for the persisted
	// daemon log.
	LogLevel string `json:"log_level"`
	// SelfTerminateOnOrphan is the maximum time the daemon
	// will run standalone after losing contact with its
	// supervisor. The daemon pings the supervisor PID every
	// HeartbeatInterval; if the supervisor stays unreachable
	// for this duration, the daemon sends itself SIGTERM and
	// shuts down. The string is parsed by time.ParseDuration,
	// so values like "10m", "1h", "30s" are accepted. The
	// empty string (default) means "never self-terminate":
	// the daemon keeps running standalone and the user gets
	// a chance to investigate. This is the right default for
	// developers who want the watcher to keep the index
	// fresh even when no supervisor is around (e.g. session
	// logout, supervisor crash). Set it explicitly only when
	// you have a strong reason to clean up.
	SelfTerminateOnOrphan string `json:"self_terminate_on_orphan,omitempty"`
}

// BuildConfig lets a user set build-time defaults from config so the
// CLI flags don't have to be repeated. The CLI flags still take
// precedence; see MergeBuild.
type BuildConfig struct {
	Jobs      int  `json:"jobs"`
	ForceRoot bool `json:"force_root"`
}

// DefaultWatch returns a WatchConfig with sensible defaults.
func DefaultWatch() WatchConfig {
	return WatchConfig{
		Enabled:       true,
		DebounceMs:    250,
		Ignore:        []string{"*.tmp", "*.swp", ".DS_Store"},
		OnStart:       "build",
		Log:           "info",
		Fallback:      "auto",
		PollIntervalS: 30,
		LogLevel:      "resumen",
		// SelfTerminateOnOrphan: empty (never). The
		// daemon's own comment on the field explains why
		// we keep the safe default.
		SelfTerminateOnOrphan: "",
	}
}

// DefaultBuild returns a BuildConfig with build-time defaults.
func DefaultBuild() BuildConfig {
	return BuildConfig{
		Jobs:      0,
		ForceRoot: false,
	}
}

// Default returns a fully populated Config with all defaults applied.
// It is the base used by Load when the file is missing.
func Default() Config {
	return Config{
		Version:  1,
		Indexers: nil,
		Watch:    DefaultWatch(),
		Build:    DefaultBuild(),
	}
}

// DefaultPath returns the canonical config path (./.mekami/config.json).
// Exposed so the CLI can echo it in --help and error messages.
func DefaultPath() string { return filepath.Join(".mekami", "config.json") }

// Save writes c to path with stable JSON formatting (two-space
// indent, trailing newline). If path is empty, DefaultPath() is
// used. The function creates parent directories as needed.
//
// Save does not Validate; callers are expected to have done so
// already. The output is deterministic for a given Config so
// successive `init` runs do not produce spurious diffs.
func Save(c Config, path string) error {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Load reads path and returns the merged config. Behaviour:
//   - file missing: returns Default() with no error (config is opt-in).
//   - file present but invalid: returns an error and the raw bytes,
//     so callers can show a useful diagnostic.
//   - file present and valid: defaults are applied for any field the
//     user left unset, so partial files are valid.
func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parse(data)
}

func parse(data []byte) (Config, error) {
	// First pass: detect unknown top-level fields. RawMessage would
	// silently absorb them, so we Decode into a generic map first
	// and check the keys explicitly.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	allowed := map[string]bool{
		"version":  true,
		"indexers": true,
		"watch":    true,
		"build":    true,
	}
	for k := range top {
		if !allowed[k] {
			return Config{}, fmt.Errorf("config: unknown top-level field %q", k)
		}
	}
	// Watch subfields: detect unknowns the same way.
	if raw, ok := top["watch"]; ok && string(raw) != "null" {
		if err := validateWatchKeys(raw); err != nil {
			return Config{}, err
		}
	}
	if raw, ok := top["build"]; ok && string(raw) != "null" {
		if err := validateBuildKeys(raw); err != nil {
			return Config{}, err
		}
	}
	if raw, ok := top["indexers"]; ok && string(raw) != "null" {
		if err := validateIndexersShape(raw); err != nil {
			return Config{}, err
		}
	}
	var versioned struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &versioned); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	cfg := Default()
	if versioned.Version != 0 {
		if versioned.Version != 1 {
			return Config{}, fmt.Errorf("config: unsupported version %d (expected 1)", versioned.Version)
		}
		cfg.Version = versioned.Version
	}
	if raw, ok := top["watch"]; ok && string(raw) != "null" {
		var w WatchConfig
		if err := json.Unmarshal(raw, &w); err != nil {
			return Config{}, fmt.Errorf("config.watch: %w", err)
		}
		cfg.Watch = mergeWatch(cfg.Watch, w)
	}
	if raw, ok := top["build"]; ok && string(raw) != "null" {
		var b BuildConfig
		if err := json.Unmarshal(raw, &b); err != nil {
			return Config{}, fmt.Errorf("config.build: %w", err)
		}
		cfg.Build = mergeBuild(cfg.Build, b)
	}
	if raw, ok := top["indexers"]; ok && string(raw) != "null" {
		var ix map[string]string
		if err := json.Unmarshal(raw, &ix); err != nil {
			return Config{}, fmt.Errorf("config.indexers: %w", err)
		}
		cfg.Indexers = ix
	}
	return cfg, nil
}

// validateIndexersShape rejects an indexers field whose top-level
// shape is wrong (e.g. an array of objects, which used to be the
// v0.1 form). The keys themselves are validated by Validate
// (regex on the name, semver on the version); this function only
// makes sure the JSON is a map[string]string.
func validateIndexersShape(raw json.RawMessage) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("config.indexers: must be a map of language name to version (e.g. {\"go\": \"v0.1.0\"}): %w", err)
	}
	for k, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return fmt.Errorf("config.indexers[%q]: value must be a string version (use \"\" when no version is registered): %w", k, err)
		}
	}
	return nil
}

func validateWatchKeys(raw json.RawMessage) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("config.watch: %w", err)
	}
	allowed := map[string]bool{
		"enabled":         true,
		"debounce_ms":     true,
		"ignore":          true,
		"on_start":        true,
		"log":             true,
		"fallback":        true,
		"poll_interval_s": true,
		"log_level":       true,
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("config.watch: unknown field %q", k)
		}
	}
	return nil
}

func validateBuildKeys(raw json.RawMessage) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("config.build: %w", err)
	}
	allowed := map[string]bool{
		"jobs":       true,
		"force_root": true,
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("config.build: unknown field %q", k)
		}
	}
	return nil
}

// mergeWatch returns a config where the user-supplied fields replace
// the defaults. The Ignore list is replaced wholesale, not merged, to
// avoid surprising "I removed a pattern but it's still there" cases.
func mergeWatch(base, override WatchConfig) WatchConfig {
	out := base
	if override.Enabled {
		out.Enabled = true
	}
	if override.DebounceMs != 0 {
		out.DebounceMs = override.DebounceMs
	}
	if override.OnStart != "" {
		out.OnStart = override.OnStart
	}
	if override.Log != "" {
		out.Log = override.Log
	}
	if override.Fallback != "" {
		out.Fallback = override.Fallback
	}
	if override.PollIntervalS != 0 {
		out.PollIntervalS = override.PollIntervalS
	}
	if override.LogLevel != "" {
		out.LogLevel = override.LogLevel
	}
	if override.Ignore != nil {
		out.Ignore = append([]string(nil), override.Ignore...)
	}
	return out
}

func mergeBuild(base, override BuildConfig) BuildConfig {
	out := base
	if override.Jobs != 0 {
		out.Jobs = override.Jobs
	}
	if override.ForceRoot {
		out.ForceRoot = true
	}
	return out
}

// Validate returns an error if the config has any inconsistent or
// out-of-range values. It is called by the CLI after Load so the user
// sees a useful message instead of a panic deep in the watcher.
func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("config: unsupported version %d", c.Version)
	}
	if c.Watch.DebounceMs < 0 {
		return fmt.Errorf("config.watch.debounce_ms: must be >= 0, got %d", c.Watch.DebounceMs)
	}
	if c.Watch.DebounceMs > 60000 {
		return fmt.Errorf("config.watch.debounce_ms: must be <= 60000, got %d", c.Watch.DebounceMs)
	}
	switch c.Watch.OnStart {
	case "", "build", "skip", "incremental":
	default:
		return fmt.Errorf("config.watch.on_start: must be one of build|skip|incremental, got %q", c.Watch.OnStart)
	}
	switch c.Watch.Log {
	case "", "info", "debug", "quiet":
	default:
		return fmt.Errorf("config.watch.log: must be one of info|debug|quiet, got %q", c.Watch.Log)
	}
	switch c.Watch.Fallback {
	case "", "auto", "fsnotify", "poll":
	default:
		return fmt.Errorf("config.watch.fallback: must be one of auto|fsnotify|poll, got %q", c.Watch.Fallback)
	}
	if c.Watch.PollIntervalS < 0 {
		return fmt.Errorf("config.watch.poll_interval_s: must be >= 0, got %d", c.Watch.PollIntervalS)
	}
	switch c.Watch.LogLevel {
	case "", "resumen", "verbose":
	default:
		return fmt.Errorf("config.watch.log_level: must be one of resumen|verbose, got %q", c.Watch.LogLevel)
	}
	if c.Build.Jobs < 0 {
		return fmt.Errorf("config.build.jobs: must be >= 0, got %d", c.Build.Jobs)
	}
	for _, p := range c.Watch.Ignore {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("config.watch.ignore: empty pattern")
		}
	}
	// Map iteration order is undefined, so we sort the keys
	// before validating. The errors stay stable enough for tests
	// to assert on substrings.
	names := make([]string, 0, len(c.Indexers))
	for name := range c.Indexers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("config.indexers: must not have empty key")
		}
		if !isValidIndexerName(name) {
			return fmt.Errorf("config.indexers: name must match [a-z0-9_-]+, got %q", name)
		}
		version := c.Indexers[name]
		if version != "" && !isValidIndexerVersion(version) {
			return fmt.Errorf("config.indexers[%q]: version must be like vX.Y.Z, got %q", name, version)
		}
	}
	return nil
}

// isValidIndexerName returns true for lowercase alnum, dash, and
// underscore. The same rule the watcher uses for client / log
// identifiers.
func isValidIndexerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// isValidIndexerVersion accepts the loose semver forms Mekami
// uses in core-install output: "v0.1.0", "0.1.0", "v1".
func isValidIndexerVersion(s string) bool {
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '.' {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
		_ = i
	}
	return true
}

// Debounce returns the debounce window as a time.Duration.
func (w WatchConfig) Debounce() time.Duration {
	if w.DebounceMs <= 0 {
		return 0
	}
	return time.Duration(w.DebounceMs) * time.Millisecond
}

// PollInterval returns the poller tick interval as a time.Duration.
// Falls back to 30s when unset or non-positive.
func (w WatchConfig) PollInterval() time.Duration {
	if w.PollIntervalS <= 0 {
		return 30 * time.Second
	}
	return time.Duration(w.PollIntervalS) * time.Second
}

// OnStartAction maps OnStart strings to constants the watcher uses.
// Unknown values fall back to OnStartBuild.
func (w WatchConfig) OnStartAction() OnStartAction {
	switch w.OnStart {
	case "skip":
		return OnStartSkip
	case "incremental":
		return OnStartIncremental
	default:
		return OnStartBuild
	}
}

// OnStartAction is the resolved form of WatchConfig.OnStart.
type OnStartAction int

const (
	OnStartBuild OnStartAction = iota
	OnStartSkip
	OnStartIncremental
)

// ShouldLog reports whether a given verbosity level should produce
// output. "debug" is the most verbose, "quiet" the least.
func (w WatchConfig) ShouldLog(level string) bool {
	switch w.Log {
	case "quiet":
		return false
	case "debug":
		return true
	case "info":
		return level == "info" || level == "error"
	default:
		return level == "info" || level == "error"
	}
}

// SortedIgnore returns the Ignore list sorted for deterministic
// output (tests, logs).
func (w WatchConfig) SortedIgnore() []string {
	out := append([]string(nil), w.Ignore...)
	sort.Strings(out)
	return out
}

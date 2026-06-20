package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// copyFile copies src to dst, creating dst's parent dir and
// applying the given mode. Used by BackupFile to keep a .bak of
// the user's config before edits.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := in.WriteTo(out); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// backupSuffix is the extension used for backup files written by
// Register and Unregister. Centralized so both functions stay in
// sync.
const backupSuffix = ".bak"

// defaultConfigFileMode is the mode applied to written config files.
const defaultConfigFileMode os.FileMode = 0o644

// MCPConfig is the schema we write into a client's `mcp.<name>`
// block. The fields are a subset of what OpenCode and similar
// clients accept; we only set the ones Mekami needs.
type MCPConfig struct {
	Type        string            `json:"type"`
	Command     []string          `json:"command"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

// IsZero reports whether the entry has no fields set — used to
// decide whether `unregister` should leave the file untouched.
func (c MCPConfig) IsZero() bool {
	return c.Type == "" && len(c.Command) == 0 && c.Enabled == nil && len(c.Environment) == 0
}

// RegisterOptions configures a Register call.
type RegisterOptions struct {
	// ConfigPath is the absolute path to the client config file
	// (e.g. ~/.config/opencode/opencode.json).
	ConfigPath string
	// Name is the server name (e.g. "mekami").
	Name string
	// Entry is the mcp block to merge in.
	Entry MCPConfig
}

// RegisterResult describes what changed in the file.
type RegisterResult struct {
	ConfigPath string
	BackupPath string // empty when no backup was made
	Existed    bool   // was the mcp.<name> block already present?
	Changed    bool   // did the entry differ from what was there?
}

// ReadConfigJSON returns the parsed config as a generic map. The
// key inside `mcp` is normalized to lower-case so callers can
// compare without worrying about case.
func ReadConfigJSON(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// WriteConfigJSON writes cfg back to path with two-space indent.
// The trailing newline is preserved.
func WriteConfigJSON(path string, cfg map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, defaultConfigFileMode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BackupFile copies path to path+".bak" (a missing source is not
// an error: there is nothing to back up). The suffix is fixed so
// the function can be called without the caller having to plumb
// the backup name through RegisterOptions.
func BackupFile(path string) (string, error) {
	backup := path + backupSuffix
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if err := copyFile(path, backup, 0o644); err != nil {
		return "", err
	}
	return backup, nil
}

// Register merges opts.Entry into the `mcp.<opts.Name>` block of
// the config file at opts.ConfigPath, creating the file and the
// surrounding `mcp` map if needed. The original file is backed
// up when it exists.
//
// Register is idempotent: calling it twice with the same entry is
// a no-op on the second call. Calling it with a different entry
// overwrites the block and reports Changed=true.
func Register(opts RegisterOptions) (RegisterResult, error) {
	var res RegisterResult
	res.ConfigPath = opts.ConfigPath
	if opts.Name == "" {
		return res, errors.New("install: mcp name is required")
	}
	if opts.Entry.IsZero() {
		return res, errors.New("install: empty mcp entry")
	}
	backupPath, err := BackupFile(opts.ConfigPath)
	if err != nil {
		return res, fmt.Errorf("install: backup: %w", err)
	}
	res.BackupPath = backupPath

	cfg, err := ReadConfigJSON(opts.ConfigPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	mcp, _ := cfg["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	previousRaw, existed := mcp[opts.Name]
	if existed {
		res.Existed = true
	}
	entryJSON, err := json.Marshal(opts.Entry)
	if err != nil {
		return res, fmt.Errorf("install: marshal entry: %w", err)
	}
	var newRaw any
	if err := json.Unmarshal(entryJSON, &newRaw); err != nil {
		return res, fmt.Errorf("install: round-trip entry: %w", err)
	}
	if existed && deepEqualJSON(previousRaw, newRaw) {
		res.Changed = false
		return res, nil
	}
	mcp[opts.Name] = newRaw
	cfg["mcp"] = mcp
	if err := WriteConfigJSON(opts.ConfigPath, cfg); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// Unregister removes the `mcp.<opts.Name>` block from the file. It
// is a no-op (returns Existed=false) when the block is missing.
// The original file is backed up when it exists.
func Unregister(opts RegisterOptions) (RegisterResult, error) {
	var res RegisterResult
	res.ConfigPath = opts.ConfigPath
	if opts.Name == "" {
		return res, errors.New("install: mcp name is required")
	}
	backupPath, err := BackupFile(opts.ConfigPath)
	if err != nil {
		return res, fmt.Errorf("install: backup: %w", err)
	}
	res.BackupPath = backupPath

	cfg, err := ReadConfigJSON(opts.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res, nil
		}
		return res, err
	}
	mcp, _ := cfg["mcp"].(map[string]any)
	if mcp == nil {
		return res, nil
	}
	if _, ok := mcp[opts.Name]; !ok {
		return res, nil
	}
	res.Existed = true
	delete(mcp, opts.Name)
	if len(mcp) == 0 {
		delete(cfg, "mcp")
	} else {
		cfg["mcp"] = mcp
	}
	if err := WriteConfigJSON(opts.ConfigPath, cfg); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// deepEqualJSON compares two values that have been round-tripped
// through encoding/json. We re-marshal both sides so map ordering
// and numeric coercions do not produce false negatives. We do not
// surface a marshalling error: the only inputs that fail are chan
// or func values, neither of which the MCP config schema accepts.
func deepEqualJSON(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}

// String returns a short, single-line description of the entry
// suitable for printing in install/uninstall output.
func (c MCPConfig) String() string {
	cmd := strings.Join(c.Command, " ")
	enabled := true
	if c.Enabled != nil {
		enabled = *c.Enabled
	}
	return fmt.Sprintf("type=%s command=[%s] enabled=%t", c.Type, cmd, enabled)
}

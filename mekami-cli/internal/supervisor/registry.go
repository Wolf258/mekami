package supervisor

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// StateDir is the per-user directory the supervisor uses for its
// socket, pid file, lock, and daemons.json. Defaults to
// $XDG_CONFIG_HOME/mekami/supervisor (or ~/.config/mekami/supervisor
// if XDG_CONFIG_HOME is unset).
func StateDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "mekami", "supervisor")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mekami", "supervisor")
}

// SocketPath is the canonical Unix socket the supervisor listens
// on. Lives in StateDir() with 0600 perms.
func SocketPath() string {
	return filepath.Join(StateDir(), "supervisor.sock")
}

// RegistryPath is the JSON file the supervisor persists its
// daemon table to.
func RegistryPath() string {
	return filepath.Join(StateDir(), "daemons.json")
}

// EnsureStateDir creates the supervisor state directory with 0700
// perms. The socket lives here, so the directory must not be
// world-accessible.
func EnsureStateDir() error {
	return os.MkdirAll(StateDir(), 0o700)
}

// DaemonState is the persisted description of a single daemon.
// Fields the supervisor tracks at runtime (PID, state, uptime)
// are not persisted: they are re-derived on every supervisor
// start. The hash of the on-disk config.json is persisted so the
// supervisor can detect drift after a manual edit.
type DaemonState struct {
	Root         string `json:"root"`
	Lang         string `json:"lang"`
	DBPath       string `json:"db_path"`
	ConfigHash   string `json:"config_hash"`
	RestartPolicy string `json:"restart_policy"`
	// Watches is the most recent fsnotify watch count reported
	// by the daemon. -1 means "unknown / not yet reported".
	Watches int64 `json:"watches,omitempty"`
	// LastState is the state the supervisor observed on last
	// write. Used to filter the registry when rehydrating after
	// a crash.
	LastState string `json:"last_state,omitempty"`
}

// Registry is the on-disk daemon table. Reads and writes are
// serialised by an external flock acquired at the supervisor
// entry points; the in-memory struct itself is not safe for
// concurrent use.
type Registry struct {
	mu      sync.Mutex
	path    string
	Version int            `json:"version"`
	Daemons []DaemonState  `json:"daemons"`
}

// LoadRegistry reads the registry from disk. A missing file
// returns an empty registry with no error.
func LoadRegistry() (*Registry, error) {
	return LoadRegistryAt(RegistryPath())
}

// LoadRegistryAt reads the registry from a custom path. Used by
// tests.
func LoadRegistryAt(path string) (*Registry, error) {
	r := &Registry{path: path, Version: 1}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return r, nil
}

// Save writes the registry to disk atomically: write to a
// sibling temp file, fsync, rename. The parent directory must
// exist.
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep the slice sorted by root for deterministic output.
	sort.Slice(r.Daemons, func(i, j int) bool {
		return r.Daemons[i].Root < r.Daemons[j].Root
	})
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), "daemons-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename never happened.
		_ = os.Remove(tmpName)
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Find returns the daemon state for root, or nil if absent.
func (r *Registry) Find(root string) *DaemonState {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Daemons {
		if r.Daemons[i].Root == root {
			return &r.Daemons[i]
		}
	}
	return nil
}

// Upsert inserts or replaces the daemon state for root.
func (r *Registry) Upsert(d DaemonState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Daemons {
		if r.Daemons[i].Root == d.Root {
			r.Daemons[i] = d
			return
		}
	}
	r.Daemons = append(r.Daemons, d)
}

// Remove deletes the daemon state for root. Returns true if a
// row was removed.
func (r *Registry) Remove(root string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Daemons {
		if r.Daemons[i].Root != root {
			continue
		}
		r.Daemons = append(r.Daemons[:i], r.Daemons[i+1:]...)
		return true
	}
	return false
}

// Roots returns the list of registered roots. Order is the
// persisted order (sorted by root after Save).
func (r *Registry) Roots() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.Daemons))
	for i, d := range r.Daemons {
		out[i] = d.Root
	}
	return out
}

// HashConfig computes a stable hash of config.json at the given
// path. Returns "" if the file is missing; returns the literal
// error otherwise. The hash is sha1 of the raw file bytes; we
// don't parse, so whitespace changes don't trigger reloads.
func HashConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read config: %w", err)
	}
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:]), nil
}

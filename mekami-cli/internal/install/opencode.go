package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// binaryName is the command name written into the mcp config when
// the user does not pin --binary. Centralized so the literal lives
// in one place; the AUR package installs the binary at this name.
const binaryName = "mekami"

// OpenCodeClient is the MCP client integration for OpenCode. It
// locates the user's opencode.json (respecting $XDG_CONFIG_HOME)
// and registers the mekami server with the name "mekami" by
// default.
type OpenCodeClient struct {
	// ServerName overrides the server name written into the mcp
	// map. Default: "mekami".
	ServerName string
	// BinaryPath overrides the path written to `command[0]`.
	// When empty, the literal string "mekami" is used so the
	// config is portable across machines (the user is expected
	// to have mekami on PATH after running `mekami mcp install`).
	BinaryPath string
	// Args is appended after the binary name. Default: ["serve"].
	Args []string
	// Environment is written into the `environment` block. Useful
	// for setting MEKAMI_BIN, custom --db paths, etc.
	Environment map[string]string
	// Enabled controls the `enabled` field. nil means true.
	Enabled *bool
}

// DefaultName returns the server name written into the config.
func (c *OpenCodeClient) DefaultName() string {
	if c.ServerName != "" {
		return c.ServerName
	}
	return binaryName
}

// Name returns the server name written into the mcp map.
// Implements the Client interface.
func (c *OpenCodeClient) Name() string { return c.DefaultName() }

// Client is the contract every MCP client integration implements.
// The interface is kept narrow on purpose: locating the config
// file and producing the (entry, options) pair needed by
// Register. Anything beyond that lives in the CLI.
type Client interface {
	// ConfigPath returns the absolute path to the JSON config
	// file. The file may not exist yet.
	ConfigPath() (string, error)
	// Entry returns the MCPConfig Mekami should register.
	Entry() (MCPConfig, error)
	// Name returns the server name written into the mcp map.
	Name() string
}

// Compile-time interface check.
var _ Client = (*OpenCodeClient)(nil)

// ConfigPath locates the opencode.json. Order of preference:
//
//  1. $XDG_CONFIG_HOME/opencode/opencode.json
//  2. $XDG_CONFIG_HOME/opencode/config.json  (alt layout seen in
//     older opencode versions)
//  3. ~/.config/opencode/opencode.json
//
// We do not auto-create the file: Register does that when there
// is something to write.
func (c *OpenCodeClient) ConfigPath() (string, error) {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	var base string
	if xdg != "" {
		base = xdg
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("opencode: cannot resolve home: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	candidates := []string{
		filepath.Join(base, "opencode", "opencode.json"),
		filepath.Join(base, "opencode", "config.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Default: write to the canonical opencode.json. The parent
	// directory will be created on write.
	return candidates[0], nil
}

// Entry builds the MCPConfig to register. When BinaryPath is set
// the config is locked to that absolute path; when it is empty we
// use the bare name "mekami" so the entry works on any machine
// where the binary is on PATH.
func (c *OpenCodeClient) Entry() (MCPConfig, error) {
	if c.BinaryPath == "" {
		// Verify the binary is on PATH so the resulting config
		// is at least *probably* runnable. We treat a missing
		// binary as a soft error: the user may have it on PATH
		// in the agent's environment but not in this shell.
		if _, err := exec.LookPath(binaryName); err != nil {
			return MCPConfig{}, fmt.Errorf(
				"opencode: %q is not on PATH; install mekami "+
					"(e.g. 'yay -S mekami-bin') or set BinaryPath explicitly: %w",
				binaryName, err)
		}
	}
	bin := c.BinaryPath
	if bin == "" {
		bin = binaryName
	}
	args := c.Args
	if len(args) == 0 {
		args = []string{"serve"}
	}
	enabled := c.Enabled
	if enabled == nil {
		t := true
		enabled = &t
	}
	entry := MCPConfig{
		Type:        "local",
		Command:     append([]string{bin}, args...),
		Enabled:     enabled,
		Environment: c.Environment,
	}
	return entry, nil
}

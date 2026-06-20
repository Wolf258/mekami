//go:build darwin

package mekami

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/supervisor"
)

func launchAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

const supervisorLaunchLabel = "dev.mekami.supervisor"

func serviceInstall() error {
	dir := launchAgentsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	plistPath := filepath.Join(dir, supervisorLaunchLabel+".plist")
	if err := os.WriteFile(plistPath, []byte(launchdPlist()), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	if err := runLaunchctl("load", "-w", plistPath); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}
	fmt.Fprintf(os.Stderr, "installed: %s\n", plistPath)
	return nil
}

func serviceUninstall() error {
	plistPath := filepath.Join(launchAgentsDir(), supervisorLaunchLabel+".plist")
	// Step 1: ask the supervisor to stop everything
	// cleanly. Same rationale as the Linux path:
	// this is the polite path; the launchctl unload
	// below is the safety net for the case where the
	// supervisor is wedged or not running.
	stopSupervisorAndDaemons()
	// Step 2: unload the launch agent. The -w flag
	// persists the disabled state across reboots.
	_ = runLaunchctl("unload", "-w", plistPath)
	// Step 3: clean up runtime state files. See the
	// Linux serviceUninstall for the rationale.
	cleanupSupervisorRuntimeState()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}
	fmt.Fprintf(os.Stderr, "uninstalled: %s\n", plistPath)
	return nil
}

func launchdPlist() string {
	logDir := supervisor.StateDir()
	out := filepath.Join(logDir, "launchd.out")
	errPath := filepath.Join(logDir, "launchd.err")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>supervise</string>
    <string>_run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, supervisorLaunchLabel, mekamiBinary(), out, errPath)
}

func mekamiBinary() string {
	if self, err := os.Executable(); err == nil {
		return self
	}
	if p, err := exec.LookPath("mekami"); err == nil {
		return p
	}
	return "mekami"
}

func runLaunchctl(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

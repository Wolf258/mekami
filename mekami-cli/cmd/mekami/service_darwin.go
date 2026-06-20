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

// serviceStatusOS reports whether the per-user LaunchAgent
// is registered, enabled (loaded with -w), and active
// (currently running). The plist file's existence is the
// registered check; `launchctl list` parses the supervisor's
// row to derive enabled/active. launchctl does not have
// separate is-enabled / is-active commands, so the two are
// inferred from a single `launchctl list` row.
//
// launchctl list output format per line:
//
//   "<pid-or-->\t<last-exit-status>\t<label>"
//
// where pid is "-" when the agent is not currently running.
// "Disabled" agents still appear in the list with a "-"
// pid; we treat that as registered+enabled+inactive.
func serviceStatusOS() (ServiceStatusReport, error) {
	plistPath := filepath.Join(launchAgentsDir(), supervisorLaunchLabel+".plist")
	report := ServiceStatusReport{UnitPath: plistPath}
	if _, err := os.Stat(plistPath); err == nil {
		report.Registered = true
	} else if !os.IsNotExist(err) {
		return report, fmt.Errorf("stat %s: %w", plistPath, err)
	}
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		// launchctl itself failing is not fatal — the file
		// stat above is the source of truth for
		// Registered. We just skip the enabled/active
		// fields and report a note.
		report.Notes = append(report.Notes, fmt.Sprintf("launchctl list failed: %v", err))
		return report, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		if strings.TrimSpace(fields[2]) != supervisorLaunchLabel {
			continue
		}
		// found the supervisor row. If pid is "-" the agent
		// is registered but not currently running. If pid
		// is a number, it is active.
		report.Enabled = true // loaded agents are considered enabled
		pid := strings.TrimSpace(fields[0])
		if pid != "-" && pid != "" {
			report.Active = true
			report.ActiveState = "pid=" + pid
		} else {
			report.ActiveState = "stopped"
		}
		return report, nil
	}
	// File exists but launchctl has no row — the agent was
	// unloaded (typical state right after `service uninstall`
	// has not yet removed the plist). Note it.
	if report.Registered {
		report.Notes = append(report.Notes, "plist exists but launchctl has no entry; run `launchctl load -w` (or `service install`) to activate")
	}
	return report, nil
}

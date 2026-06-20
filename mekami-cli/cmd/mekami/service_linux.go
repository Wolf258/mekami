//go:build linux

package mekami

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Wolf258/mekami-cli/internal/supervisor"
)

// systemdUserDir is the per-user systemd unit directory.
// We honour $XDG_CONFIG_HOME for the unit file location, falling
// back to the default.
func systemdUserDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

const supervisorUnitName = "mekami-supervisor.service"

func serviceInstall() error {
	unitDir := systemdUserDir()
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", unitDir, err)
	}
	unitPath := filepath.Join(unitDir, supervisorUnitName)
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	body := systemdUnitBody(self)
	if err := os.WriteFile(unitPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	if err := runSystemctl("--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := runSystemctl("--user", "enable", supervisorUnitName); err != nil {
		return fmt.Errorf("enable: %w", err)
	}
	if err := runSystemctl("--user", "start", supervisorUnitName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start the unit now (%v); it will activate on next login\n", err)
	}
	fmt.Fprintf(os.Stderr, "installed: %s\n", unitPath)
	return nil
}

func serviceUninstall() error {
	unitPath := filepath.Join(systemdUserDir(), supervisorUnitName)
	// Step 1: ask the supervisor to stop everything
	// cleanly. We use a fresh client (not the global
	// one) so a non-responsive supervisor does not
	// block the uninstall with a long timeout. A
	// 5-second budget is plenty: quit-all stops
	// each daemon with a brief grace period and
	// returns; if the supervisor is wedged, the
	// disable --now below will catch it.
	stopSupervisorAndDaemons()
	// Step 2: disable the unit. disable --now sends
	// SIGTERM to anything still running, which is
	// the safety net for the case where the IPC
	// call above failed (supervisor not running, or
	// quit-all returned an error). After this, the
	// supervisor process is guaranteed to be on its
	// way out (or already gone).
	_ = runSystemctl("--user", "disable", "--now", supervisorUnitName)
	// Step 3: clean up runtime state files. The
	// supervisor's own defer removes supervisor.pid,
	// and the watchdog's defer removes watchdog.pid,
	// but if either crashed hard the files may be
	// left behind. A stale supervisor.pid would
	// confuse a future supervisor start (different
	// PID, same path). Same for the socket and the
	// sentinel.
	cleanupSupervisorRuntimeState()
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit: %w", err)
	}
	_ = runSystemctl("--user", "daemon-reload")
	fmt.Fprintf(os.Stderr, "uninstalled: %s\n", unitPath)
	return nil
}

func systemdUnitBody(self string) string {
	return fmt.Sprintf(`[Unit]
Description=Mekami supervisor (per-user)
After=default.target

[Service]
Type=simple
ExecStart=%s supervise _run
# _MEKAMI_SUPERVISOR=1 is the marker the
# supervisor checks in runSupervisorMain to
# refuse to start when invoked outside the
# supervisor's own re-exec path. Without this
# env var, the binary prints a usage error and
# exits 1, and systemd keeps restarting it in a
# tight loop. The same env var is set by
# startSupervisorProcess on the manual path,
# so the systemd path needs to set it too for
# parity.
Environment=_MEKAMI_SUPERVISOR=1
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=default.target
`, self)
}

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

var _ = supervisor.StateDir

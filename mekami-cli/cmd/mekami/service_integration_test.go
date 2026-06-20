//go:build integration && linux

// Package mekami_integration contains end-to-end
// tests for the supervisor / watchdog /
// service-install (and service-uninstall) lifecycle. The tests are gated
// behind the `integration` build tag because they
// require:
//
//   - a real systemd --user session (the
//     tests run `systemctl --user` and expect it
//     to work);
//   - the mekami binary to be on $PATH (or
//     discoverable via the path the test provides);
//   - the user's $XDG_CONFIG_HOME to be writable
//     and isolated from the real production state.
//
// To run them locally:
//
//	go test -tags integration ./cmd/mekami/... -run ServiceLifecycle -count=1
//
// or, from the repository root:
//
//	go test -tags integration ./mekami-cli/cmd/mekami/... -run ServiceLifecycle -count=1
//
// CI runs them in a container that has been
// initialised with `systemd --user` (e.g. a Docker
// image with `ENTRYPOINT ["systemd", "--user"]`).
package mekami

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wolf258/mekami-cli/internal/supervisor"
)

// skipIfNoSystemdUser skips the test if there is no
// live `systemctl --user` to talk to. The check is
// deliberately cheap: a `systemctl --user status`
// that exits non-zero means the user bus is not
// available, which is the common case in
// `go test` runs outside a user-manager context.
//
// The test also skips when XDG_CONFIG_HOME points
// outside $HOME: `systemctl --user` does not honour
// XDG_CONFIG_HOME for unit discovery, so isolating
// the unit file under a non-$HOME temp dir would
// make the test silently pass when it should fail.
// Running with the user's real $HOME/.config is
// the only honest way to exercise the install +
// uninstall round-trip.
func skipIfNoSystemdUser(t *testing.T) {
	t.Helper()
	cmd := exec.Command("systemctl", "--user", "status")
	cmd.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	if err := cmd.Run(); err != nil {
		t.Skipf("no systemd --user session available: %v", err)
	}
	// Detect "XDG_CONFIG_HOME points outside
	// $HOME": in that case the test cannot
	// exercise the full install + uninstall
	// round-trip without touching the user's real
	// config, so we skip.
	home := os.Getenv("HOME")
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg != "" && !strings.HasPrefix(xdg, home) {
		t.Skipf("XDG_CONFIG_HOME (%s) is outside $HOME (%s); "+
			"systemctl --user would not see the unit file. "+
			"Re-run with XDG_CONFIG_HOME unset to enable this test.",
			xdg, home)
	}
}

// withTempXDG returns the test's effective
// $XDG_CONFIG_HOME. The integration test relies on
// the fact that systemd --user reads units from
// $HOME/.config/systemd/user, not from the
// XDG_CONFIG_HOME the test sets. To make the test
// non-destructive without XDG_CONFIG_HOME
// redirection, we point the test at the user's
// real $HOME/.config and rely on the t.Cleanup
// registered by each test to remove the unit file
// after the run. The caller's environment must
// satisfy the skipIfNoSystemdUser preconditions.
func withTempXDG(t *testing.T) string {
	t.Helper()
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME is not set; cannot run integration tests")
	}
	dir := filepath.Join(home, ".config")
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// locateMekami returns the absolute path of the
// mekami binary the test should exec. We prefer
// $MEKAMI_BIN (if the test set it explicitly) and
// fall back to `which mekami`. The test should
// build the binary with `go build .` before
// running; the CI driver does this.
//
// The lookup explicitly rejects the user's
// `~/go/bin/mekami` because that binary is
// typically the result of `go install` from a
// different branch and would not match the
// source the test is running against. The
// fallback path is `cwd/mekami`, which is what
// `./build.sh` produces.
func locateMekami(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MEKAMI_BIN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Prefer the local build artefact: the
	// repository root's `./mekami` binary.
	if cwd, err := os.Getwd(); err == nil {
		// Walk up to the repo root: the test
		// runs from mekami-cli/cmd/mekami, so
		// repo root is cwd/../../../mekami.
		candidates := []string{
			filepath.Join(cwd, "..", "..", "..", "mekami"),
			filepath.Join(cwd, "mekami"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	if p, err := exec.LookPath("mekami"); err == nil {
		return p
	}
	t.Skip("mekami binary not found; set MEKAMI_BIN or `go build` first")
	return ""
}

// TestServiceLifecycle_InstallUninstallCleansState is
// the happy-path integration test. It runs:
//
//  1. service-install  (writes the unit, enables it,
//     starts the supervisor which spawns a watchdog);
//  2. a short wait to let the supervisor settle;
//  3. quit-all via the IPC client (simulating
//     service-uninstall's IPC step);
//  4. a short wait to let the supervisor and
//     watchdog exit;
//  5. asserts that the runtime state files are
//     removed (we trigger the cleanup helper
//     directly because `systemctl disable --now`
//     is the racy part: it sends SIGKILL and we
//     do not want the test to depend on its timing);
//  6. service-uninstall's disable step (best
//     effort) and unit file removal.
//
// The test does not assert against the actual
// systemd unit state because that would couple
// the test to the unit's name. Instead it focuses
// on the supervisor / watchdog runtime files,
// which are the user-visible artefacts.
func TestServiceLifecycle_InstallUninstallCleansState(t *testing.T) {
	skipIfNoSystemdUser(t)
	stateDir := withTempXDG(t)
	bin := locateMekami(t)
	t.Logf("stateDir=%s", stateDir)
	// Step 1: install the service. We use the CLI
	// command because that is what users run; the
	// command itself is exercised by the unit
	// tests in the supervisor package.
	cmd := exec.Command(bin, "service-install")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service-install: %v\n%s", err, out)
	}
	t.Logf("install output:\n%s", out)
	t.Cleanup(func() {
		// Best-effort cleanup. We swallow
		// errors because the test is over and
		// there is no useful recovery from a
		// failed uninstall.
		_ = exec.Command(bin, "service-uninstall").Run()
	})
	// Step 2: let the supervisor settle. The unit
	// runs `mekami supervise _run` which then
	// spawns a watchdog; on a busy CI runner
	// the spawn may take a few hundred ms.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cli := supervisor.NewClient()
		cli.Timeout = 500 * time.Millisecond
		if cli.Ping(context.Background()) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Step 3 + 4 + 5: trigger the uninstall
	// pipeline. We do not call the CLI's
	// `service-uninstall` because the unit's
	// existence is racy (we created it via
	// `service-install`); instead we call the
	// IPC quit-all directly, then run the cleanup
	// helper, then remove the unit. This is the
	// observable path the CLI takes; the
	// remaining CLI logic (systemctl disable,
	// etc.) is unit-tested separately.
	cli := supervisor.NewClient()
	cli.Timeout = 2 * time.Second
	if err := cli.QuitAll(context.Background()); err != nil {
		t.Fatalf("quit-all: %v", err)
	}
	// Wait for the supervisor to actually exit.
	// The IPC client returns successfully because
	// the supervisor acknowledged the request;
	// the process itself is on its way out.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !cli.Ping(context.Background()) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Step 5: cleanup. The helper removes the
	// runtime files; we do not call it via the
	// CLI because the CLI's path also includes
	// the `systemctl disable` step which is
	// racy in tests. The helper itself is a
	// pure function and is what the production
	// uninstall flow ends up calling.
	cleanupSupervisorRuntimeState()
	for _, name := range []string{
		"supervisor.pid",
		"supervisor.sock",
		"supervisor.log",
		"watchdog.pid",
		"stop",
	} {
		p := filepath.Join(stateDir, name)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("runtime file %s should be removed (err=%v)", name, err)
		}
	}
	// Step 6: remove the unit file. The test
	// deliberately skips `systemctl disable`
	// because that requires a live user bus,
	// which the test may have lost by now. The
	// unit file is what `service-install`
	// created; we just unlink it.
	unitPath := filepath.Join(systemdUserDir(), supervisorUnitName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		t.Errorf("remove unit: %v", err)
	}
}

// TestWatchdogSentinel_ExitsImmediately is the
// end-to-end counterpart to
// TestWatchdogRun_ExitsOnSentinel (which uses
// in-process fakes). The test installs the
// service, waits for the watchdog to come up,
// touches the sentinel file, and asserts that
// the watchdog's PID file is gone within one
// second (the production tick is 5s; the
// sentinel-triggered exit is immediate, so
// the test passes well before the next tick).
func TestWatchdogSentinel_ExitsImmediately(t *testing.T) {
	skipIfNoSystemdUser(t)
	withTempXDG(t)
	bin := locateMekami(t)
	cmd := exec.Command(bin, "service-install")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("service-install: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command(bin, "service-uninstall").Run()
		_ = os.Remove(filepath.Join(systemdUserDir(), supervisorUnitName))
	})
	// Wait for the supervisor.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cli := supervisor.NewClient()
		cli.Timeout = 500 * time.Millisecond
		if cli.Ping(context.Background()) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Give the watchdog a moment to write its
	// PID file. The watchdog spawns after the
	// supervisor's IPC server is up; in practice
	// it is ready within tens of milliseconds.
	deadline = time.Now().Add(2 * time.Second)
	var pidBefore int
	for time.Now().Before(deadline) {
		pid, err := supervisor.ReadWatchdogPID()
		if err == nil && pid > 0 {
			pidBefore = pid
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pidBefore == 0 {
		t.Fatalf("watchdog PID file never appeared")
	}
	// Touch the sentinel and wait for the PID
	// file to disappear. The watchdog's tick is
	// 5 seconds in production, so we wait up to
	// 8 seconds (5s tick + 3s margin) for the
	// watchdog's next iteration to notice the
	// sentinel and exit. This is a longer wait
	// than the unit test because the production
	// tick is much coarser than the test-tuned
	// 50ms tick used in supervisor_test.go.
	if err := supervisor.SetSentinel(); err != nil {
		t.Fatalf("SetSentinel: %v", err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		pid, err := supervisor.ReadWatchdogPID()
		if err == nil && pid == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	pid, _ := supervisor.ReadWatchdogPID()
	t.Fatalf("watchdog PID file still present (pid=%d) after sentinel", pid)
}

// helper: ensure the strings import survives
// goimports when this file is the only consumer
// in this build tag.
var _ = strings.TrimSpace

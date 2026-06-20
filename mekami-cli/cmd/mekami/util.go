package mekami

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-core/store"
	"github.com/Wolf258/mekami-cli/internal/naming"
	"github.com/Wolf258/mekami-cli/internal/supervisor"
)

// namingArgMap is a tiny alias used in the mcp install runner.
type namingArgMap = naming.ArgMap

// printJSON writes v as JSON to stdout followed by a newline. The
// encoding delegates to format.JSON so the layout matches the
// MCP tool output.
func printJSON(v any) error {
	_, err := fmt.Fprintln(os.Stdout, format.JSON(v))
	return err
}

// startDaemonForRoot is the public entry point used by `init`.
// It ensures the supervisor is running, then asks the supervisor
// to spawn a daemon for root.
func startDaemonForRoot(root, dbPath, lang string, cfg config.Config) (*supervisor.DaemonView, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := ensureSupervisor(); err != nil {
		return nil, err
	}
	cli := supervisor.NewClient()
	cli.Timeout = 10 * time.Second
	names := make([]string, 0, len(cfg.Indexers))
	for name := range cfg.Indexers {
		names = append(names, name)
	}
	sort.Strings(names)
	v, err := cli.Start(context.Background(), supervisor.StartPayload{
		Root:          absRoot,
		Lang:          lang,
		DBPath:        dbPath,
		RestartPolicy: "on-crash",
		IndexerNames:  names,
	})
	if err != nil {
		return nil, err
	}
	_ = cfg
	return v, nil
}

// ensureSupervisor makes sure the supervisor is running. If
// not, it spawns it and waits for the socket to appear.
func ensureSupervisor() error {
	cli := supervisor.NewClient()
	if cli.Ping(context.Background()) {
		return nil
	}
	if err := supervisor.EnsureStateDir(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	cmd := exec.Command(self, "supervise", "_run")
	cmd.Env = append(os.Environ(), "_MEKAMI_SUPERVISOR=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork supervisor: %w", err)
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cli.Ping(context.Background()) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("supervisor did not start within 5s")
}

// loadWatchConfig reads the project's .mekami/config.json. It is
// shared by every command that needs the user's settings.
func loadWatchConfig() (config.Config, error) {
	return config.Load("")
}

// orDefault returns v if non-empty, else fallback.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// formatDuration is a tiny human-readable formatter.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)
	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	return strings.Join(parts, "")
}

// followFile is a small `tail -F` shim.
func followFile(path string) error {
	tail := exec.Command("tail", "-F", "-n", "50", path)
	tail.Stdout = os.Stdout
	tail.Stderr = os.Stderr
	if err := tail.Run(); err != nil {
		f, ferr := os.Open(path)
		if ferr != nil {
			return fmt.Errorf("open log: %w (tail: %v)", ferr, err)
		}
		defer f.Close()
		_, _ = io.Copy(os.Stdout, f)
		fmt.Fprintln(os.Stderr, "(tail not available; log dumped once)")
	}
	return nil
}

// isInteractive reports whether stdin is a terminal.
func isInteractive() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// confirm prints prompt and reads a single line of stdin.
// Returns true iff the line starts with 'y' or 'Y'.
func confirm(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	var line string
	_, err := fmt.Scanln(&line)
	if err != nil {
		return false
	}
	if line == "" {
		return false
	}
	c := line[0]
	return c == 'y' || c == 'Y'
}

// _ keeps store in the import set even if every callsite moves
// to a different package.
var _ = store.ErrNoLastRoot

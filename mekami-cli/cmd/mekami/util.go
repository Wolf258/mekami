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
	"time"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/core/store"
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
	configureDetachedProcess(cmd)
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

// followFile streams the contents of path to stdout and then
// blocks, printing any new bytes as the file grows. It is the
// portable equivalent of `tail -F -n 50` and does not require
// the system to ship a `tail` binary (which is the case on
// stock Windows). The implementation is a small poll loop:
//
//  1. Open the file and seek to the end minus a 50-line
//     history (so the user sees recent context, not the full
//     log from boot).
//  2. Read new bytes on a short poll interval (200ms) and
//     write them to stdout.
//  3. If the file shrinks (rotated / truncated) or is replaced
//     (rename + recreate), seek back to the start of the new
//     file and continue.
//
// The function returns when ctx is done or the file disappears
// without being replaced.
func followFile(path string) error {
	return followFileContext(context.Background(), path)
}

func followFileContext(ctx context.Context, path string) error {
	const (
		historyLines = 50
		pollInterval = 200 * time.Millisecond
	)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer f.Close()
	// Seed the read position 50 lines back from the end so the
	// user sees recent context. If the file is shorter than
	// 50 lines we start at offset 0.
	if err := seekBackNLines(f, historyLines); err != nil {
		return fmt.Errorf("seek history: %w", err)
	}
	// Copy the seed chunk to stdout synchronously.
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("copy history: %w", err)
	}
	// Track the inode / size so we detect rotation / truncation.
	lastStat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	lastSize := lastStat.Size()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		cur, err := os.Stat(path)
		if err != nil {
			// File gone (rotated away) and not replaced: stop.
			return nil
		}
		// Truncation / replacement: reset to start.
		if cur.Size() < lastSize {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("seek reset: %w", err)
			}
			fmt.Fprintln(os.Stdout, "--- log rotated ---")
		}
		lastSize = cur.Size()
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return fmt.Errorf("copy tail: %w", err)
		}
	}
}

// seekBackNLines positions f so that the next Read returns the
// start of the n-th line from the end of the file, in `tail -n`
// semantics. A "line" is text terminated by '\n' (or by EOF for
// the last line if the file is not newline-terminated).
//
// On a small file (fewer than n lines) it positions at offset 0.
// The function is best-effort: it reads backwards in 4 KiB
// chunks. For huge files (>1 MiB of tail) the last chunk is
// usually enough to find 50 newlines; if not, we fall back to
// offset 0.
//
// Counting strategy: walk newlines from the end of the file
// backwards. For a newline-terminated file, the N-th line from
// the end starts right after the (N+1)-th newline counted from
// the end (the +1 accounts for the trailing newline that closes
// the last line). For an unterminated file, the N-th line starts
// right after the N-th newline counted from the end (the last
// line has no terminator). If the file does not contain enough
// newlines, we fall back to offset 0.
func seekBackNLines(f *os.File, n int) error {
	const chunk = 4096
	if n <= 0 {
		return nil
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	target := n + 1
	if !endsWithNewline(f) {
		// For an unterminated file the last line has no
		// trailing newline. The N-th line from the end starts
		// right after the N-th newline counted from the end
		// (no off-by-one bump).
		target = n
	}
	offset := size
	newlines := 0
	buf := make([]byte, chunk)
	for offset > 0 {
		readSize := int64(chunk)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(f, buf[:readSize]); err != nil {
			return err
		}
		for i := int(readSize) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				newlines++
				if newlines == target {
					// Position right after this newline.
					if _, err := f.Seek(offset+int64(i)+1, io.SeekStart); err != nil {
						return err
					}
					return nil
				}
			}
		}
	}
	// Fewer than target newlines in the file: start at the
	// beginning.
	_, err = f.Seek(0, io.SeekStart)
	return err
}

// endsWithNewline reports whether f's last byte is '\n'. The
// caller must have a valid handle; the function is only used by
// seekBackNLines, which always holds a fresh handle.
func endsWithNewline(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true // treat empty as "ends with newline"
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return true
	}
	return last[0] == '\n'
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

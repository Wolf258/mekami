package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/testutil"
)

// requireIPC skips the test when the current Go build does
// not support the IPC transport the supervisor uses on this
// platform (named pipes on Windows). It is a no-op on Unix
// and on Windows builds that have the "pipe" net package
// compiled in. The check is cheap: one net.Listen that we
// close immediately.
func requireIPC(t *testing.T) {
	t.Helper()
	testutil.SkipIfNoNamedPipe(t)
}

// filepathJoinTemp returns a path inside t.TempDir(). It's a
// convenience for tests that need a stable file path.
func filepathJoinTemp(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "daemons.json")
}

func filepathJoin(parts ...string) string {
	return filepath.Join(parts...)
}

// writeFile writes content to path. Thin wrapper to keep the
// tests focused on behaviour.
func writeFile(path, content string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	return len(content), os.WriteFile(path, []byte(content), 0o644)
}

// shortSockDir delegates to testutil so the package-local tests
// can keep their short call sites. See testutil.ShortSockDir for
// the full rationale (macOS sun_path limit).
func shortSockDir(t *testing.T) string {
	t.Helper()
	return testutil.ShortSockDir(t)
}

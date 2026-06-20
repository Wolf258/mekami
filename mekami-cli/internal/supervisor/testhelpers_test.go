package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

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

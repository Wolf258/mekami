package watch

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/Wolf258/mekami-cli/internal/config"
	"github.com/Wolf258/mekami-cli/internal/testutil"
)

// fsnotifyEvent is a tiny test shim so the Translate test does not
// have to import the fsnotify op constants directly. The real type
// is fsnotify.Event; toFsnotify() converts this shim into one.
type fsnotifyEvent struct {
	Name string
	Op   uint32
}

const (
	opCreate = 1 << iota
	opWrite
	opRemove
	opRename
	opChmod
)

func (e fsnotifyEvent) toFsnotify() fsnotify.Event {
	var op fsnotify.Op
	if e.Op&opCreate != 0 {
		op |= fsnotify.Create
	}
	if e.Op&opWrite != 0 {
		op |= fsnotify.Write
	}
	if e.Op&opRemove != 0 {
		op |= fsnotify.Remove
	}
	if e.Op&opRename != 0 {
		op |= fsnotify.Rename
	}
	if e.Op&opChmod != 0 {
		op |= fsnotify.Chmod
	}
	return fsnotify.Event{Name: e.Name, Op: op}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeGoMod(dir, modPath string) error {
	return os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module "+modPath+"\n\ngo 1.22\n"), 0o644)
}

// queryDB opens the DB in read-only mode and runs a query that
// returns a single bool: true if any row matched. Used by tests
// that just want to know "is this symbol in the index?".
func queryDB(t *testing.T, dbPath, sqlStr string, args ...any) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	row := db.QueryRow(sqlStr, args...)
	var x int
	if err := row.Scan(&x); err != nil {
		if err == sql.ErrNoRows {
			return false
		}
		t.Fatalf("query: %v", err)
	}
	return true
}

func symbolInDB(t *testing.T, dbPath, qname string) bool {
	t.Helper()
	return queryDB(t, dbPath, "SELECT 1 FROM symbols WHERE qualified_name = ? LIMIT 1", qname)
}

func configOnStartBuild() config.WatchConfig {
	c := config.DefaultWatch()
	c.OnStart = "build"
	return c
}

func configOnStartSkip() config.WatchConfig {
	c := config.DefaultWatch()
	c.OnStart = "skip"
	c.DebounceMs = 50
	return c
}

func pollerFastConfig() config.WatchConfig {
	c := config.DefaultWatch()
	c.OnStart = "skip"
	c.DebounceMs = 50
	return c
}

// neverStop returns a channel that is never closed. Used by tests
// that only want to drive the coalescer through one or two Drain
// calls and rely on the debounce window to deliver the batch.
func neverStop() <-chan struct{} {
	return make(chan struct{})
}

// shortSockDir delegates to testutil so the package-local tests
// can keep their short call sites. See testutil.ShortSockDir for
// the full rationale (macOS sun_path limit).
func shortSockDir(t *testing.T) string {
	t.Helper()
	return testutil.ShortSockDir(t)
}

// requireIPC skips the test when the current Go build does
// not support the IPC transport the watch package uses on
// this platform (named pipes on Windows). It is a no-op on
// Unix and on Windows builds that have the "pipe" net
// package compiled in.
func requireIPC(t *testing.T) {
	t.Helper()
	testutil.SkipIfNoNamedPipe(t)
}

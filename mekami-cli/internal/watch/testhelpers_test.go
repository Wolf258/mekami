package watch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"

	_ "github.com/Wolf258/mekami-core-go"

	"github.com/Wolf258/mekami-core/ingest"
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

// ingestBuild is a thin shim around ingest.Build for end-to-end
// tests. The test file needs the symbol so the import in the test
// file does not have to be repeated.
func ingestBuild(ctx context.Context, root, dbPath string) error {
	_, err := ingest.Build(ctx, ingest.BuildOptions{
		Root:   root,
		DBPath: dbPath,
		Clean:  true,
		Quiet:  true,
	})
	return err
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

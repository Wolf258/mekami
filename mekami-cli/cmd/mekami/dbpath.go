package mekami

import (
	"fmt"
	"os"

	"github.com/Wolf258/mekami-cli/internal/core/store"
)

// DefaultDBPath is the relative path used when the --db flag is not
// supplied. All subcommands (read and write) fall back to this.
const DefaultDBPath = ".mekami/graph.db"

// defaultDBPath returns name if non-empty, otherwise DefaultDBPath.
// Unlike resolveDBPath it does not stat the file, so it is safe to use
// from the build command (which creates the DB) and from any caller
// that only needs the canonical path.
func defaultDBPath(name string) string {
	if name == "" {
		return DefaultDBPath
	}
	return name
}

// resolveDBPath returns the absolute or user-supplied DB path, or an
// error if the file does not exist. Used by every subcommand that
// reads or writes the graph database.
func resolveDBPath(name string) (string, error) {
	name = defaultDBPath(name)
	if _, err := os.Stat(name); os.IsNotExist(err) {
		return "", fmt.Errorf("graph database not found at %s\n\nRun 'mekami build' first to generate it, e.g.:\n  mekami build\n\nOr specify a custom location:\n  mekami --db /path/to/graph.db", name)
	}
	return name, nil
}

// openStore resolves the DB path (using the persistent --db flag
// when name is empty) and opens a graph.Store ready for queries.
// The caller is responsible for Close.
func openStore(name string) (*store.Store, error) {
	path, err := resolveDBPath(name)
	if err != nil {
		return nil, err
	}
	return store.Open(path)
}

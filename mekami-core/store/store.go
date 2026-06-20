package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/Wolf258/mekami-core/model"
)

// ErrNoLastRoot is returned by GetMeta (and propagated by callers
// like SourceSlice) when the meta key has never been written. The
// practical trigger is reading the DB before a build has ever run.
var ErrNoLastRoot = errors.New("no last_root set; run 'mekami build' first")

// Meta keys persisted in the meta table. Centralized here so
// SetMeta/GetMeta callers and the meta-write code in ingest
// cannot drift apart.
const (
	MetaLastRoot      = "last_root"
	MetaIsWorkspace   = "is_workspace"
	MetaWorkspaceMods = "workspace_modules"
	MetaPrimaryModule = "primary_module"
	MetaRootModule    = "root_module"
	MetaLastBuildAt   = "last_build_at" // RFC3339 timestamp of the most recent successful build
)

// optionalMetaKeys are meta keys that may legitimately be absent
// (they're written by Build and read by query tools). Reading a
// missing optional key returns ErrNoLastRoot; reading a missing
// required key returns a generic "not set" error.
var optionalMetaKeys = map[string]bool{
	MetaLastRoot:   true,
	MetaRootModule: true,
}

//go:embed schema.sql
var schemaSQL string

// Store is the handle for a single SQLite database. The zero value
// is not usable; obtain one via Open.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path. The schema is
// applied idempotently on every open so the DB is always consistent
// with the embedded schema.sql. WAL mode and foreign keys are
// enabled.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	s := &Store{db: db}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return s, nil
}

// Close runs a final WAL checkpoint with an uncancellable context
// so a shutdown ctx that was already cancelled cannot prevent the
// checkpoint from completing, then closes the DB.
func (s *Store) Close() error {
	_, _ = s.db.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Used by sibling packages
// (queries, path, diff) that issue their own SELECTs but should
// not re-open the file. The returned handle is safe for
// concurrent use for read queries; writes must use Tx.
func (s *Store) DB() *sql.DB { return s.db }

// SetMeta writes k=v into the meta table, overwriting any prior value.
func (s *Store) SetMeta(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	return err
}

// GetMeta reads the value for k. A missing key returns
// ErrNoLastRoot when the key is optional, or a generic error
// otherwise.
func (s *Store) GetMeta(ctx context.Context, k string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if optionalMetaKeys[k] {
			return "", ErrNoLastRoot
		}
		return "", fmt.Errorf("meta key %q not set", k)
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// Begin starts a new transaction. The caller is responsible for
// Commit / Rollback.
func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx}, nil
}

// Tx wraps a single SQL transaction. All writes in the ingest
// pipeline go through a Tx; reads also work transparently.
type Tx struct {
	tx *sql.Tx
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// QueryContext runs a read query inside the transaction. Use
// this only for SELECTs; writes must use the dedicated helpers
// (UpsertFile, InsertSymbol, etc.) so the schema and
// bookkeeping stay consistent.
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

// Rollback aborts the transaction. Safe to call after a
// successful Commit (it is then a no-op via database/sql).
func (t *Tx) Rollback() error { return t.tx.Rollback() }

// UpsertModule inserts name into the modules table, or no-ops if
// the row is already there.
func (t *Tx) UpsertModule(name string) error {
	_, err := t.tx.Exec(`INSERT INTO modules(name) VALUES(?) ON CONFLICT(name) DO NOTHING`, name)
	return err
}

// UpsertPackage inserts p into the packages table, or updates the
// existing row's name and dir if the (module_id, package_id) pair
// is already there. Returns the row's id.
func (t *Tx) UpsertPackage(p model.Package) (int64, error) {
	_, err := t.tx.Exec(`
		INSERT INTO packages(module_id,package_id,name,dir) VALUES(?,?,?,?)
		ON CONFLICT(module_id,package_id) DO UPDATE SET name=excluded.name, dir=excluded.dir`,
		p.ModuleID, p.PackageID, p.Name, p.Dir)
	if err != nil {
		return 0, err
	}
	var pid int64
	err = t.tx.QueryRow(
		`SELECT id FROM packages WHERE module_id=? AND package_id=?`,
		p.ModuleID, p.PackageID).Scan(&pid)
	return pid, err
}

// UpsertFile inserts f into the files table, or updates the
// existing row's hash/mtime/size/lang if the path is already
// there. Returns the row's id.
func (t *Tx) UpsertFile(f model.File) (int64, error) {
	_, err := t.tx.Exec(`
		INSERT INTO files(path,hash,mtime,size,lang) VALUES(?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash, mtime=excluded.mtime, size=excluded.size, lang=excluded.lang`,
		f.Path, f.Hash, f.Mtime, f.Size, f.Lang)
	if err != nil {
		return 0, err
	}
	var fid int64
	err = t.tx.QueryRow(`SELECT id FROM files WHERE path=?`, f.Path).Scan(&fid)
	return fid, err
}

// DeleteFileContent removes the symbols and refs associated with
// the given file. The file row itself is left in place; the
// caller decides whether to keep the file row (re-ingest will
// re-use it) or remove it.
func (t *Tx) DeleteFileContent(fileID int64) error {
	if _, err := t.tx.Exec(`DELETE FROM refs WHERE from_symbol IN (SELECT id FROM symbols WHERE file_id=?)`, fileID); err != nil {
		return err
	}
	if _, err := t.tx.Exec(`DELETE FROM symbols WHERE file_id=?`, fileID); err != nil {
		return err
	}
	return nil
}

// RemoveFile deletes the file row and all its content. Use
// RemoveFileByPath when the caller only has a path.
func (t *Tx) RemoveFile(fileID int64) error {
	if err := t.DeleteFileContent(fileID); err != nil {
		return err
	}
	_, err := t.tx.Exec(`DELETE FROM files WHERE id=?`, fileID)
	return err
}

// RemoveFileByPath deletes the file row (and via cascade, its
// symbols and refs) for the given relative path. Returns
// (id, true, nil) if a row was found and removed, (0, false, nil)
// if the path was unknown. Used by the incremental builder to
// react to delete events.
func (t *Tx) RemoveFileByPath(path string) (int64, bool, error) {
	var id int64
	err := t.tx.QueryRow(`SELECT id FROM files WHERE path=?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if err := t.DeleteFileContent(id); err != nil {
		return 0, true, err
	}
	if _, err := t.tx.Exec(`DELETE FROM files WHERE id=?`, id); err != nil {
		return 0, true, err
	}
	return id, true, nil
}

// RemoveFilesByLang deletes every file row whose lang matches,
// cascading to symbols and refs via the FK constraints defined
// in schema.sql. Returns the number of file rows removed. Used
// by the cross-language cleanup that runs before an ingest when
// the config's indexers list no longer matches the data on disk.
func (t *Tx) RemoveFilesByLang(lang string) (int64, error) {
	res, err := t.tx.Exec(`DELETE FROM files WHERE lang = ?`, lang)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// CountFilesByLang returns the number of file rows tagged with
// the given language. Used by the cross-language cleanup to
// report per-language removal counts before the DELETE.
func (t *Tx) CountFilesByLang(lang string) (int64, error) {
	var n int64
	err := t.tx.QueryRow(`SELECT COUNT(*) FROM files WHERE lang = ?`, lang).Scan(&n)
	return n, err
}

// CountSymbolsForLang returns the number of symbol rows that
// belong to files with the given language. The cross-language
// cleanup uses this to log a per-language removal summary before
// the DELETE; the count is not authoritative once the prune
// runs, so the caller should only use it for human-readable
// output.
func (t *Tx) CountSymbolsForLang(lang string) (int64, error) {
	var n int64
	err := t.tx.QueryRow(`
		SELECT COUNT(*) FROM symbols
		WHERE file_id IN (SELECT id FROM files WHERE lang = ?)`,
		lang,
	).Scan(&n)
	return n, err
}

// CountRefsForLang returns the number of ref rows whose source
// symbol lives in a file with the given language. The cross-
// language cleanup uses it for the human-readable summary.
func (t *Tx) CountRefsForLang(lang string) (int64, error) {
	var n int64
	err := t.tx.QueryRow(`
		SELECT COUNT(*) FROM refs
		WHERE from_symbol IN (
			SELECT s.id FROM symbols s
			JOIN files f ON f.id = s.file_id
			WHERE f.lang = ?
		)`, lang,
	).Scan(&n)
	return n, err
}

// InsertSymbol persists s and returns its assigned id.
func (t *Tx) InsertSymbol(s model.Symbol) (int64, error) {
	var sig any
	if s.Signature != "" {
		sig = s.Signature
	}
	var parent any
	if s.ParentSymbol != nil {
		parent = *s.ParentSymbol
	}
	exported := 0
	if s.Exported {
		exported = 1
	}
	res, err := t.tx.Exec(`
		INSERT INTO symbols(file_id,package_id,kind,name,qualified_name,start_line,end_line,exported,signature,parent_symbol)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		s.FileID, s.PackageID, s.Kind, s.Name, s.QualifiedName, s.StartLine, s.EndLine, exported, sig, parent)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertRef persists r.
func (t *Tx) InsertRef(r model.Ref) error {
	_, err := t.tx.Exec(`
		INSERT INTO refs(from_symbol,to_qualified,kind,line) VALUES(?,?,?,?)`,
		r.FromSymbol, r.ToQualified, r.Kind, r.Line)
	return err
}

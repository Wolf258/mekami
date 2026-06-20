CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS modules (
    name TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS packages (
    id         INTEGER PRIMARY KEY,
    module_id  TEXT    NOT NULL REFERENCES modules(name),
    package_id TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    dir        TEXT    NOT NULL,
    UNIQUE(module_id, package_id)
);

CREATE TABLE IF NOT EXISTS files (
    id    INTEGER PRIMARY KEY,
    path  TEXT    NOT NULL UNIQUE,
    hash  TEXT    NOT NULL,
    mtime INTEGER NOT NULL,
    size  INTEGER NOT NULL,
    lang  TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS symbols (
    id             INTEGER PRIMARY KEY,
    file_id        INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    package_id     INTEGER NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    kind           TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    qualified_name TEXT    NOT NULL,
    start_line     INTEGER NOT NULL,
    end_line       INTEGER NOT NULL,
    exported       INTEGER NOT NULL,
    signature      TEXT,
    parent_symbol  INTEGER REFERENCES symbols(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sym_qn      ON symbols(qualified_name);
CREATE INDEX IF NOT EXISTS idx_sym_pkg     ON symbols(package_id);
CREATE INDEX IF NOT EXISTS idx_sym_name    ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_sym_parent  ON symbols(parent_symbol);

CREATE TABLE IF NOT EXISTS refs (
    id           INTEGER PRIMARY KEY,
    from_symbol  INTEGER REFERENCES symbols(id) ON DELETE CASCADE,
    to_qualified TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    line         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ref_to   ON refs(to_qualified);
CREATE INDEX IF NOT EXISTS idx_ref_from ON refs(from_symbol);

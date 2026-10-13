// Package store keeps Logbook's lines in SQLite: one row per line, a full
// text index over them, and hourly counts per source. Prune holds the
// database under its size cap and retention.
//
// The schema is created with CREATE ... IF NOT EXISTS; later changes go in
// migrations, which run once each in order and are recorded in
// PRAGMA user_version.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the build stays static
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sources (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	image      TEXT NOT NULL DEFAULT '',
	first_seen INTEGER NOT NULL,
	last_line  INTEGER NOT NULL DEFAULT 0,
	-- Docker's timestamp of the last line read, in ns: where to resume.
	cursor_ns  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS lines (
	id     INTEGER PRIMARY KEY,
	ts     INTEGER NOT NULL,
	source INTEGER NOT NULL,
	level  INTEGER NOT NULL,
	kind   TEXT NOT NULL DEFAULT '',
	msg    TEXT NOT NULL,
	trace  TEXT NOT NULL DEFAULT '',
	user   TEXT NOT NULL DEFAULT '',
	err    TEXT NOT NULL DEFAULT '',
	dur_ms INTEGER,
	attrs  TEXT NOT NULL DEFAULT '',
	stderr INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS lines_ts ON lines (ts);
CREATE INDEX IF NOT EXISTS lines_source_ts ON lines (source, ts);
CREATE INDEX IF NOT EXISTS lines_trace ON lines (trace) WHERE trace <> '';
CREATE INDEX IF NOT EXISTS lines_problems ON lines (level, ts) WHERE level >= 4;
CREATE VIRTUAL TABLE IF NOT EXISTS lines_fts USING fts5 (
	msg, err, attrs, content = 'lines', content_rowid = 'id', tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER IF NOT EXISTS lines_ai AFTER INSERT ON lines BEGIN
	INSERT INTO lines_fts (rowid, msg, err, attrs) VALUES (new.id, new.msg, new.err, new.attrs);
END;
CREATE TRIGGER IF NOT EXISTS lines_ad AFTER DELETE ON lines BEGIN
	INSERT INTO lines_fts (lines_fts, rowid, msg, err, attrs) VALUES ('delete', old.id, old.msg, old.err, old.attrs);
END;
-- Lines per source per hour, kept longer than the lines themselves: the
-- daily quota, the charts and the Foyer card read these.
CREATE TABLE IF NOT EXISTS counts (
	hour    INTEGER NOT NULL,
	source  INTEGER NOT NULL,
	lines   INTEGER NOT NULL DEFAULT 0,
	warns   INTEGER NOT NULL DEFAULT 0,
	errors  INTEGER NOT NULL DEFAULT 0,
	dropped INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (hour, source)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// migrations run after the schema, once each: append, never edit or
// reorder. migrations[i] moves the database to user_version i+1.
var migrations = []string{}

// Open opens (or creates) the database. auto_vacuum has to come before the
// first table, so that pruning gives space back to the disk.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=auto_vacuum(INCREMENTAL)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)&_pragma=cache_size(-2048)&_pragma=journal_size_limit(33554432)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: writes come from one process, and every open SQLite
	// connection holds its own page cache. Closed when idle for a while.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Get reads a JSON setting into v; a missing key leaves v as it was.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

// Put stores v as a JSON setting.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(b))
	return err
}

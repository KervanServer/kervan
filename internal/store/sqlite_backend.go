package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	// Pure-Go SQLite (no cgo), so release builds stay CGO_ENABLED=0.
	_ "modernc.org/sqlite"
)

// sqliteFileName is the database file in the data directory.
const sqliteFileName = "kervan-store.db"

// sqliteSchema keeps the JSON store's model: one row per collection/key,
// the value being the same JSON document the JSON backend stores. WITHOUT
// ROWID clusters rows by (coll, k), so List is a range scan in key order.
const sqliteSchema = `CREATE TABLE IF NOT EXISTS kv (
	coll TEXT NOT NULL,
	k    TEXT NOT NULL,
	v    BLOB NOT NULL,
	PRIMARY KEY (coll, k)
) WITHOUT ROWID`

// sqliteBackend stores records in an SQLite database in WAL mode.
//
// Cross-process safety (server and CLI open the same file) is SQLite's:
// every write runs in a BEGIN IMMEDIATE transaction, which takes the
// database's write lock up front, so a read-check-write in update cannot
// interleave with another writer in this or another process. busy_timeout
// makes a second writer wait for the lock instead of failing at once.
// WAL lets readers proceed while a write is in progress and always see the
// latest committed state, so changes from another process are visible on
// the next read without any refresh logic.
type sqliteBackend struct {
	db *sql.DB
	// writeMu serializes this process's writers. SQLite would do it anyway;
	// doing it here first keeps them from spinning on busy_timeout against
	// each other, so busy waits only happen across processes.
	writeMu sync.Mutex
}

func openSQLiteBackend(dataDir string) (*sqliteBackend, error) {
	path := filepath.Join(dataDir, sqliteFileName)
	// A plain path (no "file:" prefix): the driver strips the query string
	// before opening, so characters in dataDir are never parsed as URI
	// syntax. The parameters apply to every pooled connection.
	dsn := path + "?" + strings.Join([]string{
		"_pragma=busy_timeout(10000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(FULL)",
		"_pragma=foreign_keys(OFF)",
		"_txlock=immediate",
	}, "&")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store database %s: %w", path, err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialise store database %s: %w", path, err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("check store database %s: %w", path, err)
	}
	if !strings.EqualFold(mode, "wal") {
		_ = db.Close()
		return nil, fmt.Errorf("store database %s: journal_mode is %q, want wal", path, mode)
	}
	// The database and its WAL/shared-memory files hold password hashes and
	// API key hashes: owner-only, like kervan-store.json.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, fmt.Errorf("restrict permissions of %s: %w", p, err)
		}
	}
	return &sqliteBackend{db: db}, nil
}

func (s *sqliteBackend) close() error {
	return s.db.Close()
}

func (s *sqliteBackend) get(collection, key string) (json.RawMessage, bool, error) {
	return getRow(s.db, collection, key)
}

func (s *sqliteBackend) put(collection, key string, raw json.RawMessage) error {
	return s.update(func(tx txBackend) error { return tx.put(collection, key, raw) })
}

func (s *sqliteBackend) del(collection, key string) error {
	return s.update(func(tx txBackend) error { return tx.del(collection, key) })
}

func (s *sqliteBackend) list(collection string) ([]json.RawMessage, error) {
	rows, err := s.db.Query(`SELECT v FROM kv WHERE coll = ? ORDER BY k`, collection)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", collection, err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var v []byte
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("list %s: %w", collection, err)
		}
		out = append(out, json.RawMessage(v))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s: %w", collection, err)
	}
	return out, nil
}

// update runs fn in one BEGIN IMMEDIATE transaction (the DSN's _txlock), so
// the write lock is held from the first read; fn's error or a panic rolls
// it back.
func (s *sqliteBackend) update(fn func(txBackend) error) (err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin store transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stx := &sqliteTx{tx: tx}
	if err := fn(stx); err != nil {
		return err
	}
	if stx.err != nil {
		return stx.err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit store transaction: %w", err)
	}
	committed = true
	return nil
}

// sqliteTx is the backend's view inside update.
type sqliteTx struct {
	tx  *sql.Tx
	err error // first error from a write whose caller cannot return one
}

func (t *sqliteTx) get(collection, key string) (json.RawMessage, bool, error) {
	return getRow(t.tx, collection, key)
}

func (t *sqliteTx) put(collection, key string, raw json.RawMessage) error {
	if _, err := t.tx.Exec(`INSERT INTO kv (coll, k, v) VALUES (?, ?, ?)
		ON CONFLICT (coll, k) DO UPDATE SET v = excluded.v`, collection, key, []byte(raw)); err != nil {
		return fmt.Errorf("put %s/%s: %w", collection, key, err)
	}
	return nil
}

func (t *sqliteTx) del(collection, key string) error {
	if _, err := t.tx.Exec(`DELETE FROM kv WHERE coll = ? AND k = ?`, collection, key); err != nil {
		return fmt.Errorf("delete %s/%s: %w", collection, key, err)
	}
	return nil
}

// fail records an error from Tx.Delete, which has no error result; update
// then rolls back and returns it.
func (t *sqliteTx) fail(err error) {
	if t.err == nil {
		t.err = err
	}
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func getRow(q rowQuerier, collection, key string) (json.RawMessage, bool, error) {
	var v []byte
	err := q.QueryRow(`SELECT v FROM kv WHERE coll = ? AND k = ?`, collection, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get %s/%s: %w", collection, key, err)
	}
	return json.RawMessage(v), true, nil
}

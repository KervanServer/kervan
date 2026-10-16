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

// sqliteMetaSchema records one-time facts about the database, such as
// whether kervan-store.json has been imported.
const sqliteMetaSchema = `CREATE TABLE IF NOT EXISTS meta (
	k TEXT NOT NULL PRIMARY KEY,
	v TEXT NOT NULL
) WITHOUT ROWID`

// metaJSONImport is the meta key set once the database has taken over from
// kervan-store.json: "imported" (rows were copied), "none" (there was no
// JSON store) or "skipped" (the database already held rows).
const metaJSONImport = "json_import"

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
	db   *sql.DB
	path string
	// writeMu serializes this process's writers. SQLite would do it anyway;
	// doing it here first keeps them from spinning on busy_timeout against
	// each other, so busy waits only happen across processes.
	writeMu sync.Mutex
}

// sqliteDSN is the driver DSN for a database file. A plain path (no "file:"
// prefix): the driver strips the query string before opening, so characters
// in the path are never parsed as URI syntax. The parameters apply to every
// pooled connection.
func sqliteDSN(path string) string {
	return path + "?" + strings.Join([]string{
		"_pragma=busy_timeout(10000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(FULL)",
		"_pragma=foreign_keys(OFF)",
		"_txlock=immediate",
	}, "&")
}

func openSQLiteBackend(dataDir string) (*sqliteBackend, error) {
	path := filepath.Join(dataDir, sqliteFileName)
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open store database %s: %w", path, err)
	}
	if err := initSchema(db, path); err != nil {
		_ = db.Close()
		return nil, err
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
	b := &sqliteBackend{db: db, path: path}
	if err := b.importJSONStore(dataDir); err != nil {
		_ = db.Close()
		return nil, err
	}
	return b, nil
}

// initSchema creates the store tables. It holds the store's cross-process
// lock, because this is the one step busy_timeout does not cover: when the
// server and a CLI command open a not-yet-existing database at the same time,
// they race on creating the file itself and SQLite answers SQLITE_BUSY after
// microseconds, without ever consulting the busy handler. The lock makes a
// loser wait for the winner's CREATE instead of failing the whole open.
func initSchema(db *sql.DB, path string) error {
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return fmt.Errorf("lock store database %s: %w", path, err)
	}
	defer unlock()
	for _, ddl := range []string{sqliteSchema, sqliteMetaSchema} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("initialise store database %s: %w", path, err)
		}
	}
	return nil
}

// importJSONStore copies kervan-store.json into the database the first time
// the database is opened, so switching an existing installation to SQLite
// keeps its users, groups, keys and links.
//
// It runs once per database: the outcome is recorded in the meta table in
// the same BEGIN IMMEDIATE transaction that copies the rows, so concurrent
// first opens (server and CLI) import exactly once. The JSON store's own
// lock is held as well, so a process still on the JSON backend cannot write
// half-way through. A corrupt JSON file falls back to its .bak copy, like
// the JSON backend does; if neither can be read, opening fails rather than
// starting with an empty store. After the import commits, the JSON files are
// renamed to *.migrated (kept, not deleted).
func (b *sqliteBackend) importJSONStore(dataDir string) error {
	jsonPath := filepath.Join(dataDir, "kervan-store.json")
	backupPath := jsonPath + ".bak"

	var done string
	err := b.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, metaJSONImport).Scan(&done)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read store import state: %w", err)
	}

	unlock, err := lockFile(jsonPath + ".lock")
	if err != nil {
		return fmt.Errorf("lock %s for import: %w", jsonPath, err)
	}
	defer unlock()

	imported := false
	err = b.update(func(txb txBackend) error {
		tx := txb.(*sqliteTx).tx
		// Re-check inside the write transaction: another process may have
		// finished the import since the read above.
		var state string
		switch err := tx.QueryRow(`SELECT v FROM meta WHERE k = ?`, metaJSONImport).Scan(&state); {
		case err == nil:
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		mark := func(state string) error {
			_, err := tx.Exec(`INSERT INTO meta (k, v) VALUES (?, ?)`, metaJSONImport, state)
			return err
		}
		var rows int
		if err := tx.QueryRow(`SELECT count(*) FROM kv`).Scan(&rows); err != nil {
			return err
		}
		if rows > 0 {
			// A database that already holds data is never overwritten.
			return mark("skipped")
		}
		data, err := readJSONStoreForImport(jsonPath, backupPath)
		if err != nil {
			return err
		}
		if data == nil {
			return mark("none")
		}
		for ck, raw := range data {
			collection, key, ok := strings.Cut(ck, ":")
			if !ok {
				return fmt.Errorf("import %s: malformed key %q", jsonPath, ck)
			}
			if err := txb.put(collection, key, raw); err != nil {
				return fmt.Errorf("import %s: %w", jsonPath, err)
			}
		}
		imported = true
		return mark("imported")
	})
	if err != nil {
		return fmt.Errorf("import %s into %s: %w", jsonPath, b.path, err)
	}
	if imported {
		for _, p := range []string{jsonPath, backupPath} {
			if err := os.Rename(p, p+".migrated"); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("imported %s, but could not rename %s: %w", jsonPath, p, err)
			}
		}
	}
	return nil
}

// readJSONStoreForImport reads the JSON store, falling back to its backup
// copy when the primary is missing or unreadable. It returns nil, nil when
// there is no JSON store at all.
//
// A zero-length file counts as unreadable rather than as an empty store: the
// JSON backend always writes at least "{}", so an empty file can only be a
// truncated write. Importing it as "nothing to migrate" would mark the
// migration done with no rows, rename the intact .bak out of reach, and leave
// the store empty for good.
func readJSONStoreForImport(path, backupPath string) (map[string]json.RawMessage, error) {
	data, err := readMigratableStoreFile(path)
	if err == nil {
		return data, nil
	}
	primaryErr := err
	data, err = readMigratableStoreFile(backupPath)
	if err == nil {
		return data, nil
	}
	if errors.Is(primaryErr, os.ErrNotExist) && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return nil, fmt.Errorf("read JSON store: %v; backup: %v", primaryErr, err)
}

// readMigratableStoreFile reads one store file for migration. A zero-length
// file is rejected as the truncated write it is; that rule now lives in
// loadStoreFile, which every store file shares, so this only narrows the
// return to what the migration needs.
func readMigratableStoreFile(path string) (map[string]json.RawMessage, error) {
	data, _, err := loadStoreFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// writeSnapshot writes a consistent copy of the database to dst (which must
// not exist) with VACUUM INTO. It runs as a read transaction, so writers in
// this and other processes are not blocked.
func (b *sqliteBackend) writeSnapshot(dst string) error {
	if _, err := b.db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("snapshot %s to %s: %w", b.path, dst, err)
	}
	return os.Chmod(dst, 0o600)
}

// VerifySQLiteFile checks that path is a sound Kervan store database: SQLite
// reports no corruption and the kv table is present with the columns the
// store reads and writes. It is meant for files that are not in use, such as
// a backup copy before it is restored. Opening may create -wal/-shm files
// next to path; only files it created are removed again.
func VerifySQLiteFile(path string) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	var created []string
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); errors.Is(err, os.ErrNotExist) {
			created = append(created, path+suffix)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); err == nil && cerr != nil {
			err = cerr
		}
		for _, p := range created {
			_ = os.Remove(p)
		}
	}()
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("integrity check of %s: %w", path, err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check of %s failed: %s", path, result)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'kv'`).Scan(&n); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if n != 1 {
		return fmt.Errorf("%s is not a Kervan store database (no kv table)", path)
	}
	// The table's NAME is not enough. Every store query names coll, k and v,
	// so a database whose kv table lacks them is sound SQLite but unusable as
	// a store: it would be restored and then fail on its first read or write.
	columns, err := tableColumns(db, "kv")
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	for _, want := range []string{"coll", "k", "v"} {
		if !columns[want] {
			return fmt.Errorf("%s is not a Kervan store database (kv table has no %s column)", path, want)
		}
	}
	return nil
}

// tableColumns returns the column names of a table in db.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

func (s *sqliteBackend) close() error {
	return s.db.Close()
}

func (s *sqliteBackend) filePath() string { return s.path }

func (s *sqliteBackend) snapshot(dst string) error { return s.writeSnapshot(dst) }

// BuildSQLiteFromJSON creates the SQLite store database dst from the JSON
// store file jsonPath, using the same import as the first open of an
// existing installation. dst must not exist. The work is done in a
// temporary directory next to dst, so a failure leaves nothing behind.
func BuildSQLiteFromJSON(jsonPath, dst string) error {
	raw, err := os.ReadFile(jsonPath) // #nosec G304 -- caller-provided local file
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(dst), ".kervan-store-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := writeFileAtomically(filepath.Join(work, "kervan-store.json"), raw, 0o600); err != nil {
		return err
	}
	b, err := openSQLiteBackend(work)
	if err != nil {
		return err
	}
	// Close checkpoints the WAL into the database file, so the file is
	// complete on its own and can be moved.
	if err := b.close(); err != nil {
		return err
	}
	return os.Rename(filepath.Join(work, sqliteFileName), dst)
}

// ReplaceSQLiteFile moves the verified database file src over the store
// database dst. Stale -wal/-shm files of the old database are removed first:
// SQLite would otherwise replay the old database's journal onto the new one.
// The store must not be in use.
func ReplaceSQLiteFile(src, dst string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dst + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale %s: %w", dst+suffix, err)
		}
	}
	if err := os.Chmod(src, 0o600); err != nil {
		return err
	}
	return ReplaceTempFileAtomically(src, dst)
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

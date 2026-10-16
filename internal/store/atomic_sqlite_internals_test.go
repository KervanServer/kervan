package store

// Coverage for the two internal surfaces that no test referenced before:
// tableColumns (sqlite_backend.go) and syncDir (atomic.go).
//
// syncDir's contract is deliberately best-effort — it returns nil on every
// path, because dir.Sync() fails with EINVAL on some filesystems and the store
// must still open there. That tolerance is a decision worth pinning, so these
// tests assert it holds rather than asserting an error we never want.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// syncDir must never return an error: a missing path, a regular file and a
// real directory are all tolerated. ReplaceTempFileAtomically wraps whatever
// comes back, so a non-nil return here would fail every atomic write on
// platforms where directory fsync is unsupported.
func TestSyncDirNeverReturnsAnError(t *testing.T) {
	dir := t.TempDir()

	if err := syncDir(dir); err != nil {
		t.Errorf("syncDir(real dir) = %v, want nil", err)
	}

	// A path that does not exist: os.Open fails and is swallowed.
	if err := syncDir(filepath.Join(dir, "does-not-exist")); err != nil {
		t.Errorf("syncDir(missing path) = %v, want nil", err)
	}

	// A regular file, not a directory: os.Open succeeds, Sync succeeds.
	file := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := syncDir(file); err != nil {
		t.Errorf("syncDir(regular file) = %v, want nil", err)
	}
}

// tableColumns reports a table's column names and treats a missing table as
// an empty set rather than an error — VerifySQLiteFile relies on that to tell
// "no kv table" apart from a driver failure.
func TestTableColumnsReportsColumnsAndMissingTableAsEmpty(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "cols.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE kv (coll TEXT NOT NULL, k TEXT NOT NULL, v BLOB NOT NULL)`); err != nil {
		t.Fatalf("create kv: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create meta: %v", err)
	}

	cols, err := tableColumns(db, "kv")
	if err != nil {
		t.Fatalf("tableColumns(kv): %v", err)
	}
	for _, want := range []string{"coll", "k", "v"} {
		if !cols[want] {
			t.Errorf("tableColumns(kv) is missing %q; got %v", want, cols)
		}
	}
	if len(cols) != 3 {
		t.Errorf("tableColumns(kv) = %v, want exactly coll, k, v", cols)
	}

	// A table with a different shape must not be mistaken for the real one —
	// this is the check VerifySQLiteFile uses to reject a bad backup.
	if _, err := db.Exec(`CREATE TABLE kv_bad (x TEXT)`); err != nil {
		t.Fatalf("create kv_bad: %v", err)
	}
	bad, err := tableColumns(db, "kv_bad")
	if err != nil {
		t.Fatalf("tableColumns(kv_bad): %v", err)
	}
	if bad["coll"] || bad["k"] || bad["v"] {
		t.Errorf("tableColumns(kv_bad) = %v, must not contain coll/k/v", bad)
	}

	// A missing table is an empty set, not an error.
	missing, err := tableColumns(db, "no_such_table")
	if err != nil {
		t.Fatalf("tableColumns(missing table) returned %v, want nil error", err)
	}
	if len(missing) != 0 {
		t.Errorf("tableColumns(missing table) = %v, want empty", missing)
	}
}
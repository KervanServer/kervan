package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// seedJSONStore writes records through the JSON backend, as an existing
// installation would have them.
func seedJSONStore(t *testing.T, dir string, records map[string]map[string]string) {
	t.Helper()
	st, err := OpenWithOptions(dir, Options{Backend: BackendJSON})
	if err != nil {
		t.Fatal(err)
	}
	for coll, kv := range records {
		for k, v := range kv {
			if err := st.Put(coll, k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return false
}

// Step 2: the first SQLite open of an existing installation imports
// kervan-store.json, then keeps the JSON files as *.migrated.
func TestSQLiteImportsExistingJSONStore(t *testing.T) {
	dir := t.TempDir()
	seedJSONStore(t, dir, map[string]map[string]string{
		"users":              {"u1": "alice", "u2": "bob"},
		"users_idx_username": {"alice": "u1", "bob": "u2"},
		"api_keys":           {"k1": "key"},
	})

	st := openSQLite(t, dir)
	var users []string
	if err := st.List("users", &users); err != nil || len(users) != 2 || users[0] != "alice" || users[1] != "bob" {
		t.Fatalf("imported users = %v (%v)", users, err)
	}
	var id string
	if err := st.Get("users_idx_username", "bob", &id); err != nil || id != "u2" {
		t.Fatalf("imported index = %q (%v)", id, err)
	}

	jsonPath := filepath.Join(dir, "kervan-store.json")
	for _, p := range []string{jsonPath, jsonPath + ".bak"} {
		if exists(t, p) {
			t.Fatalf("%s still present after import; it must be renamed so it is not mistaken for live data", filepath.Base(p))
		}
		if !exists(t, p+".migrated") {
			t.Fatalf("%s.migrated missing: the JSON store must be kept", filepath.Base(p))
		}
	}
	var state string
	if err := st.b.(*sqliteBackend).db.QueryRow(`SELECT v FROM meta WHERE k = ?`, metaJSONImport).Scan(&state); err != nil || state != "imported" {
		t.Fatalf("import state = %q (%v)", state, err)
	}
}

// The import happens once: a JSON file that reappears later (e.g. restored
// by hand, or written by an old binary) is not merged into the database.
func TestSQLiteImportRunsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "alice"}})
	st := openSQLite(t, dir)
	if err := st.Put("users", "u1", "alice-renamed"); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "stale", "u9": "ghost"}})
	again := openSQLite(t, dir)
	var users []string
	if err := again.List("users", &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != "alice-renamed" {
		t.Fatalf("users after reopen = %v: a later JSON file must not be imported again", users)
	}
}

// A fresh installation (no JSON store) imports nothing and records that, so
// a JSON file appearing later is not imported either.
func TestSQLiteFreshInstallRecordsNoImport(t *testing.T) {
	dir := t.TempDir()
	st := openSQLite(t, dir)
	var state string
	if err := st.b.(*sqliteBackend).db.QueryRow(`SELECT v FROM meta WHERE k = ?`, metaJSONImport).Scan(&state); err != nil || state != "none" {
		t.Fatalf("import state = %q (%v), want none", state, err)
	}
}

// A corrupt kervan-store.json falls back to its .bak copy, as the JSON
// backend does; with both unreadable, opening fails instead of silently
// starting an empty store.
func TestSQLiteImportUsesBackupAndRefusesUnreadableStore(t *testing.T) {
	dir := t.TempDir()
	seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "alice"}})
	jsonPath := filepath.Join(dir, "kervan-store.json")
	if err := os.WriteFile(jsonPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := openSQLite(t, dir)
	var v string
	if err := st.Get("users", "u1", &v); err != nil || v != "alice" {
		t.Fatalf("import from .bak: %q (%v)", v, err)
	}

	broken := t.TempDir()
	for _, p := range []string{"kervan-store.json", "kervan-store.json.bak"} {
		if err := os.WriteFile(filepath.Join(broken, p), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := OpenWithOptions(broken, Options{Backend: BackendSQLite}); err == nil {
		_ = s.Close()
		t.Fatal("opened with an unreadable JSON store and no usable backup: data would silently disappear")
	}
	if !exists(t, filepath.Join(broken, "kervan-store.json")) {
		t.Fatal("failed import must leave the JSON store in place")
	}
	// The failed import was rolled back (no import state recorded), so once
	// the JSON store is repaired the next open imports it.
	good := filepath.Join(t.TempDir(), "good")
	seedJSONStore(t, good, map[string]map[string]string{"users": {"u7": "dora"}})
	for _, p := range []string{"kervan-store.json", "kervan-store.json.bak"} {
		raw, err := os.ReadFile(filepath.Join(good, p))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken, p), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	retried := openSQLite(t, broken)
	if err := retried.Get("users", "u7", &v); err != nil || v != "dora" {
		t.Fatalf("import was not retried after the JSON store was repaired: %q (%v)", v, err)
	}
}

// A zero-length kervan-store.json is a truncated write, not an empty store:
// the JSON backend always writes at least "{}", so the intact .bak must be
// used instead of importing "nothing to migrate" — which would also record the
// migration as done and rename the recovery copy away for good.
func TestSQLiteImportTreatsEmptyJSONStoreAsTruncated(t *testing.T) {
	dir := t.TempDir()
	seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "alice", "u2": "bob"}})
	if err := os.WriteFile(filepath.Join(dir, "kervan-store.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	st := openSQLite(t, dir)
	var users []string
	if err := st.List("users", &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0] != "alice" || users[1] != "bob" {
		t.Fatalf("imported users = %v, want [alice bob] recovered from kervan-store.json.bak", users)
	}

	// With no usable copy at all, opening must fail rather than start with an
	// empty store.
	broken := t.TempDir()
	for _, p := range []string{"kervan-store.json", "kervan-store.json.bak"} {
		if err := os.WriteFile(filepath.Join(broken, p), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := OpenWithOptions(broken, Options{Backend: BackendSQLite}); err == nil {
		_ = s.Close()
		t.Fatal("opened with an empty JSON store and no usable backup: data would silently disappear")
	}
}

// Concurrent first opens (server and CLI starting together) import once.
func TestSQLiteConcurrentFirstOpensImportOnce(t *testing.T) {
	dir := t.TempDir()
	seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "alice", "u2": "bob"}})
	var wg sync.WaitGroup
	stores := make([]*Store, 4)
	errs := make([]error, 4)
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stores[i], errs[i] = OpenWithOptions(dir, Options{Backend: BackendSQLite})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		defer stores[i].Close()
	}
	var users []string
	if err := stores[0].List("users", &users); err != nil || len(users) != 2 {
		t.Fatalf("users = %v (%v)", users, err)
	}
}

// Snapshot writes a consistent, verifiable copy while the store is in use,
// and BuildSQLiteFromJSON converts a JSON store file into a database.
func TestSQLiteSnapshotAndConversion(t *testing.T) {
	dir := t.TempDir()
	st := openSQLite(t, dir)
	if err := st.Put("users", "u1", "alice"); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := st.Snapshot(snap); err != nil {
		t.Fatal(err)
	}
	if err := VerifySQLiteFile(snap); err != nil {
		t.Fatalf("snapshot does not verify: %v", err)
	}
	if exists(t, snap+"-wal") || exists(t, snap+"-shm") {
		t.Fatal("verification left -wal/-shm files behind")
	}
	if info, _ := os.Stat(snap); info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("snapshot mode %v, want owner-only", info.Mode().Perm())
	}
	if st.Path() != filepath.Join(dir, sqliteFileName) {
		t.Fatalf("Path() = %q", st.Path())
	}

	jsonDir := t.TempDir()
	seedJSONStore(t, jsonDir, map[string]map[string]string{"users": {"u1": "carol"}})
	converted := filepath.Join(t.TempDir(), "converted.db")
	if err := BuildSQLiteFromJSON(filepath.Join(jsonDir, "kervan-store.json"), converted); err != nil {
		t.Fatal(err)
	}
	if err := VerifySQLiteFile(converted); err != nil {
		t.Fatal(err)
	}
	restoredDir := t.TempDir()
	if err := ReplaceSQLiteFile(converted, filepath.Join(restoredDir, sqliteFileName)); err != nil {
		t.Fatal(err)
	}
	got := openSQLite(t, restoredDir)
	var v string
	if err := got.Get("users", "u1", &v); err != nil || v != "carol" {
		t.Fatalf("converted store: %q (%v)", v, err)
	}

	// A file that is not a Kervan store is rejected.
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySQLiteFile(junk); err == nil {
		t.Fatal("garbage accepted as a store database")
	}

	jsonStore, err := OpenWithOptions(t.TempDir(), Options{Backend: BackendJSON})
	if err != nil {
		t.Fatal(err)
	}
	defer jsonStore.Close()
	if err := jsonStore.Snapshot(filepath.Join(t.TempDir(), "x")); !errors.Is(err, ErrSnapshotUnsupported) {
		t.Fatalf("JSON Snapshot = %v, want ErrSnapshotUnsupported", err)
	}
}

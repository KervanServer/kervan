package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func openSQLite(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := OpenWithOptions(dir, Options{Backend: BackendSQLite})
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenSelectsBackend(t *testing.T) {
	t.Setenv(backendEnv, "")
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Backend() != BackendJSON {
		t.Fatalf("default backend = %q, want %q", st.Backend(), BackendJSON)
	}
	_ = st.Close()

	sq := openSQLite(t, t.TempDir())
	if sq.Backend() != BackendSQLite {
		t.Fatalf("backend = %q, want %q", sq.Backend(), BackendSQLite)
	}

	t.Setenv(backendEnv, "sqlite")
	viaEnv, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer viaEnv.Close()
	if viaEnv.Backend() != BackendSQLite {
		t.Fatalf("KERVAN_STORE_BACKEND=sqlite gave %q", viaEnv.Backend())
	}

	if _, err := OpenWithOptions(t.TempDir(), Options{Backend: "bolt"}); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

// The SQLite backend keeps its data in kervan-store.db in WAL mode and
// leaves no kervan-store.json behind; its files are owner-only.
func TestSQLiteBackendFiles(t *testing.T) {
	dir := t.TempDir()
	st := openSQLite(t, dir)
	if err := st.Put("users", "alice", map[string]string{"username": "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kervan-store.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sqlite backend created kervan-store.json: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, sqliteFileName))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX group/other permission bits: os.Chmod there only
	// toggles the read-only attribute, so a 0600 file still reports 0666.
	// Production still calls Chmod(0o600) on the db and its -wal/-shm files
	// (sqlite_backend.go), but the restriction is not expressible there.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("%s has mode %v, want no group/other access", sqliteFileName, perm)
		}
	}
	var mode string
	if err := st.b.(*sqliteBackend).db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
}

// A failing Update leaves nothing behind, Tx reads see the transaction's own
// writes, and List returns values in key order.
func TestSQLiteUpdateSemantics(t *testing.T) {
	st := openSQLite(t, t.TempDir())
	boom := errors.New("boom")
	err := st.Update(func(tx *Tx) error {
		if err := tx.Put("c", "a", "1"); err != nil {
			return err
		}
		var v string
		if err := tx.Get("c", "a", &v); err != nil || v != "1" {
			t.Errorf("read-own-write inside tx: %q %v", v, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update error = %v, want boom", err)
	}
	var v string
	if err := st.Get("c", "a", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back write is visible: %q %v", v, err)
	}

	for _, k := range []string{"b", "c", "a"} {
		if err := st.Put("c", k, k); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Update(func(tx *Tx) error { tx.Delete("c", "b"); return nil }); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := st.List("c", &got); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[a c]" {
		t.Fatalf("List = %v, want [a c]", got)
	}
	var empty []string
	if err := st.List("nothing", &empty); err != nil || len(empty) != 0 {
		t.Fatalf("List of empty collection = %v %v", empty, err)
	}
}

// Read-check-write transactions from two Store instances on the same file
// (the server and a CLI command) serialise: a counter incremented by both
// loses no increment.
func TestSQLiteUpdateIsAtomicAcrossStores(t *testing.T) {
	dir := t.TempDir()
	a, b := openSQLite(t, dir), openSQLite(t, dir)
	if err := a.Put("c", "n", 0); err != nil {
		t.Fatal(err)
	}
	const perStore = 100
	var wg sync.WaitGroup
	for _, st := range []*Store{a, b} {
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(st *Store) {
				defer wg.Done()
				for i := 0; i < perStore/4; i++ {
					if err := st.Update(func(tx *Tx) error {
						var n int
						if err := tx.Get("c", "n", &n); err != nil {
							return err
						}
						return tx.Put("c", "n", n+1)
					}); err != nil {
						t.Error(err)
						return
					}
				}
			}(st)
		}
	}
	wg.Wait()
	var n int
	if err := b.Get("c", "n", &n); err != nil {
		t.Fatal(err)
	}
	if n != 2*perStore {
		t.Fatalf("counter = %d, want %d: transactions from two stores interleaved", n, 2*perStore)
	}
}

// The same counter test with a real second process, so SQLite's file
// locking is exercised between processes, not just between handles.
func TestSQLiteUpdateIsAtomicAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("KERVAN_STORE_HELPER_DIR"); dir != "" {
		st, err := OpenWithOptions(dir, Options{Backend: BackendSQLite})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		n, _ := strconv.Atoi(os.Getenv("KERVAN_STORE_HELPER_N"))
		for i := 0; i < n; i++ {
			if err := st.Update(func(tx *Tx) error {
				var v int
				if err := tx.Get("c", "n", &v); err != nil {
					return err
				}
				return tx.Put("c", "n", v+1)
			}); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(3)
			}
		}
		_ = st.Close()
		os.Exit(0)
	}

	dir := t.TempDir()
	st := openSQLite(t, dir)
	if err := st.Put("c", "n", 0); err != nil {
		t.Fatal(err)
	}
	const perProc, procs = 100, 3
	var wg sync.WaitGroup
	errs := make(chan error, procs+1)
	for p := 0; p < procs; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteUpdateIsAtomicAcrossProcesses$")
			cmd.Env = append(os.Environ(), "KERVAN_STORE_HELPER_DIR="+dir, "KERVAN_STORE_HELPER_N="+strconv.Itoa(perProc))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("helper process: %v: %s", err, out)
			}
		}()
	}
	wg.Add(1)
	go func() { // this process takes part too
		defer wg.Done()
		for i := 0; i < perProc; i++ {
			if err := st.Update(func(tx *Tx) error {
				var v int
				if err := tx.Get("c", "n", &v); err != nil {
					return err
				}
				return tx.Put("c", "n", v+1)
			}); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var n int
	if err := st.Get("c", "n", &n); err != nil {
		t.Fatal(err)
	}
	if want := perProc * (procs + 1); n != want {
		t.Fatalf("counter = %d, want %d: transactions from separate processes interleaved", n, want)
	}
}

// Reads from another goroutine are not blocked by a transaction in
// progress (WAL), and see the last committed value.
func TestSQLiteReadersNotBlockedByWriter(t *testing.T) {
	st := openSQLite(t, t.TempDir())
	if err := st.Put("c", "k", "old"); err != nil {
		t.Fatal(err)
	}
	runWithin(t, func() {
		_ = st.Update(func(tx *Tx) error {
			if err := tx.Put("c", "k", "new"); err != nil {
				return err
			}
			result := make(chan string, 1)
			go func() {
				var v string
				_ = st.Get("c", "k", &v)
				result <- v
			}()
			select {
			case v := <-result:
				if v != "old" {
					t.Errorf("reader saw uncommitted value %q", v)
				}
			case <-time.After(3 * time.Second):
				t.Error("reader blocked by a transaction in progress")
			}
			return nil
		})
	})
}

// The server and a CLI command opening the store for the first time race to
// create kervan-store.db. SQLite answers that race with SQLITE_BUSY after
// microseconds without consulting busy_timeout, so every concurrent opener
// must still succeed and see the imported records.
// An already-initialised store is the common case, so openSQLiteBackend must
// not need the cross-process lock there. Holding that lock externally and then
// opening proves it: if initSchema still locked, this would block until the
// watchdog fires. flock is per open file description, so a second lockFile in
// the same process genuinely contends.
func TestSQLiteOpenSkipsLockWhenSchemaExists(t *testing.T) {
	dir := t.TempDir()
	openSQLite(t, dir) // creates the schema

	unlock, err := lockFile(filepath.Join(dir, sqliteFileName) + ".lock")
	if err != nil {
		t.Fatalf("hold store lock: %v", err)
	}
	defer unlock()

	runWithin(t, func() {
		st, err := OpenWithOptions(dir, Options{Backend: BackendSQLite})
		if err != nil {
			t.Errorf("open while another holder has the store lock: %v", err)
			return
		}
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
}

func TestSQLiteConcurrentFirstOpensInitialiseSchema(t *testing.T) {
	const openers, rounds = 8, 40
	for r := 0; r < rounds; r++ {
		dir := t.TempDir()
		seedJSONStore(t, dir, map[string]map[string]string{"users": {"u1": "alice", "u2": "bob"}})

		gate := make(chan struct{})
		stores := make([]*Store, openers)
		errs := make([]error, openers)
		var wg sync.WaitGroup
		for i := range stores {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-gate // every opener reaches its first open together
				stores[i], errs[i] = OpenWithOptions(dir, Options{Backend: BackendSQLite})
			}(i)
		}
		close(gate)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d: concurrent first open %d: %v", r, i, err)
			}
			var users []string
			if err := stores[i].List("users", &users); err != nil {
				t.Fatalf("round %d: open %d: list imported users: %v", r, i, err)
			}
			if len(users) != 2 || users[0] != "alice" || users[1] != "bob" {
				t.Fatalf("round %d: open %d: imported users = %v, want [alice bob]", r, i, users)
			}
			if err := stores[i].Close(); err != nil {
				t.Fatalf("round %d: open %d: close: %v", r, i, err)
			}
		}
	}
}

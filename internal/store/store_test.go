package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// requireJSONBackend skips tests about the JSON backend's files (the .bak
// recovery copy, atomic temp files) when the suite runs against another
// backend via KERVAN_STORE_BACKEND.
func requireJSONBackend(t *testing.T) {
	t.Helper()
	if b := os.Getenv(backendEnv); b != "" && b != BackendJSON {
		t.Skipf("tests the JSON backend's files; suite runs on %q", b)
	}
}

func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if err := st.Put("users", "alice", map[string]any{"username": "alice", "admin": true}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() reopen: %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	var got map[string]any
	if err := reopened.Get("users", "alice", &got); err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got["username"] != "alice" {
		t.Fatalf("unexpected username: %#v", got)
	}
}

func TestStoreAtomicFlushLeavesNoTempFiles(t *testing.T) {
	requireJSONBackend(t)
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	if err := st.Put("users", "alice", map[string]any{"username": "alice"}); err != nil {
		t.Fatalf("Put(): %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "kervan-store.json.*.tmp"))
	if err != nil {
		t.Fatalf("Glob(): %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no temp files left behind, got %v", matches)
	}
}

func TestStoreDeletePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if err := st.Put("users", "alice", map[string]any{"username": "alice"}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if err := st.Delete("users", "alice"); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() reopen: %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	var got map[string]any
	if err := reopened.Get("users", "alice", &got); err == nil {
		t.Fatalf("expected record to stay deleted, got %#v", got)
	}
}

// Store.Close retries a write that failed to reach disk. The store file is
// made a directory so writeFileAtomically's rename fails regardless of
// privilege (chmod is ignored by root): the pending record then exists ONLY
// in memory, so clearing the obstruction and closing proves Close is what
// persists it.
//
// Blocking only kervan-store.json.bak would NOT test this: flush writes the
// primary first, so the record is already on disk before Close runs.
func TestStoreClosePersistsWriteThatFailedToReachDisk(t *testing.T) {
	requireJSONBackend(t)
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if err := st.Put("users", "before", "kept"); err != nil {
		t.Fatalf("seed Put(): %v", err)
	}

	primary := filepath.Join(dir, "kervan-store.json")
	if err := os.Remove(primary); err != nil {
		t.Fatalf("remove store file: %v", err)
	}
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatalf("mkdir over store file: %v", err)
	}

	if err := st.Put("users", "pending", "must-survive"); err == nil {
		t.Fatal("setup broken: Put succeeded despite an unwritable store file")
	}
	var pending string
	if err := st.Get("users", "pending", &pending); err != nil {
		t.Fatalf("pending record must stay readable in memory: %v", err)
	}

	if err := os.Remove(primary); err != nil {
		t.Fatalf("clear obstruction: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() should retry the failed write: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	var got string
	if err := reopened.Get("users", "pending", &got); err != nil {
		t.Fatalf("Close() did not persist the failed write: %v", err)
	}
	if got != pending {
		t.Fatalf("persisted value = %q, want %q", got, pending)
	}
	if err := reopened.Get("users", "before", &got); err != nil || got != "kept" {
		t.Fatalf("earlier record lost: %q (%v)", got, err)
	}
}

func TestStoreRecoversFromBackupWhenPrimaryIsCorrupt(t *testing.T) {
	requireJSONBackend(t)
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if err := st.Put("users", "alice", map[string]any{"username": "alice"}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	mainPath := filepath.Join(dir, "kervan-store.json")
	if err := os.WriteFile(mainPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile(corrupt): %v", err)
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() recover: %v", err)
	}
	t.Cleanup(func() {
		_ = recovered.Close()
	})

	var got map[string]any
	if err := recovered.Get("users", "alice", &got); err != nil {
		t.Fatalf("Get() after recovery: %v", err)
	}
	if got["username"] != "alice" {
		t.Fatalf("unexpected recovered payload: %#v", got)
	}
}

// A zero-length kervan-store.json is a truncated write, not a store with no
// records: the store is marshalled from a map, so "{}" is the smallest file the
// backend can ever write. Reading one as an empty store loaded zero records and,
// worse, returned before the .bak fallback that holds them.
func TestStoreRecoversFromBackupWhenPrimaryIsEmpty(t *testing.T) {
	requireJSONBackend(t)
	dir := t.TempDir()

	seed := func(t *testing.T) {
		t.Helper()
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("Open(): %v", err)
		}
		if err := st.Put("users", "alice", map[string]any{"username": "alice"}); err != nil {
			_ = st.Close()
			t.Fatalf("Put(): %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}
	seed(t)

	// Premise: a store with no records is written as "{}", never as zero bytes.
	emptied, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() emptied: %v", err)
	}
	if err := emptied.Delete("users", "alice"); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if err := emptied.Close(); err != nil {
		t.Fatalf("Close() emptied: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "kervan-store.json")); err != nil {
		t.Fatalf("ReadFile(emptied store): %v", err)
	} else if len(raw) == 0 {
		t.Fatal("premise broken: the backend wrote a zero-length store file")
	}

	// Truncate the primary, leaving the .bak holding the record.
	seed(t)
	if err := os.WriteFile(filepath.Join(dir, "kervan-store.json"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(truncated): %v", err)
	}
	if bak, err := os.Stat(filepath.Join(dir, "kervan-store.json.bak")); err != nil || bak.Size() == 0 {
		t.Fatalf("setup: .bak should hold the record, got %v (%v)", bak, err)
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() recover: %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })

	var got map[string]any
	if err := recovered.Get("users", "alice", &got); err != nil {
		t.Fatalf("Get() after recovery: %v", err)
	}
	if got["username"] != "alice" {
		t.Fatalf("unexpected recovered payload: %#v", got)
	}
}

func TestStoreRecoversFromBackupWhenPrimaryIsMissing(t *testing.T) {
	requireJSONBackend(t)
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if err := st.Put("users", "alice", map[string]any{"username": "alice"}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	mainPath := filepath.Join(dir, "kervan-store.json")
	if err := os.Remove(mainPath); err != nil {
		t.Fatalf("Remove(main): %v", err)
	}

	recovered, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() recover missing primary: %v", err)
	}
	t.Cleanup(func() {
		_ = recovered.Close()
	})

	var got map[string]any
	if err := recovered.Get("users", "alice", &got); err != nil {
		t.Fatalf("Get() after missing primary recovery: %v", err)
	}
	if got["username"] != "alice" {
		t.Fatalf("unexpected recovered payload: %#v", got)
	}
}

func TestStoreConcurrentPutsPersistAllEntries(t *testing.T) {
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("user-%02d", i)
			if err := st.Put("users", key, map[string]any{"username": key}); err != nil {
				t.Errorf("Put(%s): %v", key, err)
			}
		}()
	}
	wg.Wait()
	if err := st.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() reopen: %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	var users []map[string]any
	if err := reopened.List("users", &users); err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(users) != 50 {
		t.Fatalf("expected 50 persisted users, got %d", len(users))
	}
}

// Two Store instances on one directory model the running server and a CLI
// command. Each must see the other's writes, and neither write nor Close may
// erase what the other persisted.
func TestStoreConcurrentProcessesDoNotLoseWrites(t *testing.T) {
	dir := t.TempDir()
	server, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.Put("users", "admin", "a"); err != nil {
		t.Fatal(err)
	}

	cli, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Put("users", "bob", "b"); err != nil {
		t.Fatal(err)
	}

	// The server sees the CLI's user without a restart...
	var got string
	if err := server.Get("users", "bob", &got); err != nil || got != "b" {
		t.Fatalf("server did not observe CLI write: %q %v", got, err)
	}
	// ...and its own next write (e.g. a last-login update) keeps it.
	if err := server.Put("users", "admin", "a2"); err != nil {
		t.Fatal(err)
	}
	// A stale CLI Close must not roll the server's write back.
	if err := cli.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var users []string
	if err := reopened.List("users", &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0] != "a2" || users[1] != "b" {
		t.Fatalf("persisted users = %v, want [a2 b]", users)
	}

	// Deletes from another process are observed too.
	if err := reopened.Delete("users", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := server.Get("users", "bob", &got); err != ErrNotFound {
		t.Fatalf("deleted key still visible to server: %v", err)
	}
}

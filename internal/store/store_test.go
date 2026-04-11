package store

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestStoreRecoversFromBackupWhenPrimaryIsCorrupt(t *testing.T) {
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

func TestStoreRecoversFromBackupWhenPrimaryIsMissing(t *testing.T) {
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

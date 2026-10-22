package s3

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"testing"
)

// Exclusive create is an atomic no-overwrite contract (POSIX open(2) EEXIST,
// the memory backend's exclusive-create guard, os.OpenFile for the local
// backend). Before the pre-flight refusal in Open, an O_CREATE|O_EXCL open
// of an existing object returned a writable handle that re-uploaded and
// clobbered the object on Close — silently merging over the previous content
// without O_TRUNC, or replacing it wholesale with O_TRUNC. SFTP SSH_FXF_EXCL
// opens, FTP STOU and the API upload staging path all rely on the refusal.

func newExclusiveCreateBackend(t *testing.T) *Backend {
	t.Helper()
	server := httptest.NewServer(newFakeS3Server())
	t.Cleanup(server.Close)
	backend, err := New(Options{
		Endpoint:     server.URL,
		Bucket:       "test-bucket",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func writeExclusiveCreateFile(t *testing.T, backend *Backend, name, content string) {
	t.Helper()
	file, err := backend.Open(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", name, err)
	}
	if _, err := io.Copy(file, bytes.NewBufferString(content)); err != nil {
		t.Fatalf("Copy(%s) error = %v", name, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", name, err)
	}
}

func readExclusiveCreateFile(t *testing.T, backend *Backend, name string) string {
	t.Helper()
	file, err := backend.Open(name, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestS3ExclusiveCreateRefusesExisting(t *testing.T) {

	t.Run("exclusive create on existing object", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		const original = "existing-content"
		writeExclusiveCreateFile(t, backend, "/taken.txt", original)

		f, err := backend.Open("/taken.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			t.Fatalf("FAIL: O_CREATE|O_EXCL on an existing object succeeded; want os.ErrExist like the memory/local backends")
		}
		if !os.IsExist(err) {
			t.Fatalf("FAIL: O_CREATE|O_EXCL on an existing object returned %v; want os.ErrExist", err)
		}
		if got := readExclusiveCreateFile(t, backend, "/taken.txt"); got != original {
			t.Errorf("FAIL: refused exclusive create changed /taken.txt to %q, want %q", got, original)
		}
	})

	t.Run("exclusive create with O_TRUNC on existing object", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		const original = "keep-me"
		writeExclusiveCreateFile(t, backend, "/victim.txt", original)

		f, err := backend.Open("/victim.txt", os.O_CREATE|os.O_EXCL|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			t.Fatalf("FAIL: O_CREATE|O_EXCL|O_TRUNC on an existing object succeeded; want os.ErrExist (O_EXCL must win over O_TRUNC)")
		}
		if !os.IsExist(err) {
			t.Fatalf("FAIL: O_CREATE|O_EXCL|O_TRUNC on an existing object returned %v; want os.ErrExist", err)
		}
		if got := readExclusiveCreateFile(t, backend, "/victim.txt"); got != original {
			t.Errorf("FAIL: refused exclusive create changed /victim.txt to %q, want %q", got, original)
		}
	})

	t.Run("exclusive create on existing directory", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		if err := backend.MkdirAll("/dir/child", 0o755); err != nil {
			t.Fatal(err)
		}
		writeExclusiveCreateFile(t, backend, "/dir/child/data.txt", "data")
		if f, err := backend.Open("/dir", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			t.Fatalf("FAIL: O_CREATE|O_EXCL on an existing directory succeeded; want os.ErrExist")
		} else if !os.IsExist(err) {
			t.Fatalf("FAIL: O_CREATE|O_EXCL on an existing directory returned %v; want os.ErrExist", err)
		}
	})

	t.Run("control exclusive create on fresh path succeeds then refuses re-create", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		f, err := backend.Open("/fresh.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("control: exclusive create on a fresh path failed: %v", err)
		}
		if _, err := f.Write([]byte("created")); err != nil {
			t.Fatalf("control: write failed: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("control: close failed: %v", err)
		}
		if got := readExclusiveCreateFile(t, backend, "/fresh.txt"); got != "created" {
			t.Errorf("control: /fresh.txt = %q, want %q", got, "created")
		}
		if f, err := backend.Open("/fresh.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			t.Fatal("control: second exclusive create of the same path succeeded; want os.ErrExist")
		}
	})

	t.Run("control plain create over existing still replaces", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		writeExclusiveCreateFile(t, backend, "/replace.txt", "old")
		f, err := backend.Open("/replace.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("control: plain O_CREATE over existing failed: %v", err)
		}
		if _, err := f.Write([]byte("new")); err != nil {
			t.Fatalf("control: write failed: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("control: close failed: %v", err)
		}
		if got := readExclusiveCreateFile(t, backend, "/replace.txt"); got != "new" {
			t.Errorf("control: /replace.txt = %q, want %q", got, "new")
		}
	})

	t.Run("control write without create on missing path still fails", func(t *testing.T) {
		backend := newExclusiveCreateBackend(t)
		if f, err := backend.Open("/missing.txt", os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			t.Fatal("control: O_WRONLY without O_CREATE on a missing path succeeded; want os.ErrNotExist")
		}
	})

}

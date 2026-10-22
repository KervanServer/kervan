package s3

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"testing"
)

// Renaming onto an existing destination follows the replace contract of
// UserVFS.Rename, rename(2) and the memory/local backends: a file may replace
// an existing file, and a directory may replace an empty directory; every
// other existing-target combination must be rejected. Before the destination
// guard, Rename copied without inspecting the target: a file object at the
// exact key shadowed a directory (Stat reported a file, ReadDir failed, the
// children stranded), a directory rename onto a file stranded the moved
// subtree behind the file object, and a directory rename onto a non-empty
// directory silently overwrote colliding destination keys.

func newRenameDestinationBackend(t *testing.T) *Backend {
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

func writeRenameDestinationFile(t *testing.T, backend *Backend, name, content string) {
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

func readRenameDestinationFile(t *testing.T, backend *Backend, name string) string {
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

func TestS3RenameOntoExistingDestinationIsRejected(t *testing.T) {

	t.Run("directory onto existing file", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/srcd/inner", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/srcd/inner/data.txt", "data")
		writeRenameDestinationFile(t, backend, "/target.txt", "target")
		if err := backend.Rename("/srcd", "/target.txt"); err == nil {
			t.Fatalf("FAIL: renaming a directory onto an existing file succeeded; want rejection like the local/POSIX backend")
		}
		if got := readRenameDestinationFile(t, backend, "/target.txt"); got != "target" {
			t.Errorf("FAIL: /target.txt = %q, want %q", got, "target")
		}
		if got := readRenameDestinationFile(t, backend, "/srcd/inner/data.txt"); got != "data" {
			t.Errorf("FAIL: /srcd/inner/data.txt = %q, want %q", got, "data")
		}
	})

	t.Run("file onto existing directory", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/victim/inner", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/victim/inner/child.txt", "child-content")
		writeRenameDestinationFile(t, backend, "/file.txt", "file-content")
		if err := backend.Rename("/file.txt", "/victim"); err == nil {
			t.Fatalf("FAIL: renaming a file onto an existing directory succeeded; want rejection like the local/POSIX backend")
		}
		if info, err := backend.Stat("/victim"); err != nil || !info.IsDir() {
			t.Errorf("FAIL: refused rename left /victim as a non-directory: info=%v err=%v", info, err)
		}
		if got := readRenameDestinationFile(t, backend, "/victim/inner/child.txt"); got != "child-content" {
			t.Errorf("FAIL: /victim/inner/child.txt = %q, want %q", got, "child-content")
		}
		if got := readRenameDestinationFile(t, backend, "/file.txt"); got != "file-content" {
			t.Errorf("FAIL: source /file.txt lost after refused rename: %q", got)
		}
	})

	t.Run("directory onto existing non-empty directory", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/a", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := backend.MkdirAll("/b", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/a/keep.txt", "source-content")
		writeRenameDestinationFile(t, backend, "/b/keep.txt", "dest-content")
		writeRenameDestinationFile(t, backend, "/b/other.txt", "other")
		if err := backend.Rename("/a", "/b"); err == nil {
			t.Fatalf("FAIL: renaming a directory onto a non-empty directory succeeded; want rejection like the local/POSIX backend")
		}
		if got := readRenameDestinationFile(t, backend, "/b/keep.txt"); got != "dest-content" {
			t.Errorf("FAIL: /b/keep.txt = %q, want %q", got, "dest-content")
		}
		if got := readRenameDestinationFile(t, backend, "/b/other.txt"); got != "other" {
			t.Errorf("FAIL: /b/other.txt = %q, want %q", got, "other")
		}
		if got := readRenameDestinationFile(t, backend, "/a/keep.txt"); got != "source-content" {
			t.Errorf("FAIL: /a/keep.txt = %q, want %q", got, "source-content")
		}
	})

	t.Run("control file onto existing file replaces", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		writeRenameDestinationFile(t, backend, "/one.txt", "one")
		writeRenameDestinationFile(t, backend, "/two.txt", "two")
		if err := backend.Rename("/one.txt", "/two.txt"); err != nil {
			t.Fatalf("control: file-over-file rename failed: %v", err)
		}
		if got := readRenameDestinationFile(t, backend, "/two.txt"); got != "one" {
			t.Errorf("control: /two.txt = %q, want %q", got, "one")
		}
		if _, err := backend.Stat("/one.txt"); !os.IsNotExist(err) {
			t.Errorf("control: /one.txt still exists after rename: %v", err)
		}
	})

	t.Run("control directory rename to a fresh path succeeds", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/fresh-src/inner", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/fresh-src/inner/data.txt", "data")
		if err := backend.Rename("/fresh-src", "/fresh-dst"); err != nil {
			t.Fatalf("control: directory rename to a fresh path failed: %v", err)
		}
		if got := readRenameDestinationFile(t, backend, "/fresh-dst/inner/data.txt"); got != "data" {
			t.Errorf("control: /fresh-dst/inner/data.txt = %q, want %q", got, "data")
		}
	})

	t.Run("control directory onto existing empty directory replaces", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/empty-src", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := backend.MkdirAll("/empty-dst", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/empty-src/file.txt", "moved")
		if err := backend.Rename("/empty-src", "/empty-dst"); err != nil {
			t.Fatalf("control: dir-over-empty-dir rename failed: %v", err)
		}
		if got := readRenameDestinationFile(t, backend, "/empty-dst/file.txt"); got != "moved" {
			t.Errorf("control: /empty-dst/file.txt = %q, want %q", got, "moved")
		}
		if _, err := backend.Stat("/empty-src"); !os.IsNotExist(err) {
			t.Errorf("control: /empty-src still exists after rename: %v", err)
		}
	})

}

func TestS3RenameBoundaryTargets(t *testing.T) {
	t.Run("renaming onto the root is rejected", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/tree/inner", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/tree/inner/data.txt", "data")
		if err := backend.Rename("/tree", "/"); err == nil {
			t.Fatal("FAIL: renaming a directory onto the root succeeded; want rejection")
		}
		if got := readRenameDestinationFile(t, backend, "/tree/inner/data.txt"); got != "data" {
			t.Errorf("FAIL: refused rename lost /tree/inner/data.txt: %q", got)
		}
		writeRenameDestinationFile(t, backend, "/plain.txt", "plain")
		if err := backend.Rename("/plain.txt", "/"); err == nil {
			t.Fatal("FAIL: renaming a file onto the root succeeded; want rejection")
		}
		if got := readRenameDestinationFile(t, backend, "/plain.txt"); got != "plain" {
			t.Errorf("FAIL: /plain.txt = %q after refused rename, want %q", got, "plain")
		}
	})

	t.Run("renaming into its own descendant is rejected", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/dir/child", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/dir/file.txt", "content")
		if err := backend.Rename("/dir", "/dir/child/moved"); err == nil {
			t.Fatal("FAIL: renaming a directory into its own descendant succeeded; want rejection")
		}
		if got := readRenameDestinationFile(t, backend, "/dir/file.txt"); got != "content" {
			t.Errorf("FAIL: /dir/file.txt = %q after refused rename, want %q", got, "content")
		}
	})

	t.Run("same-path rename is a no-op", func(t *testing.T) {
		backend := newRenameDestinationBackend(t)
		if err := backend.MkdirAll("/same", 0o755); err != nil {
			t.Fatal(err)
		}
		writeRenameDestinationFile(t, backend, "/same/file.txt", "kept")
		if err := backend.Rename("/same", "/same"); err != nil {
			t.Fatalf("same-path directory rename: %v", err)
		}
		if err := backend.Rename("/same/file.txt", "/same/file.txt"); err != nil {
			t.Fatalf("same-path file rename: %v", err)
		}
		if got := readRenameDestinationFile(t, backend, "/same/file.txt"); got != "kept" {
			t.Errorf("FAIL: /same/file.txt = %q after same-path renames, want %q", got, "kept")
		}
	})
}

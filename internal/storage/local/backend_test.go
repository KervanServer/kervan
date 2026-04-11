package local

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackendFileLifecycleAndPathSafety(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	backend, err := New(Options{Root: root, CreateRoot: true, SyncWrites: true})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}

	if err := backend.MkdirAll("/nested", 0); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	file, err := backend.Open("/nested/file.txt", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	info, err := backend.Stat("/nested/file.txt")
	if err != nil || info.Size() != 5 {
		t.Fatalf("unexpected stat: info=%v err=%v", info, err)
	}
	entries, err := backend.ReadDir("/nested")
	if err != nil || len(entries) != 1 || entries[0].Name() != "file.txt" {
		t.Fatalf("unexpected readdir: entries=%v err=%v", entries, err)
	}

	if err := backend.Rename("/nested/file.txt", "/nested/file2.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := backend.Chmod("/nested/file2.txt", 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	now := time.Now().Add(-time.Hour).UTC()
	if err := backend.Chtimes("/nested/file2.txt", now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, err := backend.Statvfs("/nested/file2.txt"); err != nil {
		t.Fatalf("statvfs: %v", err)
	}

	linkTarget := filepath.Join(root, "nested", "file2.txt")
	if err := backend.Symlink("/nested/file2.txt", "/nested/link.txt"); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if got, err := backend.Readlink("/nested/link.txt"); err != nil || got != "/nested/file2.txt" {
		t.Fatalf("unexpected readlink: got=%q err=%v target=%q", got, err, linkTarget)
	}

	if err := backend.Remove("/nested/link.txt"); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if err := backend.Remove("/nested/file2.txt"); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := backend.RemoveAll("/nested"); err != nil {
		t.Fatalf("removeall nested: %v", err)
	}
	if err := backend.RemoveAll("/"); !errors.Is(err, ErrCannotRemoveRoot) {
		t.Fatalf("expected root removal error, got %v", err)
	}
	if sanitized, err := backend.physicalPath("/../../escape"); err != nil {
		t.Fatalf("expected sanitized path to stay inside root, got err=%v", err)
	} else if filepath.Dir(sanitized) != root {
		t.Fatalf("expected sanitized path to stay within root, got %q", sanitized)
	}
}

func TestNewRejectsNonDirectoryRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	_, err := New(Options{Root: path})
	if !errors.Is(err, ErrRootNotDirectory) {
		t.Fatalf("expected ErrRootNotDirectory, got %v", err)
	}
}

func TestLocalFileReadDirDelegates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	backend, err := New(Options{Root: root, CreateRoot: true})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if err := backend.Mkdir("/dir", 0); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := backend.Open("/dir", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open dir: %v", err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("readdir on dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty dir entries, got %v", entries)
	}
}

func TestBackendAdditionalErrorAndMetadataPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if _, err := New(Options{Root: filepath.Join(root, "missing")}); err == nil {
		t.Fatal("expected missing root without CreateRoot to fail")
	}

	backend, err := New(Options{Root: root, CreateRoot: true, FilePermissions: 0o640, DirPermissions: 0o750})
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	if err := backend.Mkdir("/dir", 0); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	file, err := backend.Open("/dir/file.txt", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	info, err := backend.Stat("/dir/file.txt")
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Mode().IsDir() {
		t.Fatalf("expected created path to be a file, got mode=%v", info.Mode())
	}

	if err := backend.Symlink("/dir/file.txt", "/dir/link.txt"); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	linkInfo, err := backend.Lstat("/dir/link.txt")
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected lstat to preserve symlink bit, mode=%v", linkInfo.Mode())
	}
	if _, err := backend.Readlink("/dir/file.txt"); err == nil {
		t.Fatal("expected readlink on plain file to fail")
	}
	if err := backend.Remove("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected remove missing to fail with os.ErrNotExist, got %v", err)
	}
	if _, err := backend.ReadDir("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected readdir missing to fail with os.ErrNotExist, got %v", err)
	}
}

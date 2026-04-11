package memory

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestBackendFileLifecycle(t *testing.T) {
	backend := New()
	if err := backend.MkdirAll("/docs/sub", 0); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	file, err := backend.Open("/docs/sub/file.txt", os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek start: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := file.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("unexpected file contents: %q", string(buf))
	}
	if _, err := file.WriteAt([]byte("!"), 5); err != nil {
		t.Fatalf("writeat: %v", err)
	}
	if err := file.Truncate(3); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	stat, err := file.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if stat.Size() != 3 {
		t.Fatalf("expected size 3 after truncate, got %d", stat.Size())
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	info, err := backend.Stat("/docs/sub/file.txt")
	if err != nil || info.Size() != 3 {
		t.Fatalf("unexpected backend stat: info=%v err=%v", info, err)
	}
	if err := backend.Rename("/docs/sub/file.txt", "/docs/sub/file2.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := backend.Chmod("/docs/sub/file2.txt", 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	mtime := time.Now().Add(-time.Minute).UTC()
	if err := backend.Chtimes("/docs/sub/file2.txt", mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, err := backend.Statvfs("/docs/sub/file2.txt"); err != nil {
		t.Fatalf("statvfs: %v", err)
	}
	entries, err := backend.ReadDir("/docs/sub")
	if err != nil || len(entries) != 1 || entries[0].Name() != "file2.txt" {
		t.Fatalf("unexpected readdir: entries=%v err=%v", entries, err)
	}
	if err := backend.Remove("/docs/sub/file2.txt"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := backend.RemoveAll("/docs"); err != nil {
		t.Fatalf("removeall: %v", err)
	}
}

func TestBackendErrorsAndUnsupportedOperations(t *testing.T) {
	backend := New()

	if _, err := backend.Open("/missing.txt", os.O_RDONLY, 0); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing open to return os.ErrNotExist, got %v", err)
	}
	if err := backend.Mkdir("/dir", 0); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if _, err := backend.Open("/dir", os.O_RDONLY, 0); err == nil {
		t.Fatal("expected opening directory as file to fail")
	}
	if err := backend.Remove("/"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected root remove permission error, got %v", err)
	}
	if err := backend.RemoveAll("/"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected root removeall permission error, got %v", err)
	}
	if err := backend.Mkdir("/dir", 0); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected duplicate mkdir to fail with os.ErrExist, got %v", err)
	}
	if err := backend.Symlink("/a", "/b"); err == nil {
		t.Fatal("expected symlink to be unsupported")
	}
	if _, err := backend.Readlink("/b"); err == nil {
		t.Fatal("expected readlink to be unsupported")
	}
	if _, err := backend.ReadDir("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing readdir to fail, got %v", err)
	}

	file, err := backend.Open("/dir/file.txt", os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("expected negative seek to fail")
	}
	if _, err := file.Seek(0, 99); err == nil {
		t.Fatal("expected invalid whence to fail")
	}
	if err := file.Truncate(-1); err == nil {
		t.Fatal("expected negative truncate to fail")
	}
	if _, err := file.ReadDir(-1); err == nil {
		t.Fatal("expected file readdir to fail")
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := file.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected read after close to fail with os.ErrClosed, got %v", err)
	}
}

func TestBackendAdditionalEdgeCases(t *testing.T) {
	backend := New()

	if err := backend.MkdirAll("/parent", 0); err != nil {
		t.Fatalf("mkdirall parent: %v", err)
	}
	parentFile, err := backend.Open("/parent/file.txt", os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open parent file: %v", err)
	}
	if err := parentFile.Close(); err != nil {
		t.Fatalf("close parent file: %v", err)
	}

	if _, err := backend.Open("/parent/file.txt/child", os.O_CREATE|os.O_RDWR, 0); err == nil {
		t.Fatal("expected open under file parent to fail")
	}
	if err := backend.Mkdir("/parent/file.txt/child", 0); err == nil {
		t.Fatal("expected mkdir under file parent to fail")
	}
	if err := backend.Rename("/missing", "/new"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected rename of missing file to fail with os.ErrNotExist, got %v", err)
	}

	if err := backend.MkdirAll("/dir/sub", 0); err != nil {
		t.Fatalf("mkdirall dir/sub: %v", err)
	}
	if err := backend.Remove("/dir"); err == nil {
		t.Fatal("expected remove of non-empty directory to fail")
	}
	if _, err := backend.ReadDir("/parent/file.txt"); err == nil {
		t.Fatal("expected readdir on file path to fail")
	}
	if _, err := backend.Stat("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stat missing to fail with os.ErrNotExist, got %v", err)
	}
	if _, err := backend.Lstat("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lstat missing to fail with os.ErrNotExist, got %v", err)
	}
	if err := backend.Chmod("/missing", 0o600); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected chmod missing to fail with os.ErrNotExist, got %v", err)
	}
	if err := backend.Chtimes("/missing", time.Now(), time.Now()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected chtimes missing to fail with os.ErrNotExist, got %v", err)
	}
	if err := backend.Chown("/missing", 1, 1); err != nil {
		t.Fatalf("expected chown to be a no-op, got %v", err)
	}
}

func TestMemFileAppendSparseWriteAndReadAt(t *testing.T) {
	backend := New()
	file, err := backend.Open("/append.txt", os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open append file: %v", err)
	}
	if _, err := file.Write([]byte("abc")); err != nil {
		t.Fatalf("write seed data: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close append file: %v", err)
	}

	appendFile, err := backend.Open("/append.txt", os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open append mode: %v", err)
	}
	if _, err := appendFile.Write([]byte("def")); err != nil {
		t.Fatalf("append write: %v", err)
	}
	if err := appendFile.Close(); err != nil {
		t.Fatalf("close append mode file: %v", err)
	}

	verifyFile, err := backend.Open("/append.txt", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("reopen file: %v", err)
	}
	readAtBuf := make([]byte, 4)
	n, err := verifyFile.ReadAt(readAtBuf, 4)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected partial readat to end with EOF, got %v", err)
	}
	if n != 2 || string(readAtBuf[:n]) != "ef" {
		t.Fatalf("unexpected partial readat result n=%d buf=%q", n, string(readAtBuf[:n]))
	}
	if _, err := verifyFile.Seek(8, io.SeekStart); err != nil {
		t.Fatalf("seek beyond end: %v", err)
	}
	if _, err := verifyFile.Write([]byte("z")); err != nil {
		t.Fatalf("sparse write: %v", err)
	}
	if err := verifyFile.Close(); err != nil {
		t.Fatalf("close sparse file: %v", err)
	}

	finalFile, err := backend.Open("/append.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open final file: %v", err)
	}
	defer finalFile.Close()
	data := make([]byte, 9)
	if _, err := finalFile.Read(data); err != nil {
		t.Fatalf("read final contents: %v", err)
	}
	if string(data) != "abcdef\x00\x00z" {
		t.Fatalf("unexpected final file contents: %q", string(data))
	}
}

func TestMemFileCloseTwiceAndStatAfterClose(t *testing.T) {
	backend := New()
	file, err := backend.Open("/close.txt", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("second close should stay nil, got %v", err)
	}
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("expected stat after close to fail with os.ErrClosed, got %v", err)
	}
}

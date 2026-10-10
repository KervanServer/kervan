package vfs_test

import (
	"errors"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

type quotaStub struct {
	used     int64
	max      int64
	files    int64
	maxFiles int64
}

func (q *quotaStub) OnFileCreated() error {
	if q.maxFiles > 0 && q.files+1 > q.maxFiles {
		return errors.New("file count quota exceeded")
	}
	q.files++
	return nil
}

func (q *quotaStub) OnFilesRemoved(n int64) { q.files = max(q.files-n, 0) }

func (q *quotaStub) OnGrow(n int64) error {
	if q.max > 0 && q.used+n > q.max {
		return errors.New("storage quota exceeded")
	}
	q.used += n
	return nil
}

func (q *quotaStub) OnShrink(n int64) {
	q.used -= n
	if q.used < 0 {
		q.used = 0
	}
}

func TestUserVFSQuotaAndMaxFileSize(t *testing.T) {
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)

	tracker := &quotaStub{max: 10}

	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload:      true,
		Download:    true,
		Delete:      true,
		Rename:      true,
		CreateDir:   true,
		ListDir:     true,
		MaxFileSize: 5,
	}, tracker)

	file, err := fsys.Open("/a.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(/a.txt) error = %v", err)
	}
	if _, err := file.Write([]byte("12345")); err != nil {
		t.Fatalf("Write(/a.txt) error = %v", err)
	}
	if _, err := file.Write([]byte("6")); !errors.Is(err, vfs.ErrFileTooLarge) {
		t.Fatalf("Write over file limit error = %v, want ErrFileTooLarge", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(/a.txt) error = %v", err)
	}

	file, err = fsys.Open("/b.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(/b.txt) error = %v", err)
	}
	if _, err := file.Write([]byte("12345")); err != nil {
		t.Fatalf("Write(/b.txt) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(/b.txt) error = %v", err)
	}

	file, err = fsys.Open("/c.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(/c.txt) error = %v", err)
	}
	if _, err := file.Write([]byte("1")); err == nil || err.Error() != "storage quota exceeded" {
		t.Fatalf("Write over quota error = %v, want storage quota exceeded", err)
	}
	_ = file.Close()

	if err := fsys.Remove("/b.txt"); err != nil {
		t.Fatalf("Remove(/b.txt) error = %v", err)
	}

	file, err = fsys.Open("/c.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(/c.txt) second error = %v", err)
	}
	if _, err := file.Write([]byte("1234")); err != nil {
		t.Fatalf("Write(/c.txt) after reclaim error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(/c.txt) second error = %v", err)
	}
}

func TestUserVFSFileCountQuotaAndRenameOverwrite(t *testing.T) {
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	tracker := &quotaStub{maxFiles: 2}
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true}, tracker)
	write := func(name, content string) error {
		f, err := fsys.Open(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(content))
		_ = f.Close()
		return err
	}
	if err := write("/a", "aaaa"); err != nil {
		t.Fatal(err)
	}
	if err := write("/b", "bb"); err != nil {
		t.Fatal(err)
	}
	if err := write("/c", "c"); err == nil {
		t.Fatal("third file created over a 2-file quota")
	}
	// Overwriting an existing file is not a new file.
	if err := write("/a", "a2"); err != nil {
		t.Fatalf("overwrite refused: %v", err)
	}
	if tracker.files != 2 {
		t.Fatalf("files = %d, want 2", tracker.files)
	}
	// Renaming onto an existing file releases the replaced file's usage.
	usedBefore := tracker.used
	if err := fsys.Rename("/b", "/a"); err != nil {
		t.Fatal(err)
	}
	if tracker.files != 1 || tracker.used != usedBefore-2 {
		t.Fatalf("after rename overwrite: files=%d used=%d (before %d)", tracker.files, tracker.used, usedBefore)
	}
	if err := write("/c", "c"); err != nil {
		t.Fatalf("slot not freed by rename overwrite: %v", err)
	}
	// Deleting a directory tree releases all its files.
	_ = fsys.Mkdir("/d", 0o755)
	_ = fsys.Remove("/c")
	if err := write("/d/x", "x"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.RemoveAll("/d"); err != nil {
		t.Fatal(err)
	}
	if tracker.files != 1 {
		t.Fatalf("files after RemoveAll = %d, want 1", tracker.files)
	}
}

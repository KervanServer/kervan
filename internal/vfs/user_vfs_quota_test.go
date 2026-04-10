package vfs_test

import (
	"errors"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

type quotaStub struct {
	used int64
	max  int64
}

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

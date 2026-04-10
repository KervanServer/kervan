package vfs

import (
	"io/fs"
	"os"
	"testing"
	"time"
)

type stubFS struct{}

func (stubFS) Open(name string, flags int, perm os.FileMode) (File, error) {
	return nil, os.ErrNotExist
}
func (stubFS) Stat(name string) (os.FileInfo, error)             { return nil, os.ErrNotExist }
func (stubFS) Lstat(name string) (os.FileInfo, error)            { return nil, os.ErrNotExist }
func (stubFS) Rename(oldname, newname string) error              { return nil }
func (stubFS) Remove(name string) error                          { return nil }
func (stubFS) RemoveAll(name string) error                       { return nil }
func (stubFS) Mkdir(name string, perm os.FileMode) error         { return nil }
func (stubFS) MkdirAll(path string, perm os.FileMode) error      { return nil }
func (stubFS) ReadDir(name string) ([]fs.DirEntry, error)        { return nil, nil }
func (stubFS) Symlink(oldname, newname string) error             { return nil }
func (stubFS) Readlink(name string) (string, error)              { return "", nil }
func (stubFS) Chmod(name string, mode os.FileMode) error         { return nil }
func (stubFS) Chown(name string, uid, gid int) error             { return nil }
func (stubFS) Chtimes(name string, atime, mtime time.Time) error { return nil }
func (stubFS) Statvfs(path string) (*StatVFS, error)             { return &StatVFS{}, nil }

func TestMountLookupRootCoversSubpaths(t *testing.T) {
	mt := NewMountTable()
	mt.Mount("/", stubFS{}, false)

	_, rel, _, err := mt.Lookup("/docs/file.txt")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	if rel != "/docs/file.txt" {
		t.Fatalf("unexpected relative path: %s", rel)
	}
}

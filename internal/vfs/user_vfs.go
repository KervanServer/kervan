package vfs

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

type QuotaTracker interface {
	OnWrite(n int64) error
}

type UserVFS struct {
	mounts      *MountTable
	resolver    *Resolver
	permissions *UserPermissions
	quota       QuotaTracker
}

func NewUserVFS(mounts *MountTable, perms *UserPermissions, quota QuotaTracker) *UserVFS {
	if perms == nil {
		perms = &UserPermissions{
			Upload:    true,
			Download:  true,
			Delete:    true,
			Rename:    true,
			CreateDir: true,
			ListDir:   true,
		}
	}
	return &UserVFS{
		mounts:      mounts,
		resolver:    NewResolver(),
		permissions: perms,
		quota:       quota,
	}
}

func (u *UserVFS) Open(name string, flags int, perm os.FileMode) (File, error) {
	resolved, err := u.resolver.Resolve(name)
	if err != nil {
		return nil, err
	}
	backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
	if err != nil {
		return nil, err
	}

	isWrite := flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if isWrite {
		if readOnly || !u.permissions.Upload {
			return nil, os.ErrPermission
		}
		if err := checkExtension(name, u.permissions); err != nil {
			return nil, err
		}
	}

	isRead := !isWrite || (flags&os.O_RDWR) != 0
	if isRead && !u.permissions.Download {
		return nil, os.ErrPermission
	}

	f, err := backend.Open(relPath, flags, perm)
	if err != nil {
		return nil, err
	}
	if isWrite && u.quota != nil {
		return &quotaFile{File: f, quota: u.quota}, nil
	}
	return f, nil
}

func (u *UserVFS) Stat(name string) (os.FileInfo, error) {
	backend, rel, _, err := u.lookup(name)
	if err != nil {
		return nil, err
	}
	return backend.Stat(rel)
}

func (u *UserVFS) Lstat(name string) (os.FileInfo, error) {
	backend, rel, _, err := u.lookup(name)
	if err != nil {
		return nil, err
	}
	return backend.Lstat(rel)
}

func (u *UserVFS) Rename(oldname, newname string) error {
	if !u.permissions.Rename {
		return os.ErrPermission
	}
	oldBackend, oldRel, oldRO, err := u.lookup(oldname)
	if err != nil {
		return err
	}
	newBackend, newRel, newRO, err := u.lookup(newname)
	if err != nil {
		return err
	}
	if oldRO || newRO {
		return os.ErrPermission
	}
	if oldBackend != newBackend {
		return os.ErrPermission
	}
	return oldBackend.Rename(oldRel, newRel)
}

func (u *UserVFS) Remove(name string) error {
	if !u.permissions.Delete {
		return os.ErrPermission
	}
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Remove(rel)
}

func (u *UserVFS) RemoveAll(name string) error {
	if !u.permissions.Delete {
		return os.ErrPermission
	}
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.RemoveAll(rel)
}

func (u *UserVFS) Mkdir(name string, perm os.FileMode) error {
	if !u.permissions.CreateDir {
		return os.ErrPermission
	}
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Mkdir(rel, perm)
}

func (u *UserVFS) MkdirAll(name string, perm os.FileMode) error {
	if !u.permissions.CreateDir {
		return os.ErrPermission
	}
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.MkdirAll(rel, perm)
}

func (u *UserVFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !u.permissions.ListDir {
		return nil, os.ErrPermission
	}
	resolved, err := u.resolver.Resolve(name)
	if err != nil {
		return nil, err
	}
	backend, rel, _, err := u.mounts.Lookup(resolved)
	if err != nil {
		return nil, err
	}
	entries, err := backend.ReadDir(rel)
	if err != nil {
		return nil, err
	}
	for _, mp := range u.mounts.ListMountPoints(resolved) {
		entries = append(entries, mountDirEntry{name: mp})
	}
	return entries, nil
}

func (u *UserVFS) Symlink(oldname, newname string) error {
	backend, rel, ro, err := u.lookup(newname)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Symlink(oldname, rel)
}

func (u *UserVFS) Readlink(name string) (string, error) {
	backend, rel, _, err := u.lookup(name)
	if err != nil {
		return "", err
	}
	return backend.Readlink(rel)
}

func (u *UserVFS) Chmod(name string, mode os.FileMode) error {
	if !u.permissions.Chmod {
		return os.ErrPermission
	}
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Chmod(rel, mode)
}

func (u *UserVFS) Chown(name string, uid, gid int) error {
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Chown(rel, uid, gid)
}

func (u *UserVFS) Chtimes(name string, atime, mtime time.Time) error {
	backend, rel, ro, err := u.lookup(name)
	if err != nil {
		return err
	}
	if ro {
		return os.ErrPermission
	}
	return backend.Chtimes(rel, atime, mtime)
}

func (u *UserVFS) Statvfs(p string) (*StatVFS, error) {
	backend, rel, _, err := u.lookup(p)
	if err != nil {
		return nil, err
	}
	return backend.Statvfs(rel)
}

func (u *UserVFS) lookup(p string) (FileSystem, string, bool, error) {
	resolved, err := u.resolver.Resolve(p)
	if err != nil {
		return nil, "", false, err
	}
	return u.mounts.Lookup(resolved)
}

type quotaFile struct {
	File
	quota QuotaTracker
}

func (q *quotaFile) Write(p []byte) (int, error) {
	n, err := q.File.Write(p)
	if n > 0 {
		if quotaErr := q.quota.OnWrite(int64(n)); quotaErr != nil {
			return n, quotaErr
		}
	}
	return n, err
}

func checkExtension(name string, perms *UserPermissions) error {
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return nil
	}
	if len(perms.AllowedExts) > 0 {
		ok := false
		for _, allowed := range perms.AllowedExts {
			if strings.EqualFold(allowed, ext) {
				ok = true
				break
			}
		}
		if !ok {
			return ErrForbiddenExtension
		}
	}
	for _, denied := range perms.DeniedExts {
		if strings.EqualFold(denied, ext) {
			return ErrForbiddenExtension
		}
	}
	return nil
}

type mountDirEntry struct {
	name string
}

func (m mountDirEntry) Name() string               { return m.name }
func (m mountDirEntry) IsDir() bool                { return true }
func (m mountDirEntry) Type() fs.FileMode          { return fs.ModeDir | 0o755 }
func (m mountDirEntry) Info() (fs.FileInfo, error) { return mountInfo{name: m.name}, nil }

type mountInfo struct {
	name string
}

func (m mountInfo) Name() string       { return m.name }
func (m mountInfo) Size() int64        { return 0 }
func (m mountInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (m mountInfo) ModTime() time.Time { return time.Now().UTC() }
func (m mountInfo) IsDir() bool        { return true }
func (m mountInfo) Sys() any           { return nil }

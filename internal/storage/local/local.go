package local

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
)

var (
	ErrRootNotDirectory = errors.New("root path is not a directory")
	ErrCannotRemoveRoot = errors.New("cannot remove backend root")
)

type Options struct {
	Root            string
	CreateRoot      bool
	FilePermissions os.FileMode
	DirPermissions  os.FileMode
	SyncWrites      bool
}

type Backend struct {
	root       string
	filePerms  os.FileMode
	dirPerms   os.FileMode
	syncWrites bool
}

func New(opts Options) (*Backend, error) {
	if opts.FilePermissions == 0 {
		opts.FilePermissions = 0o644
	}
	if opts.DirPermissions == 0 {
		opts.DirPermissions = 0o755
	}
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	if opts.CreateRoot {
		if err := os.MkdirAll(root, opts.DirPermissions); err != nil {
			return nil, err
		}
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrRootNotDirectory
	}
	return &Backend{
		root:       root,
		filePerms:  opts.FilePermissions,
		dirPerms:   opts.DirPermissions,
		syncWrites: opts.SyncWrites,
	}, nil
}

func (b *Backend) Open(name string, flags int, perm os.FileMode) (vfs.File, error) {
	p, err := b.resolvedPath(name, true, flags&os.O_CREATE != 0)
	if err != nil {
		return nil, err
	}
	if perm == 0 {
		perm = b.filePerms
	}
	// #nosec G304 -- resolvedPath enforces backend root containment and symlink safety.
	f, err := os.OpenFile(p, flags, perm)
	if err != nil {
		return nil, err
	}
	return &localFile{File: f, syncWrites: b.syncWrites}, nil
}

func (b *Backend) Stat(name string) (os.FileInfo, error) {
	p, err := b.resolvedPath(name, true, false)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

func (b *Backend) Lstat(name string) (os.FileInfo, error) {
	p, err := b.resolvedPath(name, false, false)
	if err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

func (b *Backend) Rename(oldname, newname string) error {
	oldP, err := b.resolvedPath(oldname, false, false)
	if err != nil {
		return err
	}
	newP, err := b.resolvedPath(newname, false, true)
	if err != nil {
		return err
	}
	return os.Rename(oldP, newP)
}

func (b *Backend) Remove(name string) error {
	p, err := b.resolvedPath(name, false, false)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func (b *Backend) RemoveAll(name string) error {
	p, err := b.resolvedPath(name, false, false)
	if err != nil {
		return err
	}
	if filepath.Clean(p) == filepath.Clean(b.root) {
		return ErrCannotRemoveRoot
	}
	return os.RemoveAll(p)
}

func (b *Backend) Mkdir(name string, perm os.FileMode) error {
	p, err := b.resolvedPath(name, false, true)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = b.dirPerms
	}
	return os.Mkdir(p, perm)
}

func (b *Backend) MkdirAll(path string, perm os.FileMode) error {
	p, err := b.resolvedPath(path, false, true)
	if err != nil {
		return err
	}
	if perm == 0 {
		perm = b.dirPerms
	}
	return os.MkdirAll(p, perm)
}

func (b *Backend) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := b.resolvedPath(name, true, false)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(p)
}

func (b *Backend) Symlink(oldname, newname string) error {
	oldP, err := b.resolvedPath(oldname, true, false)
	if err != nil {
		return err
	}
	newP, err := b.resolvedPath(newname, false, true)
	if err != nil {
		return err
	}
	return os.Symlink(oldP, newP)
}

func (b *Backend) Readlink(name string) (string, error) {
	p, err := b.resolvedPath(name, false, false)
	if err != nil {
		return "", err
	}
	target, err := os.Readlink(p)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(p), target)
	}
	target, err = b.ensureWithinRoot(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(b.root, target)
	if err != nil {
		return "", err
	}
	return "/" + filepath.ToSlash(rel), nil
}

func (b *Backend) Chmod(name string, mode os.FileMode) error {
	p, err := b.resolvedPath(name, true, false)
	if err != nil {
		return err
	}
	return os.Chmod(p, mode)
}

func (b *Backend) Chown(name string, uid, gid int) error {
	p, err := b.resolvedPath(name, true, false)
	if err != nil {
		return err
	}
	return os.Chown(p, uid, gid)
}

func (b *Backend) Chtimes(name string, atime, mtime time.Time) error {
	p, err := b.resolvedPath(name, true, false)
	if err != nil {
		return err
	}
	return os.Chtimes(p, atime, mtime)
}

func (b *Backend) Statvfs(name string) (*vfs.StatVFS, error) {
	_, err := b.resolvedPath(name, true, false)
	if err != nil {
		return nil, err
	}
	return &vfs.StatVFS{}, nil
}

func (b *Backend) physicalPath(name string) (string, error) {
	rel := strings.TrimPrefix(filepath.Clean(filepath.FromSlash(name)), string(filepath.Separator))
	p := filepath.Join(b.root, rel)
	return b.ensureWithinRoot(p)
}

func (b *Backend) ensureWithinRoot(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rootWithSep := filepath.Clean(b.root) + string(filepath.Separator)
	cleanAbs := filepath.Clean(abs)
	if cleanAbs != filepath.Clean(b.root) && !strings.HasPrefix(cleanAbs, rootWithSep) {
		return "", vfs.ErrPathEscape
	}
	return cleanAbs, nil
}

func (b *Backend) resolvedPath(name string, followFinal bool, allowMissingFinal bool) (string, error) {
	p, err := b.physicalPath(name)
	if err != nil {
		return "", err
	}
	if !followFinal {
		return b.resolveParentPath(p, allowMissingFinal)
	}
	resolved, err := filepath.EvalSymlinks(p)
	switch {
	case err == nil:
		return b.ensureWithinRoot(resolved)
	case errors.Is(err, os.ErrNotExist) && allowMissingFinal:
		return b.resolveParentPath(p, true)
	default:
		return "", err
	}
}

func (b *Backend) resolveParentPath(p string, allowMissingFinal bool) (string, error) {
	if filepath.Clean(p) == filepath.Clean(b.root) {
		return filepath.Clean(b.root), nil
	}
	parent, err := b.resolveExistingAncestor(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(parent, filepath.Base(p))
	if !allowMissingFinal {
		return b.ensureWithinRoot(candidate)
	}
	return b.ensureWithinRoot(candidate)
}

func (b *Backend) resolveExistingAncestor(p string) (string, error) {
	missing := make([]string, 0, 4)
	current := p
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return b.ensureWithinRoot(resolved)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

type localFile struct {
	*os.File
	syncWrites bool
}

func (f *localFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	if err == nil && f.syncWrites {
		_ = f.File.Sync()
	}
	return n, err
}

func (f *localFile) ReadDir(n int) ([]fs.DirEntry, error) {
	return f.File.ReadDir(n)
}

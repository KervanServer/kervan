package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

var (
	ErrNoMount            = errors.New("no mount found for path")
	ErrPathEscape         = errors.New("path escapes virtual root")
	ErrForbiddenPathChar  = errors.New("path contains forbidden characters")
	ErrPathTooDeep        = errors.New("path exceeds maximum depth")
	ErrForbiddenExtension = errors.New("extension is not allowed")
	ErrFileTooLarge       = errors.New("file exceeds maximum allowed size")
)

type FileSystem interface {
	Open(name string, flags int, perm os.FileMode) (File, error)
	Stat(name string) (os.FileInfo, error)
	Lstat(name string) (os.FileInfo, error)
	Rename(oldname, newname string) error
	Remove(name string) error
	RemoveAll(name string) error
	Mkdir(name string, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Symlink(oldname, newname string) error
	Readlink(name string) (string, error)
	Chmod(name string, mode os.FileMode) error
	Chown(name string, uid, gid int) error
	Chtimes(name string, atime, mtime time.Time) error
	Statvfs(path string) (*StatVFS, error)
}

type File interface {
	io.Reader
	io.ReaderAt
	io.Writer
	io.WriterAt
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(size int64) error
	ReadDir(n int) ([]fs.DirEntry, error)
	Name() string
}

type StatVFS struct {
	BlockSize   uint64
	TotalBlocks uint64
	FreeBlocks  uint64
	AvailBlocks uint64
	TotalFiles  uint64
	FreeFiles   uint64
	NameMaxLen  uint64
}

type UserPermissions struct {
	Upload      bool
	Download    bool
	Delete      bool
	Rename      bool
	CreateDir   bool
	ListDir     bool
	Chmod       bool
	MaxFileSize int64
	AllowedExts []string
	DeniedExts  []string
}

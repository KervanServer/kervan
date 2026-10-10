package quota

import (
	"errors"
	"os"
	"sync"

	"github.com/kervanserver/kervan/internal/vfs"
)

var (
	ErrStorageExceeded   = errors.New("storage quota exceeded")
	ErrFileCountExceeded = errors.New("file count quota exceeded")
)

// Tracker enforces a user's storage and file-count quotas. Limits of 0 are
// unlimited. Only regular files count toward the file limit.
type Tracker struct {
	mu         sync.Mutex
	usedBytes  int64
	maxStorage int64
	usedFiles  int64
	maxFiles   int64
}

func NewTracker(fsys vfs.FileSystem, maxStorage, maxFiles int64) (*Tracker, error) {
	usedBytes, usedFiles, err := MeasureUsage(fsys, "/")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Tracker{
		usedBytes:  usedBytes,
		maxStorage: maxStorage,
		usedFiles:  usedFiles,
		maxFiles:   maxFiles,
	}, nil
}

func (t *Tracker) OnGrow(n int64) error {
	if t == nil || n <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.maxStorage > 0 && t.usedBytes+n > t.maxStorage {
		return ErrStorageExceeded
	}
	t.usedBytes += n
	return nil
}

func (t *Tracker) OnShrink(n int64) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usedBytes = max(t.usedBytes-n, 0)
}

// OnFileCreated reserves one file against the file-count quota.
func (t *Tracker) OnFileCreated() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.maxFiles > 0 && t.usedFiles+1 > t.maxFiles {
		return ErrFileCountExceeded
	}
	t.usedFiles++
	return nil
}

// OnFilesRemoved releases n files.
func (t *Tracker) OnFilesRemoved(n int64) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usedFiles = max(t.usedFiles-n, 0)
}

func (t *Tracker) UsedBytes() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usedBytes
}

func (t *Tracker) UsedFiles() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usedFiles
}

func (t *Tracker) MaxStorage() int64 {
	if t == nil {
		return 0
	}
	return t.maxStorage
}

// MeasureUsage returns the bytes and regular-file count under root.
func MeasureUsage(fsys vfs.FileSystem, root string) (int64, int64, error) {
	info, err := fsys.Stat(root)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		return info.Size(), 1, nil
	}
	return measureDirUsage(fsys, root)
}

func measureDirUsage(fsys vfs.FileSystem, dir string) (int64, int64, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	var bytes, files int64
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		childPath := joinPath(dir, entry.Name())
		if entry.IsDir() {
			b, f, err := measureDirUsage(fsys, childPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return bytes, files, err
			}
			bytes += b
			files += f
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return bytes, files, err
		}
		bytes += info.Size()
		files++
	}
	return bytes, files, nil
}

func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

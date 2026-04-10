package quota

import (
	"errors"
	"io/fs"
	"os"
	"sync"

	"github.com/kervanserver/kervan/internal/vfs"
)

var ErrStorageExceeded = errors.New("storage quota exceeded")

type Tracker struct {
	mu         sync.Mutex
	usedBytes  int64
	maxStorage int64
}

func NewTracker(fsys vfs.FileSystem, maxStorage int64) (*Tracker, error) {
	usedBytes, err := MeasureUsage(fsys, "/")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Tracker{
		usedBytes:  usedBytes,
		maxStorage: maxStorage,
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
	t.usedBytes -= n
	if t.usedBytes < 0 {
		t.usedBytes = 0
	}
}

func (t *Tracker) UsedBytes() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usedBytes
}

func (t *Tracker) MaxStorage() int64 {
	if t == nil {
		return 0
	}
	return t.maxStorage
}

func MeasureUsage(fsys vfs.FileSystem, root string) (int64, error) {
	info, err := fsys.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	return measureDirUsage(fsys, root)
}

func measureDirUsage(fsys vfs.FileSystem, dir string) (int64, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		childPath := joinPath(dir, entry.Name())
		if entry.IsDir() {
			size, err := measureDirUsage(fsys, childPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return total, err
			}
			total += size
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return total, err
		}
		total += info.Size()
	}
	return total, nil
}

func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

func MeasureDirEntries(fsys vfs.FileSystem, root string) (int64, error) {
	info, err := fsys.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 1, nil
	}
	return measureDirEntries(fsys, root)
}

func measureDirEntries(fsys vfs.FileSystem, dir string) (int64, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		childPath := joinPath(dir, entry.Name())
		if entry.IsDir() {
			count, err := measureDirEntries(fsys, childPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return total, err
			}
			total += count
			continue
		}
		total++
	}
	return total, nil
}

type dirEntryInfo interface {
	Info() (fs.FileInfo, error)
}

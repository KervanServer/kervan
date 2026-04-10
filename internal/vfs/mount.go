package vfs

import (
	"path"
	"sort"
	"strings"
	"sync"
)

type MountEntry struct {
	Path     string
	Backend  FileSystem
	ReadOnly bool
}

type MountTable struct {
	mu     sync.RWMutex
	mounts []MountEntry
}

func NewMountTable() *MountTable {
	return &MountTable{}
}

func (mt *MountTable) Mount(virtualPath string, backend FileSystem, readOnly bool) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	entry := MountEntry{
		Path:     path.Clean("/" + virtualPath),
		Backend:  backend,
		ReadOnly: readOnly,
	}
	mt.mounts = append(mt.mounts, entry)
	sort.Slice(mt.mounts, func(i, j int) bool {
		return len(mt.mounts[i].Path) > len(mt.mounts[j].Path)
	})
}

func (mt *MountTable) Lookup(virtualPath string) (FileSystem, string, bool, error) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	cleaned := path.Clean("/" + virtualPath)
	for _, m := range mt.mounts {
		if m.Path == "/" {
			rel := cleaned
			if rel == "" {
				rel = "/"
			}
			return m.Backend, rel, m.ReadOnly, nil
		}
		if cleaned == m.Path || strings.HasPrefix(cleaned, m.Path+"/") {
			rel := strings.TrimPrefix(cleaned, m.Path)
			if rel == "" {
				rel = "/"
			}
			return m.Backend, rel, m.ReadOnly, nil
		}
	}
	return nil, "", false, ErrNoMount
}

func (mt *MountTable) ListMountPoints(dir string) []string {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	d := path.Clean("/" + dir)
	var points []string
	for _, m := range mt.mounts {
		parent := path.Dir(m.Path)
		if parent == d && m.Path != d {
			points = append(points, path.Base(m.Path))
		}
	}
	sort.Strings(points)
	return points
}

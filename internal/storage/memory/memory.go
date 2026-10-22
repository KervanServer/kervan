package memory

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
)

type Backend struct {
	mu    sync.RWMutex
	nodes map[string]*node
}

type node struct {
	name    string
	data    []byte
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func New() *Backend {
	return &Backend{
		nodes: map[string]*node{
			"/": {
				name:    "/",
				isDir:   true,
				mode:    0o755 | fs.ModeDir,
				modTime: time.Now().UTC(),
			},
		},
	}
}

func (b *Backend) Open(name string, flags int, perm os.FileMode) (vfs.File, error) {
	p := clean(name)
	b.mu.Lock()
	defer b.mu.Unlock()

	n, ok := b.nodes[p]
	if ok && flags&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL {
		return nil, os.ErrExist
	}
	if !ok {
		if flags&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		if err := b.ensureParentLocked(path.Dir(p)); err != nil {
			return nil, err
		}
		if perm == 0 {
			perm = 0o644
		}
		n = &node{name: path.Base(p), mode: perm, modTime: time.Now().UTC()}
		b.nodes[p] = n
	}
	if n.isDir {
		return nil, errors.New("cannot open directory")
	}
	if flags&os.O_TRUNC != 0 {
		n.data = nil
		n.modTime = time.Now().UTC()
	}
	if flags&os.O_APPEND != 0 {
		return newMemFile(b, p, n, len(n.data)), nil
	}
	return newMemFile(b, p, n, 0), nil
}

func (b *Backend) Stat(name string) (os.FileInfo, error) {
	p := clean(name)
	b.mu.RLock()
	defer b.mu.RUnlock()
	n, ok := b.nodes[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return n.info(), nil
}

func (b *Backend) Lstat(name string) (os.FileInfo, error) { return b.Stat(name) }

func (b *Backend) Rename(oldname, newname string) error {
	oldP := clean(oldname)
	newP := clean(newname)
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[oldP]
	if !ok {
		return os.ErrNotExist
	}
	if oldP == newP {
		return nil
	}
	if n.isDir && strings.HasPrefix(newP, strings.TrimSuffix(oldP, "/")+"/") {
		return os.ErrInvalid
	}
	if err := b.ensureParentLocked(path.Dir(newP)); err != nil {
		return err
	}
	if n.isDir {
		// Move descendants as well as the directory node. Stage the new keys
		// separately so iterating nodes cannot revisit newly inserted entries.
		moved := make(map[string]*node)
		prefix := oldP + "/"
		for p, child := range b.nodes {
			if strings.HasPrefix(p, prefix) {
				moved[path.Join(newP, strings.TrimPrefix(p, prefix))] = child
				delete(b.nodes, p)
			}
		}
		for p, child := range moved {
			b.nodes[p] = child
		}
	}
	delete(b.nodes, oldP)
	n.name = path.Base(newP)
	n.modTime = time.Now().UTC()
	b.nodes[newP] = n
	return nil
}

func (b *Backend) Remove(name string) error {
	p := clean(name)
	if p == "/" {
		return os.ErrPermission
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[p]
	if !ok {
		return os.ErrNotExist
	}
	if n.isDir {
		for k := range b.nodes {
			if strings.HasPrefix(k, p+"/") {
				return errors.New("directory not empty")
			}
		}
	}
	delete(b.nodes, p)
	return nil
}

func (b *Backend) RemoveAll(name string) error {
	p := clean(name)
	if p == "/" {
		return os.ErrPermission
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for k := range b.nodes {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(b.nodes, k)
		}
	}
	return nil
}

func (b *Backend) Mkdir(name string, perm os.FileMode) error {
	p := clean(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.nodes[p]; ok {
		return os.ErrExist
	}
	if err := b.ensureParentLocked(path.Dir(p)); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o755
	}
	b.nodes[p] = &node{name: path.Base(p), isDir: true, mode: perm | fs.ModeDir, modTime: time.Now().UTC()}
	return nil
}

func (b *Backend) MkdirAll(name string, perm os.FileMode) error {
	p := clean(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	if perm == 0 {
		perm = 0o755
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		if cur == "/" {
			cur = "/" + part
		} else {
			cur = cur + "/" + part
		}
		if n, ok := b.nodes[cur]; !ok {
			b.nodes[cur] = &node{name: part, isDir: true, mode: perm | fs.ModeDir, modTime: time.Now().UTC()}
		} else if !n.isDir {
			return errors.New("path component is not a directory")
		}
	}
	return nil
}

func (b *Backend) ReadDir(name string) ([]fs.DirEntry, error) {
	p := clean(name)
	b.mu.RLock()
	defer b.mu.RUnlock()
	n, ok := b.nodes[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	if !n.isDir {
		return nil, errors.New("not a directory")
	}

	children := map[string]*node{}
	prefix := p
	if prefix != "/" {
		prefix += "/"
	}
	for key, child := range b.nodes {
		if !strings.HasPrefix(key, prefix) || key == p {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if strings.Contains(rest, "/") {
			continue
		}
		children[rest] = child
	}
	names := make([]string, 0, len(children))
	for n := range children {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		out = append(out, memDirEntry{n: children[name]})
	}
	return out, nil
}

func (b *Backend) Symlink(_, _ string) error         { return errors.New("not supported") }
func (b *Backend) Readlink(_ string) (string, error) { return "", errors.New("not supported") }
func (b *Backend) Chmod(name string, mode os.FileMode) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[clean(name)]
	if !ok {
		return os.ErrNotExist
	}
	n.mode = mode
	return nil
}
func (b *Backend) Chown(_ string, _, _ int) error { return nil }
func (b *Backend) Chtimes(name string, atime, mtime time.Time) error {
	_ = atime
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.nodes[clean(name)]
	if !ok {
		return os.ErrNotExist
	}
	n.modTime = mtime
	return nil
}
func (b *Backend) Statvfs(_ string) (*vfs.StatVFS, error) { return &vfs.StatVFS{}, nil }

func (b *Backend) ensureParentLocked(parent string) error {
	parent = clean(parent)
	n, ok := b.nodes[parent]
	if !ok {
		return os.ErrNotExist
	}
	if !n.isDir {
		return errors.New("parent is not a directory")
	}
	return nil
}

func clean(p string) string {
	out := path.Clean("/" + p)
	if out == "." {
		return "/"
	}
	return out
}

func (n *node) info() os.FileInfo {
	mode := n.mode
	if n.isDir {
		mode |= fs.ModeDir
	}
	return memFileInfo{
		name:    n.name,
		size:    int64(len(n.data)),
		mode:    mode,
		modTime: n.modTime,
		isDir:   n.isDir,
	}
}

type memFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (m memFileInfo) Name() string       { return m.name }
func (m memFileInfo) Size() int64        { return m.size }
func (m memFileInfo) Mode() os.FileMode  { return m.mode }
func (m memFileInfo) ModTime() time.Time { return m.modTime }
func (m memFileInfo) IsDir() bool        { return m.isDir }
func (m memFileInfo) Sys() any           { return nil }

type memDirEntry struct {
	n *node
}

func (m memDirEntry) Name() string               { return m.n.name }
func (m memDirEntry) IsDir() bool                { return m.n.isDir }
func (m memDirEntry) Type() fs.FileMode          { return m.n.mode.Type() }
func (m memDirEntry) Info() (fs.FileInfo, error) { return m.n.info(), nil }

type memFile struct {
	backend *Backend
	path    string
	node    *node
	offset  int
	closed  bool
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

func newMemFile(b *Backend, path string, n *node, offset int) *memFile {
	return &memFile{
		backend: b,
		path:    path,
		node:    n,
		offset:  offset,
		data:    append([]byte(nil), n.data...),
		mode:    n.mode,
		modTime: n.modTime,
	}
}

func (m *memFile) Read(p []byte) (int, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	data := m.data
	if m.offset >= len(data) {
		return 0, io.EOF
	}
	n := copy(p, data[m.offset:])
	m.offset += n
	return n, nil
}

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	data := m.data
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// maxMemFileSize caps a single in-memory file. The memory backend keeps
// whole files in RAM, so writes past this point are refused rather than
// attempted (a write at a huge offset would otherwise try to allocate it).
const maxMemFileSize = 4 << 30

// maxMemHole bounds how far past the end of a file a write or truncate may
// start. Files here cannot be sparse, so a gap is allocated in full; real
// clients write at most a few MiB ahead (pipelined SFTP requests), so a
// larger gap is refused instead of letting one request allocate gigabytes.
const maxMemHole = 64 << 20

var (
	errMemFileTooLarge = errors.New("memory backend: file would exceed 4 GiB")
	errMemHoleTooLarge = errors.New("memory backend: write leaves a gap larger than 64 MiB")
)

func (m *memFile) Write(p []byte) (int, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	if m.offset > maxMemFileSize || len(p) > maxMemFileSize-m.offset {
		return 0, errMemFileTooLarge
	}
	if m.offset > len(m.data)+maxMemHole {
		return 0, errMemHoleTooLarge
	}
	end := m.offset + len(p)
	if end > len(m.data) {
		oldLen := len(m.data)
		if end <= cap(m.data) {
			m.data = m.data[:end]
			// Bytes past the old length may hold data from before a
			// truncate; a gap must read back as zeros.
			clear(m.data[oldLen:m.offset])
		} else {
			grown := make([]byte, end, max(end, 2*cap(m.data)))
			copy(grown, m.data)
			m.data = grown
		}
	}
	copy(m.data[m.offset:], p)
	m.offset = end
	return len(p), nil
}

func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	_, err := m.Seek(off, io.SeekStart)
	if err != nil {
		return 0, err
	}
	return m.Write(p)
}

func (m *memFile) Seek(offset int64, whence int) (int64, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	base := 0
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = m.offset
	case io.SeekEnd:
		base = len(m.data)
	default:
		return 0, errors.New("invalid whence")
	}
	if offset > math.MaxInt-int64(base) {
		return 0, errors.New("seek offset overflows")
	}
	next := base + int(offset)
	if next < 0 {
		return 0, errors.New("negative seek")
	}
	m.offset = next
	return int64(next), nil
}

func (m *memFile) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	m.backend.mu.Lock()
	// Write back through the node retained at open time: the map entry may
	// have moved (Rename) or vanished (Remove) while this handle was open.
	// Rename keeps the same node, so buffered data follows the new name; a
	// removed node is unreachable, so buffered writes are discarded like
	// writes to an unlinked POSIX inode.
	m.node.data = append([]byte(nil), m.data...)
	m.node.modTime = time.Now().UTC()
	m.node.mode = m.mode
	m.backend.mu.Unlock()
	return nil
}

func (m *memFile) Stat() (os.FileInfo, error) {
	if m.closed {
		return nil, os.ErrClosed
	}
	return memFileInfo{
		name:    path.Base(m.path),
		size:    int64(len(m.data)),
		mode:    m.mode,
		modTime: m.modTime,
	}, nil
}

func (m *memFile) Sync() error { return nil }

func (m *memFile) Truncate(size int64) error {
	if size < 0 {
		return errors.New("negative size")
	}
	if size > maxMemFileSize {
		return errMemFileTooLarge
	}
	if size > int64(len(m.data))+maxMemHole {
		return errMemHoleTooLarge
	}
	switch {
	case int(size) < len(m.data):
		m.data = m.data[:size]
	case int(size) > len(m.data):
		m.data = append(m.data, make([]byte, int(size)-len(m.data))...)
	}
	if m.offset > int(size) {
		m.offset = int(size)
	}
	return nil
}

func (m *memFile) ReadDir(_ int) ([]fs.DirEntry, error) { return nil, errors.New("not a directory") }

func (m *memFile) Name() string { return m.path }

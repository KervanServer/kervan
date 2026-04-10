package memory

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
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
	if err := b.ensureParentLocked(path.Dir(newP)); err != nil {
		return err
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
		if _, ok := b.nodes[cur]; !ok {
			b.nodes[cur] = &node{name: part, isDir: true, mode: perm | fs.ModeDir, modTime: time.Now().UTC()}
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
	offset  int
	closed  bool
	buf     *bytes.Buffer
	mode    os.FileMode
	modTime time.Time
}

func newMemFile(b *Backend, path string, n *node, offset int) *memFile {
	return &memFile{
		backend: b,
		path:    path,
		offset:  offset,
		buf:     bytes.NewBuffer(append([]byte(nil), n.data...)),
		mode:    n.mode,
		modTime: n.modTime,
	}
}

func (m *memFile) Read(p []byte) (int, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	data := m.buf.Bytes()
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
	data := m.buf.Bytes()
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memFile) Write(p []byte) (int, error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	data := m.buf.Bytes()
	if m.offset > len(data) {
		pad := make([]byte, m.offset-len(data))
		_, _ = m.buf.Write(pad)
		data = m.buf.Bytes()
	}
	head := append([]byte(nil), data[:m.offset]...)
	tail := []byte{}
	if m.offset < len(data) {
		tail = data[m.offset:]
	}
	next := append(head, p...)
	if len(tail) > len(p) {
		next = append(next, tail[len(p):]...)
	}
	m.buf = bytes.NewBuffer(next)
	m.offset += len(p)
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
		base = len(m.buf.Bytes())
	default:
		return 0, errors.New("invalid whence")
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
	n := m.backend.nodes[m.path]
	n.data = append([]byte(nil), m.buf.Bytes()...)
	n.modTime = time.Now().UTC()
	n.mode = m.mode
	m.backend.mu.Unlock()
	return nil
}

func (m *memFile) Stat() (os.FileInfo, error) {
	if m.closed {
		return nil, os.ErrClosed
	}
	return memFileInfo{
		name:    path.Base(m.path),
		size:    int64(len(m.buf.Bytes())),
		mode:    m.mode,
		modTime: m.modTime,
	}, nil
}

func (m *memFile) Sync() error { return nil }

func (m *memFile) Truncate(size int64) error {
	if size < 0 {
		return errors.New("negative size")
	}
	data := m.buf.Bytes()
	switch {
	case int(size) < len(data):
		data = data[:size]
	case int(size) > len(data):
		data = append(data, make([]byte, int(size)-len(data))...)
	}
	m.buf = bytes.NewBuffer(data)
	if m.offset > int(size) {
		m.offset = int(size)
	}
	return nil
}

func (m *memFile) ReadDir(_ int) ([]fs.DirEntry, error) { return nil, errors.New("not a directory") }

func (m *memFile) Name() string { return m.path }

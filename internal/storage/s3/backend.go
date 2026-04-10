package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
)

type Options struct {
	Endpoint     string
	Region       string
	Bucket       string
	Prefix       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
	DisableSSL   bool
}

type Backend struct {
	client *Client
	bucket string
	prefix string
}

func New(opts Options) (*Backend, error) {
	if strings.TrimSpace(opts.Bucket) == "" {
		return nil, errors.New("s3 bucket is required")
	}
	client, err := NewClient(ClientConfig{
		Endpoint:     opts.Endpoint,
		Region:       opts.Region,
		AccessKey:    opts.AccessKey,
		SecretKey:    opts.SecretKey,
		UsePathStyle: opts.UsePathStyle,
		DisableSSL:   opts.DisableSSL,
	})
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(strings.TrimSpace(opts.Prefix), "/")
	if prefix != "" {
		prefix += "/"
	}
	return &Backend{
		client: client,
		bucket: strings.TrimSpace(opts.Bucket),
		prefix: prefix,
	}, nil
}

func (b *Backend) Open(name string, flags int, _ os.FileMode) (vfs.File, error) {
	key := b.s3Key(name)
	writeMode := flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if !writeMode {
		data, modTime, err := b.readObject(key)
		if err != nil {
			return nil, err
		}
		return newFile(name, data, modTime, false, b, key), nil
	}

	var initial []byte
	var modTime time.Time
	if flags&os.O_TRUNC == 0 {
		data, existingModTime, err := b.readObject(key)
		switch {
		case err == nil:
			initial = data
			modTime = existingModTime
		case errors.Is(err, os.ErrNotExist):
			if flags&os.O_CREATE == 0 && flags&os.O_APPEND == 0 {
				return nil, err
			}
		default:
			return nil, err
		}
	}

	file := newFile(name, initial, modTime, true, b, key)
	if flags&os.O_APPEND != 0 {
		_, _ = file.Seek(0, io.SeekEnd)
	}
	return file, nil
}

func (b *Backend) Stat(name string) (os.FileInfo, error) {
	cleanName := clean(name)
	if cleanName == "/" {
		return fileInfo{name: "/", mode: fs.ModeDir | 0o755, isDir: true, modTime: time.Now().UTC()}, nil
	}

	key := b.s3Key(cleanName)
	resp, err := b.client.HeadObject(context.Background(), b.bucket, key)
	if err == nil {
		return fileInfo{
			name:    path.Base(cleanName),
			size:    resp.ContentLength,
			mode:    0o644,
			modTime: resp.LastModified,
		}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	dirPrefix := b.dirKey(cleanName)
	list, listErr := b.client.ListObjectsV2(context.Background(), b.bucket, dirPrefix, "/", 1)
	if listErr != nil {
		return nil, listErr
	}
	if len(list.Contents) > 0 || len(list.CommonPrefixes) > 0 {
		return fileInfo{name: path.Base(cleanName), mode: fs.ModeDir | 0o755, isDir: true, modTime: time.Now().UTC()}, nil
	}

	marker, markerErr := b.client.HeadObject(context.Background(), b.bucket, dirPrefix)
	if markerErr == nil {
		return fileInfo{
			name:    path.Base(cleanName),
			mode:    fs.ModeDir | 0o755,
			isDir:   true,
			modTime: marker.LastModified,
		}, nil
	}
	if !errors.Is(markerErr, ErrNotFound) {
		return nil, markerErr
	}

	return nil, os.ErrNotExist
}

func (b *Backend) Lstat(name string) (os.FileInfo, error) { return b.Stat(name) }

func (b *Backend) Rename(oldname, newname string) error {
	oldClean := clean(oldname)
	newClean := clean(newname)

	info, err := b.Stat(oldClean)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return b.renameDir(oldClean, newClean)
	}
	oldKey := b.s3Key(oldClean)
	newKey := b.s3Key(newClean)
	if err := b.client.CopyObject(context.Background(), b.bucket, oldKey, b.bucket, newKey); err != nil {
		return mapS3Error(err)
	}
	return mapS3Error(b.client.DeleteObject(context.Background(), b.bucket, oldKey))
}

func (b *Backend) Remove(name string) error {
	cleanName := clean(name)
	if cleanName == "/" {
		return os.ErrPermission
	}
	info, err := b.Stat(cleanName)
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := b.ReadDir(cleanName)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return errors.New("directory not empty")
		}
		return mapS3Error(b.client.DeleteObject(context.Background(), b.bucket, b.dirKey(cleanName)))
	}
	return mapS3Error(b.client.DeleteObject(context.Background(), b.bucket, b.s3Key(cleanName)))
}

func (b *Backend) RemoveAll(name string) error {
	cleanName := clean(name)
	if cleanName == "/" {
		return os.ErrPermission
	}
	info, err := b.Stat(cleanName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return mapS3Error(b.client.DeleteObject(context.Background(), b.bucket, b.s3Key(cleanName)))
	}
	prefix := b.dirKey(cleanName)
	if err := b.walkObjects(prefix, func(key string) error {
		return b.client.DeleteObject(context.Background(), b.bucket, key)
	}); err != nil {
		return mapS3Error(err)
	}
	return mapS3Error(b.client.DeleteObject(context.Background(), b.bucket, prefix))
}

func (b *Backend) Mkdir(name string, _ os.FileMode) error {
	key := b.dirKey(name)
	return mapS3Error(b.client.PutObject(context.Background(), b.bucket, key, bytes.NewReader(nil), 0, "application/x-directory"))
}

func (b *Backend) MkdirAll(name string, perm os.FileMode) error {
	cleanName := clean(name)
	if cleanName == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(cleanName, "/"), "/")
	cur := ""
	for _, part := range parts {
		cur += "/" + part
		if err := b.Mkdir(cur, perm); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) ReadDir(name string) ([]fs.DirEntry, error) {
	cleanName := clean(name)
	if cleanName != "/" {
		info, err := b.Stat(cleanName)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("not a directory")
		}
	}

	prefix := ""
	if cleanName != "/" {
		prefix = b.dirKey(cleanName)
	} else if b.prefix != "" {
		prefix = b.prefix
	}

	response, err := b.client.ListObjectsV2(context.Background(), b.bucket, prefix, "/", 1000)
	if err != nil {
		return nil, mapS3Error(err)
	}

	entryMap := map[string]dirEntry{}
	for _, cp := range response.CommonPrefixes {
		name := strings.TrimSuffix(strings.TrimPrefix(cp, prefix), "/")
		if name == "" {
			continue
		}
		entryMap[name] = dirEntry{info: fileInfo{name: name, mode: fs.ModeDir | 0o755, isDir: true}}
	}
	for _, obj := range response.Contents {
		name := strings.TrimPrefix(obj.Key, prefix)
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "/") {
			name = strings.TrimSuffix(name, "/")
			if name == "" {
				continue
			}
			entryMap[name] = dirEntry{info: fileInfo{name: name, mode: fs.ModeDir | 0o755, isDir: true, modTime: obj.LastModified}}
			continue
		}
		entryMap[name] = dirEntry{info: fileInfo{name: name, size: obj.Size, mode: 0o644, modTime: obj.LastModified}}
	}

	names := make([]string, 0, len(entryMap))
	for name := range entryMap {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, entryMap[name])
	}
	return entries, nil
}

func (b *Backend) Symlink(_, _ string) error              { return errors.New("not supported") }
func (b *Backend) Readlink(_ string) (string, error)      { return "", errors.New("not supported") }
func (b *Backend) Chmod(_ string, _ os.FileMode) error    { return nil }
func (b *Backend) Chown(_ string, _, _ int) error         { return nil }
func (b *Backend) Chtimes(_ string, _, _ time.Time) error { return nil }

func (b *Backend) Statvfs(_ string) (*vfs.StatVFS, error) {
	return &vfs.StatVFS{
		BlockSize:   4096,
		TotalBlocks: 1 << 40,
		FreeBlocks:  1 << 40,
		AvailBlocks: 1 << 40,
		TotalFiles:  1 << 32,
		FreeFiles:   1 << 32,
		NameMaxLen:  1024,
	}, nil
}

func (b *Backend) readObject(key string) ([]byte, time.Time, error) {
	resp, err := b.client.GetObject(context.Background(), b.bucket, key)
	if err != nil {
		return nil, time.Time{}, mapS3Error(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, resp.LastModified, nil
}

func (b *Backend) s3Key(name string) string {
	cleanName := clean(name)
	if cleanName == "/" {
		return strings.TrimSuffix(b.prefix, "/")
	}
	item := strings.TrimPrefix(cleanName, "/")
	if b.prefix == "" {
		return item
	}
	return b.prefix + item
}

func (b *Backend) dirKey(name string) string {
	key := b.s3Key(name)
	key = strings.TrimSuffix(key, "/")
	if key == "" {
		return b.prefix
	}
	return key + "/"
}

func (b *Backend) renameDir(oldname, newname string) error {
	oldPrefix := b.dirKey(oldname)
	newPrefix := b.dirKey(newname)
	var keys []string
	if err := b.walkObjects(oldPrefix, func(key string) error {
		keys = append(keys, key)
		return nil
	}); err != nil {
		return err
	}
	if len(keys) == 0 {
		return os.ErrNotExist
	}
	sort.Strings(keys)
	for _, key := range keys {
		suffix := strings.TrimPrefix(key, oldPrefix)
		target := newPrefix + suffix
		if suffix == "" {
			target = newPrefix
		}
		if err := b.client.CopyObject(context.Background(), b.bucket, key, b.bucket, target); err != nil {
			return mapS3Error(err)
		}
	}
	for _, key := range keys {
		if err := b.client.DeleteObject(context.Background(), b.bucket, key); err != nil {
			return mapS3Error(err)
		}
	}
	return nil
}

func (b *Backend) walkObjects(prefix string, fn func(key string) error) error {
	token := ""
	for {
		resp, err := b.client.ListObjectsV2WithToken(context.Background(), b.bucket, prefix, "", 1000, token)
		if err != nil {
			return err
		}
		for _, obj := range resp.Contents {
			if err := fn(obj.Key); err != nil {
				return err
			}
		}
		if !resp.IsTruncated {
			return nil
		}
		token = resp.NextContinuationToken
	}
}

func clean(name string) string {
	p := path.Clean("/" + name)
	if p == "." {
		return "/"
	}
	return p
}

func mapS3Error(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return os.ErrNotExist
	default:
		return err
	}
}

type fileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (f fileInfo) Name() string       { return f.name }
func (f fileInfo) Size() int64        { return f.size }
func (f fileInfo) Mode() os.FileMode  { return f.mode }
func (f fileInfo) ModTime() time.Time { return f.modTime }
func (f fileInfo) IsDir() bool        { return f.isDir }
func (f fileInfo) Sys() any           { return nil }

type dirEntry struct {
	info fileInfo
}

func (d dirEntry) Name() string               { return d.info.name }
func (d dirEntry) IsDir() bool                { return d.info.isDir }
func (d dirEntry) Type() fs.FileMode          { return d.info.mode.Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.info, nil }

type file struct {
	name     string
	key      string
	buf      []byte
	offset   int64
	modTime  time.Time
	writable bool
	closed   bool
	backend  *Backend
}

func newFile(name string, data []byte, modTime time.Time, writable bool, backend *Backend, key string) *file {
	if modTime.IsZero() {
		modTime = time.Now().UTC()
	}
	return &file{
		name:     clean(name),
		key:      key,
		buf:      append([]byte(nil), data...),
		modTime:  modTime,
		writable: writable,
		backend:  backend,
	}
}

func (f *file) Read(p []byte) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if f.offset >= int64(len(f.buf)) {
		return 0, io.EOF
	}
	n := copy(p, f.buf[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if off >= int64(len(f.buf)) {
		return 0, io.EOF
	}
	n := copy(p, f.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *file) Write(p []byte) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if !f.writable {
		return 0, os.ErrPermission
	}
	end := int(f.offset) + len(p)
	if end > len(f.buf) {
		next := make([]byte, end)
		copy(next, f.buf)
		f.buf = next
	}
	copy(f.buf[f.offset:], p)
	f.offset += int64(len(p))
	f.modTime = time.Now().UTC()
	return len(p), nil
}

func (f *file) WriteAt(p []byte, off int64) (int, error) {
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return f.Write(p)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = f.offset
	case io.SeekEnd:
		base = int64(len(f.buf))
	default:
		return 0, errors.New("invalid whence")
	}
	next := base + offset
	if next < 0 {
		return 0, errors.New("negative seek")
	}
	f.offset = next
	return next, nil
}

func (f *file) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if !f.writable {
		return nil
	}
	return f.backend.client.PutObject(context.Background(), f.backend.bucket, f.key, bytes.NewReader(f.buf), int64(len(f.buf)), "application/octet-stream")
}

func (f *file) Stat() (os.FileInfo, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	return fileInfo{
		name:    path.Base(f.name),
		size:    int64(len(f.buf)),
		mode:    0o644,
		modTime: f.modTime,
	}, nil
}

func (f *file) Sync() error { return nil }

func (f *file) Truncate(size int64) error {
	if !f.writable {
		return os.ErrPermission
	}
	if size < 0 {
		return errors.New("negative size")
	}
	switch {
	case size < int64(len(f.buf)):
		f.buf = f.buf[:size]
	case size > int64(len(f.buf)):
		next := make([]byte, size)
		copy(next, f.buf)
		f.buf = next
	}
	if f.offset > size {
		f.offset = size
	}
	f.modTime = time.Now().UTC()
	return nil
}

func (f *file) ReadDir(_ int) ([]fs.DirEntry, error) { return nil, errors.New("not a directory") }
func (f *file) Name() string                         { return f.name }

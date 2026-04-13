package s3

import (
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

const defaultOperationTimeout = 30 * time.Second

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
	client  *Client
	bucket  string
	prefix  string
	tempDir string
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
		file, err := b.openTempFile(name, key, false)
		if err != nil {
			return nil, err
		}
		return file, nil
	}

	var modTime time.Time
	var file *file
	if flags&os.O_TRUNC == 0 {
		existing, err := b.openTempFile(name, key, true)
		switch {
		case err == nil:
			file = existing
			modTime = existing.modTime
		case errors.Is(err, os.ErrNotExist):
			if flags&os.O_CREATE == 0 && flags&os.O_APPEND == 0 {
				return nil, err
			}
		default:
			return nil, err
		}
	}

	if file == nil {
		var err error
		file, err = b.newEmptyTempFile(name, key, true)
		if err != nil {
			return nil, err
		}
		file.modTime = modTime
	}
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
	ctx, cancel := b.operationContext()
	defer cancel()
	resp, err := b.client.HeadObject(ctx, b.bucket, key)
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
	ctx, cancel = b.operationContext()
	defer cancel()
	list, listErr := b.client.ListObjectsV2(ctx, b.bucket, dirPrefix, "/", 1)
	if listErr != nil {
		return nil, listErr
	}
	if len(list.Contents) > 0 || len(list.CommonPrefixes) > 0 {
		return fileInfo{name: path.Base(cleanName), mode: fs.ModeDir | 0o755, isDir: true, modTime: time.Now().UTC()}, nil
	}

	ctx, cancel = b.operationContext()
	defer cancel()
	marker, markerErr := b.client.HeadObject(ctx, b.bucket, dirPrefix)
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
	ctx, cancel := b.operationContext()
	defer cancel()
	if err := b.client.CopyObject(ctx, b.bucket, oldKey, b.bucket, newKey); err != nil {
		return mapS3Error(err)
	}
	ctx, cancel = b.operationContext()
	defer cancel()
	return mapS3Error(b.client.DeleteObject(ctx, b.bucket, oldKey))
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
		ctx, cancel := b.operationContext()
		defer cancel()
		return mapS3Error(b.client.DeleteObject(ctx, b.bucket, b.dirKey(cleanName)))
	}
	ctx, cancel := b.operationContext()
	defer cancel()
	return mapS3Error(b.client.DeleteObject(ctx, b.bucket, b.s3Key(cleanName)))
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
		ctx, cancel := b.operationContext()
		defer cancel()
		return mapS3Error(b.client.DeleteObject(ctx, b.bucket, b.s3Key(cleanName)))
	}
	prefix := b.dirKey(cleanName)
	if err := b.walkObjects(prefix, func(key string) error {
		ctx, cancel := b.operationContext()
		defer cancel()
		return b.client.DeleteObject(ctx, b.bucket, key)
	}); err != nil {
		return mapS3Error(err)
	}
	ctx, cancel := b.operationContext()
	defer cancel()
	return mapS3Error(b.client.DeleteObject(ctx, b.bucket, prefix))
}

func (b *Backend) Mkdir(name string, _ os.FileMode) error {
	key := b.dirKey(name)
	ctx, cancel := b.operationContext()
	defer cancel()
	return mapS3Error(b.client.PutObject(ctx, b.bucket, key, strings.NewReader(""), 0, "application/x-directory"))
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

	ctx, cancel := b.operationContext()
	defer cancel()
	response, err := b.client.ListObjectsV2(ctx, b.bucket, prefix, "/", 1000)
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

func (b *Backend) openTempFile(name, key string, writable bool) (*file, error) {
	ctx, cancel := b.operationContext()
	defer cancel()
	resp, err := b.client.GetObject(ctx, b.bucket, key)
	if err != nil {
		return nil, mapS3Error(err)
	}
	defer resp.Body.Close()
	tempFile, tempPath, err := b.createTempFile()
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, err
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, err
	}
	info, err := tempFile.Stat()
	if err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, err
	}
	return &file{
		name:     clean(name),
		key:      key,
		handle:   tempFile,
		tempPath: tempPath,
		size:     info.Size(),
		modTime:  nonZeroModTime(resp.LastModified),
		writable: writable,
		backend:  b,
	}, nil
}

func (b *Backend) newEmptyTempFile(name, key string, writable bool) (*file, error) {
	tempFile, tempPath, err := b.createTempFile()
	if err != nil {
		return nil, err
	}
	return &file{
		name:     clean(name),
		key:      key,
		handle:   tempFile,
		tempPath: tempPath,
		modTime:  time.Now().UTC(),
		writable: writable,
		backend:  b,
	}, nil
}

func (b *Backend) createTempFile() (*os.File, string, error) {
	tempFile, err := os.CreateTemp(b.tempDir, "kervan-s3-*")
	if err != nil {
		return nil, "", err
	}
	return tempFile, tempFile.Name(), nil
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
		ctx, cancel := b.operationContext()
		err := b.client.CopyObject(ctx, b.bucket, key, b.bucket, target)
		cancel()
		if err != nil {
			return mapS3Error(err)
		}
	}
	for _, key := range keys {
		ctx, cancel := b.operationContext()
		err := b.client.DeleteObject(ctx, b.bucket, key)
		cancel()
		if err != nil {
			return mapS3Error(err)
		}
	}
	return nil
}

func (b *Backend) walkObjects(prefix string, fn func(key string) error) error {
	token := ""
	for {
		ctx, cancel := b.operationContext()
		resp, err := b.client.ListObjectsV2WithToken(ctx, b.bucket, prefix, "", 1000, token)
		cancel()
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
	handle   *os.File
	tempPath string
	size     int64
	modTime  time.Time
	writable bool
	closed   bool
	backend  *Backend
}

func (f *file) Read(p []byte) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	return f.handle.Read(p)
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	return f.handle.ReadAt(p, off)
}

func (f *file) Write(p []byte) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if !f.writable {
		return 0, os.ErrPermission
	}
	n, err := f.handle.Write(p)
	if err != nil {
		return n, err
	}
	if pos, seekErr := f.handle.Seek(0, io.SeekCurrent); seekErr == nil && pos > f.size {
		f.size = pos
	}
	f.modTime = time.Now().UTC()
	return n, nil
}

func (f *file) WriteAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	if !f.writable {
		return 0, os.ErrPermission
	}
	n, err := f.handle.WriteAt(p, off)
	if end := off + int64(n); end > f.size {
		f.size = end
	}
	if err == nil {
		f.modTime = time.Now().UTC()
	}
	return n, err
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, os.ErrClosed
	}
	return f.handle.Seek(offset, whence)
}

func (f *file) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	var result error
	if f.writable {
		if _, err := f.handle.Seek(0, io.SeekStart); err != nil {
			result = err
		} else {
			if info, err := f.handle.Stat(); err == nil {
				f.size = info.Size()
			}
			ctx, cancel := f.backend.operationContext()
			err := f.backend.client.PutObject(ctx, f.backend.bucket, f.key, f.handle, f.size, "application/octet-stream")
			cancel()
			if err != nil {
				result = err
			}
		}
	}
	if err := f.handle.Close(); result == nil && err != nil && !errors.Is(err, os.ErrClosed) {
		result = err
	}
	if err := os.Remove(f.tempPath); result == nil && err != nil && !errors.Is(err, os.ErrNotExist) {
		result = err
	}
	return result
}

func (f *file) Stat() (os.FileInfo, error) {
	if f.closed {
		return nil, os.ErrClosed
	}
	info, err := f.handle.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfo{
		name:    path.Base(f.name),
		size:    info.Size(),
		mode:    0o644,
		modTime: f.modTime,
	}, nil
}

func (f *file) Sync() error {
	if f.closed {
		return os.ErrClosed
	}
	return f.handle.Sync()
}

func (f *file) Truncate(size int64) error {
	if f.closed {
		return os.ErrClosed
	}
	if !f.writable {
		return os.ErrPermission
	}
	if size < 0 {
		return errors.New("negative size")
	}
	if err := f.handle.Truncate(size); err != nil {
		return err
	}
	f.size = size
	if pos, err := f.handle.Seek(0, io.SeekCurrent); err == nil && pos > size {
		if _, err := f.handle.Seek(size, io.SeekStart); err != nil {
			return err
		}
	}
	f.modTime = time.Now().UTC()
	return nil
}

func (f *file) ReadDir(_ int) ([]fs.DirEntry, error) { return nil, errors.New("not a directory") }
func (f *file) Name() string                         { return f.name }

func nonZeroModTime(modTime time.Time) time.Time {
	if modTime.IsZero() {
		return time.Now().UTC()
	}
	return modTime
}

func (b *Backend) operationContext() (context.Context, context.CancelFunc) {
	timeout := defaultOperationTimeout
	if b != nil && b.client != nil && b.client.httpClient != nil && b.client.httpClient.Timeout > 0 {
		timeout = b.client.httpClient.Timeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

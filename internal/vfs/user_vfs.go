package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/throttle"
)

type QuotaTracker interface {
	OnGrow(n int64) error
	OnShrink(n int64)
	// OnFileCreated reserves a new regular file; OnFilesRemoved releases.
	OnFileCreated() error
	OnFilesRemoved(n int64)
}

// QuotaChangeMarker is implemented by quota trackers that are shared between
// sessions and occasionally re-measured from storage. UserVFS brackets every
// change to usage (an open write handle, a remove, a rename) with
// BeginChange/EndChange, so a re-measurement never runs while reserved or
// released usage has not yet reached storage.
type QuotaChangeMarker interface {
	BeginChange()
	EndChange()
}

// Releaser is implemented by filesystems that hold per-session state which
// must be given back when the session ends, whether or not the client closed
// everything it opened.
type Releaser interface {
	Release()
}

// ReleaseFS releases fsys if it holds per-session state. It is safe to call
// with nil and with filesystems that hold none.
func ReleaseFS(fsys FileSystem) {
	if r, ok := fsys.(Releaser); ok {
		r.Release()
	}
}

// UserVFS is one session's view of a user's storage: one per FTP/SFTP/SCP
// connection or WebUI/API request. Its owner calls Release when the session
// ends.
type UserVFS struct {
	mounts      *MountTable
	resolver    *Resolver
	permissions *UserPermissions
	quota       QuotaTracker
	limiters    []*throttle.Limiter

	// changes holds the quota changes this session has begun and not yet
	// ended (mostly open write handles), so Release can end them.
	changesMu  sync.Mutex
	changes    map[uint64]func()
	nextChange uint64
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

	// A new file counts against the file-count quota; overwriting an
	// existing one does not.
	reservedFile := false
	// truncatedBytes is the size of the previous version of a file that is
	// about to be O_TRUNC'd: those bytes are about to be discarded from disk,
	// so their quota has to be released. It must be read BEFORE the open,
	// because O_TRUNC is applied by the backend at open time and a Stat
	// afterwards always reports 0 — the old usage would then leak for the
	// life of the process. One Stat serves both quota facts, so a write open
	// pays at most one extra HEAD on the S3 backend.
	var truncatedBytes int64
	// The change stays open until the write handle is closed.
	endChange := func() {}
	if isWrite && u.quota != nil {
		endChange = u.beginQuotaChange()
	}
	if isWrite && u.quota != nil && (flags&os.O_CREATE != 0 || flags&os.O_TRUNC != 0) {
		info, statErr := backend.Stat(relPath)
		switch {
		case statErr != nil && errors.Is(statErr, os.ErrNotExist) && flags&os.O_CREATE != 0:
			if err := u.quota.OnFileCreated(); err != nil {
				endChange()
				return nil, err
			}
			reservedFile = true
		case statErr == nil && info != nil && !info.IsDir() && flags&os.O_TRUNC != 0:
			truncatedBytes = info.Size()
		}
	}
	f, err := backend.Open(relPath, flags, perm)
	if err != nil {
		if reservedFile {
			u.quota.OnFilesRemoved(1)
		}
		endChange()
		return nil, err
	}
	if len(u.limiters) > 0 {
		f = &throttledFile{File: f, limiters: u.limiters}
	}
	if isWrite && u.quota != nil {
		if truncatedBytes > 0 {
			u.quota.OnShrink(truncatedBytes)
		}
		// O_TRUNC already discarded the previous contents, so the file
		// starts empty no matter what a post-open Stat reports.
		initialSize := int64(0)
		if flags&os.O_TRUNC == 0 {
			if info, statErr := f.Stat(); statErr == nil && info != nil {
				initialSize = info.Size()
			}
		}
		initialOffset := int64(0)
		if flags&os.O_APPEND != 0 {
			initialOffset = initialSize
		}
		return &quotaFile{
			File:        f,
			quota:       u.quota,
			size:        initialSize,
			offset:      initialOffset,
			maxFileSize: u.permissions.MaxFileSize,
			endChange:   endChange,
		}, nil
	}
	return f, nil
}

// beginQuotaChange marks a usage change in progress on a shared tracker and
// returns the function that ends it; calling that more than once is safe.
// The change is recorded so Release can end it if the caller never does.
func (u *UserVFS) beginQuotaChange() func() {
	marker, ok := u.quota.(QuotaChangeMarker)
	if !ok {
		return func() {}
	}
	marker.BeginChange()
	u.changesMu.Lock()
	defer u.changesMu.Unlock()
	if u.changes == nil {
		u.changes = make(map[uint64]func())
	}
	id := u.nextChange
	u.nextChange++
	var once sync.Once
	end := func() {
		once.Do(func() {
			u.changesMu.Lock()
			delete(u.changes, id)
			u.changesMu.Unlock()
			marker.EndChange()
		})
	}
	u.changes[id] = end
	return end
}

// Release ends every quota change this session still has open, typically
// write handles a disconnected or aborted client never closed. Without it
// one leaked handle would keep the user's shared tracker "busy" and stop
// re-measuring from storage for good. Handles closed later end nothing a
// second time. Only the session that owns this UserVFS may call Release.
func (u *UserVFS) Release() {
	if u == nil {
		return
	}
	u.changesMu.Lock()
	ends := make([]func(), 0, len(u.changes))
	for _, end := range u.changes {
		ends = append(ends, end)
	}
	u.changesMu.Unlock()
	for _, end := range ends {
		end()
	}
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
	// Renaming onto an existing file replaces it: release its usage, or the
	// quota would keep counting data that no longer exists.
	var replacedBytes, replacedFiles int64
	if u.quota != nil {
		defer u.beginQuotaChange()()
	}
	if u.quota != nil && oldRel != newRel {
		if info, err := newBackend.Stat(newRel); err == nil && !info.IsDir() {
			replacedBytes, replacedFiles = info.Size(), 1
		}
	}
	if err := oldBackend.Rename(oldRel, newRel); err != nil {
		return err
	}
	if u.quota != nil {
		u.quota.OnShrink(replacedBytes)
		u.quota.OnFilesRemoved(replacedFiles)
	}
	return nil
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
	var reclaimBytes, reclaimFiles int64
	if u.quota != nil {
		defer u.beginQuotaChange()()
		reclaimBytes, reclaimFiles, _ = measureUsageForQuota(backend, rel)
	}
	if err := backend.Remove(rel); err != nil {
		return err
	}
	if u.quota != nil {
		u.quota.OnShrink(reclaimBytes)
		u.quota.OnFilesRemoved(reclaimFiles)
	}
	return nil
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
	var reclaimBytes, reclaimFiles int64
	if u.quota != nil {
		defer u.beginQuotaChange()()
		reclaimBytes, reclaimFiles, _ = measureUsageForQuota(backend, rel)
	}
	if err := backend.RemoveAll(rel); err != nil {
		return err
	}
	if u.quota != nil {
		u.quota.OnShrink(reclaimBytes)
		u.quota.OnFilesRemoved(reclaimFiles)
	}
	return nil
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
	// Changing timestamps is metadata like chmod (SFTP SETSTAT treats the
	// chmod permission as its gate: "a user without the chmod permission
	// gets OK with the change skipped"), so it is denied without it.
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
	return backend.Chtimes(rel, atime, mtime)
}

// QuotaReporter is implemented by quota trackers that can report usage, so
// free-space queries reflect the user's quota rather than the whole disk.
type QuotaReporter interface {
	Usage() (usedBytes, maxBytes, usedFiles, maxFiles int64)
}

func (u *UserVFS) Statvfs(p string) (*StatVFS, error) {
	backend, rel, _, err := u.lookup(p)
	if err != nil {
		return nil, err
	}
	st, err := backend.Statvfs(rel)
	if err != nil {
		return nil, err
	}
	if reporter, ok := u.quota.(QuotaReporter); ok {
		capStatVFS(st, reporter)
	}
	return st, nil
}

// capStatVFS narrows backend free-space figures to the user's quota. A
// backend that reports nothing (zero totals) takes the quota figures as is.
func capStatVFS(st *StatVFS, r QuotaReporter) {
	usedBytes, maxBytes, usedFiles, maxFiles := r.Usage()
	if maxBytes > 0 {
		if st.BlockSize == 0 {
			st.BlockSize = 4096
		}
		known := st.TotalBlocks > 0
		total := uint64(maxBytes) / st.BlockSize                  // #nosec G115 -- maxBytes > 0
		free := uint64(max(maxBytes-usedBytes, 0)) / st.BlockSize // #nosec G115 -- non-negative
		st.TotalBlocks = capCount(st.TotalBlocks, total, known)
		st.FreeBlocks = capCount(st.FreeBlocks, free, known)
		st.AvailBlocks = capCount(st.AvailBlocks, free, known)
	}
	if maxFiles > 0 {
		known := st.TotalFiles > 0
		st.TotalFiles = capCount(st.TotalFiles, uint64(maxFiles), known)                 // #nosec G115 -- maxFiles > 0
		st.FreeFiles = capCount(st.FreeFiles, uint64(max(maxFiles-usedFiles, 0)), known) // #nosec G115 -- non-negative
	}
}

func capCount(backend, quota uint64, known bool) uint64 {
	if !known {
		return quota
	}
	return min(backend, quota)
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
	quota       QuotaTracker
	size        int64
	offset      int64
	maxFileSize int64
	// endChange ends the quota change opened for this write handle.
	endChange func()
}

// Close ends the handle's quota change only after the backend has finished
// writing (S3 uploads on Close), so a re-measurement sees the final size.
func (q *quotaFile) Close() error {
	err := q.File.Close()
	if q.endChange != nil {
		q.endChange()
	}
	return err
}

func (q *quotaFile) Write(p []byte) (int, error) {
	if q.maxFileSize > 0 && q.offset+int64(len(p)) > q.maxFileSize {
		return 0, ErrFileTooLarge
	}
	growth := projectedGrowth(q.size, q.offset, int64(len(p)))
	if growth > 0 {
		if quotaErr := q.quota.OnGrow(growth); quotaErr != nil {
			return 0, quotaErr
		}
	}
	previousSize := q.size
	previousOffset := q.offset
	n, err := q.File.Write(p)
	actualEnd := previousOffset + int64(n)
	if actualEnd > q.size {
		q.size = actualEnd
	}
	q.offset = actualEnd
	actualGrowth := q.size - previousSize
	if growth > actualGrowth {
		q.quota.OnShrink(growth - actualGrowth)
	}
	return n, err
}

func (q *quotaFile) Read(p []byte) (int, error) {
	n, err := q.File.Read(p)
	q.offset += int64(n)
	return n, err
}

func (q *quotaFile) ReadAt(p []byte, off int64) (int, error) {
	return q.File.ReadAt(p, off)
}

func (q *quotaFile) WriteAt(p []byte, off int64) (int, error) {
	current := q.offset
	q.offset = off
	n, err := q.Write(p)
	q.offset = current
	return n, err
}

func (q *quotaFile) Seek(offset int64, whence int) (int64, error) {
	next, err := q.File.Seek(offset, whence)
	if err == nil {
		q.offset = next
	}
	return next, err
}

func (q *quotaFile) Truncate(size int64) error {
	if q.maxFileSize > 0 && size > q.maxFileSize {
		return ErrFileTooLarge
	}
	growth := int64(0)
	if size > q.size {
		growth = size - q.size
		if err := q.quota.OnGrow(growth); err != nil {
			return err
		}
	}
	if err := q.File.Truncate(size); err != nil {
		if growth > 0 {
			q.quota.OnShrink(growth)
		}
		return err
	}
	if size < q.size {
		q.quota.OnShrink(q.size - size)
	}
	q.size = size
	if q.offset > size {
		q.offset = size
	}
	return nil
}

func projectedGrowth(currentSize, offset, writeLen int64) int64 {
	end := offset + writeLen
	if end <= currentSize {
		return 0
	}
	return end - currentSize
}

// measureUsageForQuota returns the bytes and regular-file count at name.
func measureUsageForQuota(fsys FileSystem, name string) (int64, int64, error) {
	info, err := fsys.Stat(name)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		return info.Size(), 1, nil
	}
	entries, err := fsys.ReadDir(name)
	if err != nil {
		return 0, 0, err
	}
	var bytes, files int64
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		b, f, err := measureUsageForQuota(fsys, path.Join(name, entry.Name()))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return bytes, files, err
		}
		bytes += b
		files += f
	}
	return bytes, files, nil
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
func (m mountDirEntry) Info() (fs.FileInfo, error) { return mountInfo(m), nil }

type mountInfo struct {
	name string
}

func (m mountInfo) Name() string       { return m.name }
func (m mountInfo) Size() int64        { return 0 }
func (m mountInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (m mountInfo) ModTime() time.Time { return time.Now().UTC() }
func (m mountInfo) IsDir() bool        { return true }
func (m mountInfo) Sys() any           { return nil }

package quota

import (
	"errors"
	"math"
	"os"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
)

var (
	ErrStorageExceeded   = errors.New("storage quota exceeded")
	ErrFileCountExceeded = errors.New("file count quota exceeded")
)

// Tracker enforces a user's storage and file-count quotas. Limits of 0 are
// unlimited. Only regular files count toward the file limit.
//
// One Tracker must be shared by all of a user's sessions (see Registry):
// each session holding its own copy would let parallel uploads together
// exceed the quota, because every copy starts from the same measurement.
type Tracker struct {
	mu         sync.Mutex
	usedBytes  int64
	maxStorage int64
	usedFiles  int64
	maxFiles   int64

	// inflight counts changes in progress (open write handles, removes,
	// renames). Usage is only re-measured from storage while it is zero,
	// because a walk cannot see bytes that are reserved but not yet
	// written, or a removal that is about to be released.
	inflight int64
	// busySince is when inflight last went from zero to non-zero. A tracker
	// busy for much longer than the re-measure interval most likely holds a
	// change that will never end (a code path that dropped a write handle
	// without Close or Release), so refresh stops waiting for it.
	busySince time.Time
	// gen advances on every change, so a re-measurement that raced one is
	// discarded instead of overwriting the incremental figures.
	gen        uint64
	measured   bool
	measuredAt time.Time
	// measureMu serializes storage walks for this tracker.
	measureMu sync.Mutex
}

func NewTracker(fsys vfs.FileSystem, maxStorage, maxFiles int64) (*Tracker, error) {
	t := &Tracker{maxStorage: maxStorage, maxFiles: maxFiles}
	if err := t.refresh(fsys, 0, time.Now()); err != nil {
		return nil, err
	}
	return t, nil
}

// SetLimits updates the limits, e.g. after an admin changed the user's or
// group's quota. Usage above a lowered limit blocks further growth only.
func (t *Tracker) SetLimits(maxStorage, maxFiles int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxStorage, t.maxFiles = maxStorage, maxFiles
}

// BeginChange marks a change in progress; it must be paired with EndChange.
// While any change is in progress, usage is not re-measured from storage.
func (t *Tracker) BeginChange() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == 0 {
		t.busySince = time.Now()
	}
	t.inflight++
	t.gen++
}

// EndChange ends a change started with BeginChange.
func (t *Tracker) EndChange() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight = max(t.inflight-1, 0)
	if t.inflight == 0 {
		t.busySince = time.Time{}
	}
	t.gen++
}

// StuckChangeFactor bounds how long changes in progress may hold off a
// re-measurement: once a tracker has been busy without a break for this many
// re-measure intervals, refresh walks storage anyway. Every session ends the
// changes it began (UserVFS.Release), so this only matters if some code path
// leaks one; without the bound, one leak would stop re-measuring for that
// user for the life of the process.
const StuckChangeFactor = 10

// MinStuckAge is the shortest time changes must be in progress before they
// count as stuck, whatever the re-measure interval. Without it a very short
// quota.check_interval would declare every ordinary upload stuck and let a
// walk run under it.
const MinStuckAge = 10 * time.Minute

// stuckLocked reports whether changes have been in progress, without a
// break, for longer than StuckChangeFactor re-measure intervals (and at
// least MinStuckAge). maxAge <= 0 (re-measure whenever idle) sets no bound.
// Callers hold t.mu.
func (t *Tracker) stuckLocked(maxAge time.Duration, now time.Time) bool {
	if t.inflight == 0 || maxAge <= 0 {
		return false
	}
	bound := MinStuckAge
	if maxAge <= math.MaxInt64/StuckChangeFactor {
		bound = max(bound, maxAge*StuckChangeFactor)
	} else {
		return false // an interval this long never re-measures anyway
	}
	return now.Sub(t.busySince) >= bound
}

// refresh re-measures usage from fsys when the tracker has never been
// measured, or when it is idle (no change in progress) and its last
// measurement is older than maxAge (maxAge <= 0: whenever idle). Picking up
// files changed outside Kervan is the point; between refreshes usage is
// tracked incrementally. A failed re-measurement keeps the incremental
// figures; only a failed first measurement is an error.
//
// Changes in progress normally hold off the walk, because it cannot see
// bytes reserved but not yet written. If they never stop (see
// StuckChangeFactor), the walk runs anyway: usage may then briefly undercount
// uploads that are genuinely in progress, which the next idle re-measurement
// corrects, instead of never picking up outside changes again.
func (t *Tracker) refresh(fsys vfs.FileSystem, maxAge time.Duration, now time.Time) error {
	t.measureMu.Lock()
	defer t.measureMu.Unlock()

	t.mu.Lock()
	stuck := t.stuckLocked(maxAge, now)
	busy := t.inflight > 0 && !stuck
	if t.measured && (busy || (maxAge > 0 && now.Sub(t.measuredAt) < maxAge)) {
		t.mu.Unlock()
		return nil
	}
	gen, measured := t.gen, t.measured
	t.mu.Unlock()

	usedBytes, usedFiles, err := MeasureUsage(fsys, "/")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if measured {
			return nil
		}
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.measured && (t.gen != gen || (t.inflight > 0 && !stuck)) {
		// A change ran during the walk; the walk may have seen it only in
		// part. Keep the incremental figures and re-measure next time.
		return nil
	}
	t.usedBytes, t.usedFiles = usedBytes, usedFiles
	t.measured, t.measuredAt = true, now
	return nil
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
	t.gen++
	return nil
}

func (t *Tracker) OnShrink(n int64) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usedBytes = max(t.usedBytes-n, 0)
	t.gen++
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
	t.gen++
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
	t.gen++
}

// Usage reports current usage against the limits (0 = unlimited).
func (t *Tracker) Usage() (usedBytes, maxBytes, usedFiles, maxFiles int64) {
	if t == nil {
		return 0, 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usedBytes, t.maxStorage, t.usedFiles, t.maxFiles
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
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxStorage
}

// Registry hands out one shared Tracker per user, so all of a user's
// sessions (FTP, SFTP, SCP and WebUI/API requests) draw on one quota.
type Registry struct {
	mu    sync.Mutex
	users map[string]*registryEntry
}

type registryEntry struct {
	scope   string
	tracker *Tracker
}

func NewRegistry() *Registry {
	return &Registry{users: make(map[string]*registryEntry)}
}

// ForUser returns the shared tracker for the account identified by key, with
// the given limits. key must be stable for the life of the account and never
// reused by another one (the user ID): keyed by username, a renamed account
// would split its quota between old and new sessions, and a deleted account
// re-created under the same name would inherit the old usage figures.
//
// scope identifies the storage the tracker measures (backend and home
// directory): when it changes, a fresh tracker replaces the old one. Sessions
// that already hold the old tracker keep it until they end.
//
// Usage is measured from fsys the first time, and re-measured when the
// tracker is idle and its last measurement is older than maxAge (maxAge <= 0
// re-measures whenever it is idle), so files changed outside Kervan are
// picked up without letting a re-measurement erase another session's
// reservations.
func (r *Registry) ForUser(key, scope string, fsys vfs.FileSystem, maxStorage, maxFiles int64, maxAge time.Duration) (*Tracker, error) {
	if r == nil {
		return NewTracker(fsys, maxStorage, maxFiles)
	}
	r.mu.Lock()
	entry := r.users[key]
	if entry == nil || entry.scope != scope {
		entry = &registryEntry{scope: scope, tracker: &Tracker{}}
		r.users[key] = entry
	}
	t := entry.tracker
	r.mu.Unlock()

	t.SetLimits(maxStorage, maxFiles)
	// The walk runs outside the registry lock, so one user's large tree
	// does not delay everyone else's sign-in.
	if err := t.refresh(fsys, maxAge, time.Now()); err != nil {
		return nil, err
	}
	return t, nil
}

// Prune drops the trackers whose key keep rejects, e.g. accounts that were
// deleted, and reports how many it dropped. keep runs without the registry
// lock held, so it may do I/O. Sessions still holding a dropped tracker keep
// using it until they end.
func (r *Registry) Prune(keep func(key string) bool) int {
	if r == nil || keep == nil {
		return 0
	}
	r.mu.Lock()
	snapshot := make(map[string]*registryEntry, len(r.users))
	for key, entry := range r.users {
		snapshot[key] = entry
	}
	r.mu.Unlock()

	dropped := 0
	for key, entry := range snapshot {
		if keep(key) {
			continue
		}
		r.mu.Lock()
		// Drop only the entry that was checked, not one created since.
		if r.users[key] == entry {
			delete(r.users, key)
			dropped++
		}
		r.mu.Unlock()
	}
	return dropped
}

// Len reports how many trackers the registry holds.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.users)
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

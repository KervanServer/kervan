package quota

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func seedFile(t *testing.T, fsys vfs.FileSystem, name string, size int) {
	t.Helper()
	f, err := fsys.Open(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySharesTrackerPerUser(t *testing.T) {
	fsys := memory.New()
	seedFile(t, fsys, "/seed", 4)
	r := NewRegistry()

	a, err := r.ForUser("alice", "s", fsys, 10, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.ForUser("alice", "s", fsys, 10, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("two sessions of one user got different trackers")
	}
	other, err := r.ForUser("bob", "s", fsys, 10, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if other == a {
		t.Fatal("different users share a tracker")
	}

	if err := a.OnGrow(6); err != nil { // 4 measured + 6 = 10
		t.Fatal(err)
	}
	if err := b.OnGrow(1); !errors.Is(err, ErrStorageExceeded) {
		t.Fatalf("shared tracker allowed growth past the limit: %v", err)
	}
}

func TestRegistryUpdatesLimitsOfSharedTracker(t *testing.T) {
	r := NewRegistry()
	fsys := memory.New()
	tr, err := r.ForUser("alice", "s", fsys, 2, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.OnGrow(3); !errors.Is(err, ErrStorageExceeded) {
		t.Fatalf("limit 2 not applied: %v", err)
	}
	if _, err := r.ForUser("alice", "s", fsys, 8, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := tr.OnGrow(3); err != nil {
		t.Fatalf("raised limit did not reach the tracker already in use: %v", err)
	}
	if err := tr.OnFileCreated(); err != nil {
		t.Fatal(err)
	}
	if err := tr.OnFileCreated(); !errors.Is(err, ErrFileCountExceeded) {
		t.Fatalf("file limit 1 not applied: %v", err)
	}
}

func TestRegistryScopeChangeStartsFreshTracker(t *testing.T) {
	r := NewRegistry()
	home1, home2 := memory.New(), memory.New()
	seedFile(t, home1, "/a", 5)
	t1, err := r.ForUser("alice", "home1", home1, 0, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := r.ForUser("alice", "home2", home2, 0, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if t1 == t2 {
		t.Fatal("tracker reused after the user's storage changed")
	}
	if got := t2.UsedBytes(); got != 0 {
		t.Fatalf("new home measured %d bytes, want 0", got)
	}
}

// Files changed outside Kervan are picked up once the measurement is older
// than maxAge, but only while no session has a change in progress.
func TestRegistryRemeasuresOnlyWhenIdleAndStale(t *testing.T) {
	r := NewRegistry()
	fsys := memory.New()
	tr, err := r.ForUser("alice", "s", fsys, 0, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	seedFile(t, fsys, "/external", 7) // written behind Kervan's back

	// Fresh measurement: not re-measured yet.
	if _, err := r.ForUser("alice", "s", fsys, 0, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 0 {
		t.Fatalf("re-measured before maxAge elapsed: %d", got)
	}

	// Stale, but a change is in progress: the walk would miss reserved
	// bytes, so the incremental figures stay.
	tr.BeginChange()
	if err := tr.OnGrow(3); err != nil {
		t.Fatal(err)
	}
	if err := tr.refresh(fsys, time.Hour, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 3 {
		t.Fatalf("re-measured during an in-progress change: used = %d, want 3", got)
	}
	tr.EndChange()

	// Stale and idle: re-measured from storage.
	if err := tr.refresh(fsys, time.Hour, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, files := tr.UsedBytes(), tr.UsedFiles(); got != 7 || files != 1 {
		t.Fatalf("after idle re-measure: %d bytes / %d files, want 7 / 1", got, files)
	}
}

// A change that lands while a re-measurement walks storage must not be
// overwritten by the walk's (possibly partial) result.
func TestTrackerDiscardsMeasurementThatRacedAChange(t *testing.T) {
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	walk := &hookFS{FileSystem: fsys, onStat: func() {
		_ = tr.OnGrow(5) // another session writes mid-walk
	}}
	if err := tr.refresh(walk, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 5 {
		t.Fatalf("raced measurement overwrote the incremental figure: used = %d, want 5", got)
	}
}

func TestTrackerFailedRemeasureKeepsFigures(t *testing.T) {
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.OnGrow(9)
	broken := &hookFS{FileSystem: fsys, statErr: errors.New("backend down")}
	if err := tr.refresh(broken, 0, time.Now()); err != nil {
		t.Fatalf("a failed re-measurement must not fail the sign-in: %v", err)
	}
	if got := tr.UsedBytes(); got != 9 {
		t.Fatalf("used = %d, want 9", got)
	}
	if _, err := NewTracker(broken, 0, 0); err == nil {
		t.Fatal("a failed first measurement must be reported")
	}
}

func TestRegistryPruneDropsRejectedKeys(t *testing.T) {
	r := NewRegistry()
	fsys := memory.New()
	for _, key := range []string{"id:1", "id:2", "id:3"} {
		if _, err := r.ForUser(key, "s", fsys, 0, 0, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	dropped := r.Prune(func(key string) bool { return key != "id:2" })
	if dropped != 1 || r.Len() != 2 {
		t.Fatalf("Prune dropped %d, %d left; want 1 dropped, 2 left", dropped, r.Len())
	}
	// A key created again after pruning gets a fresh tracker.
	before, _ := r.ForUser("id:1", "s", fsys, 0, 0, time.Hour)
	_ = before.OnGrow(5)
	r.Prune(func(key string) bool { return key != "id:1" })
	after, _ := r.ForUser("id:1", "s", fsys, 0, 0, time.Hour)
	if after == before || after.UsedBytes() != 0 {
		t.Fatal("pruned key reused its old tracker")
	}
	var nilReg *Registry
	if nilReg.Prune(func(string) bool { return false }) != 0 || nilReg.Len() != 0 {
		t.Fatal("nil registry must be inert")
	}
}

// A change that never ends (a write handle some code path dropped without
// Close or Release) must not stop re-measurement for good: once the tracker
// has been busy for StuckChangeFactor intervals, refresh walks anyway.
func TestTrackerRemeasuresDespiteStuckChange(t *testing.T) {
	const interval = time.Minute
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr.BeginChange() // leaked: never ended
	seedFile(t, fsys, "/outside", 7)

	start := time.Now()
	// Stale, but busy for less than the bound: the change is trusted.
	if err := tr.refresh(fsys, interval, start.Add((StuckChangeFactor-1)*interval)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 0 {
		t.Fatalf("re-measured while a change was in progress for under %d intervals: used = %d", StuckChangeFactor, got)
	}

	// Busy past the bound: re-measured.
	if err := tr.refresh(fsys, interval, start.Add((StuckChangeFactor+1)*interval)); err != nil {
		t.Fatal(err)
	}
	if got, files := tr.UsedBytes(), tr.UsedFiles(); got != 7 || files != 1 {
		t.Fatalf("stuck change still blocks re-measurement: %d bytes / %d files, want 7 / 1", got, files)
	}

	// It keeps being re-measured once per interval, not on every call.
	seedFile(t, fsys, "/more", 3)
	if err := tr.refresh(fsys, interval, start.Add((StuckChangeFactor+1)*interval+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 7 {
		t.Fatalf("re-measured before the interval elapsed: used = %d", got)
	}
	if err := tr.refresh(fsys, interval, start.Add((StuckChangeFactor+2)*interval+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 10 {
		t.Fatalf("stuck tracker not re-measured on the next interval: used = %d, want 10", got)
	}
}

// The bound counts from when the tracker last became busy, so ordinary
// back-to-back uploads, each of which ends, never count as stuck, and with
// maxAge <= 0 there is no bound at all.
func TestTrackerStuckBoundResetsWhenIdle(t *testing.T) {
	const interval = time.Minute
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr.BeginChange()
	tr.EndChange()
	tr.BeginChange() // a new change, busy from now
	seedFile(t, fsys, "/outside", 4)

	tr.mu.Lock()
	tr.busySince = time.Now()
	tr.mu.Unlock()
	if err := tr.refresh(fsys, interval, time.Now().Add(2*interval)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 0 {
		t.Fatalf("a freshly begun change was treated as stuck: used = %d", got)
	}
	if err := tr.refresh(fsys, 0, time.Now().Add(1000*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 0 {
		t.Fatalf("maxAge <= 0 must set no stuck bound: used = %d", got)
	}
	tr.EndChange()
}

// A stuck tracker whose change is genuinely active (bytes keep arriving)
// still discards a walk that raced a write, so a long upload is never
// overwritten by a partial view of it.
func TestTrackerStuckWalkStillDiscardedOnRace(t *testing.T) {
	const interval = time.Minute
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr.BeginChange()
	walk := &hookFS{FileSystem: fsys, onStat: func() {
		_ = tr.OnGrow(5) // the long upload writes during the walk
	}}
	if err := tr.refresh(walk, interval, time.Now().Add((StuckChangeFactor+1)*interval)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 5 {
		t.Fatalf("walk that raced an active upload overwrote it: used = %d, want 5", got)
	}
	tr.EndChange()
}

// A very short check_interval must not make ordinary uploads count as
// stuck: the bound is never shorter than MinStuckAge.
func TestTrackerStuckBoundHasFloor(t *testing.T) {
	const interval = time.Second // 10 intervals = 10s, far below the floor
	fsys := memory.New()
	tr, err := NewTracker(fsys, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr.BeginChange()
	seedFile(t, fsys, "/outside", 6)
	start := time.Now()

	if err := tr.refresh(fsys, interval, start.Add(MinStuckAge-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 0 {
		t.Fatalf("an upload in progress for under MinStuckAge was treated as stuck: used = %d", got)
	}
	if err := tr.refresh(fsys, interval, start.Add(MinStuckAge+time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := tr.UsedBytes(); got != 6 {
		t.Fatalf("not re-measured once past MinStuckAge: used = %d, want 6", got)
	}
	tr.EndChange()
}

type hookFS struct {
	vfs.FileSystem
	onStat  func()
	statErr error
}

func (h *hookFS) Stat(name string) (os.FileInfo, error) {
	if h.statErr != nil {
		return nil, h.statErr
	}
	if h.onStat != nil {
		h.onStat()
	}
	return h.FileSystem.Stat(name)
}

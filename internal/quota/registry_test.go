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

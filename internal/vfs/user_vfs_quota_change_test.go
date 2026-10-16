package vfs_test

import (
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// markingQuota records BeginChange/EndChange so tests can check that every
// usage change is bracketed, and that a write handle keeps its change open
// until Close.
type markingQuota struct {
	quotaStub
	inflight int
	begun    int
}

func (m *markingQuota) BeginChange() { m.inflight++; m.begun++ }
func (m *markingQuota) EndChange()   { m.inflight-- }

func TestUserVFSBracketsQuotaChanges(t *testing.T) {
	q := &markingQuota{}
	u := vfs.NewUserVFS(func() *vfs.MountTable {
		mt := vfs.NewMountTable()
		mt.Mount("/", memory.New(), false)
		return mt
	}(), nil, q)

	f, err := u.Open("/a", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if q.inflight != 1 {
		t.Fatalf("open write handle: inflight = %d, want 1", q.inflight)
	}
	if _, err := f.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // a second Close must not end the change twice
	if q.inflight != 0 {
		t.Fatalf("after Close: inflight = %d, want 0", q.inflight)
	}

	// Reads do not touch the quota.
	r, err := u.Open("/a", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if q.begun != 1 {
		t.Fatalf("read open began a quota change (begun = %d)", q.begun)
	}

	if err := u.Rename("/a", "/b"); err != nil {
		t.Fatal(err)
	}
	if err := u.Remove("/b"); err != nil {
		t.Fatal(err)
	}
	if err := u.MkdirAll("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := u.RemoveAll("/d"); err != nil {
		t.Fatal(err)
	}
	if q.inflight != 0 || q.begun != 4 {
		t.Fatalf("rename/remove/removeall: inflight = %d, begun = %d, want 0 and 4", q.inflight, q.begun)
	}

	// A refused open ends its change.
	q.maxFiles, q.files = 1, 1
	if _, err := u.Open("/c", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		t.Fatal("file-count quota not enforced")
	}
	if q.inflight != 0 {
		t.Fatalf("refused open left a change open: inflight = %d", q.inflight)
	}
}

// Release ends the changes of handles the session never closed, once, and
// a later Close of such a handle does not end it again.
func TestUserVFSReleaseEndsUnclosedChanges(t *testing.T) {
	q := &markingQuota{}
	mt := vfs.NewMountTable()
	mt.Mount("/", memory.New(), false)
	u := vfs.NewUserVFS(mt, nil, q)

	leaked1, err := u.Open("/a", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Open("/b", os.O_CREATE|os.O_WRONLY, 0o644); err != nil {
		t.Fatal(err)
	}
	closed, err := u.Open("/c", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	if q.inflight != 2 {
		t.Fatalf("inflight = %d before Release, want 2", q.inflight)
	}

	vfs.ReleaseFS(u)
	if q.inflight != 0 {
		t.Fatalf("inflight = %d after Release, want 0", q.inflight)
	}
	_ = leaked1.Close()
	vfs.ReleaseFS(u)
	if q.inflight != 0 {
		t.Fatalf("late Close or a second Release ended a change twice: inflight = %d", q.inflight)
	}

	vfs.ReleaseFS(nil)                                           // nil-safe
	vfs.ReleaseFS(memory.New())                                  // not a Releaser
	vfs.ReleaseFS(vfs.NewUserVFS(vfs.NewMountTable(), nil, nil)) // no tracker
}

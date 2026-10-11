package session

import (
	"testing"
	"time"
)

func TestManagerLifecycleAndStats(t *testing.T) {
	mgr := NewManager()

	ftp := mgr.Start("alice", "ftp", "10.0.0.1:1000")
	sftp := mgr.Start("bob", "sftp", "10.0.0.2:1000")
	if ftp == nil || sftp == nil {
		t.Fatal("expected sessions to be created")
	}

	got := mgr.Get(ftp.ID)
	if got == nil {
		t.Fatal("expected session to be retrievable")
	}
	if !got.LastSeenAt.Equal(ftp.LastSeenAt) {
		t.Fatalf("expected copied session to preserve last seen timestamp: start=%s got=%s", ftp.LastSeenAt, got.LastSeenAt)
	}
	if got.terminate != nil {
		t.Fatal("expected copied session to omit terminator")
	}

	terminated := false
	if !mgr.AttachTerminator(sftp.ID, func() { terminated = true }) {
		t.Fatal("expected terminator to attach")
	}
	if !mgr.Kill(sftp.ID) {
		t.Fatal("expected kill to return true")
	}
	if !terminated {
		t.Fatal("expected terminator to run on kill")
	}
	if mgr.Kill("missing") {
		t.Fatal("expected kill on missing session to return false")
	}

	list := mgr.List()
	if len(list) != 1 || list[0].ID != ftp.ID {
		t.Fatalf("unexpected session list: %#v", list)
	}

	active, total := mgr.ProtocolStats()
	if active["ftp"] != 1 || active["sftp"] != 0 {
		t.Fatalf("unexpected active stats: %#v", active)
	}
	if total["ftp"] != 1 || total["sftp"] != 1 {
		t.Fatalf("unexpected total stats: %#v", total)
	}

	mgr.End(ftp.ID)
	if mgr.Get(ftp.ID) != nil {
		t.Fatal("expected end to remove session")
	}
}

func TestAttachTerminatorRejectsMissingOrNil(t *testing.T) {
	mgr := NewManager()
	sess := mgr.Start("alice", "ftp", "10.0.0.1:1000")

	if mgr.AttachTerminator(sess.ID, nil) {
		t.Fatal("expected nil terminator to be rejected")
	}
	if mgr.AttachTerminator("missing", func() {}) {
		t.Fatal("expected missing session terminator attach to fail")
	}
}

func TestTouchUpdatesLastSeen(t *testing.T) {
	m := NewManager()
	s := m.Start("alice", "sftp", "127.0.0.1:1")
	m.mu.Lock()
	m.sessions[s.ID].LastSeenAt = s.LastSeenAt.Add(-time.Minute)
	m.mu.Unlock()
	m.Touch(s.ID)
	if got := m.Get(s.ID).LastSeenAt; time.Since(got) > 5*time.Second {
		t.Fatalf("LastSeenAt not refreshed: %v", got)
	}
	m.Touch("missing") // must not panic
	var nilM *Manager
	nilM.Touch(s.ID)
}

func TestStartLimited(t *testing.T) {
	m := NewManager()
	a, ok := m.StartLimited("bob", "ftp", "192.0.2.1:1", 2)
	if !ok {
		t.Fatal("first session refused")
	}
	if _, ok := m.StartLimited("bob", "sftp", "192.0.2.1:2", 2); !ok {
		t.Fatal("second session refused")
	}
	if _, ok := m.StartLimited("bob", "ftp", "192.0.2.1:3", 2); ok {
		t.Fatal("third session admitted over the cap")
	}
	if _, ok := m.StartLimited("carol", "ftp", "192.0.2.1:4", 2); !ok {
		t.Fatal("another user's session refused")
	}
	m.End(a.ID)
	if _, ok := m.StartLimited("bob", "ftp", "192.0.2.1:5", 2); !ok {
		t.Fatal("slot not freed by End")
	}
	if got := m.CountUser("bob"); got != 2 {
		t.Fatalf("CountUser = %d, want 2", got)
	}
}

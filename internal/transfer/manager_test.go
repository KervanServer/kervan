package transfer

import "testing"

func TestManagerLifecycle(t *testing.T) {
	m := NewManager(10)
	id := m.Start("alice", "ftp", "/a.txt", DirectionUpload, 100)
	m.AddBytes(id, 40)
	m.AddBytes(id, 60)
	m.End(id, StatusCompleted, "")

	stats := m.Stats()
	if stats.TotalTransfers != 1 {
		t.Fatalf("total transfers mismatch: %d", stats.TotalTransfers)
	}
	if stats.Completed != 1 {
		t.Fatalf("completed mismatch: %d", stats.Completed)
	}
	if stats.UploadBytes != 100 {
		t.Fatalf("upload bytes mismatch: %d", stats.UploadBytes)
	}
	if stats.ActiveTransfers != 0 {
		t.Fatalf("active mismatch: %d", stats.ActiveTransfers)
	}
	recent := m.Recent(5)
	if len(recent) != 1 {
		t.Fatalf("recent length mismatch: %d", len(recent))
	}
	if recent[0].ID != id {
		t.Fatalf("recent id mismatch: %s", recent[0].ID)
	}
}

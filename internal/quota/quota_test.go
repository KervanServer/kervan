package quota

import (
	"errors"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
)

func TestTrackerMeasuresUsageAndEnforcesGrowth(t *testing.T) {
	fsys := memory.New()
	file, err := fsys.Open("/seed.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, err := file.Write([]byte("1234")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	tracker, err := NewTracker(fsys, 5)
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}
	if got := tracker.UsedBytes(); got != 4 {
		t.Fatalf("UsedBytes() = %d, want 4", got)
	}
	if err := tracker.OnGrow(1); err != nil {
		t.Fatalf("OnGrow(1) error = %v", err)
	}
	if err := tracker.OnGrow(1); !errors.Is(err, ErrStorageExceeded) {
		t.Fatalf("OnGrow() error = %v, want ErrStorageExceeded", err)
	}
	tracker.OnShrink(2)
	if got := tracker.UsedBytes(); got != 3 {
		t.Fatalf("UsedBytes() after shrink = %d, want 3", got)
	}
}

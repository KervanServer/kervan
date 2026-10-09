package audit

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type tick struct{ t time.Time }

func (c *tick) now() time.Time { c.t = c.t.Add(time.Second); return c.t }

func verifyLog(t *testing.T, path string, key []byte) VerifyReport {
	t.Helper()
	r, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	report, err := VerifyChain(r, key)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func writeRotating(t *testing.T, path string, key []byte, clock *tick, n int, offset int) {
	t.Helper()
	sink, err := OpenFileSink(FileSinkOptions{Path: path, ChainKey: key, MaxSize: 1024, MaxBackups: 2, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		evt := Event{ID: "e" + strconv.Itoa(offset+i), Type: EventFileWrite, Username: "user", Path: strings.Repeat("p", 100)}
		if err := sink.Write(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	_ = sink.Close()
}

func TestRotationRetentionAndChainVerification(t *testing.T) {
	dir := t.TempDir()
	key, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	path := filepath.Join(dir, "audit.jsonl")
	clock := &tick{t: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}

	writeRotating(t, path, key, clock, 40, 0)
	writeRotating(t, path, key, clock, 20, 40) // restart keeps rotating and chaining

	rotated, _ := RotatedFiles(path)
	if len(rotated) != 2 {
		t.Fatalf("retention kept %d rotated files, want 2", len(rotated))
	}
	for _, f := range append(rotated, path) {
		if info, _ := os.Stat(f); info.Size() > 1024 {
			t.Fatalf("%s is %d bytes, over the 1024-byte limit", f, info.Size())
		}
	}
	report := verifyLog(t, path, key)
	if !report.OK() {
		t.Fatalf("pruned-but-intact log reported problems: %+v", report.Problems)
	}
	raw, _ := io.ReadAll(mustOpenLog(t, path))
	if !strings.Contains(string(raw), `"type":"audit.pruned"`) {
		t.Fatal("no audit.pruned record written")
	}
	if !strings.Contains(string(raw), `"id":"e59"`) {
		t.Fatal("newest event missing from the log set")
	}

	// Deleting a rotated file by hand (no pruned record) is detected...
	_ = os.Remove(rotated[0])
	if report := verifyLog(t, path, key); report.OK() {
		t.Fatal("manually removed oldest file not detected")
	}
}

func TestRotationMiddleFileRemovalDetected(t *testing.T) {
	dir := t.TempDir()
	key, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	path := filepath.Join(dir, "audit.jsonl")
	clock := &tick{t: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	sink, _ := OpenFileSink(FileSinkOptions{Path: path, ChainKey: key, MaxSize: 1024, Now: clock.now})
	for i := 0; i < 40; i++ {
		_ = sink.Write(context.Background(), Event{ID: strconv.Itoa(i), Type: EventFileRead, Path: strings.Repeat("x", 100)})
	}
	_ = sink.Close()
	rotated, _ := RotatedFiles(path)
	if len(rotated) < 3 {
		t.Fatalf("expected several rotated files, got %d", len(rotated))
	}
	if report := verifyLog(t, path, key); !report.OK() {
		t.Fatalf("intact log: %+v", report.Problems)
	}
	_ = os.Remove(rotated[1])
	if report := verifyLog(t, path, key); report.OK() {
		t.Fatal("removed middle file not detected")
	}
}

func TestResumeAfterRotationWithEmptyLiveFile(t *testing.T) {
	dir := t.TempDir()
	key, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	path := filepath.Join(dir, "audit.jsonl")
	writeChained(t, path, key, 3, 0)
	// Simulate a crash right after a rotation: the live file is empty.
	_ = os.Rename(path, rotatedName(path, time.Now()))
	_ = os.WriteFile(path, nil, 0o600)
	writeChained(t, path, key, 2, 3)
	if report := verifyLog(t, path, key); !report.OK() || report.LastSeq != 5 || report.Segments != 1 {
		t.Fatalf("chain did not resume across the rotation: %+v", report)
	}
}

func mustOpenLog(t *testing.T, path string) io.Reader {
	t.Helper()
	r, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

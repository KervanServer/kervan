package memory

import (
	"io"
	"math"
	"os"
	"testing"
)

func TestMemFileWriteSemantics(t *testing.T) {
	b := New()
	f, err := b.Open("/f", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	// Overwrite in the middle.
	if _, err := f.WriteAt([]byte("WORLD"), 6); err != nil {
		t.Fatal(err)
	}
	// Truncate, then write past the end: the gap must read as zeros, not
	// as the bytes that were truncated away.
	if err := f.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("!"), 5); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, _ = b.Open("/f", os.O_RDONLY, 0)
	got, _ := io.ReadAll(f)
	_ = f.Close()
	if want := "he\x00\x00\x00!"; string(got) != want {
		t.Fatalf("content = %q, want %q", got, want)
	}

	// Writes past the size cap fail instead of allocating.
	f, _ = b.Open("/big", os.O_CREATE|os.O_RDWR, 0o644)
	defer f.Close()
	if _, err := f.WriteAt([]byte("x"), 1<<62); err == nil {
		t.Fatal("write at a huge offset succeeded")
	}
	if _, err := f.WriteAt([]byte("x"), 1<<30); err == nil {
		t.Fatal("write leaving a 1 GiB gap succeeded")
	}
	if err := f.Truncate(1 << 30); err == nil {
		t.Fatal("truncate growing by 1 GiB succeeded")
	}
	if _, err := f.WriteAt([]byte("x"), 1<<20); err != nil {
		t.Fatalf("write leaving a small gap failed: %v", err)
	}
	if _, err := f.Seek(math.MaxInt64, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(1, io.SeekCurrent); err == nil {
		t.Fatal("overflowing seek succeeded")
	}
}

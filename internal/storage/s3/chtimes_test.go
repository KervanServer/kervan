package s3

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
)

// Chtimes must not report a success it did not achieve. S3's Last-Modified
// is server-managed and cannot be set by any operation, so Chtimes refuses:
// FTP MFMT then answers 550 instead of a 213 that the next MDTM/MLSD read
// contradicts, SFTP SETSTAT applies its documented OK-with-skip policy
// (isHardSetstatError only hard-fails on os.ErrNotExist), and SCP keeps
// treating times as best-effort. A missing path still fails with
// os.ErrNotExist like the other backends.

func newChtimesTestBackend(t *testing.T) *Backend {
	t.Helper()
	server := httptest.NewServer(newFakeS3Server())
	t.Cleanup(server.Close)
	backend, err := New(Options{
		Endpoint:     server.URL,
		Bucket:       "test-bucket",
		UsePathStyle: true,
		DisableSSL:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func TestS3ChtimesRefusesInsteadOfFakingSuccess(t *testing.T) {

	t.Run("existing object refuses with a soft error", func(t *testing.T) {
		backend := newChtimesTestBackend(t)
		file, err := backend.Open("/file.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("data")); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := backend.Stat("/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		want := info.ModTime().Add(-2 * time.Hour)
		err = backend.Chtimes("/file.txt", want, want)
		if err == nil {
			after, statErr := backend.Stat("/file.txt")
			if statErr != nil {
				t.Fatal(statErr)
			}
			t.Fatalf("FAIL: Chtimes succeeded without effect; ModTime %v != requested %v (MFMT would answer 213 for a change that never happened)", after.ModTime(), want)
		}
		// A truthful refusal must not be a hard SETSTAT failure.
		if os.IsNotExist(err) {
			t.Fatalf("FAIL: Chtimes on an existing object returned os.ErrNotExist; want a soft not-supported error")
		}
		// The object itself is untouched by the refusal.
		after, statErr := backend.Stat("/file.txt")
		if statErr != nil {
			t.Fatal(statErr)
		}
		if !after.ModTime().Equal(info.ModTime()) {
			t.Errorf("FAIL: refused Chtimes changed ModTime from %v to %v", info.ModTime(), after.ModTime())
		}
	})

	t.Run("existing directory refuses with a soft error", func(t *testing.T) {
		backend := newChtimesTestBackend(t)
		if err := backend.MkdirAll("/dir", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := backend.Chtimes("/dir", time.Now(), time.Now()); err == nil {
			t.Fatal("FAIL: Chtimes on an existing directory succeeded; want a refusal")
		} else if os.IsNotExist(err) {
			t.Fatalf("FAIL: Chtimes on an existing directory returned os.ErrNotExist; want a soft not-supported error")
		}
	})

	t.Run("missing path reports not-exist", func(t *testing.T) {
		backend := newChtimesTestBackend(t)
		if err := backend.Chtimes("/missing.txt", time.Now(), time.Now()); !os.IsNotExist(err) {
			t.Fatalf("FAIL: Chtimes on a missing path returned %v; want os.ErrNotExist", err)
		}
	})

	t.Run("control memory backend applies the requested time", func(t *testing.T) {
		backend := memory.New()
		f, err := backend.Open("/file.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		want := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		if err := backend.Chtimes("/file.txt", want, want); err != nil {
			t.Fatalf("control: memory Chtimes failed: %v", err)
		}
		info, err := backend.Stat("/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(want) {
			t.Errorf("control: memory ModTime = %v, want %v", info.ModTime(), want)
		}
	})

}

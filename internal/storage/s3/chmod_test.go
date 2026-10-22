package s3

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/memory"
)

// Chmod must not report a success it did not achieve. S3 objects carry no
// POSIX mode bits — Stat, listings and SFTP attrs always report 0o644 — so
// Chmod refuses: FTP SITE CHMOD then answers 550 instead of a 200 that the
// next LIST/STAT read contradicts, and SFTP SETSTAT applies its documented
// OK-with-skip policy (isHardSetstatError only hard-fails on
// os.ErrNotExist). A missing path still fails with os.ErrNotExist like the
// other backends. (Chown keeps its no-op: UserVFS.Chown has no production
// caller and the memory backend no-ops identically, so nothing observable
// can contradict it.)

func newChmodTestBackend(t *testing.T) *Backend {
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

func TestS3ChmodRefusesInsteadOfFakingSuccess(t *testing.T) {

	t.Run("existing object refuses with a soft error", func(t *testing.T) {
		backend := newChmodTestBackend(t)
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
		err = backend.Chmod("/file.txt", 0o600)
		if err == nil {
			info, statErr := backend.Stat("/file.txt")
			if statErr != nil {
				t.Fatal(statErr)
			}
			t.Fatalf("FAIL: Chmod succeeded without effect; mode remains %o (SITE CHMOD would answer 200 for a change that never happened)", info.Mode()&os.ModePerm)
		}
		// A truthful refusal must not be a hard SETSTAT failure.
		if os.IsNotExist(err) {
			t.Fatalf("FAIL: Chmod on an existing object returned os.ErrNotExist; want a soft not-supported error")
		}
		// The object itself is untouched by the refusal.
		info, statErr := backend.Stat("/file.txt")
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode()&os.ModePerm != 0o644 {
			t.Errorf("FAIL: refused Chmod changed mode to %o, want 644", info.Mode()&os.ModePerm)
		}
	})

	t.Run("existing directory refuses with a soft error", func(t *testing.T) {
		backend := newChmodTestBackend(t)
		if err := backend.MkdirAll("/dir", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := backend.Chmod("/dir", 0o700); err == nil {
			t.Fatal("FAIL: Chmod on an existing directory succeeded; want a refusal")
		} else if os.IsNotExist(err) {
			t.Fatalf("FAIL: Chmod on an existing directory returned os.ErrNotExist; want a soft not-supported error")
		}
	})

	t.Run("missing path reports not-exist", func(t *testing.T) {
		backend := newChmodTestBackend(t)
		if err := backend.Chmod("/missing.txt", 0o600); !os.IsNotExist(err) {
			t.Fatalf("FAIL: Chmod on a missing path returned %v; want os.ErrNotExist", err)
		}
	})

	t.Run("control memory backend applies the requested mode", func(t *testing.T) {
		backend := memory.New()
		f, err := backend.Open("/file.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := backend.Chmod("/file.txt", 0o600); err != nil {
			t.Fatalf("control: memory Chmod failed: %v", err)
		}
		info, err := backend.Stat("/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModePerm != 0o600 {
			t.Errorf("control: memory mode = %o, want 600", info.Mode()&os.ModePerm)
		}
	})

}

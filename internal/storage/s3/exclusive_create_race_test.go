package s3

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"testing"
)

// The exclusive-create guard in Open is a pre-flight Stat, so an object
// created between the open and the handle's Close would still be clobbered
// by Close's unconditional PutObject. Exclusive handles therefore commit
// with a conditional PUT (If-None-Match: *): if a racing writer lands first,
// S3 answers 412 Precondition Failed and Close reports os.ErrExist with the
// winner's object untouched. The interleaving here is forced deterministically
// — no sleeps — by committing the racing write while the exclusive handle is
// still open.

func newExclusiveRaceBackend(t *testing.T) *Backend {
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

func writeExclusiveRaceFile(t *testing.T, backend *Backend, name, content string) {
	t.Helper()
	file, err := backend.Open(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", name, err)
	}
	if _, err := io.Copy(file, bytes.NewBufferString(content)); err != nil {
		t.Fatalf("Copy(%s) error = %v", name, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", name, err)
	}
}

func readExclusiveRaceFile(t *testing.T, backend *Backend, name string) string {
	t.Helper()
	file, err := backend.Open(name, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestS3ExclusiveCreateRefusesObjectCreatedAfterOpen(t *testing.T) {
	backend := newExclusiveRaceBackend(t)

	// The exclusive handle opens on a fresh path and stays open.
	exclusive, err := backend.Open("/race.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("exclusive open on a fresh path failed: %v", err)
	}
	if _, err := exclusive.Write([]byte("exclusive")); err != nil {
		t.Fatal(err)
	}

	// The racing writer commits an object at the same key while the
	// exclusive handle is open — between the open's pre-flight Stat and the
	// handle's Close.
	writeExclusiveRaceFile(t, backend, "/race.txt", "winner")

	closeErr := exclusive.Close()
	if closeErr == nil {
		t.Fatalf("FAIL: exclusive-create Close succeeded after another writer created the object; want os.ErrExist")
	}
	if !os.IsExist(closeErr) {
		t.Fatalf("FAIL: exclusive-create Close returned %v; want os.ErrExist", closeErr)
	}
	if got := readExclusiveRaceFile(t, backend, "/race.txt"); got != "winner" {
		t.Errorf("FAIL: refused exclusive create left %q, want %q", got, "winner")
	}
}

func TestS3ExclusiveCreateStillSucceedsWithoutRacer(t *testing.T) {
	backend := newExclusiveRaceBackend(t)
	f, err := backend.Open("/fresh.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("created")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("control: exclusive create with no racer failed: %v", err)
	}
	if got := readExclusiveRaceFile(t, backend, "/fresh.txt"); got != "created" {
		t.Errorf("control: /fresh.txt = %q, want %q", got, "created")
	}
}

func TestS3PlainOverwriteStillReplaces(t *testing.T) {
	backend := newExclusiveRaceBackend(t)
	writeExclusiveRaceFile(t, backend, "/replace.txt", "old")
	writeExclusiveRaceFile(t, backend, "/replace.txt", "new")
	if got := readExclusiveRaceFile(t, backend, "/replace.txt"); got != "new" {
		t.Errorf("control: /replace.txt = %q, want %q", got, "new")
	}
}

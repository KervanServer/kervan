package ftp

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

// releaseCountingFS records Release calls: the session-end hook through
// which a UserVFS gives back the quota changes of uploads that a client
// abandoned without closing.
type releaseCountingFS struct {
	vfs.FileSystem
	released *atomic.Int32
}

func (r releaseCountingFS) Release() { r.released.Add(1) }

// An FTP connection that drops mid-session, without QUIT, must release the
// filesystem its login built; a re-login on the same connection releases the
// previous one right away.
func TestFTPConnectionEndReleasesUserFS(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := auth.NewEngine(auth.NewUserRepository(st), "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "secret123", "/", false); err != nil {
		t.Fatal(err)
	}
	var released, built atomic.Int32
	srv := NewServer(Config{Banner: "t", IdleTimeout: 30 * time.Second}, nil, engine, session.NewManager(), nil,
		func(*auth.User) (vfs.FileSystem, error) {
			built.Add(1)
			return releaseCountingFS{FileSystem: memory.New(), released: &released}, nil
		}, nil)

	client, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleConn(context.Background(), serverConn, false)
	}()
	r := bufio.NewReader(client)
	read := func() string {
		t.Helper()
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		return strings.TrimSpace(line)
	}
	send := func(cmd string) string {
		t.Helper()
		if _, err := client.Write([]byte(cmd + "\r\n")); err != nil {
			t.Fatal(err)
		}
		return read()
	}
	read() // 220
	login := func() {
		send("USER alice")
		if reply := send("PASS secret123"); !strings.HasPrefix(reply, "230") {
			t.Fatalf("PASS: %q", reply)
		}
	}
	login()
	if got := released.Load(); got != 0 {
		t.Fatalf("released %d filesystems while the session is live", got)
	}
	login() // re-login replaces the session and its filesystem
	if got := released.Load(); got != 1 {
		t.Fatalf("re-login released %d filesystems, want 1", got)
	}

	_ = client.Close() // the client vanishes without QUIT
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConn did not return after the client went away")
	}
	if got, want := released.Load(), built.Load(); got != want {
		t.Fatalf("released %d of %d filesystems after the connection ended", got, want)
	}
}

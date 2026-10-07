package sftp

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/netguard"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

func startAdmissionSFTP(t *testing.T, filter *netguard.IPFilter, maxConns int) string {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := auth.NewEngine(auth.NewUserRepository(st), "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	srv := NewServer(Config{
		ListenAddr:     "127.0.0.1",
		Port:           port,
		HostKeyDir:     t.TempDir(),
		IdleTimeout:    30 * time.Second,
		IPFilter:       filter,
		MaxConnections: maxConns,
	}, nil, engine, session.NewManager(), nil, func(string) (vfs.FileSystem, error) {
		return memory.New(), nil
	}, nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func dialSFTP(t *testing.T, addr string) (func(), error) {
	t.Helper()
	cc, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		cc.Close()
		return nil, err
	}
	return func() { ch.Close(); conn.Close(); cc.Close() }, nil
}

func TestSFTPDeniedIPCannotHandshake(t *testing.T) {
	filter, err := netguard.NewIPFilter([]string{"10.0.0.0/8"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := startAdmissionSFTP(t, filter, 0)
	if closeFn, err := dialSFTP(t, addr); err == nil {
		closeFn()
		t.Fatal("client outside allow list completed the SSH handshake")
	}
	if err := filter.Update([]string{"127.0.0.1"}, nil); err != nil {
		t.Fatal(err)
	}
	closeFn, err := dialSFTP(t, addr)
	if err != nil {
		t.Fatalf("allowed client rejected: %v", err)
	}
	closeFn()
}

func TestSFTPMaxConnections(t *testing.T) {
	addr := startAdmissionSFTP(t, nil, 1)
	closeFirst, err := dialSFTP(t, addr)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	if closeFn, err := dialSFTP(t, addr); err == nil {
		closeFn()
		t.Fatal("second connection admitted beyond max_connections=1")
	}
	closeFirst()
	deadline := time.Now().Add(3 * time.Second)
	for {
		closeFn, err := dialSFTP(t, addr)
		if err == nil {
			closeFn()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never released: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

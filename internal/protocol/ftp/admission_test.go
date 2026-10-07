package ftp

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/netguard"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startAdmissionFTP(t *testing.T, filter *netguard.IPFilter, maxConns int) string {
	t.Helper()
	port := freeTCPPort(t)
	srv := NewServer(Config{
		ListenAddr:     "127.0.0.1",
		Port:           port,
		IdleTimeout:    30 * time.Second,
		IPFilter:       filter,
		MaxConnections: maxConns,
	}, nil, nil, nil, nil, nil, nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func dialBanner(t *testing.T, addr string) (net.Conn, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && err != io.EOF {
		return c, ""
	}
	return c, line
}

func TestFTPDeniedIPIsDisconnected(t *testing.T) {
	filter, err := netguard.NewIPFilter(nil, []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	addr := startAdmissionFTP(t, filter, 0)
	c, banner := dialBanner(t, addr)
	defer c.Close()
	if banner != "" {
		t.Fatalf("denied client received a banner: %q", banner)
	}

	// Lifting the deny list at runtime admits the next connection.
	if err := filter.Update(nil, nil); err != nil {
		t.Fatal(err)
	}
	c2, banner2 := dialBanner(t, addr)
	defer c2.Close()
	if !strings.HasPrefix(banner2, "220") {
		t.Fatalf("expected 220 banner after filter update, got %q", banner2)
	}
}

func TestFTPMaxConnectionsRejectsWith421(t *testing.T) {
	addr := startAdmissionFTP(t, nil, 1)
	first, banner := dialBanner(t, addr)
	if !strings.HasPrefix(banner, "220") {
		t.Fatalf("first connection: expected 220, got %q", banner)
	}
	second, banner2 := dialBanner(t, addr)
	second.Close()
	if !strings.HasPrefix(banner2, "421") {
		t.Fatalf("second connection: expected 421, got %q", banner2)
	}

	// Closing the first connection frees its slot.
	_ = first.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, b := dialBanner(t, addr)
		c.Close()
		if strings.HasPrefix(b, "220") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never released; last reply %q", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestFTPRepeatedFailedLoginsBanAddress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := auth.NewEngine(auth.NewUserRepository(st), "bcrypt", 100, time.Minute)
	if _, err := engine.CreateUser("alice", "correct-horse", "/", false); err != nil {
		t.Fatal(err)
	}
	throttle, err := auth.NewIPThrottle(auth.IPThrottleConfig{Enabled: true, Threshold: 2, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetIPThrottle(throttle)

	port := freeTCPPort(t)
	srv := NewServer(Config{ListenAddr: "127.0.0.1", Port: port, IdleTimeout: 30 * time.Second},
		nil, engine, session.NewManager(), nil,
		func(*auth.User) (vfs.FileSystem, error) { return memory.New(), nil }, nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	login := func(pass string) string {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		r := bufio.NewReader(c)
		readFTPReply(t, r)
		ftpCmd(t, c, r, "USER alice")
		return ftpCmd(t, c, r, "PASS "+pass)
	}
	for i := 0; i < 2; i++ {
		if reply := login("wrong"); !strings.HasPrefix(reply, "530") {
			t.Fatalf("attempt %d: expected 530, got %q", i, reply)
		}
	}
	// Even the correct password is refused while the address is banned.
	if reply := login("correct-horse"); !strings.HasPrefix(reply, "421") {
		t.Fatalf("expected 421 ban, got %q", reply)
	}
}

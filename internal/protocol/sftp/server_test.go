package sftp

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
	"golang.org/x/crypto/ssh"
)

// Contract: sftp.idle_timeout must behave as an IDLE timeout — the control
// connection deadline set in handleConn is renewed on observed session
// activity (SFTP packets, SCP data flow) and fully idle connections are
// still reaped. Regression: the deadline was set once at accept and never
// renewed, so every session (active transfers included) was hard-killed at
// the deadline.

func sftpIdleServer(t *testing.T, idle time.Duration) (*Server, *ssh.ServerConfig) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	s := NewServer(Config{
		Port:        2222,
		IdleTimeout: idle,
	}, nil, engine, session.NewManager(), nil, func(string) (vfs.FileSystem, error) {
		return memory.New(), nil
	}, nil)

	hostKeyPath := filepath.Join(t.TempDir(), "hostkey")
	if err := crypto.GenerateED25519HostKey(hostKeyPath); err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := crypto.LoadSigner(hostKeyPath)
	if err != nil {
		t.Fatalf("load host key: %v", err)
	}
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			user, authErr := engine.Authenticate(context.Background(), meta.User(), string(pass))
			if authErr != nil {
				return nil, errors.New("invalid credentials")
			}
			return &ssh.Permissions{Extensions: map[string]string{
				"username": user.Username,
				"user_id":  user.ID,
			}}, nil
		},
	}
	sshCfg.AddHostKey(signer)
	return s, sshCfg
}

func sftpStartListener(t *testing.T, s *Server, sshCfg *ssh.ServerConfig) net.Conn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(sshCfg, conn)
		}
	}()
	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

func sftpClientHandshake(t *testing.T, cc net.Conn) (ssh.Conn, ssh.Channel, error) {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{ssh.Password("pw12345")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, _, reqs, err := ssh.NewClientConn(cc, "kervan-test:22", cfg)
	if err != nil {
		return nil, nil, err
	}
	go ssh.DiscardRequests(reqs)
	ch, _, err := conn.OpenChannel("session", nil)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ V string }{"sftp"}))
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, err
	}
	if !ok {
		ch.Close()
		conn.Close()
		return nil, nil, errors.New("sftp subsystem request rejected")
	}
	return conn, ch, nil
}

func sftpInitRoundTrip(t *testing.T, ch ssh.Channel) error {
	t.Helper()
	watchdog := time.AfterFunc(15*time.Second, func() { panic("sftp round-trip timed out after 15s") })
	defer watchdog.Stop()
	init := make([]byte, 9)
	binary.BigEndian.PutUint32(init[0:4], 5)
	init[4] = 1
	binary.BigEndian.PutUint32(init[5:9], 3)
	if _, err := ch.Write(init); err != nil {
		return err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(ch, lenBuf[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n < 5 || n > 1024 {
		return errors.New("implausible reply length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(ch, body); err != nil {
		return err
	}
	if body[0] != 2 {
		return errors.New("unexpected reply packet type")
	}
	return nil
}

func TestSFTPHandshakeAndInitRoundTrip(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 2*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatalf("init round trip within idle window: %v", err)
	}
}

func TestSFTPSessionSurvivesIdleTimeoutWhenActive(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 2*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()

	deadline := time.Now().Add(3200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := sftpInitRoundTrip(t, ch); err != nil {
			t.Fatalf("active SFTP session died while exchanging packets past idle_timeout=2s — the connection deadline must be renewed on session activity: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func TestSFTPSessionReapedWhenIdle(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 2*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, _, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()

	time.Sleep(3500 * time.Millisecond)
	_ = cc.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, werr := cc.Write([]byte{0}); werr == nil {
		t.Fatalf("fully idle connection was not reaped after idle_timeout — renewal must stay activity-driven")
	}
}

// Contract: subsystem re-dispatch — a second (and concurrent) session channel
// on the same authenticated SSH connection gets a fresh, functional SFTP
// handler, and activity on re-dispatched channels keeps the connection alive
// past idle_timeout.

func openSecondSFTPChannel(t *testing.T, conn ssh.Conn) ssh.Channel {
	t.Helper()
	ch, _, err := conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open second session channel: %v", err)
	}
	ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ V string }{"sftp"}))
	if err != nil {
		ch.Close()
		t.Fatalf("sftp subsystem request on second channel: %v", err)
	}
	if !ok {
		ch.Close()
		t.Fatalf("sftp subsystem request on second channel was rejected")
	}
	return ch
}

func TestSFTPSubsystemRedispatchOnSameConnection(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 5*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch1, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch1); err != nil {
		t.Fatalf("init round trip on first session channel: %v", err)
	}
	ch1.Close()

	ch2 := openSecondSFTPChannel(t, sshConn)
	defer ch2.Close()
	if err := sftpInitRoundTrip(t, ch2); err != nil {
		t.Fatalf("re-dispatched session channel did not serve SFTP: %v", err)
	}
}

func TestSFTPActivityOnRedispatchedChannelSurvivesIdleTimeout(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 2*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch1, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch1); err != nil {
		t.Fatalf("init round trip on first session channel: %v", err)
	}
	ch1.Close()

	ch2 := openSecondSFTPChannel(t, sshConn)
	defer ch2.Close()
	deadline := time.Now().Add(3200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := sftpInitRoundTrip(t, ch2); err != nil {
			t.Fatalf("activity on a re-dispatched session channel did not keep the connection alive past idle_timeout=2s: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func TestSFTPConcurrentSubsystemHandlers(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 5*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch1, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	ch2 := openSecondSFTPChannel(t, sshConn)
	defer ch2.Close()

	for i := 0; i < 3; i++ {
		if err := sftpInitRoundTrip(t, ch1); err != nil {
			t.Fatalf("concurrent channel 1 round trip %d: %v", i+1, err)
		}
		if err := sftpInitRoundTrip(t, ch2); err != nil {
			t.Fatalf("concurrent channel 2 round trip %d: %v", i+1, err)
		}
	}
}

// Round-50 regression coverage: the SFTP INIT/VERSION negotiation contract.
// Spec-literal packet types per draft-ietf-secsh-filexfer-02:
// SSH_FXP_INIT = 1, SSH_FXP_VERSION = 2.
func sftpSendRaw(t *testing.T, ch ssh.Channel, pktType byte, payload []byte) {
	t.Helper()
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(1+len(payload)))
	frame[4] = pktType
	frame = append(frame, payload...)
	if _, err := ch.Write(frame); err != nil {
		t.Fatalf("write raw packet type %d: %v", pktType, err)
	}
}

func sftpReadVersion(t *testing.T, ch ssh.Channel) (uint32, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(ch, hdr); err != nil {
		return 0, err
	}
	if hdr[4] != 2 {
		return 0, errors.New("reply type is not VERSION (2)")
	}
	vp := make([]byte, 4)
	if _, err := io.ReadFull(ch, vp); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(vp), nil
}

func TestSFTPInitVersionNegotiation(t *testing.T) {
	srv, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, srv, sshCfg)
	conn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer conn.Close()

	// INIT with version 3 → VERSION 3.
	sftpSendRaw(t, ch, 1, []byte{0, 0, 0, 3})
	if v, err := sftpReadVersion(t, ch); err != nil {
		t.Fatalf("INIT 3: %v", err)
	} else if v != 3 {
		t.Fatalf("INIT 3 → VERSION %d, want 3", v)
	}

	// INIT with a higher client version (6) → the server pins VERSION 3
	// (the spec-correct fallback for a v3-only server).
	sftpSendRaw(t, ch, 1, []byte{0, 0, 0, 6})
	if v, err := sftpReadVersion(t, ch); err != nil {
		t.Fatalf("INIT 6: %v", err)
	} else if v != 3 {
		t.Fatalf("INIT 6 → VERSION %d, want 3", v)
	}

	// A repeated INIT mid-session is harmless: VERSION 3 again.
	sftpSendRaw(t, ch, 1, []byte{0, 0, 0, 3})
	if v, err := sftpReadVersion(t, ch); err != nil {
		t.Fatalf("repeated INIT: %v", err)
	} else if v != 3 {
		t.Fatalf("repeated INIT → VERSION %d, want 3", v)
	}
}

func TestSFTPTruncatedInitClosesSession(t *testing.T) {
	srv, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, srv, sshCfg)
	conn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer conn.Close()

	// A truncated INIT payload (2 bytes instead of 4): handleInit returns an
	// error and the command loop terminates the session — the connection must
	// CLOSE (EOF within the deadline), never hang.
	sftpSendRaw(t, ch, 1, []byte{0, 0})
	_ = cc.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 64)
	if _, err := ch.Read(buf); err == nil {
		t.Fatalf("the session stayed open after a truncated INIT (read data instead of closing)")
	}
}

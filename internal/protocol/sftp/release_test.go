package sftp

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
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

type releaseCountingFS struct {
	vfs.FileSystem
	released *atomic.Int32
}

func (r releaseCountingFS) Release() { r.released.Add(1) }

// An SFTP connection that drops with a session channel still open must
// release its filesystem once the channel goroutines have stopped, so quota
// changes of uploads it never finished are given back.
func TestSFTPConnectionEndReleasesUserFS(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := auth.NewEngine(auth.NewUserRepository(st), "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatal(err)
	}
	var released atomic.Int32
	s := NewServer(Config{Port: 2222, IdleTimeout: 30 * time.Second}, nil, engine, session.NewManager(), nil,
		func(string) (vfs.FileSystem, error) {
			return releaseCountingFS{FileSystem: memory.New(), released: &released}, nil
		}, nil)

	hostKeyPath := filepath.Join(t.TempDir(), "hostkey")
	if err := crypto.GenerateED25519HostKey(hostKeyPath); err != nil {
		t.Fatal(err)
	}
	signer, err := crypto.LoadSigner(hostKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			user, authErr := engine.Authenticate(context.Background(), meta.User(), string(pass))
			if authErr != nil {
				return nil, errors.New("invalid credentials")
			}
			return &ssh.Permissions{Extensions: map[string]string{"username": user.Username, "user_id": user.ID}}, nil
		},
	}
	sshCfg.AddHostKey(signer)

	cc := sftpStartListener(t, s, sshCfg)
	conn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatal(err)
	}
	if got := released.Load(); got != 0 {
		t.Fatalf("released while the session is live: %d", got)
	}

	_ = cc.Close() // the client vanishes with the channel still open
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for released.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("filesystem released %d times after the connection ended, want 1", got)
	}
}

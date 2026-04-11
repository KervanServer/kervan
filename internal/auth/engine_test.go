package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/store"
	"golang.org/x/crypto/ssh"
)

func TestAuthenticatePublicKey(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	repo := NewUserRepository(st)
	engine := NewEngine(repo, "argon2id", 5, 15*time.Minute)

	rawPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&rawPrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}

	user := &User{
		Username:       "alice",
		PasswordHash:   "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
		AuthorizedKeys: []string{normalizeAuthorizedKey(ssh.MarshalAuthorizedKey(publicKey))},
		HomeDir:        "/",
		Enabled:        true,
		Type:           UserTypeVirtual,
		Permissions:    DefaultUserPermissions(),
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	authenticated, err := engine.AuthenticatePublicKey(context.Background(), "alice", publicKey)
	if err != nil {
		t.Fatalf("AuthenticatePublicKey: %v", err)
	}
	if authenticated == nil || authenticated.Username != "alice" {
		t.Fatalf("unexpected authenticated user: %#v", authenticated)
	}
}

func TestCreateUserEnforcesMinPasswordLength(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	repo := NewUserRepository(st)
	engine := NewEngine(repo, "argon2id", 5, 15*time.Minute)
	engine.SetMinPasswordLength(12)

	if _, err := engine.CreateUser("alice", "short", "/", false); err == nil {
		t.Fatal("expected short password to be rejected")
	}
}

func TestResetPasswordEnforcesMinPasswordLength(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	repo := NewUserRepository(st)
	engine := NewEngine(repo, "argon2id", 5, 15*time.Minute)
	engine.SetMinPasswordLength(12)

	if _, err := engine.CreateUser("alice", "LongEnough123!", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := engine.ResetPassword("alice", "short"); err == nil {
		t.Fatal("expected short password reset to be rejected")
	}

	user, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user == nil {
		t.Fatal("expected user to exist")
	}
	if !VerifyPassword("LongEnough123!", user.PasswordHash) {
		t.Fatal("expected original password hash to remain unchanged")
	}
}

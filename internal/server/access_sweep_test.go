package server

import (
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/store"
)

func TestRevokeIneligibleSessions(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, time.Minute)
	for _, name := range []string{"ok", "expired", "moved", "disabled"} {
		if _, err := engine.CreateUser(name, "StrongPass123!", "/", false); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Minute)
	update := func(name string, fn func(*auth.User)) {
		u, _ := repo.GetByUsername(name)
		fn(u)
		if err := repo.Update(u); err != nil {
			t.Fatal(err)
		}
	}
	update("expired", func(u *auth.User) { u.ExpiresAt = &past })
	update("moved", func(u *auth.User) { u.AllowedIPs = []string{"10.0.0.0/8"} })
	update("disabled", func(u *auth.User) { u.Enabled = false })

	sessions := session.NewManager()
	killed := map[string]bool{}
	for _, name := range []string{"ok", "expired", "moved", "disabled", "ghost"} {
		s := sessions.Start(name, "sftp", "192.0.2.1:2222")
		sessions.AttachTerminator(s.ID, func() { killed[name] = true })
	}

	app := &App{sessions: sessions, authRepo: repo}
	if n := app.revokeIneligibleSessions(time.Now()); n != 4 {
		t.Fatalf("revoked %d sessions, want 4", n)
	}
	for _, name := range []string{"expired", "moved", "disabled", "ghost"} {
		if !killed[name] {
			t.Fatalf("session of %s not terminated", name)
		}
	}
	if killed["ok"] || sessions.CountUser("ok") != 1 {
		t.Fatal("eligible session was terminated")
	}
}

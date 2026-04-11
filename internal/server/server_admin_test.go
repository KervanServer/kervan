package server

import (
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

func TestEnsureAdminRequiresBootstrapPassword(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.WebUI.AdminPassword = ""

	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, 5, 15*time.Minute)
	app := &App{
		cfg:      cfg,
		store:    st,
		authRepo: repo,
		auth:     engine,
	}

	err = app.ensureAdmin()
	if err == nil {
		t.Fatal("expected bootstrap admin error")
	}
	if !strings.Contains(err.Error(), "no admin user found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureAdminCreatesConfiguredBootstrapUser(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.WebUI.AdminPassword = "StrongPass123!"

	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, 5, 15*time.Minute)
	app := &App{
		cfg:      cfg,
		store:    st,
		authRepo: repo,
		auth:     engine,
	}

	if err := app.ensureAdmin(); err != nil {
		t.Fatalf("ensureAdmin: %v", err)
	}

	user, err := repo.GetByUsername(cfg.WebUI.AdminUsername)
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user == nil {
		t.Fatal("expected bootstrap admin user to exist")
	}
	if user.Type != auth.UserTypeAdmin {
		t.Fatalf("expected admin type, got %q", user.Type)
	}
}

func TestApplyRuntimeConfigUpdatesMinPasswordLength(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()

	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, cfg.Auth.PasswordHash, 5, 15*time.Minute)
	engine.SetMinPasswordLength(cfg.Auth.MinPasswordLength)
	app := &App{
		cfg:      cfg,
		store:    st,
		authRepo: repo,
		auth:     engine,
	}

	next := config.DefaultConfig()
	next.Server.DataDir = cfg.Server.DataDir
	next.Auth.MinPasswordLength = 14

	applied, restart := app.applyRuntimeConfig(next)
	if len(restart) != 0 {
		t.Fatalf("expected no restart-required paths, got %v", restart)
	}
	if len(applied) != 1 || applied[0] != "auth.min_password_length" {
		t.Fatalf("unexpected applied paths: %v", applied)
	}

	if _, err := app.auth.CreateUser("alice", "short-pass", "/", false); err == nil {
		t.Fatal("expected updated runtime password policy to be enforced")
	}
}

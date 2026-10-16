package server

import (
	"strings"
	"sync"
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

// TestApplyRuntimeConfigDoesNotRaceWithPolicyDefaults is the regression for a
// data race on the shared App config. applyRuntimeConfig mutated a.cfg while a
// live server reads the runtime-reloadable fields, so those writes must sit
// inside a.cfgMu's critical section — the contract cfgMu itself declares ("guards
// runtime-reloadable cfg fields read per connection").
//
// The reader here mirrors App.policyDefaults, the only production reader of
// those fields, which takes cfgMu.RLock. Reading them unlocked would be a race
// against any writer and would say nothing about the reload path.
//
// Run with -race.
func TestApplyRuntimeConfigDoesNotRaceWithPolicyDefaults(t *testing.T) {
	dataDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = dataDir

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	repo := auth.NewUserRepository(st)
	app := &App{
		cfg:      cfg,
		store:    st,
		authRepo: repo,
		auth:     auth.NewEngine(repo, cfg.Auth.PasswordHash, 5, 0),
	}

	next := config.DefaultConfig()
	next.Server.DataDir = dataDir
	next.WebUI.SessionTimeout = 42
	next.WebUI.CORSOrigins = []string{"https://a.example"}
	next.Auth.MinPasswordLength = 14
	next.Auth.RequireSpecialChar = true
	next.Security.BruteForce.MaxAttempts = 9
	next.Security.BruteForce.IPBanThreshold = 7
	next.Security.BruteForce.WhitelistIPs = []string{"10.0.0.1"}

	var wg sync.WaitGroup
	start := make(chan struct{})
	done := make(chan struct{})

	// Writer: a reload, as the admin config endpoint performs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 200; i++ {
			_, _ = app.applyRuntimeConfig(next)
		}
		close(done)
	}()

	// Reader: the per-connection policy lookup. It spins for the whole writer
	// run because applyRuntimeConfig spends most of its time in
	// classifyRuntimeChanges (a YAML round-trip); a short loop would finish
	// before the writer reached its first field write and never overlap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for {
			select {
			case <-done:
				return
			default:
			}
			app.cfgMu.RLock()
			_ = app.cfg.WebUI.SessionTimeout
			_ = app.cfg.WebUI.TOTPEnabled
			_ = len(app.cfg.WebUI.CORSOrigins)
			_ = app.cfg.Auth.MinPasswordLength
			_ = app.cfg.Auth.RequireSpecialChar
			_ = app.cfg.Security.BruteForce.Enabled
			_ = app.cfg.Security.BruteForce.MaxAttempts
			_ = app.cfg.Security.BruteForce.LockoutDuration
			_ = app.cfg.Security.BruteForce.IPBanThreshold
			_ = app.cfg.Security.BruteForce.IPBanDuration
			_ = len(app.cfg.Security.BruteForce.WhitelistIPs)
			_ = app.cfg.Security.MaxConnectionsPerIP
			_ = app.cfg.Bandwidth
			app.cfgMu.RUnlock()
		}
	}()

	close(start)
	wg.Wait()

	if got := app.cfg.WebUI.SessionTimeout; got != 42 {
		t.Fatalf("reload did not apply: SessionTimeout = %d, want 42", got)
	}
}

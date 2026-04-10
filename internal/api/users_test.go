package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/store"
)

func TestHandleUsersRequiresAdmin(t *testing.T) {
	srv, _ := newUserTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleUsers(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", rec.Code)
	}
}

func TestHandleUsersUpdateByAdmin(t *testing.T) {
	srv, repo := newUserTestServer(t)

	target, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if target == nil {
		t.Fatal("expected alice to exist")
	}

	body, err := json.Marshal(map[string]any{
		"id":       target.ID,
		"enabled":  false,
		"home_dir": "/archive",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/users", bytes.NewReader(body))
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()

	srv.handleUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := repo.GetByID(target.ID)
	if err != nil {
		t.Fatalf("reload updated user: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated user to exist")
	}
	if updated.Enabled {
		t.Fatal("expected user to be disabled")
	}
	if updated.HomeDir != "/archive" {
		t.Fatalf("expected updated home dir, got %q", updated.HomeDir)
	}
}

func newUserTestServer(t *testing.T) (*Server, *auth.UserRepository) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, 15*time.Minute)
	if _, err := engine.CreateUser("admin", "StrongPass123!", "/", true); err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if _, err := engine.CreateUser("alice", "StrongPass123!", "/", false); err != nil {
		t.Fatalf("create regular user: %v", err)
	}

	return &Server{
		auth:  engine,
		users: repo,
	}, repo
}

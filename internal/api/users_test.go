package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHandleUsersImportByAdmin(t *testing.T) {
	srv, repo := newUserTestServer(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "users.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.WriteString(part, `[
  {"username":"bob","password":"StrongPass123!","home_dir":"/bob"},
  {"username":"admin","password":"Duplicate123!"},
  {"username":"ops","password":"AdminPass123!","role":"admin","enabled":false}
]`); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()

	srv.handleUsersImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var report userImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode import report: %v", err)
	}
	if report.Created != 2 || report.Skipped != 1 || report.Total != 3 {
		t.Fatalf("unexpected import report: %#v", report)
	}

	bob, err := repo.GetByUsername("bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob == nil || bob.HomeDir != "/bob" {
		t.Fatalf("unexpected bob user: %#v", bob)
	}

	ops, err := repo.GetByUsername("ops")
	if err != nil {
		t.Fatalf("get ops: %v", err)
	}
	if ops == nil {
		t.Fatal("expected ops user to exist")
	}
	if ops.Type != auth.UserTypeAdmin || ops.Enabled {
		t.Fatalf("unexpected ops user: %#v", ops)
	}
}

func TestHandleUsersExportByAdmin(t *testing.T) {
	srv, repo := newUserTestServer(t)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice == nil {
		t.Fatal("expected alice to exist")
	}
	alice.Email = "alice@example.com"
	if err := repo.Update(alice); err != nil {
		t.Fatalf("update alice: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/export?format=csv", nil)
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()

	srv.handleUsersExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `users.csv`) {
		t.Fatalf("expected csv content-disposition, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "username,email,role,type,home_dir,enabled") {
		t.Fatalf("expected csv header, got %q", body)
	}
	if !strings.Contains(body, "alice,alice@example.com,user,virtual,/,true") {
		t.Fatalf("expected alice row in csv export, got %q", body)
	}
	if strings.Contains(body, "password_hash") {
		t.Fatalf("expected password hash to be omitted by default, got %q", body)
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

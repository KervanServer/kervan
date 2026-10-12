package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// An admin working on another user's files is the audited actor; the owner
// goes into meta.target_user.
func TestAdminFileActionAuditsTheAdmin(t *testing.T) {
	srv, token := newGroupsTestServer(t) // root is an admin
	mounts := vfs.NewMountTable()
	mounts.Mount("/", memory.New(), false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, CreateDir: true, ListDir: true}, nil)
	srv.fsBuilder = func(string) (vfs.FileSystem, error) { return fsys, nil }
	sink := &memorySink{}
	engine := audit.NewEngine(nil, sink)
	srv.audit = engine
	files := srv.withAuth(srv.handleFilesV1)

	if code, body := callJSON(t, files, token, http.MethodPost, "/api/v1/files/alice/mkdir", map[string]any{"path": "/reports"}); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("admin mkdir for alice: %d %v", code, body)
	}
	if code, _ := callJSON(t, files, token, http.MethodPost, "/api/v1/files/ghost/mkdir", map[string]any{"path": "/x"}); code != http.StatusNotFound {
		t.Fatalf("unknown target user: %d, want 404", code)
	}
	engine.Close()
	got := sink.find(audit.EventDirCreate)
	if len(got) != 1 || got[0].Username != "root" || got[0].Meta["target_user"] != "alice" {
		t.Fatalf("dir.create events: %+v", got)
	}
}

// Client-sent X-Auth-* headers never reach handlers.
func TestSpoofedAuthHeadersAreStripped(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	var seen http.Header
	h := srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/share/x/info", nil)
	for _, name := range internalAuthHeaders {
		req.Header.Set(name, "root")
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	for _, name := range internalAuthHeaders {
		if v := seen.Get(name); v != "" {
			t.Fatalf("%s survived the middleware: %q", name, v)
		}
	}
}

// Password hashes are exported only to a signed-in admin, not an API key.
func TestUserExportHashesNeedSession(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/export?include_password_hashes=1", nil)
	req.Header.Set("X-Auth-User", "root")
	req.Header.Set("X-Auth-Method", "api-key")
	rec := httptest.NewRecorder()
	srv.handleUsersExport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("api-key hash export: %d", rec.Code)
	}
	req.Header.Set("X-Auth-Method", "bearer")
	rec = httptest.NewRecorder()
	srv.handleUsersExport(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatalf("session hash export: %d", rec.Code)
	}
}

// The WebSocket applies the per-user access policy like other requests.
func TestWebSocketHonoursAccessPolicy(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)
	alice, _ := repo.GetByUsername("alice")
	alice.AllowedIPs = []string{"10.0.0.0/8"} // httptest requests come from 192.0.2.1
	if err := repo.Update(alice); err != nil {
		t.Fatal(err)
	}
	token, err := signToken(srv.secret, "alice", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://kervan.local/api/v1/ws", nil)
	req.Host = "kervan.local"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Protocol", "kervan.v1")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.handleWebSocket(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "address") {
		t.Fatalf("expected 401 for a disallowed address, got %d: %s", rec.Code, rec.Body)
	}
}

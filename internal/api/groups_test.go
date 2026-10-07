package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
)

func newGroupsTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, repo := newAuthTestServer(t, false)
	srv.groups = auth.NewGroupRepository(srv.apiKeys.store, repo)
	srv.cfg.QuotaEnabled = true
	srv.cfg.DefaultMaxStorage = 1000
	if _, err := srv.auth.CreateUser("root", "StrongPass123!", "/", true); err != nil {
		t.Fatal(err)
	}
	token, err := signToken(srv.secret, "root", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return srv, token
}

func callJSON(t *testing.T, h http.HandlerFunc, token, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, url, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestGroupsAPIAndUserPolicy(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	groups := srv.withAuth(srv.handleGroups)
	users := srv.withAuth(srv.handleUsers)

	code, created := callJSON(t, groups, token, http.MethodPost, "/api/v1/groups", map[string]any{
		"name":        "readers",
		"description": "read-only",
		"permissions": map[string]any{"download": true, "list_dir": true},
		"max_storage": 500,
	})
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, created)
	}
	groupID := created["id"].(string)

	if code, body := callJSON(t, groups, token, http.MethodPost, "/api/v1/groups", map[string]any{"name": "bad name"}); code != http.StatusBadRequest {
		t.Fatalf("invalid name accepted: %d %v", code, body)
	}

	// Assigning a missing group is rejected; an existing one is applied.
	alice, _ := srv.users.GetByUsername("alice")
	if code, _ := callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{"id": alice.ID, "primary_group": "ghosts"}); code != http.StatusBadRequest {
		t.Fatalf("unknown group accepted: %d", code)
	}
	code, updated := callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{"id": alice.ID, "primary_group": "READERS"})
	if code != http.StatusOK {
		t.Fatalf("assign group: %d %v", code, updated)
	}
	if updated["primary_group"] != "readers" {
		t.Fatalf("group name not canonicalized: %v", updated["primary_group"])
	}
	eff := updated["effective"].(map[string]any)
	perms := eff["permissions"].(map[string]any)
	if perms["upload"] != false || perms["download"] != true || eff["max_storage"].(float64) != 500 || eff["group"] != "readers" {
		t.Fatalf("effective policy wrong: %v", eff)
	}

	// Per-user overrides.
	code, updated = callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{
		"id": alice.ID, "custom_permissions": true, "max_storage": -1,
		"permissions": map[string]any{"upload": true, "download": true},
	})
	if code != http.StatusOK {
		t.Fatalf("override: %d %v", code, updated)
	}
	eff = updated["effective"].(map[string]any)
	if eff["permissions"].(map[string]any)["upload"] != true || eff["max_storage"].(float64) != 0 {
		t.Fatalf("overrides not effective: %v", eff)
	}

	// Create a user directly into a group.
	code, bob := callJSON(t, users, token, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "bob", "password": "StrongPass123!", "primary_group": "readers",
	})
	if code != http.StatusCreated || bob["primary_group"] != "readers" {
		t.Fatalf("create in group: %d %v", code, bob)
	}

	// Members block deletion until forced; listing reports the count.
	_, list := callJSON(t, groups, token, http.MethodGet, "/api/v1/groups", nil)
	if n := list["groups"].([]any)[0].(map[string]any)["member_count"].(float64); n != 2 {
		t.Fatalf("member_count = %v, want 2", n)
	}
	if code, _ := callJSON(t, groups, token, http.MethodDelete, "/api/v1/groups?id="+groupID, nil); code != http.StatusConflict {
		t.Fatalf("delete with members: %d", code)
	}
	if code, _ := callJSON(t, groups, token, http.MethodDelete, "/api/v1/groups?id="+groupID+"&force=true", nil); code != http.StatusOK {
		t.Fatalf("forced delete: %d", code)
	}
	if b, _ := srv.users.GetByUsername("bob"); b.PrimaryGroup != "" {
		t.Fatalf("membership kept after delete: %q", b.PrimaryGroup)
	}
}

func TestGroupsAPIRequiresAdmin(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	token, _ := signToken(srv.secret, "alice", time.Hour)
	if code, _ := callJSON(t, srv.withAuth(srv.handleGroups), token, http.MethodGet, "/api/v1/groups", nil); code != http.StatusForbidden {
		t.Fatalf("non-admin got %d", code)
	}
}

func TestUserImportAssignsGroupAndQuota(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	if err := srv.groups.Create(&auth.Group{Name: "staff", Permissions: auth.DefaultUserPermissions()}); err != nil {
		t.Fatal(err)
	}
	user, err := srv.createImportedUser(userImportRecord{Username: "carl", Password: "StrongPass123!", PrimaryGroup: "staff", MaxStorage: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if user.PrimaryGroup != "staff" || user.MaxStorage != 2048 {
		t.Fatalf("imported user = %q/%d", user.PrimaryGroup, user.MaxStorage)
	}
	if _, err := srv.createImportedUser(userImportRecord{Username: "dina", Password: "StrongPass123!", PrimaryGroup: "nope"}); err == nil {
		t.Fatal("import into a missing group accepted")
	}
	if u, _ := srv.users.GetByUsername("dina"); u != nil {
		t.Fatal("user created despite invalid group")
	}
}

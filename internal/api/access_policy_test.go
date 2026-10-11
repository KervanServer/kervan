package api

import (
	"net/http"
	"testing"
	"time"
)

func TestUserAccessPolicyAPI(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	users := srv.withAuth(srv.handleUsers)
	alice, _ := srv.users.GetByUsername("alice")

	code, body := callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{
		"id":                alice.ID,
		"expires_at":        "2099-01-31T23:59:59Z",
		"allowed_ips":       []string{"10.0.0.9/8", "192.0.2.1"},
		"allowed_protocols": []string{"SFTP", "ftps"},
		"max_sessions":      3,
	})
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, body)
	}
	if body["expires_at"] != "2099-01-31T23:59:59Z" || body["expired"] != false || body["max_sessions"].(float64) != 3 {
		t.Fatalf("unexpected policy fields: %v", body)
	}
	if ips := body["allowed_ips"].([]any); len(ips) != 2 || ips[0] != "10.0.0.0/8" {
		t.Fatalf("allowed_ips not normalized: %v", ips)
	}
	if protocols := body["allowed_protocols"].([]any); len(protocols) != 2 || protocols[0] != "ftps" || protocols[1] != "sftp" {
		t.Fatalf("allowed_protocols not normalized: %v", protocols)
	}

	for _, bad := range []map[string]any{
		{"expires_at": "next tuesday"},
		{"allowed_ips": []string{"not-an-ip"}},
		{"allowed_protocols": []string{"telnet"}},
		{"max_sessions": -2},
	} {
		bad["id"] = alice.ID
		if code, body := callJSON(t, users, token, http.MethodPut, "/api/v1/users", bad); code != http.StatusBadRequest {
			t.Fatalf("accepted %v: %d %v", bad, code, body)
		}
	}

	// Clearing.
	code, body = callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{
		"id": alice.ID, "expires_at": "", "allowed_ips": []string{}, "allowed_protocols": []string{}, "max_sessions": 0,
	})
	if code != http.StatusOK || body["expires_at"] != nil || len(body["allowed_ips"].([]any)) != 0 || len(body["allowed_protocols"].([]any)) != 0 {
		t.Fatalf("clear: %d %v", code, body)
	}
}

func TestAccessPolicyEnforcedOnWebSignIn(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	root, _ := srv.users.GetByUsername("root")
	me := srv.withAuth(srv.handleUsers)

	// httptest requests come from 192.0.2.1.
	root.AllowedIPs = []string{"10.0.0.0/8"}
	if err := srv.users.Update(root); err != nil {
		t.Fatal(err)
	}
	login := map[string]any{"username": "root", "password": "StrongPass123!"}
	if code, body := callJSON(t, srv.handleLogin, "", http.MethodPost, "/api/v1/auth/login", login); code != http.StatusForbidden || body["code"] != "access_denied" {
		t.Fatalf("login from disallowed address: %d %v", code, body)
	}
	// An existing session is cut off too.
	if code, _ := callJSON(t, me, token, http.MethodGet, "/api/v1/users", nil); code != http.StatusForbidden {
		t.Fatalf("existing session from disallowed address: %d", code)
	}

	root.AllowedIPs = []string{"192.0.2.0/24"}
	root.AllowedProtocols = []string{"sftp"}
	if err := srv.users.Update(root); err != nil {
		t.Fatal(err)
	}
	if code, _ := callJSON(t, srv.handleLogin, "", http.MethodPost, "/api/v1/auth/login", login); code != http.StatusForbidden {
		t.Fatalf("login with webui not allowed: %d", code)
	}

	root.AllowedProtocols = nil
	past := time.Now().Add(-time.Second)
	root.ExpiresAt = &past
	if err := srv.users.Update(root); err != nil {
		t.Fatal(err)
	}
	if code, _ := callJSON(t, me, token, http.MethodGet, "/api/v1/users", nil); code != http.StatusUnauthorized {
		t.Fatalf("expired account kept its session: %d", code)
	}
	if code, _ := callJSON(t, srv.handleLogin, "", http.MethodPost, "/api/v1/auth/login", login); code != http.StatusUnauthorized {
		t.Fatalf("expired account signed in: %d", code)
	}

	root.ExpiresAt = nil
	if err := srv.users.Update(root); err != nil {
		t.Fatal(err)
	}
	if code, body := callJSON(t, srv.handleLogin, "", http.MethodPost, "/api/v1/auth/login", login); code != http.StatusOK {
		t.Fatalf("login after clearing policy: %d %v", code, body)
	}
}

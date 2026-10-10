package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
)

func newSSHKeyLine(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
}

// tokenIssuedBefore signs a token whose iat is a few seconds in the past,
// standing in for a session opened before a password change.
func tokenIssuedBefore(t *testing.T, srv *Server, user string) string {
	t.Helper()
	tok, err := signToken(srv.secret, user, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // next whole second
	return tok
}

func TestAccountPasswordChangeRevokesOldSessions(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	account := srv.withAuth(srv.handleAccount)
	change := srv.withAuth(srv.handleAccountPassword)
	oldToken := tokenIssuedBefore(t, srv, "alice")

	if code, body := callJSON(t, change, oldToken, http.MethodPost, "/api/v1/account/password", map[string]string{
		"current_password": "wrong", "new_password": "Brand-New-Pass-1!",
	}); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "incorrect") {
		t.Fatalf("wrong current password: %d %v", code, body)
	}
	code, body := callJSON(t, change, oldToken, http.MethodPost, "/api/v1/account/password", map[string]string{
		"current_password": "StrongPass123!", "new_password": "Brand-New-Pass-1!",
	})
	if code != http.StatusOK || body["token"] == "" {
		t.Fatalf("change: %d %v", code, body)
	}
	if code, _ := callJSON(t, account, oldToken, http.MethodGet, "/api/v1/account", nil); code != http.StatusUnauthorized {
		t.Fatalf("old session still valid after password change: %d", code)
	}
	if code, me := callJSON(t, account, body["token"].(string), http.MethodGet, "/api/v1/account", nil); code != http.StatusOK || me["username"] != "alice" || me["password_changeable"] != true {
		t.Fatalf("new session: %d %v", code, me)
	}
	if _, err := srv.auth.Authenticate(t.Context(), "alice", "Brand-New-Pass-1!"); err != nil {
		t.Fatalf("new password does not work: %v", err)
	}
}

func TestAccountPasswordNotManagedForExternalUsers(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	repo := srv.users
	alice, _ := repo.GetByUsername("alice")
	alice.AuthProvider = auth.AuthProviderOIDC
	_ = repo.Update(alice)
	token, _ := signToken(srv.secret, "alice", time.Hour)
	if code, body := callJSON(t, srv.withAuth(srv.handleAccountPassword), token, http.MethodPost, "/api/v1/account/password", map[string]string{
		"current_password": "StrongPass123!", "new_password": "Brand-New-Pass-1!",
	}); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "external identity provider") {
		t.Fatalf("oidc password change: %d %v", code, body)
	}
}

func TestAccountKeys(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	repo := srv.users
	token, _ := signToken(srv.secret, "alice", time.Hour)
	keys := srv.withAuth(srv.handleAccountKeys)
	k1, k2 := newSSHKeyLine(t, "laptop"), newSSHKeyLine(t, "ci")

	code, body := callJSON(t, keys, token, http.MethodPut, "/api/v1/account/keys", map[string]any{"authorized_keys": []string{k1, k2, k1 + " duplicate", "", "# comment"}})
	if code != http.StatusOK {
		t.Fatalf("put keys: %d %v", code, body)
	}
	listed := body["authorized_keys"].([]any)
	if len(listed) != 2 || !strings.HasPrefix(listed[0].(map[string]any)["fingerprint"].(string), "SHA256:") || listed[0].(map[string]any)["comment"] != "laptop" {
		t.Fatalf("keys = %v", listed)
	}
	for _, bad := range []string{"ssh-ed25519 notbase64", `from="10.0.0.0/8" ` + k1, k1 + "\n" + k2} {
		if code, _ := callJSON(t, keys, token, http.MethodPut, "/api/v1/account/keys", map[string]any{"authorized_keys": []string{bad}}); code != http.StatusBadRequest {
			t.Errorf("bad key %q accepted: %d", bad, code)
		}
	}
	alice, _ := repo.GetByUsername("alice")
	if len(alice.AuthorizedKeys) != 2 {
		t.Fatalf("stored keys = %d", len(alice.AuthorizedKeys))
	}
}

func TestAccountEndpointsRefuseAPIKeys(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	alice, _ := srv.users.GetByUsername("alice")
	rawKey, _, err := srv.apiKeys.Create(alice.ID, "k", "read-write")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account", nil)
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()
	srv.withAuth(srv.handleAccount)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("api key accepted for account endpoint: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminPasswordResetRevokesSessionsAndSetsKeys(t *testing.T) {
	srv, _ := newGroupsTestServer(t)
	repo := srv.users
	srv.sessions = session.NewManager()
	srv.auth.SetMinPasswordLength(8)
	adminToken, _ := signToken(srv.secret, "root", time.Hour)
	aliceToken := tokenIssuedBefore(t, srv, "alice")
	killed := false
	sess := srv.sessions.Start("alice", "sftp", "10.0.0.1:1")
	srv.sessions.AttachTerminator(sess.ID, func() { killed = true })
	alice, _ := repo.GetByUsername("alice")

	code, body := callJSON(t, srv.withAuth(srv.handleUsers), adminToken, http.MethodPut, "/api/v1/users", map[string]any{
		"id": alice.ID, "password": "Reset-By-Admin-1!", "authorized_keys": []string{newSSHKeyLine(t, "admin-added")},
	})
	if code != http.StatusOK || len(body["authorized_keys"].([]any)) != 1 {
		t.Fatalf("admin update: %d %v", code, body)
	}
	if !killed {
		t.Fatal("protocol session survived an admin password reset")
	}
	if code, _ := callJSON(t, srv.withAuth(srv.handleAccount), aliceToken, http.MethodGet, "/api/v1/account", nil); code != http.StatusUnauthorized {
		t.Fatalf("alice's old session still valid: %d", code)
	}
	if _, err := srv.auth.Authenticate(t.Context(), "alice", "Reset-By-Admin-1!"); err != nil {
		t.Fatalf("reset password does not work: %v", err)
	}
	if code, _ := callJSON(t, srv.withAuth(srv.handleUsers), adminToken, http.MethodPut, "/api/v1/users", map[string]any{"id": alice.ID, "password": "short"}); code != http.StatusBadRequest {
		t.Fatalf("weak reset password accepted: %d", code)
	}
}

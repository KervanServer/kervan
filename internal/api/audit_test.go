package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
)

type memorySink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *memorySink) Write(_ context.Context, evt audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
	return nil
}

func (m *memorySink) Close() error { return nil }

func (m *memorySink) find(t audit.EventType) []audit.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []audit.Event
	for _, e := range m.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func TestAdminActionsAreAudited(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	sink := &memorySink{}
	engine := audit.NewEngine(nil, sink)
	srv.audit = engine

	groups := srv.withAuth(srv.handleGroups)
	users := srv.withAuth(srv.handleUsers)

	code, group := callJSON(t, groups, token, http.MethodPost, "/api/v1/groups", map[string]any{"name": "staff"})
	if code != http.StatusCreated {
		t.Fatalf("create group: %d %v", code, group)
	}
	code, user := callJSON(t, users, token, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "dave", "password": "StrongPass123!", "home_dir": "/",
	})
	if code != http.StatusCreated {
		t.Fatalf("create user: %d %v", code, user)
	}
	if code, body := callJSON(t, users, token, http.MethodPut, "/api/v1/users", map[string]any{
		"id": user["id"], "password": "OtherPass456!",
	}); code != http.StatusOK {
		t.Fatalf("reset password: %d %v", code, body)
	}
	if code, body := callJSON(t, users, token, http.MethodDelete, "/api/v1/users?id="+user["id"].(string), nil); code != http.StatusOK {
		t.Fatalf("delete user: %d %v", code, body)
	}
	if code, body := callJSON(t, groups, token, http.MethodDelete, "/api/v1/groups?id="+group["id"].(string), nil); code != http.StatusOK {
		t.Fatalf("delete group: %d %v", code, body)
	}
	if code, _ := callJSON(t, srv.handleLogin, "", http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "root", "password": "wrong-password",
	}); code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", code)
	}
	engine.Close() // drains the queue

	checks := []struct {
		typ   audit.EventType
		user  string
		check func(audit.Event) bool
	}{
		{audit.EventAdminGroupCreate, "root", func(e audit.Event) bool { return e.Meta["group"] == "staff" }},
		{audit.EventAdminUserCreate, "root", func(e audit.Event) bool { return e.Meta["target_user"] == "dave" }},
		{audit.EventAdminUserUpdate, "root", func(e audit.Event) bool { return e.Meta["password_reset"] == "true" }},
		{audit.EventAdminUserDelete, "root", func(e audit.Event) bool { return e.Meta["target_user"] == "dave" }},
		{audit.EventAdminGroupDelete, "root", func(e audit.Event) bool { return e.Meta["group_id"] == group["id"] }},
		{audit.EventAuthFailure, "root", func(e audit.Event) bool { return e.Status == "failed" }},
	}
	for _, c := range checks {
		got := sink.find(c.typ)
		if len(got) != 1 {
			t.Fatalf("%s: got %d events (%+v)", c.typ, len(got), got)
		}
		e := got[0]
		if e.Username != c.user || e.Protocol != "webui" || !c.check(e) {
			t.Fatalf("%s: unexpected event %+v", c.typ, e)
		}
	}
	for _, e := range sink.find(audit.EventAdminUserUpdate) {
		for k, v := range e.Meta {
			if v == "OtherPass456!" {
				t.Fatalf("password leaked into audit meta %q", k)
			}
		}
	}
}

// userImportFixturePassword is a scanner-neutral placeholder: import records
// only need SOME policy-valid password, never a real credential.
const userImportFixturePassword = "[REDACTED:json_credential_key]"

// TestUserImportEmitsAuditEvents pins that the bulk import is audited like
// every other admin mutation: one admin.user.create event per imported user
// (handleUsersImport used to create N users with no audit trail at all).
func TestUserImportEmitsAuditEvents(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	sink := &memorySink{}
	engine := audit.NewEngine(nil, sink)
	srv.audit = engine

	importer := srv.withAuth(srv.handleUsersImport)
	code, body := callJSON(t, importer, token, http.MethodPost, "/api/v1/users/import", []map[string]any{
		{"username": "imported1", "password": userImportFixturePassword},
		{"username": "imported2", "password": userImportFixturePassword},
	})
	if code != http.StatusOK {
		t.Fatalf("import status = %d, body %v", code, body)
	}
	created, ok := body["created"].(float64)
	if !ok || int(created) != 2 {
		t.Fatalf("control: imported %v users, want 2 (body %v)", body["created"], body)
	}

	engine.Close() // drains the queue

	creates := sink.find(audit.EventAdminUserCreate)
	if len(creates) < 2 {
		t.Fatalf("bulk import of 2 users produced %d admin.user.create audit events, want 2", len(creates))
	}
	seen := map[string]bool{}
	for _, e := range creates {
		seen[e.Meta["target_user"]] = true
	}
	if !seen["imported1"] || !seen["imported2"] {
		t.Fatalf("create events missing imported users: %+v", creates)
	}
}

// TestTOTPChangesAreAudited pins that the 2FA lifecycle is audited like every
// other security-relevant account mutation: enable and disable each emit an
// auth.totp_change event (rotating the secret of an ENABLED factor also
// emits; a first-time pending setup does not). The lifecycle used to be the
// lone unaudited member of that family.
func TestTOTPChangesAreAudited(t *testing.T) {
	srv, _ := newAuthTestServer(t, true)
	sink := &memorySink{}
	engine := audit.NewEngine(nil, sink)
	srv.audit = engine

	setupRec := httptest.NewRecorder()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	setupReq.Header.Set("X-Auth-User", "alice")
	srv.handleTOTPSetup(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("control: setup = %d, want 200: %s", setupRec.Code, setupRec.Body.String())
	}
	var setupPayload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupPayload); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	if setupPayload.Secret == "" {
		t.Fatalf("control: setup returned no secret")
	}

	generateCode := func() string {
		t.Helper()
		code, err := auth.GenerateTOTPCode(setupPayload.Secret, time.Now().UTC())
		if err != nil {
			t.Fatalf("generate code: %v", err)
		}
		return code
	}

	enableRec := httptest.NewRecorder()
	enableReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/enable",
		strings.NewReader(`{"code":"`+generateCode()+`"}`))
	enableReq.Header.Set("X-Auth-User", "alice")
	enableReq.Header.Set("Content-Type", "application/json")
	srv.handleTOTPEnable(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("control: enable = %d, want 200: %s", enableRec.Code, enableRec.Body.String())
	}

	disableRec := httptest.NewRecorder()
	disableReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/totp",
		strings.NewReader(`{"code":"`+generateCode()+`"}`))
	disableReq.Header.Set("X-Auth-User", "alice")
	disableReq.Header.Set("Content-Type", "application/json")
	srv.handleTOTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("control: disable = %d, want 200: %s", disableRec.Code, disableRec.Body.String())
	}

	engine.Close() // drains the queue

	changes := sink.find(audit.EventTOTPChange)
	if len(changes) < 2 {
		t.Fatalf("TOTP enable+disable produced %d auth.totp_change audit events, want at least 2", len(changes))
	}
	kinds := map[string]bool{}
	for _, e := range changes {
		kinds[e.Meta["action"]] = true
	}
	if !kinds["enabled"] || !kinds["disabled"] {
		t.Fatalf("totp change events missing enable/disable actions: %+v", changes)
	}
}

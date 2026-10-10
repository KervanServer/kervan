package api

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/kervanserver/kervan/internal/audit"
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

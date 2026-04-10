package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/transfer"
)

func TestHandleSessionsScopesNonAdminToOwnSessions(t *testing.T) {
	srv := newAccessControlServer(t)
	srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")
	srv.sessions.Start("bob", "sftp", "10.0.0.2:1000")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleSessions(rec, req)

	var payload struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Username != "alice" {
		t.Fatalf("expected only alice sessions, got %#v", payload.Sessions)
	}
}

func TestHandleSessionsAppliesFilters(t *testing.T) {
	srv := newAccessControlServer(t)
	srv.sessions.Start("alice", "ftp", "10.1.1.1:1000")
	srv.sessions.Start("alice", "sftp", "10.1.1.2:1000")
	srv.sessions.Start("bob", "ftp", "192.168.1.5:2000")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?protocol=ftp&ip=10.1.1&q=alice", nil)
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()

	srv.handleSessions(rec, req)

	var payload struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("expected one filtered session, got %#v", payload.Sessions)
	}
	if payload.Sessions[0].Username != "alice" || payload.Sessions[0].Protocol != "ftp" {
		t.Fatalf("unexpected filtered session: %#v", payload.Sessions[0])
	}
}

func TestHandleSessionsIgnoresUsernameOverrideForNonAdmin(t *testing.T) {
	srv := newAccessControlServer(t)
	srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")
	srv.sessions.Start("bob", "sftp", "10.0.0.2:1000")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?username=bob", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleSessions(rec, req)

	var payload struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Username != "alice" {
		t.Fatalf("expected scoped alice sessions, got %#v", payload.Sessions)
	}
}

func TestHandleSessionByIDDeniesOtherUsers(t *testing.T) {
	srv := newAccessControlServer(t)
	aliceSession := srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+aliceSession.ID, nil)
	req.Header.Set("X-Auth-User", "bob")
	rec := httptest.NewRecorder()

	srv.handleSessionByID(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", rec.Code)
	}
}

func TestHandleSessionByIDKillAllowsOwnSession(t *testing.T) {
	srv := newAccessControlServer(t)
	aliceSession := srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+aliceSession.ID, nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleSessionByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := srv.sessions.Get(aliceSession.ID); got != nil {
		t.Fatalf("expected session to be removed, got %#v", got)
	}
}

func TestHandleSessionByIDKillAllowsAdmin(t *testing.T) {
	srv := newAccessControlServer(t)
	aliceSession := srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+aliceSession.ID, nil)
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()

	srv.handleSessionByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := srv.sessions.Get(aliceSession.ID); got != nil {
		t.Fatalf("expected session to be removed, got %#v", got)
	}
}

func TestHandleTransfersScopesNonAdminToOwnTransfers(t *testing.T) {
	srv := newAccessControlServer(t)

	aliceTransfer := srv.transfers.Start("alice", "ftp", "/alice.txt", transfer.DirectionUpload, 100)
	srv.transfers.AddBytes(aliceTransfer, 50)

	bobTransfer := srv.transfers.Start("bob", "sftp", "/bob.txt", transfer.DirectionDownload, 100)
	srv.transfers.AddBytes(bobTransfer, 75)
	srv.transfers.End(bobTransfer, transfer.StatusCompleted, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transfers", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleTransfers(rec, req)

	var payload struct {
		Active []transfer.Transfer `json:"active"`
		Recent []transfer.Transfer `json:"recent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, item := range payload.Active {
		if item.Username != "alice" {
			t.Fatalf("expected only alice active transfers, got %#v", payload.Active)
		}
	}
	for _, item := range payload.Recent {
		if item.Username != "alice" {
			t.Fatalf("expected only alice recent transfers, got %#v", payload.Recent)
		}
	}
}

func TestHandleAuditScopesNonAdminToOwnEvents(t *testing.T) {
	srv := newAccessControlServer(t)
	events := []map[string]any{
		{"username": "alice", "type": "upload", "path": "/alice.txt"},
		{"username": "bob", "type": "download", "path": "/bob.txt"},
	}
	raw := ""
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		raw += string(line) + "\n"
	}
	if err := os.WriteFile(srv.auditLogPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?username=bob", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleAudit(rec, req)

	var payload struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Events) != 1 || payload.Events[0]["username"] != "alice" {
		t.Fatalf("expected only alice audit events, got %#v", payload.Events)
	}
}

func TestHandleAuditExportScopesNonAdminToOwnEvents(t *testing.T) {
	srv := newAccessControlServer(t)
	events := []map[string]any{
		{"timestamp": "2026-04-10T10:00:00Z", "username": "alice", "type": "upload", "path": "/alice.txt"},
		{"timestamp": "2026-04-10T11:00:00Z", "username": "bob", "type": "download", "path": "/bob.txt"},
	}
	raw := ""
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		raw += string(line) + "\n"
	}
	if err := os.WriteFile(srv.auditLogPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export?format=csv&username=bob", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleAuditExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice") || strings.Contains(body, "bob") {
		t.Fatalf("expected export to be scoped to alice, got %q", body)
	}
}

func TestBuildWebSocketSnapshotScopesNonAdminData(t *testing.T) {
	srv := newAccessControlServer(t)
	srv.sessions.Start("alice", "ftp", "10.0.0.1:1000")
	srv.sessions.Start("bob", "sftp", "10.0.0.2:1000")

	trAlice := srv.transfers.Start("alice", "ftp", "/alice.txt", transfer.DirectionUpload, 100)
	srv.transfers.AddBytes(trAlice, 25)
	trBob := srv.transfers.Start("bob", "sftp", "/bob.txt", transfer.DirectionDownload, 100)
	srv.transfers.AddBytes(trBob, 80)
	srv.transfers.End(trBob, transfer.StatusCompleted, "")

	raw := `{"username":"alice","type":"upload"}` + "\n" + `{"username":"bob","type":"download"}` + "\n"
	if err := os.WriteFile(srv.auditLogPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	snapshot := srv.buildWebSocketSnapshot("alice", map[string]struct{}{
		"sessions":  {},
		"transfers": {},
		"audit":     {},
	})

	sessionsPayload, ok := snapshot["sessions"].([]*session.Session)
	if !ok {
		t.Fatalf("expected sessions payload, got %T", snapshot["sessions"])
	}
	if len(sessionsPayload) != 1 || sessionsPayload[0].Username != "alice" {
		t.Fatalf("expected only alice websocket sessions, got %#v", sessionsPayload)
	}

	transfersPayload, ok := snapshot["transfers"].(map[string]any)
	if !ok {
		t.Fatalf("expected transfers payload, got %T", snapshot["transfers"])
	}
	activePayload, ok := transfersPayload["active"].([]*transfer.Transfer)
	if !ok {
		t.Fatalf("expected active transfers payload, got %T", transfersPayload["active"])
	}
	for _, item := range activePayload {
		if item.Username != "alice" {
			t.Fatalf("expected only alice active websocket transfers, got %#v", activePayload)
		}
	}

	auditPayload, ok := snapshot["audit"].(map[string]any)
	if !ok {
		t.Fatalf("expected audit payload, got %T", snapshot["audit"])
	}
	eventsPayload, ok := auditPayload["events"].([]map[string]any)
	if !ok {
		t.Fatalf("expected audit events payload, got %T", auditPayload["events"])
	}
	if len(eventsPayload) != 1 || eventsPayload[0]["username"] != "alice" {
		t.Fatalf("expected only alice websocket audit events, got %#v", eventsPayload)
	}
}

func newAccessControlServer(t *testing.T) *Server {
	t.Helper()

	tempDir := t.TempDir()
	st, err := store.Open(tempDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, 15*time.Minute)
	if _, err := engine.CreateUser("admin", "StrongPass123!", "/", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := engine.CreateUser("alice", "StrongPass123!", "/", false); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if _, err := engine.CreateUser("bob", "StrongPass123!", "/", false); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	return &Server{
		auth:         engine,
		users:        repo,
		store:        st,
		sessions:     session.NewManager(),
		transfers:    transfer.NewManager(32),
		auditLogPath: filepath.Join(tempDir, "audit.jsonl"),
	}
}

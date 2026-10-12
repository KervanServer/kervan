package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

func callTools(t *testing.T, server *Server, stdin *bytes.Buffer, stdout *bytes.Buffer, calls ...map[string]any) []string {
	t.Helper()
	for i, c := range calls {
		writeFrame(stdin, map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call", "params": c})
	}
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range readAllFrames(t, stdout.Bytes()) {
		if r["error"] != nil {
			out = append(out, "ERROR "+r["error"].(map[string]any)["message"].(string))
			continue
		}
		out = append(out, extractTextResult(t, r))
	}
	stdout.Reset()
	return out
}

func TestAdminTools(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo := auth.NewUserRepository(st)
	groups := auth.NewGroupRepository(st, repo)
	if err := groups.Create(&auth.Group{Name: "staff", Permissions: auth.DefaultUserPermissions(), MaxStorage: 5000}); err != nil {
		t.Fatal(err)
	}
	expiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.Create(&auth.User{
		Username: "alice", Type: auth.UserTypeVirtual, HomeDir: "/", Enabled: true,
		PasswordHash: "secret-hash", PrimaryGroup: "staff", Permissions: auth.DefaultUserPermissions(),
		ExpiresAt: &expiry, AllowedIPs: []string{"10.0.0.0/8"}, AllowedProtocols: []string{"sftp"}, MaxSessions: 2,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = dataDir
	cfg.Quota.Enabled = true
	key, err := audit.LoadOrCreateChainKey(cfg.AuditKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	auditLog := filepath.Join(dataDir, "audit.jsonl")
	sink, err := audit.NewChainedFileSink(auditLog, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []audit.Event{
		{Type: audit.EventAuthFailure, Username: "alice", Status: "failed", Timestamp: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{Type: audit.EventAuthFailure, Username: "alice", Status: "failed", Timestamp: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)},
		{Type: audit.EventAuthSuccess, Username: "alice", Status: "ok", Timestamp: time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)},
	} {
		if err := sink.Write(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	_ = sink.Close()

	stdin, stdout := bytes.NewBuffer(nil), bytes.NewBuffer(nil)
	server := NewServer(cfg, repo, nil, auditLog, stdin, stdout)
	server.SetGroups(groups)

	res := callTools(t, server, stdin, stdout,
		map[string]any{"name": "get_user", "arguments": map[string]any{"username": "alice"}},
		map[string]any{"name": "list_groups"},
		map[string]any{"name": "verify_audit"},
		map[string]any{"name": "audit_query", "arguments": map[string]any{"status": "failed", "from": "2026-10-02"}},
		map[string]any{"name": "get_user", "arguments": map[string]any{"username": "nobody"}},
		map[string]any{"name": "audit_query", "arguments": map[string]any{"from": "last week"}},
	)
	for _, want := range []string{`"allowed_protocols": [`, `"sftp"`, `"max_sessions": 2`, `"group": "staff"`, `"max_storage": 5000`} {
		if !strings.Contains(res[0], want) {
			t.Fatalf("get_user missing %s: %s", want, res[0])
		}
	}
	if strings.Contains(res[0], "secret-hash") {
		t.Fatal("get_user leaked the password hash")
	}
	if !strings.Contains(res[1], `"name": "staff"`) || !strings.Contains(res[1], `"member_count": 1`) {
		t.Fatalf("list_groups: %s", res[1])
	}
	if !strings.Contains(res[2], `"ok": true`) || !strings.Contains(res[2], `"records": 3`) {
		t.Fatalf("verify_audit: %s", res[2])
	}
	if strings.Count(res[3], `"type": "auth.failure"`) != 1 || !strings.Contains(res[3], "2026-10-05T09:00:00Z") {
		t.Fatalf("filtered audit_query: %s", res[3])
	}
	if !strings.HasPrefix(res[4], "ERROR") || !strings.HasPrefix(res[5], "ERROR") {
		t.Fatalf("expected errors, got %q / %q", res[4], res[5])
	}

	// Tampering is reported.
	raw, _ := os.ReadFile(auditLog)
	if err := os.WriteFile(auditLog, bytes.Replace(raw, []byte(`"failed"`), []byte(`"ok"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	res = callTools(t, server, stdin, stdout, map[string]any{"name": "verify_audit"})
	if !strings.Contains(res[0], `"ok": false`) {
		t.Fatalf("tampered log verified: %s", res[0])
	}
}

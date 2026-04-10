package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

func TestServerToolsAndResources(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()

	repo := auth.NewUserRepository(st)
	if err := repo.Create(&auth.User{
		Username:    "alice",
		Email:       "alice@example.com",
		Type:        auth.UserTypeVirtual,
		HomeDir:     "/alice",
		Enabled:     true,
		Permissions: auth.DefaultUserPermissions(),
	}); err != nil {
		t.Fatalf("repo.Create() error = %v", err)
	}

	auditLog := filepath.Join(dataDir, "audit.jsonl")
	sink, err := audit.NewFileSink(auditLog)
	if err != nil {
		t.Fatalf("audit.NewFileSink() error = %v", err)
	}
	if err := sink.Write(context.Background(), audit.Event{Type: audit.EventAuthSuccess, Username: "alice", Message: "ok"}); err != nil {
		t.Fatalf("sink.Write(auth) error = %v", err)
	}
	if err := sink.Write(context.Background(), audit.Event{Type: audit.EventFileWrite, Username: "alice", Path: "/alice/file.txt"}); err != nil {
		t.Fatalf("sink.Write(file) error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("sink.Close() error = %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = dataDir

	stdin := bytes.NewBuffer(nil)
	stdout := bytes.NewBuffer(nil)
	server := NewServer(cfg, repo, nil, auditLog, stdin, stdout)

	writeFrame(stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	writeFrame(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	writeFrame(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_users",
			"arguments": map[string]any{"enabled_only": true},
		},
	})
	writeFrame(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "audit_query",
			"arguments": map[string]any{"limit": 10, "username": "alice"},
		},
	})
	writeFrame(stdin, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "resources/list"})
	writeFrame(stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "kervan://users"},
	})

	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	responses := readAllFrames(t, stdout.Bytes())
	if len(responses) != 6 {
		t.Fatalf("response count = %d, want 6", len(responses))
	}

	if responses[0]["result"] == nil {
		t.Fatalf("initialize missing result: %#v", responses[0])
	}

	toolsList := responses[1]["result"].(map[string]any)
	if len(toolsList["tools"].([]any)) != 3 {
		t.Fatalf("unexpected tools payload: %#v", toolsList)
	}

	listUsersText := extractTextResult(t, responses[2])
	if !strings.Contains(listUsersText, "\"username\": \"alice\"") {
		t.Fatalf("list_users result missing alice: %s", listUsersText)
	}
	if strings.Contains(listUsersText, "password_hash") {
		t.Fatalf("list_users leaked secrets: %s", listUsersText)
	}

	auditText := extractTextResult(t, responses[3])
	if !strings.Contains(auditText, "\"type\": \"file.write\"") {
		t.Fatalf("audit_query result missing audit events: %s", auditText)
	}

	resourceList := responses[4]["result"].(map[string]any)
	if len(resourceList["resources"].([]any)) != 3 {
		t.Fatalf("unexpected resources payload: %#v", resourceList)
	}

	resourceRead := responses[5]["result"].(map[string]any)
	contents := resourceRead["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("unexpected resource contents: %#v", resourceRead)
	}
	text := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "\"username\": \"alice\"") {
		t.Fatalf("resources/read missing alice: %s", text)
	}
}

func writeFrame(w io.Writer, payload any) {
	raw, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(raw))
	_, _ = w.Write(raw)
}

func readAllFrames(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	reader := bytes.NewReader(raw)
	responses := []map[string]any{}
	for reader.Len() > 0 {
		var length int
		if _, err := fmt.Fscanf(reader, "Content-Length: %d\r\n\r\n", &length); err != nil {
			t.Fatalf("read header: %v", err)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			t.Fatalf("read payload: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		responses = append(responses, decoded)
	}
	return responses
}

func extractTextResult(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %#v", response)
	}
	content := result["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

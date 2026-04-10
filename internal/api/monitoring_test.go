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
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestHandleMetricsIncludesOperationalMetrics(t *testing.T) {
	st := openTestStore(t)
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, 15*time.Minute)

	admin := mustCreateUser(t, engine, "admin", true)
	disabled := mustCreateUser(t, engine, "disabled", false)
	disabled.Enabled = false
	if err := repo.Update(disabled); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	locked := mustCreateUser(t, engine, "locked", false)
	until := time.Now().UTC().Add(10 * time.Minute)
	locked.LockedUntil = &until
	if err := repo.Update(locked); err != nil {
		t.Fatalf("lock user: %v", err)
	}

	sessions := session.NewManager()
	sessions.Start(admin.Username, "ftp", "127.0.0.1:1000")
	sftpSession := sessions.Start(disabled.Username, "sftp", "127.0.0.1:1001")
	sessions.End(sftpSession.ID)

	transfers := transfer.NewManager(16)
	upload := transfers.Start(admin.Username, "ftp", "/upload.txt", transfer.DirectionUpload, 128)
	transfers.AddBytes(upload, 128)
	transfers.End(upload, transfer.StatusCompleted, "")

	failedDownload := transfers.Start(locked.Username, "sftp", "/download.txt", transfer.DirectionDownload, 256)
	transfers.AddBytes(failedDownload, 64)
	transfers.End(failedDownload, transfer.StatusFailed, "io error")

	activeDownload := transfers.Start(admin.Username, "ftp", "/stream.bin", transfer.DirectionDownload, 512)
	transfers.AddBytes(activeDownload, 10)

	srv := &Server{
		users:     repo,
		sessions:  sessions,
		transfers: transfers,
		status: func() map[string]any {
			return map[string]any{
				"uptime_seconds": int64(42),
			}
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.handleMetrics(rec, req)

	body := rec.Body.String()
	assertContains(t, body, "# HELP kervan_uptime_seconds Process uptime in seconds.")
	assertContains(t, body, "kervan_sessions_active 1")
	assertContains(t, body, `kervan_connections_total{protocol="ftp"} 1`)
	assertContains(t, body, `kervan_connections_total{protocol="sftp"} 1`)
	assertContains(t, body, "kervan_users_total 3")
	assertContains(t, body, "kervan_users_disabled_total 1")
	assertContains(t, body, "kervan_auth_locked_accounts 1")
	assertContains(t, body, "kervan_transfers_total 3")
	assertContains(t, body, `kervan_transfer_bytes_total{direction="upload"} 128`)
	assertContains(t, body, `kervan_transfer_bytes_total{direction="download"} 74`)
	assertContains(t, body, "kervan_memory_bytes ")
}

func TestHandleHealthBuildsStructuredSubsystemChecks(t *testing.T) {
	tempDir := t.TempDir()
	st := openTestStoreAt(t, tempDir)
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, 15*time.Minute)
	_ = mustCreateUser(t, engine, "admin", true)

	storageRoot := filepath.Join(tempDir, "storage")
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		t.Fatalf("create storage root: %v", err)
	}
	auditPath := filepath.Join(tempDir, "logs", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		t.Fatalf("create audit dir: %v", err)
	}

	srv := &Server{
		auth:  engine,
		users: repo,
		store: st,
		fsBuilder: func(string) (vfs.FileSystem, error) {
			return nil, nil
		},
		auditLogPath: auditPath,
		transfers:    transfer.NewManager(4),
		status: func() map[string]any {
			return map[string]any{
				"name":               "Kervan Test",
				"version":            "1.2.3",
				"uptime_seconds":     int64(3600),
				"ftp_enabled":        true,
				"ftp_port":           2121,
				"ftps_enabled":       true,
				"ftps_mode":          "both",
				"ftps_implicit_port": 990,
				"sftp_enabled":       true,
				"sftp_port":          2222,
				"scp_enabled":        true,
				"webui_enabled":      true,
				"webui_port":         8080,
				"storage_backend":    "local",
				"storage_root":       storageRoot,
				"store_path":         filepath.Join(tempDir, "kervan-store.json"),
			}
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.handleHealth(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}

	if got := payload["status"]; got != "healthy" {
		t.Fatalf("expected healthy status, got %v", got)
	}
	if got := payload["version"]; got != "1.2.3" {
		t.Fatalf("unexpected version: %v", got)
	}
	if got := int(payload["uptime_seconds"].(float64)); got != 3600 {
		t.Fatalf("unexpected uptime: %d", got)
	}

	checks, ok := payload["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected checks map, got %T", payload["checks"])
	}
	assertCheckStatus(t, checks, "ftp", "up")
	assertCheckStatus(t, checks, "ftps", "up")
	assertCheckStatus(t, checks, "sftp", "up")
	assertCheckStatus(t, checks, "scp", "up")
	assertCheckStatus(t, checks, "storage", "up")
	assertCheckStatus(t, checks, "cobaltdb", "up")
	assertCheckStatus(t, checks, "audit", "up")
}

func openTestStore(t *testing.T) *store.Store {
	return openTestStoreAt(t, t.TempDir())
}

func openTestStoreAt(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func mustCreateUser(t *testing.T, engine *auth.Engine, username string, admin bool) *auth.User {
	t.Helper()
	user, err := engine.CreateUser(username, "StrongPass123!", "/", admin)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return user
}

func assertContains(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Fatalf("expected output to contain %q\nfull body:\n%s", needle, body)
	}
}

func assertCheckStatus(t *testing.T, checks map[string]any, key, want string) {
	t.Helper()
	raw, ok := checks[key]
	if !ok {
		t.Fatalf("missing %s check", key)
	}
	check, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected %s check to be an object, got %T", key, raw)
	}
	if got := check["status"]; got != want {
		t.Fatalf("expected %s status %q, got %v", key, want, got)
	}
}

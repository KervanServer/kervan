package server

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/config"
)

func TestRedactionHelpers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.LDAP.BindPassword = "ldap-secret"
	cfg.WebUI.AdminPassword = "admin-secret"

	redacted := redactConfig(cfg)
	authMap, ok := redacted["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth map, got %#v", redacted["auth"])
	}
	ldapMap, ok := authMap["ldap"].(map[string]any)
	if !ok {
		t.Fatalf("expected ldap map, got %#v", authMap["ldap"])
	}
	if ldapMap["bind_password"] != "***REDACTED***" {
		t.Fatalf("expected ldap bind password to be redacted, got %#v", ldapMap["bind_password"])
	}

	if !shouldRedact("api_token") || !shouldRedact("private_key") || !shouldRedact("password") {
		t.Fatal("expected sensitive keys to be redacted")
	}
	if shouldRedact("username") {
		t.Fatal("expected non-sensitive key to remain visible")
	}

	if !isSensitivePath("auth.ldap.bind_password") || !isSensitivePath("server.private_key") {
		t.Fatal("expected sensitive config paths to be blocked")
	}
	if isSensitivePath("server.name") {
		t.Fatal("expected non-sensitive config path to remain allowed")
	}
}

func TestPatchAndMapHelpers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfgMap, err := configToMap(cfg)
	if err != nil {
		t.Fatalf("configToMap: %v", err)
	}
	cloned, err := mapToConfig(cfgMap)
	if err != nil {
		t.Fatalf("mapToConfig: %v", err)
	}
	if cloned.Server.Name != cfg.Server.Name || cloned.FTP.Port != cfg.FTP.Port {
		t.Fatalf("expected config roundtrip to preserve values, got %#v", cloned)
	}

	if joinPath("", "server") != "server" || joinPath("server", "name") != "server.name" {
		t.Fatal("unexpected joinPath behavior")
	}
	if !isSameJSONValue([]string{"a", "b"}, []string{"a", "b"}) {
		t.Fatal("expected equal json values to compare true")
	}
	if isSameJSONValue([]string{"a"}, []string{"b"}) {
		t.Fatal("expected different json values to compare false")
	}

	baseMap := map[string]any{
		"server": map[string]any{"name": "old"},
		"ftp":    map[string]any{"port": 2121},
	}
	patch := map[string]any{
		"server": map[string]any{"name": "new"},
		"ftp":    map[string]any{"port": 2121},
	}
	changed := map[string]struct{}{}
	deepMergeMap(baseMap, patch, "", changed)
	if got := baseMap["server"].(map[string]any)["name"]; got != "new" {
		t.Fatalf("expected deep merge to update nested field, got %#v", got)
	}
	if _, ok := changed["server.name"]; !ok {
		t.Fatalf("expected changed path to include server.name, got %v", changed)
	}
	if _, ok := changed["ftp.port"]; ok {
		t.Fatalf("expected unchanged field to stay out of changed set, got %v", changed)
	}

	if err := validatePatchMap(map[string]any{"server": "name"}, map[string]any{"server": map[string]any{"name": "x"}}, ""); err == nil || !strings.Contains(err.Error(), "field is not an object: server") {
		t.Fatalf("expected non-object validation error, got %v", err)
	}
}

func TestStorageAndMessageHelpers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()

	kind, root := resolveStorageStatus(cfg)
	if kind != "local" || root == "" {
		t.Fatalf("expected local storage status with resolved root, got kind=%q root=%q", kind, root)
	}

	cfg.Storage.DefaultBackend = "s3"
	cfg.Storage.Backends["s3"] = config.BackendConfig{
		Type: "s3",
		Options: map[string]string{
			"bucket": "uploads",
			"prefix": "/prod/files/",
		},
	}
	kind, root = resolveStorageStatus(cfg)
	if kind != "s3" || root != "s3://uploads/prod/files" {
		t.Fatalf("unexpected s3 storage status kind=%q root=%q", kind, root)
	}

	cfg.Storage.Backends["memory"] = config.BackendConfig{Type: "memory"}
	cfg.Storage.DefaultBackend = "memory"
	kind, root = resolveStorageStatus(cfg)
	if kind != "memory" || root != "" {
		t.Fatalf("unexpected memory storage status kind=%q root=%q", kind, root)
	}

	if !parseBoolOption("yes") || parseBoolOption("no") {
		t.Fatal("unexpected parseBoolOption behavior")
	}
	if got := joinStoragePath("", "/users/", "alice", "/docs/"); got != "users/alice/docs" {
		t.Fatalf("unexpected joinStoragePath result: %q", got)
	}
	if got := joinStoragePath("", "/"); got != "" {
		t.Fatalf("expected empty joinStoragePath result, got %q", got)
	}

	applied := []string{"webui.cors_origins"}
	restart := []string{"ftp.port"}
	if !strings.Contains(reloadMessage(applied, restart), "Restart is still required") {
		t.Fatalf("unexpected mixed reload message: %q", reloadMessage(applied, restart))
	}
	if !strings.Contains(reloadMessage(applied, nil), "applied successfully") {
		t.Fatalf("unexpected applied reload message: %q", reloadMessage(applied, nil))
	}
	if !strings.Contains(reloadMessage(nil, restart), "Restart is required") {
		t.Fatalf("unexpected restart reload message: %q", reloadMessage(nil, restart))
	}
	if !strings.Contains(updateMessage(applied, restart), "applied immediately") {
		t.Fatalf("unexpected mixed update message: %q", updateMessage(applied, restart))
	}
	if !strings.Contains(updateMessage(applied, nil), "applied immediately") {
		t.Fatalf("unexpected applied update message: %q", updateMessage(applied, nil))
	}
	if !strings.Contains(updateMessage(nil, restart), "Restart is required") {
		t.Fatalf("unexpected restart update message: %q", updateMessage(nil, restart))
	}
}

func TestGracefulShutdownTimeoutFallback(t *testing.T) {
	app := &App{cfg: &config.Config{}}
	if got := app.gracefulShutdownTimeout(); got != 30*time.Second {
		t.Fatalf("expected fallback graceful shutdown timeout, got %s", got)
	}

	cfg := config.DefaultConfig()
	cfg.Server.GracefulShutdownTimeout = 12 * time.Second
	app.cfg = cfg
	if got := app.gracefulShutdownTimeout(); got != 12*time.Second {
		t.Fatalf("expected configured graceful shutdown timeout, got %s", got)
	}
}

func TestDiffRuntimeChangesAndWriteConfigFile(t *testing.T) {
	current := config.DefaultConfig()
	next := config.DefaultConfig()
	next.WebUI.TOTPEnabled = !current.WebUI.TOTPEnabled
	next.FTPS.Enabled = !current.FTPS.Enabled

	currentMap, _ := configToMap(current)
	nextMap, _ := configToMap(next)
	changed := map[string]struct{}{}
	diffConfigMaps(currentMap, nextMap, "", changed)
	if _, ok := changed["webui.totp_enabled"]; !ok {
		t.Fatalf("expected webui.totp_enabled to be detected as changed, got %v", changed)
	}
	if _, ok := changed["ftps.enabled"]; !ok {
		t.Fatalf("expected ftps.enabled to be detected as changed, got %v", changed)
	}

	applied, restart := classifyRuntimeChanges(current, next)
	if !reflect.DeepEqual(applied, []string{"webui.totp_enabled"}) || !reflect.DeepEqual(restart, []string{"ftps.enabled"}) {
		t.Fatalf("unexpected runtime change classification: applied=%v restart=%v", applied, restart)
	}
	if gotApplied, gotRestart := classifyRuntimeChanges(nil, next); gotApplied != nil || gotRestart != nil {
		t.Fatalf("expected nil configs to produce nil slices, got applied=%v restart=%v", gotApplied, gotRestart)
	}

	configPath := filepath.Join(t.TempDir(), "kervan.yaml")
	if err := writeConfigFile(configPath, current); err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}
	if err := writeConfigFile("", current); err == nil || !strings.Contains(err.Error(), "config path is empty") {
		t.Fatalf("expected empty path error, got %v", err)
	}
}

func TestClassifyRuntimeChangesIncludesAuthMinPasswordLength(t *testing.T) {
	current := config.DefaultConfig()
	next := config.DefaultConfig()
	next.Auth.MinPasswordLength = current.Auth.MinPasswordLength + 4

	applied, restart := classifyRuntimeChanges(current, next)
	if !reflect.DeepEqual(applied, []string{"auth.min_password_length"}) {
		t.Fatalf("expected auth min password length to be runtime reloadable, got applied=%v", applied)
	}
	if len(restart) != 0 {
		t.Fatalf("expected no restart-required paths, got %v", restart)
	}
}

func TestBuildAuditSinksFallbackAndErrors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.Audit.Outputs = nil

	sinks, primary, err := buildAuditSinks(cfg)
	if err != nil {
		t.Fatalf("expected fallback file sink, got %v", err)
	}
	if len(sinks) != 1 || primary == "" {
		t.Fatalf("unexpected fallback sink result sinks=%d primary=%q", len(sinks), primary)
	}
	closeAuditSinks(sinks)

	if _, _, err := buildAuditSinks(nil); err == nil || !strings.Contains(err.Error(), "config is nil") {
		t.Fatalf("expected nil config error, got %v", err)
	}

	badCfg := config.DefaultConfig()
	badCfg.Server.DataDir = t.TempDir()
	badCfg.Audit.Outputs = []config.AuditOutput{{Type: "queue"}}
	if _, _, err := buildAuditSinks(badCfg); err == nil || !strings.Contains(err.Error(), "unsupported audit output type") {
		t.Fatalf("expected unsupported output error, got %v", err)
	}
}

func TestCloseAuditSinksClosesAll(t *testing.T) {
	sinkA := &closingSink{}
	sinkB := &closingSink{}
	closeAuditSinks([]audit.Sink{sinkA, sinkB})
	if sinkA.closed != 1 || sinkB.closed != 1 {
		t.Fatalf("expected all sinks to be closed once, got a=%d b=%d", sinkA.closed, sinkB.closed)
	}
}

type closingSink struct {
	closed int
}

func (c *closingSink) Write(context.Context, audit.Event) error { return nil }

func (c *closingSink) Close() error {
	c.closed++
	return nil
}

func (c *closingSink) Flush() error { return nil }

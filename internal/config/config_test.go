package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfigValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if cfg.WebUI.AdminPassword != "" {
		t.Fatalf("expected empty default bootstrap admin password, got %q", cfg.WebUI.AdminPassword)
	}
	if len(cfg.WebUI.CORSOrigins) != 0 {
		t.Fatalf("expected CORS to be disabled by default, got %v", cfg.WebUI.CORSOrigins)
	}
}

func TestLoadAndOverlayEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kervan.yaml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("write default: %v", err)
	}
	t.Setenv("KERVAN_FTP__PORT", "2221")
	t.Setenv("KERVAN_AUTH__PASSWORD_HASH", "bcrypt")
	t.Setenv("KERVAN_DEBUG__PORT", "6061")
	t.Setenv("KERVAN_SERVER__LOG_MAX_BACKUPS", "7")
	t.Setenv("KERVAN_WEBUI__READ_TIMEOUT", "20s")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.FTP.Port != 2221 {
		t.Fatalf("expected ftp port=2221, got=%d", cfg.FTP.Port)
	}
	if cfg.Auth.PasswordHash != "bcrypt" {
		t.Fatalf("expected bcrypt hash algo, got=%s", cfg.Auth.PasswordHash)
	}
	if cfg.Debug.Port != 6061 {
		t.Fatalf("expected debug port=6061, got=%d", cfg.Debug.Port)
	}
	if cfg.Server.LogMaxBackups != 7 {
		t.Fatalf("expected log max backups=7, got=%d", cfg.Server.LogMaxBackups)
	}
	if cfg.WebUI.ReadTimeout != 20*time.Second {
		t.Fatalf("expected webui read timeout=20s, got=%s", cfg.WebUI.ReadTimeout)
	}
}

func TestWriteDefaultFailsIfExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kervan.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(path); err == nil {
		t.Fatal("expected error when config exists")
	}
}

func TestLDAPConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.LDAP.Enabled = true
	cfg.Auth.LDAP.URL = "http://ldap.example.com"
	cfg.Auth.LDAP.BaseDN = "dc=example,dc=com"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected ldap validation error for unsupported scheme")
	}
}

func TestAutoCertValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FTPS.Enabled = true
	cfg.FTPS.AutoCert.Enabled = true
	cfg.FTPS.CertFile = ""
	cfg.FTPS.KeyFile = ""
	cfg.FTPS.AutoCert.Domains = nil

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected auto-cert validation error for missing domains")
	}
}

func TestWebUITLSValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WebUI.TLS = true
	cfg.FTPS.CertFile = ""
	cfg.FTPS.KeyFile = ""
	cfg.FTPS.AutoCert.Enabled = false

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected webui tls validation error without cert source")
	}
}

func TestDebugValidationRequiresValidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Debug.Enabled = true
	cfg.Debug.BindAddress = ""
	cfg.Debug.Port = 0
	cfg.Debug.Pprof = false

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected debug validation errors")
	}
}

func TestAuditHTTPOutputValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Audit.Outputs = []AuditOutput{
		{
			Type:       "webhook",
			URL:        "https://example.com/audit",
			BatchSize:  10,
			RetryCount: 3,
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid webhook output config, got %v", err)
	}
}

func TestWebUITimeoutValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WebUI.ReadTimeout = -1 * time.Second

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for negative webui read timeout")
	}
}

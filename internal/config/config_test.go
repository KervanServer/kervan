package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
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

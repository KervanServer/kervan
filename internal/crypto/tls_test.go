package crypto

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTLSVersion(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"1.2", false},
		{"1.3", false},
		{"tls1.2", false},
		{"tls1.3", false},
		{"", false},
		{"1.1", true},
	}
	for _, tc := range tests {
		_, err := parseTLSVersion(tc.in)
		if tc.wantErr && err == nil {
			t.Fatalf("expected error for %q", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.in, err)
		}
	}
}

func TestBuildServerTLSConfigValidationAndSource(t *testing.T) {
	if _, err := BuildServerTLSConfig("1.2", "1.3", "", ""); err == nil {
		t.Fatal("expected missing cert/key validation error")
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	notAfter := time.Now().UTC().Add(24 * time.Hour)
	writeTestCertificate(t, certPath, []string{"ftp.example.com"}, notAfter)
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read combined cert file: %v", err)
	}
	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		t.Fatalf("write combined key file: %v", err)
	}

	cfg, err := BuildServerTLSConfig("1.2", "1.3", certPath, keyPath)
	if err != nil {
		t.Fatalf("build server tls config: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 || cfg.MaxVersion != tls.VersionTLS13 {
		t.Fatalf("unexpected tls config bounds: %#v", cfg)
	}

	if _, err := BuildServerTLSConfig("1.3", "1.2", certPath, keyPath); err == nil {
		t.Fatal("expected min>max validation error")
	}

	sourceCfg, err := BuildServerTLSConfigFromSource(tls.VersionTLS12, tls.VersionTLS13, nil, nil)
	if err != nil {
		t.Fatalf("build tls config from source: %v", err)
	}
	if sourceCfg.GetCertificate != nil || len(sourceCfg.Certificates) != 0 {
		t.Fatalf("unexpected tls source config: %#v", sourceCfg)
	}
}

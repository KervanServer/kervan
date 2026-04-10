package acme

import (
	"net/http"
	"testing"
)

func TestManagerTLSConfig(t *testing.T) {
	mgr, err := New(Config{
		CacheDir: t.TempDir(),
		Email:    "admin@example.com",
		Domains:  []string{"ftp.example.com"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	cfg := mgr.TLSConfig(0, 0)
	if cfg.GetCertificate == nil {
		t.Fatal("expected GetCertificate to be set")
	}
	if len(cfg.NextProtos) == 0 {
		t.Fatal("expected NextProtos to be set")
	}
}

func TestHTTPHandler(t *testing.T) {
	mgr, err := New(Config{
		CacheDir: t.TempDir(),
		Domains:  []string{"ftp.example.com"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := mgr.HTTPHandler(http.NotFoundHandler())
	if handler == nil {
		t.Fatal("expected handler")
	}
}

package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kervanserver/kervan/internal/config"
)

func TestBuildAuditSinksSupportsFileAndWebhook(t *testing.T) {
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()

	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	cfg.Audit.Outputs = []config.AuditOutput{
		{Type: "file", Path: filepath.Join(cfg.Server.DataDir, "audit.jsonl")},
		{Type: "webhook", URL: webhook.URL, BatchSize: 1},
	}

	sinks, path, err := buildAuditSinks(cfg)
	if err != nil {
		t.Fatalf("buildAuditSinks: %v", err)
	}
	defer closeAuditSinks(sinks)

	if len(sinks) != 2 {
		t.Fatalf("expected 2 audit sinks, got %d", len(sinks))
	}
	if path == "" {
		t.Fatal("expected primary audit file path")
	}
}

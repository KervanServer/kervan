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

// TestDefaultAuditSinkPathAnchorsToDataDir pins the shipped default: an empty
// audit output path must anchor the sink to server.data_dir, not the launch
// directory (buildAuditSinks fallback, matching backupAuditPath).
func TestDefaultAuditSinkPathAnchorsToDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = dir
	sinks, primary, err := buildAuditSinks(cfg)
	if err != nil {
		t.Fatalf("buildAuditSinks: %v", err)
	}
	for _, s := range sinks {
		_ = s.Close()
	}
	want := filepath.Join(dir, "audit.jsonl")
	if primary != want {
		t.Fatalf("default-config audit sink path = %q, want %q", primary, want)
	}
}

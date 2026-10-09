package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/config"
)

func TestRunAuditVerifyAndBackupIncludesKey(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) { cfg.Server.DataDir = dataDir })
	logPath := filepath.Join(dataDir, "audit.jsonl")

	// No key yet: verification must fail without creating one.
	if err := runAuditCommand(&bytes.Buffer{}, []string{"verify", "--config", configPath}); err == nil {
		t.Fatal("verify succeeded without a key")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "audit.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("verify created an integrity key")
	}

	key, err := audit.LoadOrCreateChainKey(filepath.Join(dataDir, "audit.key"))
	if err != nil {
		t.Fatal(err)
	}
	sink, err := audit.NewChainedFileSink(logPath, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"ann", "bob", "cy"} {
		_ = sink.Write(context.Background(), audit.Event{Type: audit.EventFileWrite, Username: user})
	}
	_ = sink.Close()

	var out bytes.Buffer
	if err := runAuditCommand(&out, []string{"verify", "--config", configPath}); err != nil {
		t.Fatalf("verify clean log: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "3 chained") || !strings.Contains(out.String(), "Result:    OK") {
		t.Fatalf("output:\n%s", out.String())
	}

	// The backup carries the key next to the log.
	if err := runUserCreateCommand(&bytes.Buffer{}, []string{"--config", configPath, "--username", "ann", "--password", "StrongPass123!"}); err != nil {
		t.Fatal(err)
	}
	backupFile := filepath.Join(t.TempDir(), "b.zip")
	if err := runBackupCreateCommand(&bytes.Buffer{}, []string{"--config", configPath, "--output", backupFile}); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(backupFile)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range reader.File {
		names[f.Name] = true
	}
	_ = reader.Close()
	if !names["audit/audit.jsonl"] || !names["audit/audit.key"] {
		t.Fatalf("backup entries = %v", names)
	}

	// Tampering makes the command fail with details.
	raw, _ := os.ReadFile(logPath)
	_ = os.WriteFile(logPath, bytes.Replace(raw, []byte(`"bob"`), []byte(`"eve"`), 1), 0o600)
	out.Reset()
	if err := runAuditCommand(&out, []string{"verify", "--config", configPath}); !errors.Is(err, errAuditTampered) {
		t.Fatalf("tampered log: %v", err)
	}
	if !strings.Contains(out.String(), "PROBLEM line 2 (seq 2): MAC mismatch") {
		t.Fatalf("output:\n%s", out.String())
	}
}

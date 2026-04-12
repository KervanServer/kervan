package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/config"
	"golang.org/x/crypto/ssh"
)

func TestRunInitCommandCreatesConfigAndDataDirs(t *testing.T) {
	root := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("os.Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})
	configPath := filepath.Join(root, "config", "kervan.yaml")

	var stdout bytes.Buffer
	if err := runInitCommand(&stdout, []string{"--config", configPath}); err != nil {
		t.Fatalf("runInitCommand: %v", err)
	}
	if !strings.Contains(stdout.String(), "Config created:") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	for _, dir := range []string{cfg.Server.DataDir, cfg.SFTP.HostKeyDir, "./data/files"} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected directory %q to exist: %v", dir, err)
		}
	}
}

func TestRunKeygenCommandSupportsBothAndFingerprint(t *testing.T) {
	outputDir := t.TempDir()

	var stdout bytes.Buffer
	if err := runKeygenCommand(&stdout, []string{"--type", "both", "--output", outputDir}); err != nil {
		t.Fatalf("runKeygenCommand both: %v", err)
	}

	for _, filename := range []string{"ssh_host_ed25519_key", "ssh_host_rsa_key"} {
		path := filepath.Join(outputDir, filename)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected key %q to exist: %v", path, err)
		}
		signer, err := loadSignerForTest(path)
		if err != nil {
			t.Fatalf("load signer %q: %v", path, err)
		}
		if !strings.Contains(stdout.String(), ssh.FingerprintSHA256(signer.PublicKey())) {
			t.Fatalf("expected fingerprint for %q in output: %s", path, stdout.String())
		}
	}
}

func TestRunKeygenCommandRespectsForce(t *testing.T) {
	outputDir := t.TempDir()
	keyPath := filepath.Join(outputDir, "ssh_host_ed25519_key")
	if err := os.WriteFile(keyPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing key: %v", err)
	}

	if err := runKeygenCommand(ioDiscard{}, []string{"--type", "ed25519", "--output", outputDir}); err == nil {
		t.Fatal("expected error without --force")
	}
	if err := runKeygenCommand(ioDiscard{}, []string{"--type", "ed25519", "--output", outputDir, "--force"}); err != nil {
		t.Fatalf("runKeygenCommand --force: %v", err)
	}
}

func TestRunAdminCommandsCreateListAndReset(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	var createOut bytes.Buffer
	if err := runAdminCreateCommand(&createOut, []string{
		"--config", configPath,
		"--username", "admin2",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("runAdminCreateCommand: %v", err)
	}

	var listOut bytes.Buffer
	if err := runAdminListCommand(&listOut, []string{"--config", configPath}); err != nil {
		t.Fatalf("runAdminListCommand: %v", err)
	}
	if !strings.Contains(listOut.String(), "admin2") {
		t.Fatalf("expected admin2 in admin list: %s", listOut.String())
	}

	var listJSON bytes.Buffer
	if err := runAdminListCommand(&listJSON, []string{"--config", configPath, "--json"}); err != nil {
		t.Fatalf("runAdminListCommand --json: %v", err)
	}
	var admins []map[string]any
	if err := json.Unmarshal(listJSON.Bytes(), &admins); err != nil {
		t.Fatalf("decode admin list json: %v", err)
	}
	if len(admins) != 1 || admins[0]["username"] != "admin2" {
		t.Fatalf("unexpected admin list payload: %#v", admins)
	}

	var resetOut bytes.Buffer
	if err := runAdminResetCommand(&resetOut, []string{
		"--config", configPath,
		"--username", "admin2",
		"--password", "NewPass123!",
	}); err != nil {
		t.Fatalf("runAdminResetCommand: %v", err)
	}
	if !strings.Contains(resetOut.String(), "Password reset: admin2") {
		t.Fatalf("unexpected reset output: %s", resetOut.String())
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func loadSignerForTest(path string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}

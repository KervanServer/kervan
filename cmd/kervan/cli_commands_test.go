package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func TestRunCheckCommand(t *testing.T) {
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	})

	var stdout bytes.Buffer
	if err := runCheckCommand(&stdout, []string{"--config", configPath}); err != nil {
		t.Fatalf("runCheckCommand: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Config valid:") {
		t.Fatalf("unexpected output: %s", output)
	}
	if !strings.Contains(output, "Services:") {
		t.Fatalf("expected services summary in output: %s", output)
	}
}

func TestRunAPIKeyScopesCommand(t *testing.T) {
	var stdout bytes.Buffer
	if err := runAPIKeyScopesCommand(&stdout, nil); err != nil {
		t.Fatalf("runAPIKeyScopesCommand: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "SCOPE") || !strings.Contains(output, "files:write") {
		t.Fatalf("unexpected scopes output: %s", output)
	}
}

func TestRunAPIKeyPresetsCommandJSON(t *testing.T) {
	var stdout bytes.Buffer
	if err := runAPIKeyPresetsCommand(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("runAPIKeyPresetsCommand: %v", err)
	}
	var presets []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &presets); err != nil {
		t.Fatalf("decode presets: %v", err)
	}
	if len(presets) == 0 {
		t.Fatal("expected at least one preset")
	}
	if presets[0]["id"] == "" {
		t.Fatalf("unexpected preset payload: %#v", presets[0])
	}
}

func TestRunAPIKeyLifecycleCommands(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
		"--home-dir", "/alice",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	var createOut bytes.Buffer
	if err := runAPIKeyCreateCommand(&createOut, []string{
		"--config", configPath,
		"--username", "alice",
		"--name", "Automation key",
		"--permissions", "files:read,files:write",
		"--json",
	}); err != nil {
		t.Fatalf("runAPIKeyCreateCommand: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal(createOut.Bytes(), &created); err != nil {
		t.Fatalf("decode created key payload: %v", err)
	}
	if created["id"] == "" || created["key"] == "" {
		t.Fatalf("unexpected create payload: %#v", created)
	}

	var listOut bytes.Buffer
	if err := runAPIKeyListCommand(&listOut, []string{
		"--config", configPath,
		"--username", "alice",
	}); err != nil {
		t.Fatalf("runAPIKeyListCommand: %v", err)
	}
	if !strings.Contains(listOut.String(), "Automation key") || !strings.Contains(listOut.String(), "files:read,files:write") {
		t.Fatalf("unexpected list output: %s", listOut.String())
	}

	var revokeOut bytes.Buffer
	if err := runAPIKeyRevokeCommand(&revokeOut, []string{
		"--config", configPath,
		"--username", "alice",
		"--id", created["id"].(string),
	}); err != nil {
		t.Fatalf("runAPIKeyRevokeCommand: %v", err)
	}
	if !strings.Contains(revokeOut.String(), "API key revoked") {
		t.Fatalf("unexpected revoke output: %s", revokeOut.String())
	}
}

func TestRunUserLifecycleCommands(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	var createOut bytes.Buffer
	if err := runUserCreateCommand(&createOut, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
		"--home-dir", "/uploads",
	}); err != nil {
		t.Fatalf("runUserCreateCommand: %v", err)
	}
	if !strings.Contains(createOut.String(), "User created: alice") {
		t.Fatalf("unexpected create output: %s", createOut.String())
	}

	var listOut bytes.Buffer
	if err := runUserListCommand(&listOut, []string{"--config", configPath}); err != nil {
		t.Fatalf("runUserListCommand: %v", err)
	}
	if !strings.Contains(listOut.String(), "alice") {
		t.Fatalf("expected alice in list output: %s", listOut.String())
	}

	var jsonOut bytes.Buffer
	if err := runUserListCommand(&jsonOut, []string{"--config", configPath, "--json"}); err != nil {
		t.Fatalf("runUserListCommand --json: %v", err)
	}
	var users []map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &users); err != nil {
		t.Fatalf("decode users json: %v", err)
	}
	if len(users) != 1 || users[0]["username"] != "alice" {
		t.Fatalf("unexpected users payload: %#v", users)
	}

	var deleteOut bytes.Buffer
	if err := runUserDeleteCommand(&deleteOut, []string{"--config", configPath, "--username", "alice"}); err != nil {
		t.Fatalf("runUserDeleteCommand: %v", err)
	}
	if !strings.Contains(deleteOut.String(), "User deleted: alice") {
		t.Fatalf("unexpected delete output: %s", deleteOut.String())
	}
}

func TestRunUserImportCommandJSON(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	importFile := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(importFile, []byte(`[
  {"username":"alice","password":"StrongPass123!","email":"alice@example.com","home_dir":"/alice"},
  {"username":"ops-admin","password":"AdminPass123!","role":"admin","home_dir":"/ops","enabled":false},
  {"username":"","password":"MissingUser123!"},
  {"username":"alice","password":"Duplicate123!"}
]`), 0o600); err != nil {
		t.Fatalf("write import file: %v", err)
	}

	var stdout bytes.Buffer
	if err := runUserImportCommand(&stdout, []string{
		"--config", configPath,
		"--file", importFile,
		"--json",
	}); err != nil {
		t.Fatalf("runUserImportCommand: %v", err)
	}

	var report userImportReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Total != 4 || report.Created != 2 || report.Skipped != 2 {
		t.Fatalf("unexpected import report: %#v", report)
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	alice, err := ctx.repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice == nil {
		t.Fatal("expected alice to exist")
	}
	if alice.Email != "alice@example.com" || alice.HomeDir != "/alice" {
		t.Fatalf("unexpected alice data: %#v", alice)
	}
	if !auth.VerifyPassword("StrongPass123!", alice.PasswordHash) {
		t.Fatalf("expected alice password hash to match")
	}

	admin, err := ctx.repo.GetByUsername("ops-admin")
	if err != nil {
		t.Fatalf("get ops-admin: %v", err)
	}
	if admin == nil {
		t.Fatal("expected ops-admin to exist")
	}
	if admin.Type != auth.UserTypeAdmin {
		t.Fatalf("expected admin type, got %s", admin.Type)
	}
	if admin.Enabled {
		t.Fatalf("expected imported admin to be disabled")
	}
}

func TestRunUserImportCommandCSV(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	importFile := filepath.Join(t.TempDir(), "users.csv")
	if err := os.WriteFile(importFile, []byte(strings.Join([]string{
		"username,password,email,role,home_dir,enabled",
		"carol,StrongPass123!,carol@example.com,user,/carol,true",
		"dave,,dave@example.com,admin,/dave,false",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write csv import file: %v", err)
	}

	var stdout bytes.Buffer
	if err := runUserImportCommand(&stdout, []string{
		"--config", configPath,
		"--file", importFile,
	}); err != nil {
		t.Fatalf("runUserImportCommand: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created: 1") || !strings.Contains(stdout.String(), "Skipped: 1") {
		t.Fatalf("unexpected import output: %s", stdout.String())
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	carol, err := ctx.repo.GetByUsername("carol")
	if err != nil {
		t.Fatalf("get carol: %v", err)
	}
	if carol == nil {
		t.Fatal("expected carol to exist")
	}
	if !auth.VerifyPassword("StrongPass123!", carol.PasswordHash) {
		t.Fatalf("expected carol password hash to match")
	}

	dave, err := ctx.repo.GetByUsername("dave")
	if err != nil {
		t.Fatalf("get dave: %v", err)
	}
	if dave != nil {
		t.Fatalf("expected dave import to be skipped")
	}
}

func TestRunUserExportCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
		"--home-dir", "/alice",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "ops-admin",
		"--password", "AdminPass123!",
		"--home-dir", "/ops",
		"--admin",
	}); err != nil {
		t.Fatalf("create ops-admin: %v", err)
	}

	exportFile := filepath.Join(t.TempDir(), "users.json")
	var summary bytes.Buffer
	if err := runUserExportCommand(&summary, []string{
		"--config", configPath,
		"--format", "json",
		"--output", exportFile,
		"--include-password-hashes",
	}); err != nil {
		t.Fatalf("runUserExportCommand json: %v", err)
	}
	if !strings.Contains(summary.String(), "Exported 2 users") {
		t.Fatalf("unexpected export summary: %s", summary.String())
	}

	rawJSON, err := os.ReadFile(exportFile)
	if err != nil {
		t.Fatalf("read export json: %v", err)
	}
	var exported []userExportRecord
	if err := json.Unmarshal(rawJSON, &exported); err != nil {
		t.Fatalf("decode exported json: %v", err)
	}
	if len(exported) != 2 {
		t.Fatalf("expected 2 exported users, got %d", len(exported))
	}
	if exported[0].PasswordHash == "" || exported[1].PasswordHash == "" {
		t.Fatalf("expected password hashes in export: %#v", exported)
	}

	var csvOut bytes.Buffer
	if err := runUserExportCommand(&csvOut, []string{
		"--config", configPath,
		"--format", "csv",
		"--output", "-",
	}); err != nil {
		t.Fatalf("runUserExportCommand csv: %v", err)
	}
	if !strings.Contains(csvOut.String(), "username,email,role,type,home_dir,enabled") {
		t.Fatalf("expected csv header in output: %s", csvOut.String())
	}
	if !strings.Contains(csvOut.String(), "alice,,user,virtual,/alice,true") {
		t.Fatalf("expected alice in csv output: %s", csvOut.String())
	}
}

func TestRunBackupCreateCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
		cfg.Audit.Outputs = []config.AuditOutput{
			{Type: "file", Path: filepath.Join(dataDir, "audit.jsonl")},
		}
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "audit.jsonl"), []byte("{\"type\":\"login\"}\n"), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "kervan-backup.zip")
	var stdout bytes.Buffer
	if err := runBackupCreateCommand(&stdout, []string{
		"--config", configPath,
		"--output", backupFile,
	}); err != nil {
		t.Fatalf("runBackupCreateCommand: %v", err)
	}
	if !strings.Contains(stdout.String(), "Backup created:") {
		t.Fatalf("unexpected backup output: %s", stdout.String())
	}

	reader, err := zip.OpenReader(backupFile)
	if err != nil {
		t.Fatalf("open backup zip: %v", err)
	}
	defer reader.Close()

	entries := make(map[string]bool, len(reader.File))
	var manifest backupManifest
	for _, file := range reader.File {
		entries[file.Name] = true
		if file.Name == backupManifestPath {
			rc, err := file.Open()
			if err != nil {
				t.Fatalf("open manifest: %v", err)
			}
			raw, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
		}
	}
	for _, required := range []string{
		backupStoreArchivePath,
		backupStoreBakArchivePath,
		backupAuditArchivePath,
		backupConfigArchivePath,
		backupManifestPath,
	} {
		if !entries[required] {
			t.Fatalf("expected backup entry %q, got %#v", required, entries)
		}
	}
	if manifest.Version < 2 {
		t.Fatalf("expected backup manifest version >= 2, got %d", manifest.Version)
	}
	for _, file := range manifest.Files {
		if file.ArchivePath == "" || file.SHA256 == "" {
			t.Fatalf("expected checksum-bearing manifest entry, got %#v", file)
		}
	}
}

func TestRunBackupRestoreCommand(t *testing.T) {
	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
		cfg.Audit.Outputs = []config.AuditOutput{
			{Type: "file", Path: filepath.Join(dataDir, "audit.jsonl")},
		}
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
		"--home-dir", "/alice",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "audit.jsonl"), []byte("{\"type\":\"upload\"}\n"), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}

	backupFile := filepath.Join(baseDir, "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("remove data dir: %v", err)
	}

	var restoreOut bytes.Buffer
	if err := runBackupRestoreCommand(&restoreOut, []string{
		"--config", configPath,
		"--input", backupFile,
	}); err != nil {
		t.Fatalf("runBackupRestoreCommand: %v", err)
	}
	if !strings.Contains(restoreOut.String(), "Backup restored:") {
		t.Fatalf("unexpected restore output: %s", restoreOut.String())
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	alice, err := ctx.repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice == nil {
		t.Fatal("expected alice after restore")
	}
	if alice.HomeDir != "/alice" {
		t.Fatalf("unexpected alice home dir after restore: %q", alice.HomeDir)
	}

	rawAudit, err := os.ReadFile(filepath.Join(dataDir, "audit.jsonl"))
	if err != nil {
		t.Fatalf("read restored audit log: %v", err)
	}
	if !strings.Contains(string(rawAudit), "\"upload\"") {
		t.Fatalf("unexpected restored audit log: %s", string(rawAudit))
	}
}

func TestRunBackupRestoreCommandRequiresForce(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
		"--include-audit=false",
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	err := runBackupRestoreCommand(io.Discard, []string{
		"--config", configPath,
		"--input", backupFile,
	})
	if err == nil || !strings.Contains(err.Error(), "without --force") {
		t.Fatalf("expected overwrite protection error, got %v", err)
	}
}

func TestRunBackupRestoreCommandRejectsTamperedArchive(t *testing.T) {
	baseDir := t.TempDir()
	dataDir := filepath.Join(baseDir, "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	backupFile := filepath.Join(baseDir, "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
		"--include-audit=false",
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	tamperedFile := filepath.Join(baseDir, "backup-tampered.zip")
	if err := rewriteBackupEntry(backupFile, tamperedFile, backupStoreArchivePath, []byte(`{"users":[]}`)); err != nil {
		t.Fatalf("tamper backup: %v", err)
	}

	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("remove data dir: %v", err)
	}

	err := runBackupRestoreCommand(io.Discard, []string{
		"--config", configPath,
		"--input", tamperedFile,
	})
	if err == nil || !strings.Contains(err.Error(), "backup integrity check failed") {
		t.Fatalf("expected integrity check error, got %v", err)
	}
}

func TestRunBackupVerifyCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
		"--include-audit=false",
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	var stdout bytes.Buffer
	if err := runBackupVerifyCommand(&stdout, []string{"--input", backupFile}); err != nil {
		t.Fatalf("verify backup: %v", err)
	}
	if !strings.Contains(stdout.String(), "Backup verified:") {
		t.Fatalf("unexpected verify output: %s", stdout.String())
	}

	var jsonOut bytes.Buffer
	if err := runBackupVerifyCommand(&jsonOut, []string{"--input", backupFile, "--json"}); err != nil {
		t.Fatalf("verify backup json: %v", err)
	}
	var payload struct {
		Verified        bool `json:"verified"`
		ManifestPresent bool `json:"manifest_present"`
		VerifiedFiles   int  `json:"verified_files"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &payload); err != nil {
		t.Fatalf("decode verify json: %v", err)
	}
	if !payload.Verified || !payload.ManifestPresent || payload.VerifiedFiles == 0 {
		t.Fatalf("unexpected verify payload: %#v", payload)
	}
}

func TestRunBackupVerifyCommandWithoutManifest(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
		cfg.Audit.Outputs = []config.AuditOutput{{
			Type: "file",
			Path: filepath.Join(dataDir, "audit.jsonl"),
		}}
	})
	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
		"--include-audit=false",
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	manifestless := filepath.Join(t.TempDir(), "backup-no-manifest.zip")
	if err := removeBackupEntry(backupFile, manifestless, backupManifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	var stdout bytes.Buffer
	if err := runBackupVerifyCommand(&stdout, []string{"--input", manifestless}); err != nil {
		t.Fatalf("verify backup without manifest: %v", err)
	}
	if !strings.Contains(stdout.String(), "Backup inspected:") {
		t.Fatalf("unexpected verify output: %s", stdout.String())
	}

	var jsonOut bytes.Buffer
	if err := runBackupVerifyCommand(&jsonOut, []string{"--input", manifestless, "--json"}); err != nil {
		t.Fatalf("verify backup without manifest json: %v", err)
	}
	var payload struct {
		Verified        bool `json:"verified"`
		ManifestPresent bool `json:"manifest_present"`
		VerifiedFiles   int  `json:"verified_files"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &payload); err != nil {
		t.Fatalf("decode verify json: %v", err)
	}
	if payload.Verified || payload.ManifestPresent || payload.VerifiedFiles != 0 {
		t.Fatalf("unexpected verify payload without manifest: %#v", payload)
	}
}

func TestRunBackupVerifyCommandRejectsOversizedManifest(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})
	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
	}); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	backupFile := filepath.Join(t.TempDir(), "backup.zip")
	if err := runBackupCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--output", backupFile,
		"--include-audit=false",
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	oversized := filepath.Join(t.TempDir(), "backup-oversized-manifest.zip")
	manifestPayload := bytes.Repeat([]byte("a"), backupManifestMaxBytes+128)
	if err := rewriteBackupEntry(backupFile, oversized, backupManifestPath, manifestPayload); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	err := runBackupVerifyCommand(io.Discard, []string{"--input", oversized})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized manifest error, got %v", err)
	}
}

func rewriteBackupEntry(srcPath, dstPath, archivePath string, replacement []byte) error {
	reader, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	writer := zip.NewWriter(out)
	for _, file := range reader.File {
		header := file.FileHeader
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			_ = writer.Close()
			return err
		}
		if file.Name == archivePath {
			if _, err := entry.Write(replacement); err != nil {
				_ = writer.Close()
				return err
			}
			continue
		}
		rc, err := file.Open()
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err := io.Copy(entry, rc); err != nil {
			_ = rc.Close()
			_ = writer.Close()
			return err
		}
		_ = rc.Close()
	}
	return writer.Close()
}

func removeBackupEntry(srcPath, dstPath, archivePath string) error {
	reader, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	writer := zip.NewWriter(out)
	for _, file := range reader.File {
		if file.Name == archivePath {
			continue
		}
		header := file.FileHeader
		entry, err := writer.CreateHeader(&header)
		if err != nil {
			_ = writer.Close()
			return err
		}
		rc, err := file.Open()
		if err != nil {
			_ = writer.Close()
			return err
		}
		if _, err := io.Copy(entry, rc); err != nil {
			_ = rc.Close()
			_ = writer.Close()
			return err
		}
		_ = rc.Close()
	}
	return writer.Close()
}

func TestRunStatusCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         "healthy",
			"version":        "test-version",
			"uptime_seconds": 120,
			"checks": map[string]any{
				"ftp": map[string]any{"status": "up"},
			},
		})
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.WebUI.BindAddress = host
		cfg.WebUI.Port = mustAtoi(t, port)
		cfg.WebUI.Enabled = true
		cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	})

	var stdout bytes.Buffer
	if err := runStatusCommand(&stdout, []string{"--config", configPath}); err != nil {
		t.Fatalf("runStatusCommand: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Server status: healthy") {
		t.Fatalf("unexpected output: %s", output)
	}
	if !strings.Contains(output, "ftp: up") {
		t.Fatalf("expected ftp check in output: %s", output)
	}
}

func TestRunStatusCommandTLSRequiresTrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "healthy"})
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.WebUI.BindAddress = host
		cfg.WebUI.Port = mustAtoi(t, port)
		cfg.WebUI.Enabled = true
		cfg.WebUI.TLS = true
		cfg.FTPS.AutoCert.Enabled = true
		cfg.FTPS.AutoCert.Domains = []string{"localhost"}
		cfg.FTPS.AutoCert.ACMEDir = filepath.Join(t.TempDir(), "acme")
		cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	})

	if err := runStatusCommand(io.Discard, []string{"--config", configPath}); err == nil {
		t.Fatal("expected TLS status check to fail without --insecure")
	}
}

func TestRunStatusCommandTLSAllowsInsecureFlag(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "healthy"})
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.WebUI.BindAddress = host
		cfg.WebUI.Port = mustAtoi(t, port)
		cfg.WebUI.Enabled = true
		cfg.WebUI.TLS = true
		cfg.FTPS.AutoCert.Enabled = true
		cfg.FTPS.AutoCert.Domains = []string{"localhost"}
		cfg.FTPS.AutoCert.ACMEDir = filepath.Join(t.TempDir(), "acme")
		cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	})

	var stdout bytes.Buffer
	if err := runStatusCommand(&stdout, []string{"--config", configPath, "--insecure"}); err != nil {
		t.Fatalf("runStatusCommand --insecure: %v", err)
	}
	if !strings.Contains(stdout.String(), "Server status: healthy") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunUserCreateCommandEnforcesConfiguredMinPasswordLength(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
		cfg.Auth.MinPasswordLength = 12
	})

	err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "short",
		"--home-dir", "/uploads",
	})
	if err == nil || !strings.Contains(err.Error(), "at least 12 characters") {
		t.Fatalf("expected password policy error, got %v", err)
	}
}

func TestRunMigrateVSFTPDCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	userDBPath := filepath.Join(t.TempDir(), "virtual_users.txt")
	if err := os.WriteFile(userDBPath, []byte(strings.Join([]string{
		"# comment",
		"alice",
		"StrongPass123!",
		"bob",
		"AnotherPass123!",
		"alice",
		"DuplicatePass123!",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write vsftpd user db: %v", err)
	}

	var stdout bytes.Buffer
	if err := runMigrateVSFTPDCommand(&stdout, []string{
		"--config", configPath,
		"--user-db", userDBPath,
		"--home-root", "/legacy",
		"--json",
	}); err != nil {
		t.Fatalf("runMigrateVSFTPDCommand: %v", err)
	}

	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode migrate report: %v", err)
	}
	if report.Total != 3 || report.Migrated != 2 || report.Skipped != 1 {
		t.Fatalf("unexpected migrate report: %#v", report)
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	alice, err := ctx.repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice == nil {
		t.Fatal("expected alice to exist")
	}
	if alice.HomeDir != "/legacy/alice" {
		t.Fatalf("expected alice home dir to be migrated, got %q", alice.HomeDir)
	}
	if !auth.VerifyPassword("StrongPass123!", alice.PasswordHash) {
		t.Fatalf("expected alice password hash to match imported password")
	}

	bob, err := ctx.repo.GetByUsername("bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob == nil {
		t.Fatal("expected bob to exist")
	}
	if bob.HomeDir != "/legacy/bob" {
		t.Fatalf("expected bob home dir to be migrated, got %q", bob.HomeDir)
	}
}

func TestRunMigrateProFTPDCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	bcryptHash, err := auth.HashPassword("LegacyPass123!", "bcrypt")
	if err != nil {
		t.Fatalf("hash legacy password: %v", err)
	}

	sourceDir := t.TempDir()
	userFilePath := filepath.Join(sourceDir, "ftpd.passwd")
	if err := os.WriteFile(userFilePath, []byte(strings.Join([]string{
		fmt.Sprintf("carol:%s:1001:1001::/srv/carol:/bin/bash", bcryptHash),
		"dave:PlainPass123!:1002:1002::/srv/dave:/bin/false",
		"erin:$1$legacyhash:1003:1003::/srv/erin:/bin/bash",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write proftpd user file: %v", err)
	}

	proftpdConfigPath := filepath.Join(sourceDir, "proftpd.conf")
	if err := os.WriteFile(proftpdConfigPath, []byte(strings.Join([]string{
		"ServerName \"Legacy ProFTPD\"",
		"AuthUserFile ftpd.passwd",
		"DefaultRoot ~",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write proftpd config: %v", err)
	}

	var stdout bytes.Buffer
	if err := runMigrateProFTPDCommand(&stdout, []string{
		"--kervan-config", configPath,
		"--config", proftpdConfigPath,
		"--json",
	}); err != nil {
		t.Fatalf("runMigrateProFTPDCommand: %v", err)
	}

	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode migrate report: %v", err)
	}
	if report.Total != 3 || report.Migrated != 2 || report.Skipped != 1 {
		t.Fatalf("unexpected migrate report: %#v", report)
	}
	if len(report.Warnings) == 0 {
		t.Fatalf("expected unsupported directive/hash warnings, got %#v", report)
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	carol, err := ctx.repo.GetByUsername("carol")
	if err != nil {
		t.Fatalf("get carol: %v", err)
	}
	if carol == nil {
		t.Fatal("expected carol to exist")
	}
	if carol.HomeDir != "/srv/carol" {
		t.Fatalf("expected carol home dir to be migrated, got %q", carol.HomeDir)
	}
	if !auth.VerifyPassword("LegacyPass123!", carol.PasswordHash) {
		t.Fatalf("expected carol bcrypt hash to be preserved")
	}

	dave, err := ctx.repo.GetByUsername("dave")
	if err != nil {
		t.Fatalf("get dave: %v", err)
	}
	if dave == nil {
		t.Fatal("expected dave to exist")
	}
	if dave.Enabled {
		t.Fatalf("expected dave to be disabled because shell is nologin")
	}

	erin, err := ctx.repo.GetByUsername("erin")
	if err != nil {
		t.Fatalf("get erin: %v", err)
	}
	if erin != nil {
		t.Fatalf("expected erin to be skipped due to unsupported hash")
	}
}

func TestRunMigrateSSHKeysCommand(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	configPath := writeTestConfig(t, func(cfg *config.Config) {
		cfg.Server.DataDir = dataDir
	})

	if err := runUserCreateCommand(io.Discard, []string{
		"--config", configPath,
		"--username", "alice",
		"--password", "StrongPass123!",
		"--home-dir", "/existing",
	}); err != nil {
		t.Fatalf("create existing alice: %v", err)
	}

	baseDir := filepath.Join(t.TempDir(), "homes")
	aliceSSHDir := filepath.Join(baseDir, "alice", ".ssh")
	bobSSHDir := filepath.Join(baseDir, "bob", ".ssh")
	if err := os.MkdirAll(aliceSSHDir, 0o700); err != nil {
		t.Fatalf("mkdir alice ssh dir: %v", err)
	}
	if err := os.MkdirAll(bobSSHDir, 0o700); err != nil {
		t.Fatalf("mkdir bob ssh dir: %v", err)
	}

	aliceKey1 := generateAuthorizedKey(t)
	aliceKey2 := generateAuthorizedKey(t)
	bobKey := generateAuthorizedKey(t)
	if err := os.WriteFile(filepath.Join(aliceSSHDir, "authorized_keys"), []byte(strings.Join([]string{
		aliceKey1,
		aliceKey2,
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write alice authorized_keys: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bobSSHDir, "authorized_keys"), []byte(strings.Join([]string{
		"# comment",
		bobKey,
		"invalid key line",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("write bob authorized_keys: %v", err)
	}

	var stdout bytes.Buffer
	if err := runMigrateSSHKeysCommand(&stdout, []string{
		"--config", configPath,
		"--authorized-keys-dir", filepath.Join(baseDir, "*", ".ssh"),
		"--json",
	}); err != nil {
		t.Fatalf("runMigrateSSHKeysCommand: %v", err)
	}

	var report migrationReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode ssh keys report: %v", err)
	}
	if report.Total != 2 || report.Migrated != 2 || report.Skipped != 0 {
		t.Fatalf("unexpected ssh keys report: %#v", report)
	}
	if report.KeyCounts["alice"] != 2 || report.KeyCounts["bob"] != 1 {
		t.Fatalf("unexpected key counts: %#v", report.KeyCounts)
	}

	ctx, err := openCLIContext(configPath)
	if err != nil {
		t.Fatalf("openCLIContext: %v", err)
	}
	defer ctx.close()

	alice, err := ctx.repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if alice == nil {
		t.Fatal("expected alice to exist")
	}
	if alice.HomeDir != "/existing" {
		t.Fatalf("expected existing alice home dir to be preserved, got %q", alice.HomeDir)
	}
	if len(alice.AuthorizedKeys) != 2 {
		t.Fatalf("expected 2 alice authorized keys, got %#v", alice.AuthorizedKeys)
	}

	bob, err := ctx.repo.GetByUsername("bob")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob == nil {
		t.Fatal("expected bob to be created")
	}
	if len(bob.AuthorizedKeys) != 1 {
		t.Fatalf("expected 1 bob authorized key, got %#v", bob.AuthorizedKeys)
	}
	if bob.HomeDir != filepath.Join(baseDir, "bob") {
		t.Fatalf("expected bob home dir to be inferred, got %q", bob.HomeDir)
	}
}

func writeTestConfig(t *testing.T, mutate func(*config.Config)) string {
	t.Helper()
	cfg := config.DefaultConfig()
	if mutate != nil {
		mutate(cfg)
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "kervan.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func mustAtoi(t *testing.T, raw string) int {
	t.Helper()
	value, err := net.LookupPort("tcp", raw)
	if err != nil {
		t.Fatalf("parse port %q: %v", raw, err)
	}
	return value
}

func generateAuthorizedKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("new public key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
}

package main

import (
	"bytes"
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

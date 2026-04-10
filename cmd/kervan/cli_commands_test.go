package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

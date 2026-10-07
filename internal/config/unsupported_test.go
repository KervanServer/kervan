package config

import (
	"strings"
	"testing"
)

func TestUnsupportedSettingWarnings(t *testing.T) {
	if w := UnsupportedSettingWarnings(DefaultConfig()); len(w) != 0 {
		t.Fatalf("defaults must not warn: %v", w)
	}
	cfg := DefaultConfig()
	cfg.SFTP.DisableShell = false
	cfg.MCP.Transport = "http"
	w := UnsupportedSettingWarnings(cfg)
	joined := strings.Join(w, "\n")
	if len(w) != 2 || !strings.Contains(joined, "sftp.disable_shell") || !strings.Contains(joined, "mcp.transport") {
		t.Fatalf("unexpected warnings: %v", w)
	}
}

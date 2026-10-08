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

func TestValidateSyslogAuditOutput(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Audit.Outputs = []AuditOutput{{Type: "syslog", URL: "tls://siem.example:6514", Format: "cef", Facility: "authpriv"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid syslog output rejected: %v", err)
	}
	cfg.Audit.Outputs = []AuditOutput{{Type: "syslog", URL: "http://siem"}, {Type: "syslog", URL: "udp://h:514", Format: "xml"}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "scheme must be") || !strings.Contains(err.Error(), "format must be") {
		t.Fatalf("invalid syslog outputs: %v", err)
	}
}

package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/config"
)

func TestMergeConfigPatchTracksChangedPaths(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"server": map[string]any{
			"name": "Kervan QA",
		},
		"ftp": map[string]any{
			"port": 2122,
		},
		"security": map[string]any{
			"allowed_ips": []any{"127.0.0.1"},
		},
	}

	merged, changed, err := mergeConfigPatch(base, patch)
	if err != nil {
		t.Fatalf("mergeConfigPatch returned error: %v", err)
	}
	if merged.Server.Name != "Kervan QA" {
		t.Fatalf("expected server.name to be updated, got %q", merged.Server.Name)
	}
	if merged.FTP.Port != 2122 {
		t.Fatalf("expected ftp.port to be updated, got %d", merged.FTP.Port)
	}
	wantPaths := []string{"ftp.port", "security.allowed_ips", "server.name"}
	if !reflect.DeepEqual(changed, wantPaths) {
		t.Fatalf("unexpected changed paths: got=%v want=%v", changed, wantPaths)
	}
}

func TestMergeConfigPatchRejectsUnknownField(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"server": map[string]any{
			"does_not_exist": "x",
		},
	}

	_, _, err := mergeConfigPatch(base, patch)
	if err == nil {
		t.Fatal("expected error for unknown config field")
	}
	if !strings.Contains(err.Error(), "unknown config field: server.does_not_exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeConfigPatchRejectsSensitiveField(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"webui": map[string]any{
			"admin_password": "secret",
		},
	}

	_, _, err := mergeConfigPatch(base, patch)
	if err == nil {
		t.Fatal("expected error for sensitive field")
	}
	if !strings.Contains(err.Error(), "updating sensitive field is not allowed: webui.admin_password") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeConfigPatchRejectsRedactedPlaceholder(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"server": map[string]any{
			"name": "***REDACTED***",
		},
	}

	_, _, err := mergeConfigPatch(base, patch)
	if err == nil {
		t.Fatal("expected error for redacted placeholder")
	}
	if !strings.Contains(err.Error(), "redacted values are not allowed in patches: server.name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeConfigPatchRejectsObjectReplacementWithScalar(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"server": "broken",
	}

	_, _, err := mergeConfigPatch(base, patch)
	if err == nil {
		t.Fatal("expected error for replacing object field with scalar")
	}
	if !strings.Contains(err.Error(), "field must be an object: server") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeConfigPatchNoChangeWhenSameValue(t *testing.T) {
	base := config.DefaultConfig()
	patch := map[string]any{
		"server": map[string]any{
			"name": base.Server.Name,
		},
	}

	merged, changed, err := mergeConfigPatch(base, patch)
	if err != nil {
		t.Fatalf("mergeConfigPatch returned error: %v", err)
	}
	if merged.Server.Name != base.Server.Name {
		t.Fatalf("expected server.name to remain %q, got %q", base.Server.Name, merged.Server.Name)
	}
	if len(changed) != 0 {
		t.Fatalf("expected no changed paths, got %v", changed)
	}
}

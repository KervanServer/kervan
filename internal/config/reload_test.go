package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLiveConfigReloadUpdatesCurrentAndHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kervan.yaml")
	if err := WriteDefault(path); err != nil {
		t.Fatalf("write default config: %v", err)
	}

	lc, err := NewLiveConfig(path)
	if err != nil {
		t.Fatalf("new live config: %v", err)
	}
	if lc.Get() == nil {
		t.Fatal("expected current config to be loaded")
	}

	var reloaded *Config
	lc.OnReload(func(cfg *Config) {
		reloaded = cfg
	})

	cfg := DefaultConfig()
	cfg.WebUI.Port = 9090
	cfg.WebUI.SessionTimeout = 2 * time.Hour
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal updated config: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write updated config: %v", err)
	}

	if err := lc.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := lc.Get().WebUI.Port; got != 9090 {
		t.Fatalf("expected reloaded port=9090, got=%d", got)
	}
	if reloaded == nil || reloaded.WebUI.SessionTimeout != 2*time.Hour {
		t.Fatalf("expected reload hook to receive updated config, got %#v", reloaded)
	}
}

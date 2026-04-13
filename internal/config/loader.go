package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/store"
	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	// #nosec G304 -- config path is explicit CLI/service input from trusted operator context.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	expanded := os.ExpandEnv(string(data))
	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("decode config file %s: %w", path, err)
	}

	cfg.OverlayEnv()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) OverlayEnv() {
	for _, raw := range os.Environ() {
		parts := strings.SplitN(raw, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		if !strings.HasPrefix(key, "KERVAN_") {
			continue
		}
		c.applyEnvOverride(strings.TrimPrefix(key, "KERVAN_"), value)
	}
}

func (c *Config) applyEnvOverride(key, value string) {
	normalized := strings.ToUpper(strings.ReplaceAll(key, "__", "."))
	switch normalized {
	case "SERVER.DATA_DIR":
		c.Server.DataDir = value
	case "SERVER.LOG_LEVEL":
		c.Server.LogLevel = value
	case "SERVER.LOG_FORMAT":
		c.Server.LogFormat = value
	case "SERVER.LOG_MAX_SIZE_MB":
		c.Server.LogMaxSizeMB = parseInt(value, c.Server.LogMaxSizeMB)
	case "SERVER.LOG_MAX_BACKUPS":
		c.Server.LogMaxBackups = parseInt(value, c.Server.LogMaxBackups)
	case "FTP.ENABLED":
		c.FTP.Enabled = parseBool(value, c.FTP.Enabled)
	case "FTP.PORT":
		c.FTP.Port = parseInt(value, c.FTP.Port)
	case "FTP.PASSIVE_PORT_RANGE":
		c.FTP.PassivePortRange = value
	case "FTP.PASSIVE_IP":
		c.FTP.PassiveIP = value
	case "SFTP.ENABLED":
		c.SFTP.Enabled = parseBool(value, c.SFTP.Enabled)
	case "SFTP.PORT":
		c.SFTP.Port = parseInt(value, c.SFTP.Port)
	case "AUTH.PASSWORD_HASH":
		c.Auth.PasswordHash = value
	case "AUTH.MIN_PASSWORD_LENGTH":
		c.Auth.MinPasswordLength = parseInt(value, c.Auth.MinPasswordLength)
	case "DEBUG.ENABLED":
		c.Debug.Enabled = parseBool(value, c.Debug.Enabled)
	case "DEBUG.BIND_ADDRESS":
		c.Debug.BindAddress = value
	case "DEBUG.PORT":
		c.Debug.Port = parseInt(value, c.Debug.Port)
	case "DEBUG.PPROF":
		c.Debug.Pprof = parseBool(value, c.Debug.Pprof)
	case "WEBUI.READ_TIMEOUT":
		c.WebUI.ReadTimeout = parseDuration(value, c.WebUI.ReadTimeout)
	case "WEBUI.READ_HEADER_TIMEOUT":
		c.WebUI.ReadHeaderTimeout = parseDuration(value, c.WebUI.ReadHeaderTimeout)
	case "WEBUI.WRITE_TIMEOUT":
		c.WebUI.WriteTimeout = parseDuration(value, c.WebUI.WriteTimeout)
	case "WEBUI.IDLE_TIMEOUT":
		c.WebUI.IdleTimeout = parseDuration(value, c.WebUI.IdleTimeout)
	case "STORAGE.DEFAULT_BACKEND":
		c.Storage.DefaultBackend = value
	}
}

func WriteDefault(path string) error {
	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal default config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config directory for %s: %w", path, err)
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("config already exists")
	}
	if err := store.WriteFileAtomically(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file %s: %w", path, err)
	}
	return nil
}

func parseInt(raw string, fallback int) int {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func parseBool(raw string, fallback bool) bool {
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return v
}

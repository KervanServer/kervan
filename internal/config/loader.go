package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	expanded := os.ExpandEnv(string(data))
	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, err
	}

	cfg.OverlayEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
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
	case "STORAGE.DEFAULT_BACKEND":
		c.Storage.DefaultBackend = value
	}
}

func WriteDefault(path string) error {
	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("config already exists")
	}
	return os.WriteFile(path, data, 0o600)
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

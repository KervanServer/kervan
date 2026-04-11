package config

import (
	"strings"
	"testing"
	"time"
)

func TestApplyEnvOverrideCoversRuntimeFieldsAndFallbacks(t *testing.T) {
	cfg := DefaultConfig()

	cfg.applyEnvOverride("FTP__ENABLED", "false")
	cfg.applyEnvOverride("FTP__PASSIVE_PORT_RANGE", "51000-51100")
	cfg.applyEnvOverride("FTP__PASSIVE_IP", "203.0.113.10")
	cfg.applyEnvOverride("SFTP__ENABLED", "false")
	cfg.applyEnvOverride("SFTP__PORT", "2022")
	cfg.applyEnvOverride("AUTH__MIN_PASSWORD_LENGTH", "12")
	cfg.applyEnvOverride("DEBUG__ENABLED", "true")
	cfg.applyEnvOverride("DEBUG__BIND_ADDRESS", "127.0.0.2")
	cfg.applyEnvOverride("DEBUG__PPROF", "false")
	cfg.applyEnvOverride("WEBUI__READ_HEADER_TIMEOUT", "11s")
	cfg.applyEnvOverride("WEBUI__WRITE_TIMEOUT", "22s")
	cfg.applyEnvOverride("WEBUI__IDLE_TIMEOUT", "33s")
	cfg.applyEnvOverride("STORAGE__DEFAULT_BACKEND", "memory")

	if cfg.FTP.Enabled {
		t.Fatal("expected ftp.enabled override to disable ftp")
	}
	if cfg.FTP.PassivePortRange != "51000-51100" {
		t.Fatalf("expected passive range override, got %q", cfg.FTP.PassivePortRange)
	}
	if cfg.FTP.PassiveIP != "203.0.113.10" {
		t.Fatalf("expected passive ip override, got %q", cfg.FTP.PassiveIP)
	}
	if cfg.SFTP.Enabled {
		t.Fatal("expected sftp.enabled override to disable sftp")
	}
	if cfg.SFTP.Port != 2022 {
		t.Fatalf("expected sftp.port=2022, got %d", cfg.SFTP.Port)
	}
	if cfg.Auth.MinPasswordLength != 12 {
		t.Fatalf("expected auth.min_password_length=12, got %d", cfg.Auth.MinPasswordLength)
	}
	if !cfg.Debug.Enabled {
		t.Fatal("expected debug.enabled override to enable debug")
	}
	if cfg.Debug.BindAddress != "127.0.0.2" {
		t.Fatalf("expected debug.bind_address override, got %q", cfg.Debug.BindAddress)
	}
	if cfg.Debug.Pprof {
		t.Fatal("expected debug.pprof override to disable pprof")
	}
	if cfg.WebUI.ReadHeaderTimeout != 11*time.Second {
		t.Fatalf("expected read header timeout=11s, got %s", cfg.WebUI.ReadHeaderTimeout)
	}
	if cfg.WebUI.WriteTimeout != 22*time.Second {
		t.Fatalf("expected write timeout=22s, got %s", cfg.WebUI.WriteTimeout)
	}
	if cfg.WebUI.IdleTimeout != 33*time.Second {
		t.Fatalf("expected idle timeout=33s, got %s", cfg.WebUI.IdleTimeout)
	}
	if cfg.Storage.DefaultBackend != "memory" {
		t.Fatalf("expected default backend=memory, got %q", cfg.Storage.DefaultBackend)
	}

	previousPort := cfg.SFTP.Port
	previousEnabled := cfg.Debug.Enabled
	previousTimeout := cfg.WebUI.WriteTimeout

	cfg.applyEnvOverride("SFTP__PORT", "not-a-number")
	cfg.applyEnvOverride("DEBUG__ENABLED", "not-a-bool")
	cfg.applyEnvOverride("WEBUI__WRITE_TIMEOUT", "not-a-duration")

	if cfg.SFTP.Port != previousPort {
		t.Fatalf("expected invalid int override to keep fallback %d, got %d", previousPort, cfg.SFTP.Port)
	}
	if cfg.Debug.Enabled != previousEnabled {
		t.Fatalf("expected invalid bool override to keep fallback %t, got %t", previousEnabled, cfg.Debug.Enabled)
	}
	if cfg.WebUI.WriteTimeout != previousTimeout {
		t.Fatalf("expected invalid duration override to keep fallback %s, got %s", previousTimeout, cfg.WebUI.WriteTimeout)
	}
}

func TestValidateReturnsDetailedErrorsForInvalidConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.DataDir = ""
	cfg.Server.LogLevel = "verbose"
	cfg.Server.LogFormat = "pretty"
	cfg.Server.LogMaxSizeMB = 0
	cfg.Server.LogMaxBackups = 0

	cfg.FTP.Enabled = true
	cfg.FTP.Port = 70000
	cfg.FTP.PassivePortRange = "bad-range"

	cfg.SFTP.Enabled = true
	cfg.SFTP.Port = 0

	cfg.FTPS.Enabled = true
	cfg.FTPS.Mode = "broken"
	cfg.FTPS.CertFile = ""
	cfg.FTPS.KeyFile = ""
	cfg.FTPS.ImplicitPort = 0
	cfg.FTPS.AutoCert.Enabled = true
	cfg.FTPS.AutoCert.Domains = nil
	cfg.FTPS.AutoCert.ACMEDir = " "

	cfg.WebUI.TLS = true
	cfg.WebUI.ReadTimeout = -1 * time.Second
	cfg.WebUI.ReadHeaderTimeout = -2 * time.Second
	cfg.WebUI.WriteTimeout = -3 * time.Second
	cfg.WebUI.IdleTimeout = -4 * time.Second

	cfg.Debug.Enabled = true
	cfg.Debug.BindAddress = ""
	cfg.Debug.Port = 70000
	cfg.Debug.Pprof = false

	cfg.Auth.PasswordHash = "sha1"
	cfg.Auth.DefaultProvider = "oauth"
	cfg.Auth.MinPasswordLength = 3
	cfg.Auth.LDAP.Enabled = true
	cfg.Auth.LDAP.URL = ""
	cfg.Auth.LDAP.BaseDN = ""
	cfg.Auth.LDAP.PoolSize = -1

	cfg.Storage.DefaultBackend = "missing"
	cfg.Storage.Backends = map[string]BackendConfig{
		"s3bad": {
			Type: "s3",
			Options: map[string]string{
				"endpoint": "",
				"bucket":   "",
			},
		},
		"weird": {
			Type: "mystery",
		},
	}

	cfg.Security.AllowedIPs = []string{"not-an-ip"}
	cfg.Security.DeniedIPs = []string{"also-not-an-ip"}
	cfg.Audit.Outputs = []AuditOutput{
		{Type: "file", Path: ""},
		{
			Type:          "http",
			URL:           "://bad-url",
			BatchSize:     -1,
			FlushInterval: -1 * time.Second,
			RetryCount:    -1,
		},
		{Type: "queue"},
	}
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = "tcp"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected invalid config to fail validation")
	}

	message := err.Error()
	expected := []string{
		"server.data_dir is required",
		"server.log_level must be debug|info|warn|error",
		"server.log_format must be json|text",
		"server.log_max_size_mb must be >= 1",
		"server.log_max_backups must be >= 1",
		"ftp.port must be 1-65535",
		"ftp.passive_port_range: invalid start port",
		"sftp.port must be 1-65535",
		"ftps.mode must be explicit|implicit|both",
		"ftps.implicit_port must be 1-65535",
		"ftps.auto_cert.domains is required when ftps.auto_cert.enabled=true",
		"ftps.auto_cert.acme_dir is required when ftps.auto_cert.enabled=true",
		"webui.read_timeout must be >= 0",
		"webui.read_header_timeout must be >= 0",
		"webui.write_timeout must be >= 0",
		"webui.idle_timeout must be >= 0",
		"debug.bind_address is required when debug.enabled=true",
		"debug.port must be 1-65535",
		"debug.pprof must be enabled when debug.enabled=true",
		"auth.password_hash must be argon2id|bcrypt",
		"auth.default_provider must be local|ldap",
		"auth.min_password_length must be >= 4",
		"auth.ldap.url is required when auth.ldap.enabled=true",
		"auth.ldap.base_dn is required when auth.ldap.enabled=true",
		"auth.ldap.connection_pool_size must be >= 0",
		"storage.default_backend must reference a configured backend",
		"storage.backends.s3bad.options.endpoint is required for s3",
		"storage.backends.s3bad.options.bucket is required for s3",
		"storage.backends.weird.type must be local|memory|s3",
		"security.allowed_ips contains invalid entry: not-an-ip",
		"security.denied_ips contains invalid entry: also-not-an-ip",
		"audit.outputs[0].path is required for file outputs",
		"audit.outputs[1].url must be a valid http:// or https:// URL",
		"audit.outputs[1].batch_size must be >= 0",
		"audit.outputs[1].flush_interval must be >= 0",
		"audit.outputs[1].retry_count must be >= 0",
		"audit.outputs[2].type must be file|http|webhook",
		"mcp.transport must be stdio",
	}

	for _, needle := range expected {
		if !strings.Contains(message, needle) {
			t.Fatalf("expected validation error to contain %q, got:\n%s", needle, message)
		}
	}
}

func TestValidateHelpersCoverEdgeCases(t *testing.T) {
	t.Run("port-range", func(t *testing.T) {
		cases := []struct {
			name  string
			input string
			valid bool
		}{
			{name: "valid", input: "1024-2048", valid: true},
			{name: "bad-start", input: "x-2048", valid: false},
			{name: "bad-end", input: "1024-y", valid: false},
			{name: "out-of-range", input: "22-21", valid: false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := validatePortRange(tc.input)
				if tc.valid && err != nil {
					t.Fatalf("expected valid range, got %v", err)
				}
				if !tc.valid && err == nil {
					t.Fatalf("expected invalid range for %q", tc.input)
				}
			})
		}
	})

	t.Run("ip-or-cidr", func(t *testing.T) {
		if !validIPOrCIDR("192.0.2.1") {
			t.Fatal("expected plain IPv4 address to be valid")
		}
		if !validIPOrCIDR("2001:db8::/32") {
			t.Fatal("expected IPv6 CIDR to be valid")
		}
		if validIPOrCIDR("   ") {
			t.Fatal("expected blank value to be invalid")
		}
		if validIPOrCIDR("not-a-network") {
			t.Fatal("expected malformed entry to be invalid")
		}
	})
}

package config

import "time"

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Name:                    "Kervan File Server",
			ListenAddress:           "0.0.0.0",
			DataDir:                 "./data",
			LogLevel:                "info",
			LogFormat:               "json",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		FTP: FTPConfig{
			Enabled:          true,
			Port:             2121,
			Banner:           "Welcome to Kervan File Server",
			PassivePortRange: "50000-50100",
			PassiveIP:        "",
			ActiveMode:       true,
			ASCIITransfer:    true,
			MaxConnections:   500,
			IdleTimeout:      300 * time.Second,
			TransferTimeout:  3600 * time.Second,
		},
		FTPS: FTPSConfig{
			Enabled:       false,
			Mode:          "explicit",
			ImplicitPort:  990,
			MinTLSVersion: "1.2",
			MaxTLSVersion: "1.3",
			ClientAuth:    "none",
		},
		SFTP: SFTPConfig{
			Enabled:           true,
			Port:              2222,
			HostKeyDir:        "./data/host_keys",
			HostKeyAlgorithms: []string{"ed25519", "rsa"},
			MaxConnections:    500,
			IdleTimeout:       300 * time.Second,
			MaxPacketSize:     32768,
			DisableShell:      true,
		},
		SCP: SCPConfig{Enabled: true},
		WebUI: WebUIConfig{
			Enabled:        true,
			Port:           8080,
			TLS:            false,
			BindAddress:    "0.0.0.0",
			AdminUsername:  "admin",
			SessionTimeout: 24 * time.Hour,
			CORSOrigins:    []string{"*"},
		},
		Auth: AuthConfig{
			DefaultProvider:   "local",
			PasswordHash:      "argon2id",
			MinPasswordLength: 8,
			LDAP: LDAPConfig{
				UsernameAttribute: "uid",
				EmailAttribute:    "mail",
				GroupAttribute:    "memberOf",
				UserFilter:        "(&(objectClass=person)(uid=%s))",
				DefaultHomeDir:    "/",
				CacheTTL:          5 * time.Minute,
				PoolSize:          4,
			},
		},
		Storage: StorageConfig{
			DefaultBackend: "local",
			Backends: map[string]BackendConfig{
				"local": {
					Type: "local",
					Options: map[string]string{
						"root": "./data/files",
					},
				},
			},
		},
		Quota: QuotaConfig{
			Enabled:           true,
			DefaultMaxStorage: 1 << 30,
			DefaultMaxFiles:   100000,
			CheckInterval:     60 * time.Second,
		},
		Audit: AuditConfig{
			Enabled: true,
			Outputs: []AuditOutput{
				{
					Type: "file",
					Path: "./data/audit.jsonl",
				},
			},
		},
		Security: SecurityConfig{
			BruteForce: BruteForceConfig{
				Enabled:         true,
				MaxAttempts:     5,
				LockoutDuration: 15 * time.Minute,
				IPBanThreshold:  20,
				IPBanDuration:   1 * time.Hour,
			},
		},
		MCP: MCPConfig{
			Enabled:   true,
			Transport: "stdio",
		},
	}
}

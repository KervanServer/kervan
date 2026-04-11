package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func (c *Config) Validate() error {
	var errs []string

	if c.Server.DataDir == "" {
		errs = append(errs, "server.data_dir is required")
	}
	if c.Server.LogLevel != "debug" && c.Server.LogLevel != "info" && c.Server.LogLevel != "warn" && c.Server.LogLevel != "error" {
		errs = append(errs, "server.log_level must be debug|info|warn|error")
	}
	if c.Server.LogFormat != "json" && c.Server.LogFormat != "text" {
		errs = append(errs, "server.log_format must be json|text")
	}
	if c.Server.LogMaxSizeMB < 1 {
		errs = append(errs, "server.log_max_size_mb must be >= 1")
	}
	if c.Server.LogMaxBackups < 1 {
		errs = append(errs, "server.log_max_backups must be >= 1")
	}

	if c.FTP.Enabled {
		if c.FTP.Port < 1 || c.FTP.Port > 65535 {
			errs = append(errs, "ftp.port must be 1-65535")
		}
		if err := validatePortRange(c.FTP.PassivePortRange); err != nil {
			errs = append(errs, "ftp.passive_port_range: "+err.Error())
		}
	}
	if c.SFTP.Enabled && (c.SFTP.Port < 1 || c.SFTP.Port > 65535) {
		errs = append(errs, "sftp.port must be 1-65535")
	}
	if c.FTPS.Enabled {
		mode := strings.ToLower(c.FTPS.Mode)
		if mode != "explicit" && mode != "implicit" && mode != "both" {
			errs = append(errs, "ftps.mode must be explicit|implicit|both")
		}
		autoCertEnabled := c.FTPS.AutoCert.Enabled
		if !autoCertEnabled && (c.FTPS.CertFile == "" || c.FTPS.KeyFile == "") {
			errs = append(errs, "ftps.cert_file and ftps.key_file are required when ftps.enabled=true unless ftps.auto_cert.enabled=true")
		}
		if c.FTPS.ImplicitPort < 1 || c.FTPS.ImplicitPort > 65535 {
			errs = append(errs, "ftps.implicit_port must be 1-65535")
		}
		if autoCertEnabled {
			if len(c.FTPS.AutoCert.Domains) == 0 {
				errs = append(errs, "ftps.auto_cert.domains is required when ftps.auto_cert.enabled=true")
			}
			if strings.TrimSpace(c.FTPS.AutoCert.ACMEDir) == "" {
				errs = append(errs, "ftps.auto_cert.acme_dir is required when ftps.auto_cert.enabled=true")
			}
		}
	}
	if c.WebUI.TLS && !c.FTPS.AutoCert.Enabled && (strings.TrimSpace(c.FTPS.CertFile) == "" || strings.TrimSpace(c.FTPS.KeyFile) == "") {
		errs = append(errs, "webui.tls requires ftps cert_file/key_file or ftps.auto_cert.enabled=true")
	}
	if c.WebUI.ReadTimeout < 0 {
		errs = append(errs, "webui.read_timeout must be >= 0")
	}
	if c.WebUI.ReadHeaderTimeout < 0 {
		errs = append(errs, "webui.read_header_timeout must be >= 0")
	}
	if c.WebUI.WriteTimeout < 0 {
		errs = append(errs, "webui.write_timeout must be >= 0")
	}
	if c.WebUI.IdleTimeout < 0 {
		errs = append(errs, "webui.idle_timeout must be >= 0")
	}
	if c.Debug.Enabled {
		if strings.TrimSpace(c.Debug.BindAddress) == "" {
			errs = append(errs, "debug.bind_address is required when debug.enabled=true")
		}
		if c.Debug.Port < 1 || c.Debug.Port > 65535 {
			errs = append(errs, "debug.port must be 1-65535")
		}
		if !c.Debug.Pprof {
			errs = append(errs, "debug.pprof must be enabled when debug.enabled=true")
		}
	}
	if c.Auth.PasswordHash != "argon2id" && c.Auth.PasswordHash != "bcrypt" {
		errs = append(errs, "auth.password_hash must be argon2id|bcrypt")
	}
	if c.Auth.DefaultProvider != "" && c.Auth.DefaultProvider != "local" && c.Auth.DefaultProvider != "ldap" {
		errs = append(errs, "auth.default_provider must be local|ldap")
	}
	if c.Auth.MinPasswordLength < 4 {
		errs = append(errs, "auth.min_password_length must be >= 4")
	}
	if c.Auth.LDAP.Enabled {
		if strings.TrimSpace(c.Auth.LDAP.URL) == "" {
			errs = append(errs, "auth.ldap.url is required when auth.ldap.enabled=true")
		} else if parsed, err := url.Parse(c.Auth.LDAP.URL); err != nil || parsed.Host == "" {
			errs = append(errs, "auth.ldap.url must be a valid ldap:// or ldaps:// URL")
		} else if parsed.Scheme != "ldap" && parsed.Scheme != "ldaps" {
			errs = append(errs, "auth.ldap.url scheme must be ldap or ldaps")
		}
		if strings.TrimSpace(c.Auth.LDAP.BaseDN) == "" {
			errs = append(errs, "auth.ldap.base_dn is required when auth.ldap.enabled=true")
		}
		if c.Auth.LDAP.PoolSize < 0 {
			errs = append(errs, "auth.ldap.connection_pool_size must be >= 0")
		}
	}

	defaultBackend := strings.TrimSpace(c.Storage.DefaultBackend)
	if defaultBackend == "" {
		defaultBackend = "local"
	}
	if defaultBackend != "local" {
		if _, ok := c.Storage.Backends[defaultBackend]; !ok {
			errs = append(errs, "storage.default_backend must reference a configured backend")
		}
	}
	for name, backend := range c.Storage.Backends {
		backendType := strings.ToLower(strings.TrimSpace(backend.Type))
		if backendType == "" {
			backendType = name
		}
		switch backendType {
		case "local", "memory":
		case "s3":
			if strings.TrimSpace(backend.Options["endpoint"]) == "" {
				errs = append(errs, "storage.backends."+name+".options.endpoint is required for s3")
			}
			if strings.TrimSpace(backend.Options["bucket"]) == "" {
				errs = append(errs, "storage.backends."+name+".options.bucket is required for s3")
			}
		default:
			errs = append(errs, "storage.backends."+name+".type must be local|memory|s3")
		}
	}

	for _, ip := range c.Security.AllowedIPs {
		if !validIPOrCIDR(ip) {
			errs = append(errs, "security.allowed_ips contains invalid entry: "+ip)
		}
	}
	for _, ip := range c.Security.DeniedIPs {
		if !validIPOrCIDR(ip) {
			errs = append(errs, "security.denied_ips contains invalid entry: "+ip)
		}
	}
	for i, output := range c.Audit.Outputs {
		prefix := fmt.Sprintf("audit.outputs[%d]", i)
		outputType := strings.ToLower(strings.TrimSpace(output.Type))
		switch outputType {
		case "", "file":
			if strings.TrimSpace(output.Path) == "" {
				errs = append(errs, prefix+".path is required for file outputs")
			}
		case "http", "webhook":
			if strings.TrimSpace(output.URL) == "" {
				errs = append(errs, prefix+".url is required for http/webhook outputs")
			} else if parsed, err := url.Parse(output.URL); err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				errs = append(errs, prefix+".url must be a valid http:// or https:// URL")
			}
			if output.BatchSize < 0 {
				errs = append(errs, prefix+".batch_size must be >= 0")
			}
			if output.FlushInterval < 0 {
				errs = append(errs, prefix+".flush_interval must be >= 0")
			}
			if output.RetryCount < 0 {
				errs = append(errs, prefix+".retry_count must be >= 0")
			}
		default:
			errs = append(errs, prefix+".type must be file|http|webhook")
		}
	}
	if c.MCP.Enabled {
		transport := strings.ToLower(strings.TrimSpace(c.MCP.Transport))
		if transport == "" {
			transport = "stdio"
		}
		if transport != "stdio" {
			errs = append(errs, "mcp.transport must be stdio")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func validatePortRange(s string) error {
	p := strings.SplitN(strings.TrimSpace(s), "-", 2)
	if len(p) != 2 {
		return fmt.Errorf("expected start-end")
	}
	start, err := strconv.Atoi(strings.TrimSpace(p[0]))
	if err != nil {
		return fmt.Errorf("invalid start port")
	}
	end, err := strconv.Atoi(strings.TrimSpace(p[1]))
	if err != nil {
		return fmt.Errorf("invalid end port")
	}
	if start < 1024 || end > 65535 || start > end {
		return fmt.Errorf("ports must be in 1024-65535 and start<=end")
	}
	return nil
}

func validIPOrCIDR(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if net.ParseIP(raw) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(raw)
	return err == nil
}

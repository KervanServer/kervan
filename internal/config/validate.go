package config

import (
	"fmt"
	"net"
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
		if c.FTPS.CertFile == "" || c.FTPS.KeyFile == "" {
			errs = append(errs, "ftps.cert_file and ftps.key_file are required when ftps.enabled=true")
		}
		if c.FTPS.ImplicitPort < 1 || c.FTPS.ImplicitPort > 65535 {
			errs = append(errs, "ftps.implicit_port must be 1-65535")
		}
	}
	if c.Auth.PasswordHash != "argon2id" && c.Auth.PasswordHash != "bcrypt" {
		errs = append(errs, "auth.password_hash must be argon2id|bcrypt")
	}
	if c.Auth.MinPasswordLength < 4 {
		errs = append(errs, "auth.min_password_length must be >= 4")
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

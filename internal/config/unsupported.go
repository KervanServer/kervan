package config

import (
	"slices"
	"strings"
	"time"
)

// UnsupportedSettingWarnings lists settings that are accepted for forward
// compatibility but have no runtime effect in this release, whenever they are
// set to something other than the inert default. Operators get a startup
// warning instead of a silent false sense of control.
func UnsupportedSettingWarnings(c *Config) []string {
	if c == nil {
		return nil
	}
	var out []string
	add := func(path, why string) { out = append(out, path+": "+why) }

	if !c.FTP.ASCIITransfer {
		add("ftp.ascii_transfer", "TYPE A is accepted but data is always transferred as binary")
	}
	if algos := c.SFTP.HostKeyAlgorithms; len(algos) > 0 && !slices.Equal(algos, []string{"ed25519", "rsa"}) {
		add("sftp.host_key_algorithms", "ignored; a single ed25519 host key is used")
	}
	if !c.SFTP.DisableShell {
		add("sftp.disable_shell", "ignored; interactive shells are never offered")
	}
	if p := strings.ToLower(strings.TrimSpace(c.Auth.DefaultProvider)); p != "" && p != "local" {
		add("auth.default_provider", "ignored; local users are tried first, then LDAP when auth.ldap.enabled")
	}
	if c.Auth.LDAP.Enabled && c.Auth.LDAP.PoolSize > 0 && c.Auth.LDAP.PoolSize != 4 {
		add("auth.ldap.connection_pool_size", "ignored; LDAP binds use one connection per authentication")
	}
	if c.Quota.Enabled && c.Quota.DefaultMaxFiles > 0 && c.Quota.DefaultMaxFiles != 100000 {
		add("quota.default_max_files", "ignored; only quota.default_max_storage is enforced")
	}
	if c.Quota.CheckInterval > 0 && c.Quota.CheckInterval != 60*time.Second {
		add("quota.check_interval", "ignored; usage is measured at login and tracked on every write")
	}
	if t := strings.ToLower(strings.TrimSpace(c.MCP.Transport)); t != "" && t != "stdio" {
		add("mcp.transport", "only stdio is supported (kervan mcp)")
	}
	return out
}

package config

import (
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	FTP      FTPConfig      `yaml:"ftp"`
	FTPS     FTPSConfig     `yaml:"ftps"`
	SFTP     SFTPConfig     `yaml:"sftp"`
	SCP      SCPConfig      `yaml:"scp"`
	WebUI    WebUIConfig    `yaml:"webui"`
	Debug    DebugConfig    `yaml:"debug"`
	Auth     AuthConfig     `yaml:"auth"`
	Storage  StorageConfig  `yaml:"storage"`
	Quota    QuotaConfig    `yaml:"quota"`
	Audit    AuditConfig    `yaml:"audit"`
	Security SecurityConfig `yaml:"security"`
	MCP      MCPConfig      `yaml:"mcp"`
}

type ServerConfig struct {
	Name                    string        `yaml:"name"`
	ListenAddress           string        `yaml:"listen_address"`
	PIDFile                 string        `yaml:"pid_file"`
	DataDir                 string        `yaml:"data_dir"`
	LogLevel                string        `yaml:"log_level"`
	LogFormat               string        `yaml:"log_format"`
	LogFile                 string        `yaml:"log_file"`
	LogMaxSizeMB            int           `yaml:"log_max_size_mb"`
	LogMaxBackups           int           `yaml:"log_max_backups"`
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout"`
}

type FTPConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Port             int           `yaml:"port"`
	Banner           string        `yaml:"banner"`
	PassivePortRange string        `yaml:"passive_port_range"`
	PassiveIP        string        `yaml:"passive_ip"`
	ActiveMode       bool          `yaml:"active_mode"`
	ASCIITransfer    bool          `yaml:"ascii_transfer"`
	MaxConnections   int           `yaml:"max_connections"`
	IdleTimeout      time.Duration `yaml:"idle_timeout"`
	TransferTimeout  time.Duration `yaml:"transfer_timeout"`
	// VirtualHosts are selected with the RFC 7151 HOST command or, for
	// FTPS, the TLS server name.
	VirtualHosts map[string]FTPVirtualHost `yaml:"virtual_hosts"`
}

type FTPVirtualHost struct {
	Banner        string   `yaml:"banner"`
	AllowedGroups []string `yaml:"allowed_groups"`
}

type FTPSConfig struct {
	Enabled       bool           `yaml:"enabled"`
	Mode          string         `yaml:"mode"`
	ImplicitPort  int            `yaml:"implicit_port"`
	MinTLSVersion string         `yaml:"min_tls_version"`
	MaxTLSVersion string         `yaml:"max_tls_version"`
	CertFile      string         `yaml:"cert_file"`
	KeyFile       string         `yaml:"key_file"`
	ClientAuth    string         `yaml:"client_auth"`
	ClientCAFile  string         `yaml:"client_ca_file"`
	AutoCert      AutoCertConfig `yaml:"auto_cert"`
}

type AutoCertConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Domains   []string `yaml:"domains"`
	ACMEEmail string   `yaml:"acme_email"`
	ACMEDir   string   `yaml:"acme_dir"`
}

type SFTPConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Port              int           `yaml:"port"`
	HostKeyDir        string        `yaml:"host_key_dir"`
	HostKeyAlgorithms []string      `yaml:"host_key_algorithms"`
	MaxConnections    int           `yaml:"max_connections"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	DisableShell      bool          `yaml:"disable_shell"`
}

type SCPConfig struct {
	Enabled bool `yaml:"enabled"`
}

type WebUIConfig struct {
	Enabled           bool          `yaml:"enabled"`
	Port              int           `yaml:"port"`
	TLS               bool          `yaml:"tls"`
	BindAddress       string        `yaml:"bind_address"`
	AdminUsername     string        `yaml:"admin_username"`
	AdminPassword     string        `yaml:"admin_password"`
	SessionTimeout    time.Duration `yaml:"session_timeout"`
	TOTPEnabled       bool          `yaml:"totp_enabled"`
	CORSOrigins       []string      `yaml:"cors_origins"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	OIDC              OIDCConfig    `yaml:"oidc"`
}

// OIDCConfig enables WebUI sign-in through an OpenID Connect provider.
// Protocol (FTP/SFTP) access is unaffected.
type OIDCConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// RedirectURL is this server's callback, registered at the provider:
	// https://<host>/api/v1/auth/oidc/callback
	RedirectURL string   `yaml:"redirect_url"`
	Scopes      []string `yaml:"scopes"`
	ButtonLabel string   `yaml:"button_label"`
	// UsernameClaim lists claim names tried in order (comma-separated); a
	// verified email works with providers that omit preferred_username.
	UsernameClaim string `yaml:"username_claim"`
	GroupsClaim   string `yaml:"groups_claim"`
	// AllowedGroups, when non-empty, restricts sign-in to members of at
	// least one of these provider groups.
	AllowedGroups []string `yaml:"allowed_groups"`
	// AdminGroups, when non-empty, makes members administrators and
	// everyone else a regular user, re-evaluated at every sign-in.
	AdminGroups []string `yaml:"admin_groups"`
	// GroupMapping maps provider group names to Kervan group names. Without
	// an entry, a provider group applies when a Kervan group has the same
	// name.
	GroupMapping map[string]string `yaml:"group_mapping"`
	// AutoCreate provisions accounts on first sign-in; when false, an admin
	// must create the oidc account first.
	AutoCreate bool `yaml:"auto_create"`
	// HomeDir for auto-created users; "{username}" is replaced.
	HomeDir string `yaml:"home_dir"`
}

type DebugConfig struct {
	Enabled     bool   `yaml:"enabled"`
	BindAddress string `yaml:"bind_address"`
	Port        int    `yaml:"port"`
	Pprof       bool   `yaml:"pprof"`
}

type AuthConfig struct {
	DefaultProvider    string     `yaml:"default_provider"`
	PasswordHash       string     `yaml:"password_hash"`
	MinPasswordLength  int        `yaml:"min_password_length"`
	RequireSpecialChar bool       `yaml:"require_special_char"`
	LDAP               LDAPConfig `yaml:"ldap"`
}

type LDAPConfig struct {
	Enabled           bool              `yaml:"enabled"`
	URL               string            `yaml:"url"`
	BindDN            string            `yaml:"bind_dn"`
	BindPassword      string            `yaml:"bind_password"`
	BaseDN            string            `yaml:"base_dn"`
	UserFilter        string            `yaml:"user_filter"`
	UsernameAttribute string            `yaml:"username_attribute"`
	EmailAttribute    string            `yaml:"email_attribute"`
	GroupAttribute    string            `yaml:"group_attribute"`
	GroupMapping      map[string]string `yaml:"group_mapping"`
	DefaultHomeDir    string            `yaml:"default_home_dir"`
	CacheTTL          time.Duration     `yaml:"cache_ttl"`
	PoolSize          int               `yaml:"connection_pool_size"`
	TLSSkipVerify     bool              `yaml:"tls_skip_verify"`
}

type StorageConfig struct {
	DefaultBackend string                   `yaml:"default_backend"`
	Backends       map[string]BackendConfig `yaml:"backends"`
}

type BackendConfig struct {
	Type    string            `yaml:"type"`
	Options map[string]string `yaml:"options"`
}

type QuotaConfig struct {
	Enabled           bool          `yaml:"enabled"`
	DefaultMaxStorage int64         `yaml:"default_max_storage"`
	DefaultMaxFiles   int64         `yaml:"default_max_files"`
	CheckInterval     time.Duration `yaml:"check_interval"`
}

type AuditConfig struct {
	Enabled   bool                 `yaml:"enabled"`
	Outputs   []AuditOutput        `yaml:"outputs"`
	Integrity AuditIntegrityConfig `yaml:"integrity"`
}

// AuditIntegrityConfig makes file outputs tamper-evident with an HMAC chain;
// check a log with "kervan audit verify".
type AuditIntegrityConfig struct {
	Enabled bool `yaml:"enabled"`
	// KeyFile holds the hex HMAC key; default <data_dir>/audit.key, created
	// on first start. Back it up: without it the log cannot be verified.
	KeyFile string `yaml:"key_file"`
}

// AuditKeyPath resolves the integrity key location.
func (c *Config) AuditKeyPath() string {
	if p := strings.TrimSpace(c.Audit.Integrity.KeyFile); p != "" {
		return p
	}
	return filepath.Join(c.Server.DataDir, "audit.key")
}

type AuditOutput struct {
	Type          string            `yaml:"type"`
	Path          string            `yaml:"path"`
	URL           string            `yaml:"url"`
	Method        string            `yaml:"method"`
	Headers       map[string]string `yaml:"headers"`
	BatchSize     int               `yaml:"batch_size"`
	FlushInterval time.Duration     `yaml:"flush_interval"`
	RetryCount    int               `yaml:"retry_count"`
	// MaxSizeMB rotates file outputs at this size (0 = 100 MB, -1 = never)
	// and MaxBackups bounds the rotated files kept (0 = 10, -1 = all).
	MaxSizeMB  int `yaml:"max_size_mb"`
	MaxBackups int `yaml:"max_backups"`
	// Format and Facility apply to syslog outputs (url: udp://, tcp://,
	// tls://, unix:// or unixgram://).
	Format   string `yaml:"format"`
	Facility string `yaml:"facility"`
}

type SecurityConfig struct {
	AllowedIPs []string         `yaml:"allowed_ips"`
	DeniedIPs  []string         `yaml:"denied_ips"`
	BruteForce BruteForceConfig `yaml:"brute_force"`
}

type BruteForceConfig struct {
	Enabled         bool          `yaml:"enabled"`
	MaxAttempts     int           `yaml:"max_attempts"`
	LockoutDuration time.Duration `yaml:"lockout_duration"`
	IPBanThreshold  int           `yaml:"ip_ban_threshold"`
	IPBanDuration   time.Duration `yaml:"ip_ban_duration"`
	WhitelistIPs    []string      `yaml:"whitelist_ips"`
}

type MCPConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Transport string `yaml:"transport"`
}

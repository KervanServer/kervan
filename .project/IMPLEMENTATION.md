# KERVAN — IMPLEMENTATION GUIDE

## v1.0 — Technical Implementation Reference

---

## 1. PROJECT BOOTSTRAP

### 1.1 Module Initialization

```bash
mkdir kervan && cd kervan
go mod init github.com/kervanserver/kervan

# Only allowed external dependencies
go get golang.org/x/crypto@latest
go get golang.org/x/sys@latest
```

### 1.2 Build System

```makefile
# Makefile
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
  -X github.com/kervanserver/kervan/internal/build.Version=$(VERSION) \
  -X github.com/kervanserver/kervan/internal/build.Commit=$(COMMIT) \
  -X github.com/kervanserver/kervan/internal/build.Date=$(DATE)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build webui clean test release

build: webui
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -trimpath -o bin/kervan ./cmd/kervan

webui:
	cd webui && npm ci && npm run build
	rm -rf internal/webui/dist
	cp -r webui/dist internal/webui/dist

release: webui
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "Building $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -trimpath \
			-o bin/kervan-$$os-$$arch$$ext ./cmd/kervan; \
	done

test:
	go test -race -cover ./...

clean:
	rm -rf bin/ internal/webui/dist
```

### 1.3 Build Info Package

```go
// internal/build/build.go
package build

var (
    Version = "dev"
    Commit  = "unknown"
    Date    = "unknown"
)

func Info() string {
    return "Kervan " + Version + " (" + Commit + ") built " + Date
}
```

---

## 2. CONFIGURATION SYSTEM

### 2.1 Config Struct

```go
// internal/config/config.go
package config

import (
    "os"
    "time"
)

type Config struct {
    Server  ServerConfig  `yaml:"server"`
    FTP     FTPConfig     `yaml:"ftp"`
    FTPS    FTPSConfig    `yaml:"ftps"`
    SFTP    SFTPConfig    `yaml:"sftp"`
    SCP     SCPConfig     `yaml:"scp"`
    WebUI   WebUIConfig   `yaml:"webui"`
    Auth    AuthConfig    `yaml:"auth"`
    Storage StorageConfig `yaml:"storage"`
    Quota   QuotaConfig   `yaml:"quota"`
    Audit   AuditConfig   `yaml:"audit"`
    Security SecurityConfig `yaml:"security"`
    MCP     MCPConfig     `yaml:"mcp"`
}

type ServerConfig struct {
    Name                   string        `yaml:"name"`
    ListenAddress          string        `yaml:"listen_address"`
    PIDFile                string        `yaml:"pid_file"`
    DataDir                string        `yaml:"data_dir"`
    LogLevel               string        `yaml:"log_level"`
    LogFormat              string        `yaml:"log_format"`
    LogFile                string        `yaml:"log_file"`
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
}

type FTPSConfig struct {
    Enabled       bool       `yaml:"enabled"`
    Mode          string     `yaml:"mode"`
    ImplicitPort  int        `yaml:"implicit_port"`
    MinTLSVersion string     `yaml:"min_tls_version"`
    MaxTLSVersion string     `yaml:"max_tls_version"`
    CertFile      string     `yaml:"cert_file"`
    KeyFile       string     `yaml:"key_file"`
    ClientAuth    string     `yaml:"client_auth"`
    ClientCAFile  string     `yaml:"client_ca_file"`
    CipherSuites  []string   `yaml:"cipher_suites"`
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
    MaxPacketSize     uint32        `yaml:"max_packet_size"`
    DisableShell      bool          `yaml:"disable_shell"`
}

type SCPConfig struct {
    Enabled bool `yaml:"enabled"`
}

type WebUIConfig struct {
    Enabled        bool          `yaml:"enabled"`
    Port           int           `yaml:"port"`
    TLS            bool          `yaml:"tls"`
    BindAddress    string        `yaml:"bind_address"`
    AdminUsername  string        `yaml:"admin_username"`
    AdminPassword  string        `yaml:"admin_password"`
    SessionTimeout time.Duration `yaml:"session_timeout"`
    TOTPEnabled    bool          `yaml:"totp_enabled"`
    CORSOrigins    []string      `yaml:"cors_origins"`
}

type AuthConfig struct {
    DefaultProvider   string     `yaml:"default_provider"`
    PasswordHash      string     `yaml:"password_hash"`
    MinPasswordLength int        `yaml:"min_password_length"`
    RequireSpecialChar bool      `yaml:"require_special_char"`
    LDAP              LDAPConfig `yaml:"ldap"`
}

type LDAPConfig struct {
    Enabled           bool              `yaml:"enabled"`
    URL               string            `yaml:"url"`
    BindDN            string            `yaml:"bind_dn"`
    BindPassword      string            `yaml:"bind_password"`
    BaseDN            string            `yaml:"base_dn"`
    UserFilter        string            `yaml:"user_filter"`
    GroupFilter       string            `yaml:"group_filter"`
    UsernameAttribute string            `yaml:"username_attribute"`
    EmailAttribute    string            `yaml:"email_attribute"`
    GroupAttribute    string            `yaml:"group_attribute"`
    GroupMapping      map[string]string `yaml:"group_mapping"`
    DefaultHomeDir    string            `yaml:"default_home_dir"`
    DefaultPermissions UserPermissions  `yaml:"default_permissions"`
    CacheTTL          time.Duration     `yaml:"cache_ttl"`
    PoolSize          int               `yaml:"connection_pool_size"`
    TLSSkipVerify     bool              `yaml:"tls_skip_verify"`
}

type StorageConfig struct {
    DefaultBackend string                    `yaml:"default_backend"`
    Backends       map[string]BackendConfig  `yaml:"backends"`
}

type BackendConfig struct {
    Type    string            `yaml:"type"`
    Options map[string]string `yaml:"options"`
}

type QuotaConfig struct {
    Enabled          bool          `yaml:"enabled"`
    DefaultMaxStorage int64        `yaml:"default_max_storage"`
    DefaultMaxFiles  int64         `yaml:"default_max_files"`
    CheckInterval    time.Duration `yaml:"check_interval"`
}

type AuditConfig struct {
    Enabled bool           `yaml:"enabled"`
    Outputs []AuditOutput  `yaml:"outputs"`
}

type AuditOutput struct {
    Type     string            `yaml:"type"`
    Path     string            `yaml:"path"`
    Rotation RotationConfig    `yaml:"rotation"`
    Network  string            `yaml:"network"`
    Address  string            `yaml:"address"`
    Facility string            `yaml:"facility"`
    Format   string            `yaml:"format"`
    URL      string            `yaml:"url"`
    Method   string            `yaml:"method"`
    Headers  map[string]string `yaml:"headers"`
    BatchSize int              `yaml:"batch_size"`
    FlushInterval time.Duration `yaml:"flush_interval"`
    RetryCount int             `yaml:"retry_count"`
    Retention time.Duration    `yaml:"retention"`
    MaxRecords int64           `yaml:"max_records"`
}

type RotationConfig struct {
    MaxSize    string `yaml:"max_size"`
    MaxAge     string `yaml:"max_age"`
    MaxBackups int    `yaml:"max_backups"`
    Compress   bool   `yaml:"compress"`
}

type SecurityConfig struct {
    AllowedIPs  []string         `yaml:"allowed_ips"`
    DeniedIPs   []string         `yaml:"denied_ips"`
    GeoBlocking GeoBlockConfig   `yaml:"geo_blocking"`
    BruteForce  BruteForceConfig `yaml:"brute_force"`
}

type GeoBlockConfig struct {
    Enabled   bool     `yaml:"enabled"`
    Mode      string   `yaml:"mode"`
    Countries []string `yaml:"countries"`
    GeoIPDB   string   `yaml:"geoip_db"`
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
```

### 2.2 YAML Parser

Use `gopkg.in/yaml.v3` for config parsing (permitted dependency), but wrap it:

```go
// internal/config/loader.go
package config

import (
    "os"
    "strings"

    "gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    // Environment variable expansion: ${VAR_NAME} → os.Getenv("VAR_NAME")
    expanded := expandEnvVars(string(data))

    cfg := DefaultConfig()
    if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
        return nil, err
    }

    if err := cfg.Validate(); err != nil {
        return nil, err
    }

    return cfg, nil
}

func expandEnvVars(s string) string {
    return os.Expand(s, func(key string) string {
        if val, ok := os.LookupEnv(key); ok {
            return val
        }
        return "${" + key + "}"
    })
}

// OverlayEnv applies KERVAN_SECTION_KEY env vars on top of loaded config
func (c *Config) OverlayEnv() {
    for _, env := range os.Environ() {
        if !strings.HasPrefix(env, "KERVAN_") {
            continue
        }
        parts := strings.SplitN(env, "=", 2)
        if len(parts) != 2 {
            continue
        }
        key := strings.TrimPrefix(parts[0], "KERVAN_")
        val := parts[1]
        c.applyEnvOverride(key, val)
    }
}
```

### 2.3 Defaults

```go
// internal/config/defaults.go
package config

import "time"

func DefaultConfig() *Config {
    return &Config{
        Server: ServerConfig{
            Name:                    "Kervan File Server",
            ListenAddress:           "0.0.0.0",
            DataDir:                 "/var/lib/kervan",
            LogLevel:                "info",
            LogFormat:               "json",
            GracefulShutdownTimeout: 30 * time.Second,
        },
        FTP: FTPConfig{
            Enabled:          true,
            Port:             2121,
            Banner:           "Welcome to Kervan File Server",
            PassivePortRange: "50000-50100",
            ActiveMode:       true,
            ASCIITransfer:    true,
            MaxConnections:   500,
            IdleTimeout:      300 * time.Second,
            TransferTimeout:  3600 * time.Second,
        },
        FTPS: FTPSConfig{
            Enabled:       true,
            Mode:          "both",
            ImplicitPort:  990,
            MinTLSVersion: "1.2",
            MaxTLSVersion: "1.3",
            ClientAuth:    "none",
        },
        SFTP: SFTPConfig{
            Enabled:           true,
            Port:              2222,
            HostKeyDir:        "/var/lib/kervan/host_keys",
            HostKeyAlgorithms: []string{"ed25519", "rsa"},
            MaxConnections:    500,
            IdleTimeout:       300 * time.Second,
            MaxPacketSize:     34000,
            DisableShell:      true,
        },
        SCP: SCPConfig{Enabled: true},
        WebUI: WebUIConfig{
            Enabled:        true,
            Port:           8443,
            TLS:            true,
            BindAddress:    "0.0.0.0",
            AdminUsername:  "admin",
            SessionTimeout: 24 * time.Hour,
        },
        Auth: AuthConfig{
            DefaultProvider:    "local",
            PasswordHash:       "argon2id",
            MinPasswordLength:  8,
        },
        Storage: StorageConfig{
            DefaultBackend: "local",
        },
        Quota: QuotaConfig{
            Enabled:           true,
            DefaultMaxStorage: 1 << 30, // 1 GB
            DefaultMaxFiles:   100000,
            CheckInterval:     60 * time.Second,
        },
        Audit: AuditConfig{Enabled: true},
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
```

### 2.4 Validation

```go
// internal/config/validate.go
package config

import (
    "fmt"
    "net"
    "strconv"
    "strings"
)

func (c *Config) Validate() error {
    var errs []string

    // Server
    if c.Server.DataDir == "" {
        errs = append(errs, "server.data_dir is required")
    }

    // FTP
    if c.FTP.Enabled {
        if c.FTP.Port < 1 || c.FTP.Port > 65535 {
            errs = append(errs, "ftp.port must be 1-65535")
        }
        if err := validatePortRange(c.FTP.PassivePortRange); err != nil {
            errs = append(errs, "ftp.passive_port_range: "+err.Error())
        }
    }

    // FTPS
    if c.FTPS.Enabled {
        if c.FTPS.Mode != "explicit" && c.FTPS.Mode != "implicit" && c.FTPS.Mode != "both" {
            errs = append(errs, "ftps.mode must be explicit|implicit|both")
        }
        if !c.FTPS.AutoCert.Enabled && c.FTPS.CertFile == "" {
            errs = append(errs, "ftps requires cert_file or auto_cert.enabled")
        }
    }

    // SFTP
    if c.SFTP.Enabled {
        if c.SFTP.Port < 1 || c.SFTP.Port > 65535 {
            errs = append(errs, "sftp.port must be 1-65535")
        }
    }

    // Auth
    if c.Auth.PasswordHash != "argon2id" && c.Auth.PasswordHash != "bcrypt" {
        errs = append(errs, "auth.password_hash must be argon2id|bcrypt")
    }
    if c.Auth.MinPasswordLength < 4 {
        errs = append(errs, "auth.min_password_length must be >= 4")
    }

    // Security — validate CIDR notation
    for _, ip := range c.Security.AllowedIPs {
        if _, _, err := net.ParseCIDR(ip); err != nil {
            if net.ParseIP(ip) == nil {
                errs = append(errs, "security.allowed_ips: invalid IP/CIDR: "+ip)
            }
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
    }
    return nil
}

func validatePortRange(s string) error {
    parts := strings.SplitN(s, "-", 2)
    if len(parts) != 2 {
        return fmt.Errorf("expected format: start-end")
    }
    start, err := strconv.Atoi(parts[0])
    if err != nil {
        return fmt.Errorf("invalid start port: %w", err)
    }
    end, err := strconv.Atoi(parts[1])
    if err != nil {
        return fmt.Errorf("invalid end port: %w", err)
    }
    if start < 1024 || end > 65535 || start > end {
        return fmt.Errorf("ports must be 1024-65535 and start <= end")
    }
    return nil
}
```

### 2.5 Hot Reload

```go
// internal/config/reload.go
package config

import (
    "os"
    "os/signal"
    "sync"
    "sync/atomic"
    "syscall"
)

type LiveConfig struct {
    current atomic.Pointer[Config]
    path    string
    mu      sync.Mutex
    onReload []func(*Config)
}

func NewLiveConfig(path string) (*LiveConfig, error) {
    cfg, err := Load(path)
    if err != nil {
        return nil, err
    }

    lc := &LiveConfig{path: path}
    lc.current.Store(cfg)
    return lc, nil
}

func (lc *LiveConfig) Get() *Config {
    return lc.current.Load()
}

func (lc *LiveConfig) OnReload(fn func(*Config)) {
    lc.mu.Lock()
    defer lc.mu.Unlock()
    lc.onReload = append(lc.onReload, fn)
}

func (lc *LiveConfig) Reload() error {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    cfg, err := Load(lc.path)
    if err != nil {
        return err
    }

    lc.current.Store(cfg)

    for _, fn := range lc.onReload {
        fn(cfg)
    }
    return nil
}

func (lc *LiveConfig) WatchSignals() {
    ch := make(chan os.Signal, 1)
    signal.Notify(ch, syscall.SIGHUP)
    go func() {
        for range ch {
            if err := lc.Reload(); err != nil {
                // Log error but don't crash
                _ = err
            }
        }
    }()
}
```

---

## 3. COBALTDB INTEGRATION

### 3.1 Store Layer

CobaltDB is Kervan's embedded database for all persistent metadata.

```go
// internal/cobalt/store.go
package cobalt

import (
    "encoding/json"
    "path/filepath"
    "sync"
    "time"
)

// Store wraps CobaltDB with domain-specific collections
type Store struct {
    db     *DB          // CobaltDB instance
    mu     sync.RWMutex
    path   string
}

// Collections (logical tables)
const (
    CollUsers       = "users"
    CollGroups      = "groups"
    CollSessions    = "sessions"
    CollAudit       = "audit"
    CollAPIKeys     = "api_keys"
    CollFileMeta    = "file_meta"
    CollConfig      = "config"
    CollShareLinks  = "share_links"
)

func Open(dataDir string) (*Store, error) {
    dbPath := filepath.Join(dataDir, "kervan.db")
    db, err := OpenDB(dbPath, DefaultOptions())
    if err != nil {
        return nil, err
    }
    return &Store{db: db, path: dbPath}, nil
}

func (s *Store) Close() error {
    return s.db.Close()
}

// Generic CRUD operations with JSON serialization

func (s *Store) Put(collection, key string, value any) error {
    data, err := json.Marshal(value)
    if err != nil {
        return err
    }
    return s.db.Put([]byte(collection+":"+key), data)
}

func (s *Store) Get(collection, key string, dest any) error {
    data, err := s.db.Get([]byte(collection + ":" + key))
    if err != nil {
        return err
    }
    return json.Unmarshal(data, dest)
}

func (s *Store) Delete(collection, key string) error {
    return s.db.Delete([]byte(collection + ":" + key))
}

func (s *Store) List(collection string, dest any) error {
    prefix := []byte(collection + ":")
    entries, err := s.db.PrefixScan(prefix)
    if err != nil {
        return err
    }
    // Deserialize into slice via reflection
    return unmarshalEntries(entries, dest)
}

func (s *Store) Query(collection string, filter func([]byte) bool) ([][]byte, error) {
    prefix := []byte(collection + ":")
    var results [][]byte
    err := s.db.PrefixScan(prefix, func(key, value []byte) bool {
        if filter(value) {
            results = append(results, value)
        }
        return true // continue scanning
    })
    return results, err
}
```

### 3.2 User Repository

```go
// internal/auth/user_repo.go
package auth

import (
    "fmt"
    "time"

    "github.com/kervanserver/kervan/internal/cobalt"
)

type UserRepository struct {
    store *cobalt.Store
}

func NewUserRepository(store *cobalt.Store) *UserRepository {
    return &UserRepository{store: store}
}

func (r *UserRepository) Create(user *User) error {
    if user.ID == "" {
        user.ID = generateULID()
    }
    user.CreatedAt = time.Now().UTC()
    user.UpdatedAt = user.CreatedAt

    // Check unique username
    existing, _ := r.GetByUsername(user.Username)
    if existing != nil {
        return fmt.Errorf("username %q already exists", user.Username)
    }

    // Store by ID (primary key)
    if err := r.store.Put(cobalt.CollUsers, user.ID, user); err != nil {
        return err
    }
    // Store username → ID index
    return r.store.Put(cobalt.CollUsers+":idx:username", user.Username, user.ID)
}

func (r *UserRepository) GetByID(id string) (*User, error) {
    var user User
    if err := r.store.Get(cobalt.CollUsers, id, &user); err != nil {
        return nil, err
    }
    return &user, nil
}

func (r *UserRepository) GetByUsername(username string) (*User, error) {
    var id string
    if err := r.store.Get(cobalt.CollUsers+":idx:username", username, &id); err != nil {
        return nil, err
    }
    return r.GetByID(id)
}

func (r *UserRepository) Update(user *User) error {
    user.UpdatedAt = time.Now().UTC()
    return r.store.Put(cobalt.CollUsers, user.ID, user)
}

func (r *UserRepository) Delete(id string) error {
    user, err := r.GetByID(id)
    if err != nil {
        return err
    }
    // Remove index
    _ = r.store.Delete(cobalt.CollUsers+":idx:username", user.Username)
    return r.store.Delete(cobalt.CollUsers, id)
}

func (r *UserRepository) List() ([]*User, error) {
    var users []*User
    if err := r.store.List(cobalt.CollUsers, &users); err != nil {
        return nil, err
    }
    return users, nil
}

func (r *UserRepository) UpdateLastLogin(id string) error {
    user, err := r.GetByID(id)
    if err != nil {
        return err
    }
    now := time.Now().UTC()
    user.LastLoginAt = &now
    return r.Update(user)
}
```

---

## 4. VIRTUAL FILESYSTEM (VFS)

### 4.1 Core Interfaces

```go
// internal/vfs/vfs.go
package vfs

import (
    "io"
    "io/fs"
    "os"
    "time"
)

// FileSystem is the core VFS interface implemented by all backends
type FileSystem interface {
    Open(name string, flags int, perm os.FileMode) (File, error)
    Stat(name string) (os.FileInfo, error)
    Lstat(name string) (os.FileInfo, error)
    Rename(oldname, newname string) error
    Remove(name string) error
    RemoveAll(name string) error
    Mkdir(name string, perm os.FileMode) error
    MkdirAll(path string, perm os.FileMode) error
    ReadDir(name string) ([]fs.DirEntry, error)
    Symlink(oldname, newname string) error
    Readlink(name string) (string, error)
    Chmod(name string, mode os.FileMode) error
    Chown(name string, uid, gid int) error
    Chtimes(name string, atime, mtime time.Time) error
    Statvfs(path string) (*StatVFS, error)
}

// File represents an open file handle
type File interface {
    io.Reader
    io.ReaderAt
    io.Writer
    io.WriterAt
    io.Seeker
    io.Closer
    Stat() (os.FileInfo, error)
    Sync() error
    Truncate(size int64) error
    ReadDir(n int) ([]fs.DirEntry, error)
    Name() string
}

// StatVFS represents filesystem statistics (statvfs equivalent)
type StatVFS struct {
    BlockSize   uint64 // Filesystem block size
    TotalBlocks uint64 // Total blocks
    FreeBlocks  uint64 // Free blocks
    AvailBlocks uint64 // Available blocks (non-root)
    TotalFiles  uint64 // Total file nodes
    FreeFiles   uint64 // Free file nodes
    NameMaxLen  uint64 // Max filename length
}

// FileInfo wraps os.FileInfo with additional metadata
type FileInfo struct {
    name    string
    size    int64
    mode    os.FileMode
    modTime time.Time
    isDir   bool
    owner   string
    group   string
    linkTarget string
}

func (fi *FileInfo) Name() string        { return fi.name }
func (fi *FileInfo) Size() int64         { return fi.size }
func (fi *FileInfo) Mode() os.FileMode   { return fi.mode }
func (fi *FileInfo) ModTime() time.Time  { return fi.modTime }
func (fi *FileInfo) IsDir() bool         { return fi.isDir }
func (fi *FileInfo) Sys() any            { return nil }
```

### 4.2 Path Resolver (Chroot)

```go
// internal/vfs/resolver.go
package vfs

import (
    "errors"
    "path"
    "strings"
)

var (
    ErrPathEscape    = errors.New("path escapes virtual root")
    ErrPathTooDeep   = errors.New("path exceeds maximum depth")
    ErrForbiddenChar = errors.New("path contains forbidden characters")
)

const MaxPathDepth = 256

// Resolver handles virtual path → physical path mapping within chroot
type Resolver struct {
    maxDepth       int
    forbiddenChars []rune
}

func NewResolver() *Resolver {
    return &Resolver{
        maxDepth:       MaxPathDepth,
        forbiddenChars: []rune{0x00}, // null byte
    }
}

// Resolve cleans and validates a virtual path, ensuring it stays within root
func (r *Resolver) Resolve(virtualPath string) (string, error) {
    // Clean the path
    cleaned := path.Clean("/" + virtualPath)

    // Check for forbidden characters
    for _, ch := range cleaned {
        for _, fc := range r.forbiddenChars {
            if ch == fc {
                return "", ErrForbiddenChar
            }
        }
    }

    // Check depth
    parts := strings.Split(cleaned, "/")
    depth := 0
    for _, p := range parts {
        if p != "" {
            depth++
        }
    }
    if depth > r.maxDepth {
        return "", ErrPathTooDeep
    }

    // Ensure no escape (path.Clean handles ../ but verify)
    if !strings.HasPrefix(cleaned, "/") {
        return "", ErrPathEscape
    }

    return cleaned, nil
}

// ResolvePair resolves two paths (for rename operations) ensuring both are safe
func (r *Resolver) ResolvePair(from, to string) (string, string, error) {
    cleanFrom, err := r.Resolve(from)
    if err != nil {
        return "", "", err
    }
    cleanTo, err := r.Resolve(to)
    if err != nil {
        return "", "", err
    }
    return cleanFrom, cleanTo, nil
}
```

### 4.3 Mount Table

```go
// internal/vfs/mount.go
package vfs

import (
    "path"
    "sort"
    "strings"
    "sync"
)

// MountEntry maps a virtual path to a storage backend
type MountEntry struct {
    Path     string            // Virtual mount point (e.g., "/archive")
    Backend  FileSystem        // Storage backend implementation
    ReadOnly bool
    Options  map[string]string
}

// MountTable manages the virtual namespace for a user session
type MountTable struct {
    mounts []MountEntry // Sorted by path length (longest first)
    mu     sync.RWMutex
}

func NewMountTable() *MountTable {
    return &MountTable{}
}

// Mount adds a filesystem at the given virtual path
func (mt *MountTable) Mount(virtualPath string, backend FileSystem, readOnly bool) {
    mt.mu.Lock()
    defer mt.mu.Unlock()

    entry := MountEntry{
        Path:     path.Clean("/" + virtualPath),
        Backend:  backend,
        ReadOnly: readOnly,
    }

    mt.mounts = append(mt.mounts, entry)

    // Sort by path length descending (longest prefix match first)
    sort.Slice(mt.mounts, func(i, j int) bool {
        return len(mt.mounts[i].Path) > len(mt.mounts[j].Path)
    })
}

// Lookup finds the backend and relative path for a virtual path
// Returns (backend, relative_path, read_only, error)
func (mt *MountTable) Lookup(virtualPath string) (FileSystem, string, bool, error) {
    mt.mu.RLock()
    defer mt.mu.RUnlock()

    cleaned := path.Clean("/" + virtualPath)

    for _, entry := range mt.mounts {
        if cleaned == entry.Path || strings.HasPrefix(cleaned, entry.Path+"/") {
            // Calculate relative path within the backend
            rel := strings.TrimPrefix(cleaned, entry.Path)
            if rel == "" {
                rel = "/"
            }
            return entry.Backend, rel, entry.ReadOnly, nil
        }
    }

    return nil, "", false, ErrNoMount
}

// ListMountPoints returns virtual paths visible at the given directory level
func (mt *MountTable) ListMountPoints(dir string) []string {
    mt.mu.RLock()
    defer mt.mu.RUnlock()

    dir = path.Clean("/" + dir)
    var points []string

    for _, entry := range mt.mounts {
        parent := path.Dir(entry.Path)
        if parent == dir && entry.Path != dir {
            points = append(points, path.Base(entry.Path))
        }
    }
    return points
}
```

### 4.4 User VFS (Composite)

```go
// internal/vfs/user_vfs.go
package vfs

import (
    "io/fs"
    "os"
    "time"
)

// UserVFS provides the complete virtual filesystem for a user session
// It combines the mount table, path resolver, and permission checks
type UserVFS struct {
    mounts     *MountTable
    resolver   *Resolver
    username   string
    permissions *UserPermissions
    quota      *QuotaTracker
}

func NewUserVFS(username string, mounts *MountTable, perms *UserPermissions, quota *QuotaTracker) *UserVFS {
    return &UserVFS{
        mounts:      mounts,
        resolver:    NewResolver(),
        username:    username,
        permissions: perms,
        quota:       quota,
    }
}

func (u *UserVFS) Open(name string, flags int, perm os.FileMode) (File, error) {
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return nil, err
    }

    backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
    if err != nil {
        return nil, err
    }

    // Check read-only
    if readOnly && (flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND)) != 0 {
        return nil, os.ErrPermission
    }

    // Check write permission
    isWrite := flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0
    if isWrite && !u.permissions.Upload {
        return nil, os.ErrPermission
    }

    // Check download permission
    isRead := flags == os.O_RDONLY || flags&os.O_RDWR != 0
    if isRead && !u.permissions.Download {
        return nil, os.ErrPermission
    }

    // Check file extension restrictions
    if isWrite {
        if err := u.checkExtension(name); err != nil {
            return nil, err
        }
    }

    // Open via backend
    file, err := backend.Open(relPath, flags, perm)
    if err != nil {
        return nil, err
    }

    // Wrap with quota tracking for writes
    if isWrite && u.quota != nil {
        return newQuotaFile(file, u.quota), nil
    }

    return file, nil
}

func (u *UserVFS) Stat(name string) (os.FileInfo, error) {
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return nil, err
    }
    backend, relPath, _, err := u.mounts.Lookup(resolved)
    if err != nil {
        return nil, err
    }
    return backend.Stat(relPath)
}

func (u *UserVFS) ReadDir(name string) ([]fs.DirEntry, error) {
    if !u.permissions.ListDir {
        return nil, os.ErrPermission
    }

    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return nil, err
    }

    backend, relPath, _, err := u.mounts.Lookup(resolved)
    if err != nil {
        return nil, err
    }

    entries, err := backend.ReadDir(relPath)
    if err != nil {
        return nil, err
    }

    // Merge mount points visible at this level
    mountPoints := u.mounts.ListMountPoints(resolved)
    for _, mp := range mountPoints {
        entries = append(entries, newDirEntry(mp, true))
    }

    return entries, nil
}

func (u *UserVFS) Remove(name string) error {
    if !u.permissions.Delete {
        return os.ErrPermission
    }
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return err
    }
    backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
    if err != nil {
        return err
    }
    if readOnly {
        return os.ErrPermission
    }
    return backend.Remove(relPath)
}

func (u *UserVFS) Rename(oldname, newname string) error {
    if !u.permissions.Rename {
        return os.ErrPermission
    }
    oldResolved, err := u.resolver.Resolve(oldname)
    if err != nil {
        return err
    }
    newResolved, err := u.resolver.Resolve(newname)
    if err != nil {
        return err
    }

    oldBackend, oldRel, oldRO, err := u.mounts.Lookup(oldResolved)
    if err != nil {
        return err
    }
    newBackend, newRel, newRO, err := u.mounts.Lookup(newResolved)
    if err != nil {
        return err
    }

    if oldRO || newRO {
        return os.ErrPermission
    }

    // Cross-mount rename: copy + delete
    if oldBackend != newBackend {
        return u.crossMountRename(oldBackend, oldRel, newBackend, newRel)
    }

    return oldBackend.Rename(oldRel, newRel)
}

func (u *UserVFS) Mkdir(name string, perm os.FileMode) error {
    if !u.permissions.CreateDir {
        return os.ErrPermission
    }
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return err
    }
    backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
    if err != nil {
        return err
    }
    if readOnly {
        return os.ErrPermission
    }
    return backend.Mkdir(relPath, perm)
}

func (u *UserVFS) Chmod(name string, mode os.FileMode) error {
    if !u.permissions.Chmod {
        return os.ErrPermission
    }
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return err
    }
    backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
    if err != nil {
        return err
    }
    if readOnly {
        return os.ErrPermission
    }
    return backend.Chmod(relPath, mode)
}

func (u *UserVFS) Chtimes(name string, atime, mtime time.Time) error {
    resolved, err := u.resolver.Resolve(name)
    if err != nil {
        return err
    }
    backend, relPath, readOnly, err := u.mounts.Lookup(resolved)
    if err != nil {
        return err
    }
    if readOnly {
        return os.ErrPermission
    }
    return backend.Chtimes(relPath, atime, mtime)
}

func (u *UserVFS) checkExtension(name string) error {
    ext := extensionOf(name)
    if len(u.permissions.AllowedExts) > 0 {
        if !contains(u.permissions.AllowedExts, ext) {
            return ErrForbiddenExtension
        }
    }
    if contains(u.permissions.DeniedExts, ext) {
        return ErrForbiddenExtension
    }
    return nil
}
```

### 4.5 Local Filesystem Backend

```go
// internal/storage/local/local.go
package local

import (
    "io/fs"
    "os"
    "path/filepath"
    "time"

    "github.com/kervanserver/kervan/internal/vfs"
)

type Backend struct {
    root       string
    filePerms  os.FileMode
    dirPerms   os.FileMode
    syncWrites bool
    uid        int
    gid        int
}

type Options struct {
    Root           string
    CreateRoot     bool
    FilePermissions os.FileMode
    DirPermissions  os.FileMode
    SyncWrites     bool
    UID            int
    GID            int
}

func New(opts Options) (*Backend, error) {
    root, err := filepath.Abs(opts.Root)
    if err != nil {
        return nil, err
    }

    if opts.CreateRoot {
        if err := os.MkdirAll(root, opts.DirPermissions); err != nil {
            return nil, err
        }
    }

    // Verify root exists
    info, err := os.Stat(root)
    if err != nil {
        return nil, err
    }
    if !info.IsDir() {
        return nil, ErrRootNotDirectory
    }

    return &Backend{
        root:       root,
        filePerms:  opts.FilePermissions,
        dirPerms:   opts.DirPermissions,
        syncWrites: opts.SyncWrites,
        uid:        opts.UID,
        gid:        opts.GID,
    }, nil
}

// physicalPath converts VFS path to physical path, preventing escape
func (b *Backend) physicalPath(name string) (string, error) {
    joined := filepath.Join(b.root, filepath.FromSlash(name))
    abs, err := filepath.Abs(joined)
    if err != nil {
        return "", err
    }
    // Ensure the resolved path is within root
    if !isSubPath(b.root, abs) {
        return "", vfs.ErrPathEscape
    }
    return abs, nil
}

func (b *Backend) Open(name string, flags int, perm os.FileMode) (vfs.File, error) {
    path, err := b.physicalPath(name)
    if err != nil {
        return nil, err
    }

    if perm == 0 {
        perm = b.filePerms
    }

    f, err := os.OpenFile(path, flags, perm)
    if err != nil {
        return nil, mapOSError(err)
    }

    return &localFile{
        File:       f,
        syncWrites: b.syncWrites,
    }, nil
}

func (b *Backend) Stat(name string) (os.FileInfo, error) {
    path, err := b.physicalPath(name)
    if err != nil {
        return nil, err
    }
    return os.Stat(path)
}

func (b *Backend) Lstat(name string) (os.FileInfo, error) {
    path, err := b.physicalPath(name)
    if err != nil {
        return nil, err
    }
    return os.Lstat(path)
}

func (b *Backend) Rename(oldname, newname string) error {
    oldPath, err := b.physicalPath(oldname)
    if err != nil {
        return err
    }
    newPath, err := b.physicalPath(newname)
    if err != nil {
        return err
    }
    return os.Rename(oldPath, newPath)
}

func (b *Backend) Remove(name string) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    return os.Remove(path)
}

func (b *Backend) RemoveAll(name string) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    // Safety: never remove root
    if path == b.root {
        return ErrCannotRemoveRoot
    }
    return os.RemoveAll(path)
}

func (b *Backend) Mkdir(name string, perm os.FileMode) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    if perm == 0 {
        perm = b.dirPerms
    }
    return os.Mkdir(path, perm)
}

func (b *Backend) MkdirAll(name string, perm os.FileMode) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    if perm == 0 {
        perm = b.dirPerms
    }
    return os.MkdirAll(path, perm)
}

func (b *Backend) ReadDir(name string) ([]fs.DirEntry, error) {
    path, err := b.physicalPath(name)
    if err != nil {
        return nil, err
    }
    return os.ReadDir(path)
}

func (b *Backend) Symlink(oldname, newname string) error {
    oldPath, err := b.physicalPath(oldname)
    if err != nil {
        return err
    }
    newPath, err := b.physicalPath(newname)
    if err != nil {
        return err
    }
    return os.Symlink(oldPath, newPath)
}

func (b *Backend) Readlink(name string) (string, error) {
    path, err := b.physicalPath(name)
    if err != nil {
        return "", err
    }
    target, err := os.Readlink(path)
    if err != nil {
        return "", err
    }
    // Convert back to VFS path
    rel, err := filepath.Rel(b.root, target)
    if err != nil {
        return "", err
    }
    return "/" + filepath.ToSlash(rel), nil
}

func (b *Backend) Chmod(name string, mode os.FileMode) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    return os.Chmod(path, mode)
}

func (b *Backend) Chown(name string, uid, gid int) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    return os.Chown(path, uid, gid)
}

func (b *Backend) Chtimes(name string, atime, mtime time.Time) error {
    path, err := b.physicalPath(name)
    if err != nil {
        return err
    }
    return os.Chtimes(path, atime, mtime)
}

func (b *Backend) Statvfs(path string) (*vfs.StatVFS, error) {
    physPath, err := b.physicalPath(path)
    if err != nil {
        return nil, err
    }
    return statFS(physPath) // OS-specific (linux: syscall.Statfs)
}

// localFile wraps os.File with optional sync-on-write
type localFile struct {
    *os.File
    syncWrites bool
}

func (f *localFile) Write(p []byte) (int, error) {
    n, err := f.File.Write(p)
    if err == nil && f.syncWrites {
        _ = f.File.Sync()
    }
    return n, err
}

func (f *localFile) ReadDir(n int) ([]fs.DirEntry, error) {
    return f.File.ReadDir(n)
}

// isSubPath checks if child is inside parent directory
func isSubPath(parent, child string) bool {
    rel, err := filepath.Rel(parent, child)
    if err != nil {
        return false
    }
    return !filepath.IsAbs(rel) && rel != ".." && rel[:3] != "../"
}
```

### 4.6 S3 Backend

```go
// internal/storage/s3/backend.go
package s3

import (
    "bytes"
    "context"
    "io"
    "io/fs"
    "os"
    "path"
    "strings"
    "time"

    "github.com/kervanserver/kervan/internal/vfs"
)

type Backend struct {
    client *Client         // From-scratch S3 client
    bucket string
    prefix string
}

type Options struct {
    Endpoint          string
    Region            string
    Bucket            string
    Prefix            string
    AccessKey         string
    SecretKey         string
    UsePathStyle      bool
    DisableSSL        bool
    MultipartThreshold int64
    MultipartChunkSize int64
    MaxRetries        int
    StorageClass      string
    SSE               string
}

func New(opts Options) (*Backend, error) {
    client, err := NewClient(ClientConfig{
        Endpoint:     opts.Endpoint,
        Region:       opts.Region,
        AccessKey:    opts.AccessKey,
        SecretKey:    opts.SecretKey,
        UsePathStyle: opts.UsePathStyle,
        DisableSSL:   opts.DisableSSL,
        MaxRetries:   opts.MaxRetries,
    })
    if err != nil {
        return nil, err
    }

    prefix := strings.TrimPrefix(opts.Prefix, "/")
    if prefix != "" && !strings.HasSuffix(prefix, "/") {
        prefix += "/"
    }

    return &Backend{
        client: client,
        bucket: opts.Bucket,
        prefix: prefix,
    }, nil
}

// s3Key converts VFS path to S3 object key
func (b *Backend) s3Key(name string) string {
    clean := strings.TrimPrefix(path.Clean(name), "/")
    return b.prefix + clean
}

func (b *Backend) Open(name string, flags int, perm os.FileMode) (vfs.File, error) {
    key := b.s3Key(name)

    // Read mode
    if flags == os.O_RDONLY {
        resp, err := b.client.GetObject(context.Background(), b.bucket, key)
        if err != nil {
            return nil, mapS3Error(err)
        }
        return newS3ReadFile(name, resp.Body, resp.ContentLength, resp.LastModified), nil
    }

    // Write mode — buffer to temp then upload on Close
    if flags&(os.O_WRONLY|os.O_CREATE|os.O_TRUNC) != 0 {
        return newS3WriteFile(name, b, key), nil
    }

    // Append mode
    if flags&os.O_APPEND != 0 {
        return newS3AppendFile(name, b, key), nil
    }

    return nil, os.ErrInvalid
}

func (b *Backend) Stat(name string) (os.FileInfo, error) {
    key := b.s3Key(name)

    // Check if it's a "directory" (has children)
    if key == b.prefix || strings.HasSuffix(key, "/") {
        return b.statDir(name, key)
    }

    // HeadObject
    resp, err := b.client.HeadObject(context.Background(), b.bucket, key)
    if err != nil {
        // Maybe it's a directory without trailing slash
        dirKey := key + "/"
        if _, listErr := b.client.ListObjectsV2(context.Background(), b.bucket, dirKey, "/", 1); listErr == nil {
            return &vfs.FileInfo{
                NameVal: path.Base(name),
                IsDir:   true,
                ModTime: time.Now(),
            }, nil
        }
        return nil, mapS3Error(err)
    }

    return &vfs.FileInfo{
        NameVal: path.Base(name),
        SizeVal: resp.ContentLength,
        ModTime: resp.LastModified,
        ModeVal: 0644,
    }, nil
}

func (b *Backend) ReadDir(name string) ([]fs.DirEntry, error) {
    prefix := b.s3Key(name)
    if !strings.HasSuffix(prefix, "/") {
        prefix += "/"
    }

    var entries []fs.DirEntry
    continuationToken := ""

    for {
        resp, err := b.client.ListObjectsV2WithToken(
            context.Background(), b.bucket, prefix, "/", 1000, continuationToken,
        )
        if err != nil {
            return nil, mapS3Error(err)
        }

        // Directories (common prefixes)
        for _, cp := range resp.CommonPrefixes {
            dirName := strings.TrimSuffix(strings.TrimPrefix(cp, prefix), "/")
            if dirName != "" {
                entries = append(entries, newDirEntry(dirName, true, 0, time.Time{}))
            }
        }

        // Files
        for _, obj := range resp.Contents {
            fileName := strings.TrimPrefix(obj.Key, prefix)
            if fileName == "" || strings.HasSuffix(fileName, "/") {
                continue // Skip directory markers
            }
            entries = append(entries, newDirEntry(fileName, false, obj.Size, obj.LastModified))
        }

        if !resp.IsTruncated {
            break
        }
        continuationToken = resp.NextContinuationToken
    }

    return entries, nil
}

func (b *Backend) Mkdir(name string, perm os.FileMode) error {
    // Create directory marker object
    key := b.s3Key(name)
    if !strings.HasSuffix(key, "/") {
        key += "/"
    }
    return b.client.PutObject(context.Background(), b.bucket, key, bytes.NewReader(nil), 0, "")
}

func (b *Backend) Remove(name string) error {
    key := b.s3Key(name)
    return b.client.DeleteObject(context.Background(), b.bucket, key)
}

func (b *Backend) RemoveAll(name string) error {
    prefix := b.s3Key(name)
    if !strings.HasSuffix(prefix, "/") {
        prefix += "/"
    }

    // List and delete all objects with prefix
    var keys []string
    continuationToken := ""
    for {
        resp, err := b.client.ListObjectsV2WithToken(
            context.Background(), b.bucket, prefix, "", 1000, continuationToken,
        )
        if err != nil {
            return err
        }
        for _, obj := range resp.Contents {
            keys = append(keys, obj.Key)
        }
        if !resp.IsTruncated {
            break
        }
        continuationToken = resp.NextContinuationToken
    }

    // Batch delete (max 1000 per request)
    for i := 0; i < len(keys); i += 1000 {
        end := i + 1000
        if end > len(keys) {
            end = len(keys)
        }
        if err := b.client.DeleteObjects(context.Background(), b.bucket, keys[i:end]); err != nil {
            return err
        }
    }
    return nil
}

func (b *Backend) Rename(oldname, newname string) error {
    oldKey := b.s3Key(oldname)
    newKey := b.s3Key(newname)

    // S3 rename = copy + delete (NOT atomic)
    if err := b.client.CopyObject(context.Background(), b.bucket, oldKey, b.bucket, newKey); err != nil {
        return err
    }
    return b.client.DeleteObject(context.Background(), b.bucket, oldKey)
}

func (b *Backend) Lstat(name string) (os.FileInfo, error)           { return b.Stat(name) }
func (b *Backend) Symlink(oldname, newname string) error            { return os.ErrInvalid }
func (b *Backend) Readlink(name string) (string, error)             { return "", os.ErrInvalid }
func (b *Backend) Chmod(name string, mode os.FileMode) error        { return nil } // No-op for S3
func (b *Backend) Chown(name string, uid, gid int) error            { return nil }
func (b *Backend) Chtimes(name string, atime, mtime time.Time) error { return nil }
func (b *Backend) MkdirAll(name string, perm os.FileMode) error     { return b.Mkdir(name, perm) }

func (b *Backend) Statvfs(path string) (*vfs.StatVFS, error) {
    // S3 has no concept of filesystem stats — return max values
    return &vfs.StatVFS{
        BlockSize:   4096,
        TotalBlocks: 1 << 40, // Effectively unlimited
        FreeBlocks:  1 << 40,
        AvailBlocks: 1 << 40,
        TotalFiles:  1 << 32,
        FreeFiles:   1 << 32,
        NameMaxLen:  1024,
    }, nil
}
```

### 4.7 S3 Client (From Scratch)

```go
// internal/storage/s3/client.go
package s3

import (
    "context"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "sort"
    "strings"
    "time"
)

// Client is a minimal from-scratch S3-compatible API client
// Implements SigV4 signing per AWS specification
type Client struct {
    endpoint     string
    region       string
    accessKey    string
    secretKey    string
    usePathStyle bool
    httpClient   *http.Client
}

type ClientConfig struct {
    Endpoint     string
    Region       string
    AccessKey    string
    SecretKey    string
    UsePathStyle bool
    DisableSSL   bool
    MaxRetries   int
}

func NewClient(cfg ClientConfig) (*Client, error) {
    scheme := "https"
    if cfg.DisableSSL {
        scheme = "http"
    }

    endpoint := cfg.Endpoint
    if !strings.HasPrefix(endpoint, "http") {
        endpoint = scheme + "://" + endpoint
    }

    return &Client{
        endpoint:     endpoint,
        region:       cfg.Region,
        accessKey:    cfg.AccessKey,
        secretKey:    cfg.SecretKey,
        usePathStyle: cfg.UsePathStyle,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
            Transport: &http.Transport{
                MaxIdleConns:        100,
                MaxIdleConnsPerHost: 100,
                IdleConnTimeout:     90 * time.Second,
            },
        },
    }, nil
}

// SigV4 signing implementation
func (c *Client) signRequest(req *http.Request, payload []byte) {
    now := time.Now().UTC()
    datestamp := now.Format("20060102")
    amzdate := now.Format("20060102T150405Z")

    req.Header.Set("x-amz-date", amzdate)
    req.Header.Set("x-amz-content-sha256", sha256Hex(payload))

    // Step 1: Canonical request
    canonicalHeaders, signedHeaders := c.buildCanonicalHeaders(req)
    canonicalRequest := strings.Join([]string{
        req.Method,
        req.URL.Path,
        req.URL.RawQuery,
        canonicalHeaders,
        signedHeaders,
        sha256Hex(payload),
    }, "\n")

    // Step 2: String to sign
    credentialScope := datestamp + "/" + c.region + "/s3/aws4_request"
    stringToSign := strings.Join([]string{
        "AWS4-HMAC-SHA256",
        amzdate,
        credentialScope,
        sha256Hex([]byte(canonicalRequest)),
    }, "\n")

    // Step 3: Signing key
    signingKey := c.deriveSigningKey(datestamp)

    // Step 4: Signature
    signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

    // Step 5: Authorization header
    authHeader := fmt.Sprintf(
        "AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
        c.accessKey, credentialScope, signedHeaders, signature,
    )
    req.Header.Set("Authorization", authHeader)
}

func (c *Client) deriveSigningKey(datestamp string) []byte {
    kDate := hmacSHA256([]byte("AWS4"+c.secretKey), []byte(datestamp))
    kRegion := hmacSHA256(kDate, []byte(c.region))
    kService := hmacSHA256(kRegion, []byte("s3"))
    kSigning := hmacSHA256(kService, []byte("aws4_request"))
    return kSigning
}

func (c *Client) buildCanonicalHeaders(req *http.Request) (canonical, signed string) {
    headers := make(map[string]string)
    var keys []string
    for key := range req.Header {
        lower := strings.ToLower(key)
        if lower == "host" || strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
            headers[lower] = strings.TrimSpace(req.Header.Get(key))
            keys = append(keys, lower)
        }
    }
    headers["host"] = req.Host
    keys = append(keys, "host")

    sort.Strings(keys)
    // Deduplicate
    keys = unique(keys)

    var canonicalParts []string
    for _, k := range keys {
        canonicalParts = append(canonicalParts, k+":"+headers[k])
    }

    return strings.Join(canonicalParts, "\n") + "\n", strings.Join(keys, ";")
}

func (c *Client) buildURL(bucket, key string) string {
    if c.usePathStyle {
        return c.endpoint + "/" + bucket + "/" + url.PathEscape(key)
    }
    // Virtual-hosted style
    u, _ := url.Parse(c.endpoint)
    u.Host = bucket + "." + u.Host
    u.Path = "/" + key
    return u.String()
}

// Core S3 operations

func (c *Client) GetObject(ctx context.Context, bucket, key string) (*GetObjectResponse, error) {
    reqURL := c.buildURL(bucket, key)
    req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
    if err != nil {
        return nil, err
    }

    c.signRequest(req, nil)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }

    if resp.StatusCode != 200 {
        resp.Body.Close()
        return nil, parseS3Error(resp)
    }

    return &GetObjectResponse{
        Body:          resp.Body,
        ContentLength: resp.ContentLength,
        LastModified:  parseHTTPDate(resp.Header.Get("Last-Modified")),
        ETag:          resp.Header.Get("ETag"),
    }, nil
}

func (c *Client) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
    reqURL := c.buildURL(bucket, key)

    var payload []byte
    if body != nil {
        var err error
        payload, err = io.ReadAll(body)
        if err != nil {
            return err
        }
    }

    req, err := http.NewRequestWithContext(ctx, "PUT", reqURL, bytes.NewReader(payload))
    if err != nil {
        return err
    }
    req.ContentLength = int64(len(payload))
    if contentType != "" {
        req.Header.Set("Content-Type", contentType)
    }

    c.signRequest(req, payload)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return parseS3Error(resp)
    }
    return nil
}

func (c *Client) HeadObject(ctx context.Context, bucket, key string) (*HeadObjectResponse, error) {
    reqURL := c.buildURL(bucket, key)
    req, err := http.NewRequestWithContext(ctx, "HEAD", reqURL, nil)
    if err != nil {
        return nil, err
    }

    c.signRequest(req, nil)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return nil, parseS3Error(resp)
    }

    return &HeadObjectResponse{
        ContentLength: resp.ContentLength,
        LastModified:  parseHTTPDate(resp.Header.Get("Last-Modified")),
        ETag:          resp.Header.Get("ETag"),
        ContentType:   resp.Header.Get("Content-Type"),
    }, nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
    reqURL := c.buildURL(bucket, key)
    req, err := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
    if err != nil {
        return err
    }
    c.signRequest(req, nil)
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 204 && resp.StatusCode != 200 {
        return parseS3Error(resp)
    }
    return nil
}

// ListObjectsV2, CopyObject, DeleteObjects, CreateMultipartUpload,
// UploadPart, CompleteMultipartUpload follow the same pattern

// Helper functions
func sha256Hex(data []byte) string {
    h := sha256.Sum256(data)
    return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
    h := hmac.New(sha256.New, key)
    h.Write(data)
    return h.Sum(nil)
}
```

---

## 5. FTP SERVER

### 5.1 Server Listener

```go
// internal/protocol/ftp/server.go
package ftp

import (
    "context"
    "crypto/tls"
    "fmt"
    "net"
    "sync"
    "sync/atomic"

    "github.com/kervanserver/kervan/internal/audit"
    "github.com/kervanserver/kervan/internal/auth"
    "github.com/kervanserver/kervan/internal/config"
    "github.com/kervanserver/kervan/internal/session"
)

type Server struct {
    cfg          *config.FTPConfig
    ftpsCfg      *config.FTPSConfig
    authEngine   *auth.Engine
    sessionMgr   *session.Manager
    auditEngine  *audit.Engine
    listener     net.Listener
    implicitLn   net.Listener        // Implicit FTPS listener
    tlsConfig    *tls.Config
    passiveRange portRange
    passiveIP    net.IP
    connections  sync.WaitGroup
    connCount    atomic.Int64
    ctx          context.Context
    cancel       context.CancelFunc
}

type portRange struct {
    start int
    end   int
    next  atomic.Int64
}

func (pr *portRange) Next() int {
    n := pr.next.Add(1)
    port := pr.start + int(n)%(pr.end-pr.start+1)
    return port
}

func NewServer(
    cfg *config.FTPConfig,
    ftpsCfg *config.FTPSConfig,
    authEngine *auth.Engine,
    sessionMgr *session.Manager,
    auditEngine *audit.Engine,
    tlsConfig *tls.Config,
) *Server {
    ctx, cancel := context.WithCancel(context.Background())
    return &Server{
        cfg:         cfg,
        ftpsCfg:     ftpsCfg,
        authEngine:  authEngine,
        sessionMgr:  sessionMgr,
        auditEngine: auditEngine,
        tlsConfig:   tlsConfig,
        ctx:         ctx,
        cancel:      cancel,
    }
}

func (s *Server) Start() error {
    // Parse passive port range
    var err error
    s.passiveRange, err = parsePortRange(s.cfg.PassivePortRange)
    if err != nil {
        return err
    }

    // Resolve passive IP
    s.passiveIP = resolvePassiveIP(s.cfg.PassiveIP)

    // Start FTP listener (explicit FTPS shares this port)
    addr := fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.Port)
    s.listener, err = net.Listen("tcp", addr)
    if err != nil {
        return err
    }

    // Start implicit FTPS listener if configured
    if s.ftpsCfg.Enabled && (s.ftpsCfg.Mode == "implicit" || s.ftpsCfg.Mode == "both") {
        implAddr := fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.ftpsCfg.ImplicitPort)
        s.implicitLn, err = tls.Listen("tcp", implAddr, s.tlsConfig)
        if err != nil {
            return err
        }
        go s.acceptLoop(s.implicitLn, true)
    }

    go s.acceptLoop(s.listener, false)
    return nil
}

func (s *Server) acceptLoop(ln net.Listener, implicitTLS bool) {
    for {
        conn, err := ln.Accept()
        if err != nil {
            select {
            case <-s.ctx.Done():
                return
            default:
                continue
            }
        }

        // Check connection limits
        if s.connCount.Load() >= int64(s.cfg.MaxConnections) {
            conn.Close()
            continue
        }

        s.connCount.Add(1)
        s.connections.Add(1)

        go func() {
            defer s.connections.Done()
            defer s.connCount.Add(-1)

            handler := newConnectionHandler(s, conn, implicitTLS)
            handler.serve()
        }()
    }
}

func (s *Server) Stop() error {
    s.cancel()
    if s.listener != nil {
        s.listener.Close()
    }
    if s.implicitLn != nil {
        s.implicitLn.Close()
    }
    s.connections.Wait()
    return nil
}
```

### 5.2 Connection Handler

```go
// internal/protocol/ftp/handler.go
package ftp

import (
    "bufio"
    "context"
    "crypto/tls"
    "fmt"
    "io"
    "net"
    "strings"
    "sync"
    "time"

    "github.com/kervanserver/kervan/internal/vfs"
)

type connectionHandler struct {
    server     *Server
    conn       net.Conn
    reader     *bufio.Reader
    writer     *bufio.Writer
    session    *ftpSession
    mu         sync.Mutex
    closed     bool
}

type ftpSession struct {
    id           string
    username     string
    authenticated bool
    vfs          *vfs.UserVFS
    cwd          string     // Current working directory (virtual)
    dataConn     net.Conn   // Active data connection
    passiveListener net.Listener
    dataType     byte       // 'A' (ASCII) or 'I' (Binary)
    tlsUpgraded  bool
    renameFrom   string     // For RNFR/RNTO sequence
    restOffset   int64      // For REST/RETR resume
    lastActivity time.Time
    ctx          context.Context
    cancel       context.CancelFunc
}

func newConnectionHandler(server *Server, conn net.Conn, implicitTLS bool) *connectionHandler {
    ctx, cancel := context.WithCancel(server.ctx)

    h := &connectionHandler{
        server: server,
        conn:   conn,
        reader: bufio.NewReaderSize(conn, 4096),
        writer: bufio.NewWriterSize(conn, 4096),
        session: &ftpSession{
            id:       generateSessionID(),
            cwd:      "/",
            dataType: 'I', // Binary by default
            ctx:      ctx,
            cancel:   cancel,
        },
    }

    if implicitTLS {
        h.session.tlsUpgraded = true
    }

    return h
}

func (h *connectionHandler) serve() {
    defer h.close()

    // Send banner
    h.reply(220, h.server.cfg.Banner)

    // Command loop
    for {
        // Set idle timeout
        h.conn.SetReadDeadline(time.Now().Add(h.server.cfg.IdleTimeout))

        line, err := h.reader.ReadString('\n')
        if err != nil {
            return
        }

        line = strings.TrimRight(line, "\r\n")
        if line == "" {
            continue
        }

        h.session.lastActivity = time.Now()

        // Parse command
        cmd, args := parseCommand(line)

        // Dispatch
        if err := h.dispatch(cmd, args); err != nil {
            if err == errQuit {
                return
            }
        }
    }
}

func (h *connectionHandler) dispatch(cmd, args string) error {
    cmd = strings.ToUpper(cmd)

    // Commands available before auth
    switch cmd {
    case "USER":
        return h.handleUSER(args)
    case "PASS":
        return h.handlePASS(args)
    case "AUTH":
        return h.handleAUTH(args)
    case "FEAT":
        return h.handleFEAT()
    case "QUIT":
        h.reply(221, "Goodbye.")
        return errQuit
    case "SYST":
        h.reply(215, "UNIX Type: L8")
        return nil
    case "NOOP":
        h.reply(200, "OK")
        return nil
    case "OPTS":
        return h.handleOPTS(args)
    case "PBSZ":
        return h.handlePBSZ(args)
    case "PROT":
        return h.handlePROT(args)
    case "HOST":
        return h.handleHOST(args)
    }

    // Commands requiring authentication
    if !h.session.authenticated {
        h.reply(530, "Please login with USER and PASS.")
        return nil
    }

    switch cmd {
    case "PWD", "XPWD":
        return h.handlePWD()
    case "CWD", "XCWD":
        return h.handleCWD(args)
    case "CDUP", "XCUP":
        return h.handleCDUP()
    case "LIST":
        return h.handleLIST(args)
    case "NLST":
        return h.handleNLST(args)
    case "MLSD":
        return h.handleMLSD(args)
    case "MLST":
        return h.handleMLST(args)
    case "RETR":
        return h.handleRETR(args)
    case "STOR":
        return h.handleSTOR(args)
    case "STOU":
        return h.handleSTOU()
    case "APPE":
        return h.handleAPPE(args)
    case "DELE":
        return h.handleDELE(args)
    case "MKD", "XMKD":
        return h.handleMKD(args)
    case "RMD", "XRMD":
        return h.handleRMD(args)
    case "RNFR":
        return h.handleRNFR(args)
    case "RNTO":
        return h.handleRNTO(args)
    case "SIZE":
        return h.handleSIZE(args)
    case "MDTM":
        return h.handleMDTM(args)
    case "TYPE":
        return h.handleTYPE(args)
    case "PASV":
        return h.handlePASV()
    case "EPSV":
        return h.handleEPSV()
    case "PORT":
        return h.handlePORT(args)
    case "EPRT":
        return h.handleEPRT(args)
    case "REST":
        return h.handleREST(args)
    case "ABOR":
        return h.handleABOR()
    case "SITE":
        return h.handleSITE(args)
    default:
        h.reply(502, "Command not implemented.")
        return nil
    }
}

func (h *connectionHandler) reply(code int, message string) {
    h.mu.Lock()
    defer h.mu.Unlock()
    fmt.Fprintf(h.writer, "%d %s\r\n", code, message)
    h.writer.Flush()
}

func (h *connectionHandler) replyMultiline(code int, lines []string) {
    h.mu.Lock()
    defer h.mu.Unlock()
    for i, line := range lines {
        if i == len(lines)-1 {
            fmt.Fprintf(h.writer, "%d %s\r\n", code, line)
        } else {
            fmt.Fprintf(h.writer, "%d-%s\r\n", code, line)
        }
    }
    h.writer.Flush()
}

func (h *connectionHandler) close() {
    h.session.cancel()
    if h.session.dataConn != nil {
        h.session.dataConn.Close()
    }
    if h.session.passiveListener != nil {
        h.session.passiveListener.Close()
    }
    h.conn.Close()
}

func parseCommand(line string) (cmd, args string) {
    parts := strings.SplitN(line, " ", 2)
    cmd = parts[0]
    if len(parts) > 1 {
        args = parts[1]
    }
    return
}
```

### 5.3 FTP Commands Implementation

```go
// internal/protocol/ftp/commands.go
package ftp

import (
    "crypto/tls"
    "fmt"
    "io"
    "net"
    "os"
    "path"
    "strconv"
    "strings"
    "time"
)

// AUTH TLS — Upgrade to TLS (explicit FTPS)
func (h *connectionHandler) handleAUTH(args string) error {
    if strings.ToUpper(args) != "TLS" && strings.ToUpper(args) != "SSL" {
        h.reply(504, "Unknown AUTH type.")
        return nil
    }
    if h.server.tlsConfig == nil {
        h.reply(534, "TLS not available.")
        return nil
    }
    if h.session.tlsUpgraded {
        h.reply(503, "Already using TLS.")
        return nil
    }

    h.reply(234, "AUTH TLS OK, starting TLS handshake.")

    // Upgrade connection to TLS
    tlsConn := tls.Server(h.conn, h.server.tlsConfig)
    if err := tlsConn.Handshake(); err != nil {
        return err
    }

    h.conn = tlsConn
    h.reader.Reset(tlsConn)
    h.writer.Reset(tlsConn)
    h.session.tlsUpgraded = true
    return nil
}

func (h *connectionHandler) handlePBSZ(args string) error {
    if !h.session.tlsUpgraded {
        h.reply(503, "TLS not established.")
        return nil
    }
    h.reply(200, "PBSZ=0")
    return nil
}

func (h *connectionHandler) handlePROT(args string) error {
    if !h.session.tlsUpgraded {
        h.reply(503, "TLS not established.")
        return nil
    }
    switch strings.ToUpper(args) {
    case "P": // Private (encrypted data channel)
        h.reply(200, "Data channel protection set to Private.")
    case "C": // Clear (unencrypted data channel)
        h.reply(200, "Data channel protection set to Clear.")
    default:
        h.reply(504, "Unknown protection level.")
    }
    return nil
}

func (h *connectionHandler) handleUSER(args string) error {
    h.session.username = args
    h.session.authenticated = false
    h.reply(331, "Username OK, need password.")
    return nil
}

func (h *connectionHandler) handlePASS(args string) error {
    if h.session.username == "" {
        h.reply(503, "Send USER first.")
        return nil
    }

    // Authenticate
    user, err := h.server.authEngine.Authenticate(h.session.username, args)
    if err != nil {
        h.server.auditEngine.Emit(audit.Event{
            Type:     audit.AuthLoginFailure,
            Protocol: "ftp",
            Username: h.session.username,
            ClientIP: h.remoteIP(),
            Error:    err.Error(),
        })
        h.reply(530, "Login incorrect.")
        return nil
    }

    // Check protocol permission
    if !user.CanUseProtocol("ftp") {
        h.reply(530, "FTP access not allowed.")
        return nil
    }

    // Setup session
    h.session.authenticated = true
    h.session.vfs = h.server.buildUserVFS(user)
    h.session.cwd = "/"

    h.server.auditEngine.Emit(audit.Event{
        Type:     audit.AuthLoginSuccess,
        Protocol: "ftp",
        Username: h.session.username,
        ClientIP: h.remoteIP(),
    })

    h.reply(230, "Login successful.")
    return nil
}

func (h *connectionHandler) handleFEAT() error {
    lines := []string{
        "Features:",
        " AUTH TLS",
        " PBSZ",
        " PROT",
        " UTF8",
        " MLSD",
        " MLST type*;size*;modify*;perm*;",
        " SIZE",
        " MDTM",
        " REST STREAM",
        " EPSV",
        " EPRT",
        " HOST",
        "End",
    }
    h.replyMultiline(211, lines)
    return nil
}

func (h *connectionHandler) handlePWD() error {
    h.reply(257, fmt.Sprintf(`"%s" is the current directory.`, h.session.cwd))
    return nil
}

func (h *connectionHandler) handleCWD(args string) error {
    target := h.resolvePath(args)

    info, err := h.session.vfs.Stat(target)
    if err != nil {
        h.reply(550, "Directory not found.")
        return nil
    }
    if !info.IsDir() {
        h.reply(550, "Not a directory.")
        return nil
    }

    h.session.cwd = target
    h.reply(250, "Directory changed.")
    return nil
}

func (h *connectionHandler) handleCDUP() error {
    return h.handleCWD("..")
}

// PASV — Enter passive mode
func (h *connectionHandler) handlePASV() error {
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", h.server.passiveRange.Next()))
    if err != nil {
        h.reply(425, "Cannot open passive connection.")
        return nil
    }

    h.session.passiveListener = ln
    addr := ln.Addr().(*net.TCPAddr)

    ip := h.server.passiveIP
    if ip == nil {
        ip = h.localIP()
    }

    // Format: h1,h2,h3,h4,p1,p2
    p1 := addr.Port / 256
    p2 := addr.Port % 256
    h.reply(227, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d).",
        ip[0], ip[1], ip[2], ip[3], p1, p2))
    return nil
}

// EPSV — Extended passive mode (IPv6 compatible)
func (h *connectionHandler) handleEPSV() error {
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", h.server.passiveRange.Next()))
    if err != nil {
        h.reply(425, "Cannot open passive connection.")
        return nil
    }

    h.session.passiveListener = ln
    port := ln.Addr().(*net.TCPAddr).Port

    h.reply(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|).", port))
    return nil
}

// RETR — Download file
func (h *connectionHandler) handleRETR(args string) error {
    filePath := h.resolvePath(args)

    file, err := h.session.vfs.Open(filePath, os.O_RDONLY, 0)
    if err != nil {
        h.reply(550, "File not found.")
        return nil
    }
    defer file.Close()

    info, err := file.Stat()
    if err != nil {
        h.reply(550, "Cannot stat file.")
        return nil
    }

    // Resume support
    if h.session.restOffset > 0 {
        if seeker, ok := file.(io.Seeker); ok {
            seeker.Seek(h.session.restOffset, io.SeekStart)
        }
        h.session.restOffset = 0
    }

    dataConn, err := h.openDataConnection()
    if err != nil {
        h.reply(425, "Cannot open data connection.")
        return nil
    }
    defer dataConn.Close()

    h.reply(150, fmt.Sprintf("Opening data connection for %s (%d bytes).", args, info.Size()))

    start := time.Now()
    n, err := io.Copy(dataConn, file)
    duration := time.Since(start)

    if err != nil {
        h.server.auditEngine.Emit(audit.Event{
            Type:     audit.FileDownloadFailed,
            Protocol: "ftp",
            Username: h.session.username,
            Path:     filePath,
            Error:    err.Error(),
        })
        h.reply(426, "Transfer aborted.")
        return nil
    }

    h.server.auditEngine.Emit(audit.Event{
        Type:     audit.FileDownloadComplete,
        Protocol: "ftp",
        Username: h.session.username,
        Path:     filePath,
        Size:     n,
        Duration: duration,
    })

    h.reply(226, "Transfer complete.")
    return nil
}

// STOR — Upload file
func (h *connectionHandler) handleSTOR(args string) error {
    filePath := h.resolvePath(args)

    file, err := h.session.vfs.Open(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
    if err != nil {
        h.reply(550, "Cannot create file: "+err.Error())
        return nil
    }
    defer file.Close()

    dataConn, err := h.openDataConnection()
    if err != nil {
        h.reply(425, "Cannot open data connection.")
        return nil
    }
    defer dataConn.Close()

    h.reply(150, "Opening data connection for upload.")

    start := time.Now()
    n, err := io.Copy(file, dataConn)
    duration := time.Since(start)

    if err != nil {
        h.server.auditEngine.Emit(audit.Event{
            Type:     audit.FileUploadFailed,
            Protocol: "ftp",
            Username: h.session.username,
            Path:     filePath,
            Error:    err.Error(),
        })
        h.reply(426, "Transfer aborted.")
        return nil
    }

    h.server.auditEngine.Emit(audit.Event{
        Type:     audit.FileUploadComplete,
        Protocol: "ftp",
        Username: h.session.username,
        Path:     filePath,
        Size:     n,
        Duration: duration,
    })

    h.reply(226, "Transfer complete.")
    return nil
}

// LIST — Directory listing (Unix ls -l format)
func (h *connectionHandler) handleLIST(args string) error {
    dir := h.session.cwd
    if args != "" && !strings.HasPrefix(args, "-") {
        dir = h.resolvePath(args)
    }

    entries, err := h.session.vfs.ReadDir(dir)
    if err != nil {
        h.reply(550, "Cannot list directory.")
        return nil
    }

    dataConn, err := h.openDataConnection()
    if err != nil {
        h.reply(425, "Cannot open data connection.")
        return nil
    }
    defer dataConn.Close()

    h.reply(150, "Opening data connection for directory listing.")

    for _, entry := range entries {
        info, _ := entry.Info()
        line := formatLIST(info)
        fmt.Fprintf(dataConn, "%s\r\n", line)
    }

    h.reply(226, "Directory listing complete.")
    return nil
}

// MLSD — Machine-readable directory listing (RFC 3659)
func (h *connectionHandler) handleMLSD(args string) error {
    dir := h.session.cwd
    if args != "" {
        dir = h.resolvePath(args)
    }

    entries, err := h.session.vfs.ReadDir(dir)
    if err != nil {
        h.reply(550, "Cannot list directory.")
        return nil
    }

    dataConn, err := h.openDataConnection()
    if err != nil {
        h.reply(425, "Cannot open data connection.")
        return nil
    }
    defer dataConn.Close()

    h.reply(150, "Opening data connection for MLSD.")

    for _, entry := range entries {
        info, _ := entry.Info()
        line := formatMLST(info)
        fmt.Fprintf(dataConn, "%s %s\r\n", line, entry.Name())
    }

    h.reply(226, "MLSD complete.")
    return nil
}

func (h *connectionHandler) handleSIZE(args string) error {
    filePath := h.resolvePath(args)
    info, err := h.session.vfs.Stat(filePath)
    if err != nil {
        h.reply(550, "File not found.")
        return nil
    }
    h.reply(213, strconv.FormatInt(info.Size(), 10))
    return nil
}

func (h *connectionHandler) handleMDTM(args string) error {
    filePath := h.resolvePath(args)
    info, err := h.session.vfs.Stat(filePath)
    if err != nil {
        h.reply(550, "File not found.")
        return nil
    }
    h.reply(213, info.ModTime().UTC().Format("20060102150405"))
    return nil
}

func (h *connectionHandler) handleTYPE(args string) error {
    switch strings.ToUpper(args) {
    case "A":
        h.session.dataType = 'A'
        h.reply(200, "Type set to ASCII.")
    case "I":
        h.session.dataType = 'I'
        h.reply(200, "Type set to Binary.")
    default:
        h.reply(504, "Unknown type.")
    }
    return nil
}

func (h *connectionHandler) handleREST(args string) error {
    offset, err := strconv.ParseInt(args, 10, 64)
    if err != nil || offset < 0 {
        h.reply(501, "Invalid restart position.")
        return nil
    }
    h.session.restOffset = offset
    h.reply(350, fmt.Sprintf("Restart position set to %d.", offset))
    return nil
}

func (h *connectionHandler) handleDELE(args string) error {
    filePath := h.resolvePath(args)
    if err := h.session.vfs.Remove(filePath); err != nil {
        h.reply(550, "Cannot delete: "+err.Error())
        return nil
    }
    h.server.auditEngine.Emit(audit.Event{
        Type:     audit.FileDelete,
        Protocol: "ftp",
        Username: h.session.username,
        Path:     filePath,
    })
    h.reply(250, "File deleted.")
    return nil
}

func (h *connectionHandler) handleMKD(args string) error {
    dirPath := h.resolvePath(args)
    if err := h.session.vfs.Mkdir(dirPath, 0755); err != nil {
        h.reply(550, "Cannot create directory: "+err.Error())
        return nil
    }
    h.reply(257, fmt.Sprintf(`"%s" created.`, dirPath))
    return nil
}

func (h *connectionHandler) handleRNFR(args string) error {
    h.session.renameFrom = h.resolvePath(args)
    h.reply(350, "Ready for RNTO.")
    return nil
}

func (h *connectionHandler) handleRNTO(args string) error {
    if h.session.renameFrom == "" {
        h.reply(503, "Send RNFR first.")
        return nil
    }
    to := h.resolvePath(args)
    if err := h.session.vfs.Rename(h.session.renameFrom, to); err != nil {
        h.reply(550, "Rename failed: "+err.Error())
        return nil
    }
    h.server.auditEngine.Emit(audit.Event{
        Type:       audit.FileRename,
        Protocol:   "ftp",
        Username:   h.session.username,
        Path:       h.session.renameFrom,
        TargetPath: to,
    })
    h.session.renameFrom = ""
    h.reply(250, "Renamed.")
    return nil
}

// Helper: resolve relative path against CWD
func (h *connectionHandler) resolvePath(p string) string {
    if path.IsAbs(p) {
        return path.Clean(p)
    }
    return path.Clean(path.Join(h.session.cwd, p))
}

// Helper: open data connection (passive or active)
func (h *connectionHandler) openDataConnection() (net.Conn, error) {
    if h.session.passiveListener != nil {
        defer func() { h.session.passiveListener = nil }()
        h.session.passiveListener.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second))
        conn, err := h.session.passiveListener.Accept()
        h.session.passiveListener.Close()
        return conn, err
    }
    if h.session.dataConn != nil {
        conn := h.session.dataConn
        h.session.dataConn = nil
        return conn, nil
    }
    return nil, fmt.Errorf("no data connection")
}
```

---

## 6. SFTP/SCP SERVER

### 6.1 SSH Server Foundation

```go
// internal/protocol/sftp/server.go
package sftp

import (
    "context"
    "fmt"
    "net"
    "sync"

    "golang.org/x/crypto/ssh"

    "github.com/kervanserver/kervan/internal/audit"
    "github.com/kervanserver/kervan/internal/auth"
    "github.com/kervanserver/kervan/internal/config"
    "github.com/kervanserver/kervan/internal/session"
    scpPkg "github.com/kervanserver/kervan/internal/protocol/scp"
)

type Server struct {
    cfg         *config.SFTPConfig
    scpCfg      *config.SCPConfig
    authEngine  *auth.Engine
    sessionMgr  *session.Manager
    auditEngine *audit.Engine
    sshConfig   *ssh.ServerConfig
    listener    net.Listener
    connections sync.WaitGroup
    ctx         context.Context
    cancel      context.CancelFunc
}

func NewServer(
    cfg *config.SFTPConfig,
    scpCfg *config.SCPConfig,
    authEngine *auth.Engine,
    sessionMgr *session.Manager,
    auditEngine *audit.Engine,
    hostKeys []ssh.Signer,
) *Server {
    ctx, cancel := context.WithCancel(context.Background())

    s := &Server{
        cfg:         cfg,
        scpCfg:      scpCfg,
        authEngine:  authEngine,
        sessionMgr:  sessionMgr,
        auditEngine: auditEngine,
        ctx:         ctx,
        cancel:      cancel,
    }

    s.sshConfig = &ssh.ServerConfig{
        PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
            user, err := authEngine.Authenticate(conn.User(), string(password))
            if err != nil {
                auditEngine.Emit(audit.Event{
                    Type:     audit.AuthLoginFailure,
                    Protocol: "sftp",
                    Username: conn.User(),
                    ClientIP: conn.RemoteAddr().String(),
                    Error:    err.Error(),
                })
                return nil, fmt.Errorf("auth failed")
            }
            return &ssh.Permissions{
                Extensions: map[string]string{
                    "user-id": user.ID,
                },
            }, nil
        },

        PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
            user, err := authEngine.AuthenticatePublicKey(conn.User(), key)
            if err != nil {
                auditEngine.Emit(audit.Event{
                    Type:     audit.AuthKeyRejected,
                    Protocol: "sftp",
                    Username: conn.User(),
                    ClientIP: conn.RemoteAddr().String(),
                })
                return nil, fmt.Errorf("key rejected")
            }
            return &ssh.Permissions{
                Extensions: map[string]string{
                    "user-id": user.ID,
                },
            }, nil
        },

        KeyboardInteractiveCallback: s.keyboardInteractiveHandler,
    }

    // Add host keys
    for _, key := range hostKeys {
        s.sshConfig.AddHostKey(key)
    }

    // Configure algorithms
    s.sshConfig.Config = ssh.Config{
        KeyExchanges: []string{
            "curve25519-sha256",
            "curve25519-sha256@libssh.org",
            "diffie-hellman-group16-sha512",
            "diffie-hellman-group14-sha256",
        },
        Ciphers: []string{
            "chacha20-poly1305@openssh.com",
            "aes256-gcm@openssh.com",
            "aes128-gcm@openssh.com",
            "aes256-ctr",
            "aes128-ctr",
        },
        MACs: []string{
            "hmac-sha2-256-etm@openssh.com",
            "hmac-sha2-512-etm@openssh.com",
            "hmac-sha2-256",
        },
    }

    return s
}

func (s *Server) Start() error {
    addr := fmt.Sprintf(":%d", s.cfg.Port)
    var err error
    s.listener, err = net.Listen("tcp", addr)
    if err != nil {
        return err
    }

    go s.acceptLoop()
    return nil
}

func (s *Server) acceptLoop() {
    for {
        conn, err := s.listener.Accept()
        if err != nil {
            select {
            case <-s.ctx.Done():
                return
            default:
                continue
            }
        }

        s.connections.Add(1)
        go func() {
            defer s.connections.Done()
            s.handleConnection(conn)
        }()
    }
}

func (s *Server) handleConnection(netConn net.Conn) {
    defer netConn.Close()

    // SSH handshake
    sshConn, chans, reqs, err := ssh.NewServerConn(netConn, s.sshConfig)
    if err != nil {
        return
    }
    defer sshConn.Close()

    userID := sshConn.Permissions.Extensions["user-id"]
    user, err := s.authEngine.GetUserByID(userID)
    if err != nil {
        return
    }

    s.auditEngine.Emit(audit.Event{
        Type:     audit.AuthLoginSuccess,
        Protocol: "sftp",
        Username: user.Username,
        ClientIP: netConn.RemoteAddr().String(),
    })

    // Discard global requests
    go ssh.DiscardRequests(reqs)

    // Handle channels
    for newChannel := range chans {
        switch newChannel.ChannelType() {
        case "session":
            channel, requests, err := newChannel.Accept()
            if err != nil {
                continue
            }
            go s.handleSession(channel, requests, user, netConn.RemoteAddr().String())

        default:
            newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
        }
    }
}

func (s *Server) handleSession(channel ssh.Channel, requests <-chan *ssh.Request, user *auth.User, clientIP string) {
    defer channel.Close()

    for req := range requests {
        switch req.Type {
        case "subsystem":
            subsystem := string(req.Payload[4:]) // Skip 4-byte length prefix
            switch subsystem {
            case "sftp":
                if !user.CanUseProtocol("sftp") {
                    req.Reply(false, nil)
                    continue
                }
                req.Reply(true, nil)
                userVFS := s.buildUserVFS(user)
                handler := NewSFTPHandler(channel, userVFS, user.Username, clientIP, s.auditEngine)
                handler.Serve()
                return
            default:
                req.Reply(false, nil)
            }

        case "exec":
            // SCP comes through exec channel
            cmdLen := int(req.Payload[3]) | int(req.Payload[2])<<8 | int(req.Payload[1])<<16 | int(req.Payload[0])<<24
            cmd := string(req.Payload[4 : 4+cmdLen])

            if s.scpCfg.Enabled && isSCPCommand(cmd) {
                if !user.CanUseProtocol("scp") {
                    req.Reply(false, nil)
                    continue
                }
                req.Reply(true, nil)
                userVFS := s.buildUserVFS(user)
                scpHandler := scpPkg.NewHandler(channel, userVFS, user.Username, clientIP, s.auditEngine, cmd)
                scpHandler.Serve()
                // Send exit status
                channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
                return
            }

            // Shell disabled
            if s.cfg.DisableShell {
                req.Reply(false, nil)
                continue
            }

        case "shell":
            if s.cfg.DisableShell {
                req.Reply(false, nil)
                continue
            }

        default:
            if req.WantReply {
                req.Reply(false, nil)
            }
        }
    }
}

func isSCPCommand(cmd string) bool {
    return len(cmd) > 3 && cmd[:4] == "scp "
}
```

### 6.2 SFTP Handler

```go
// internal/protocol/sftp/handler.go
package sftp

import (
    "encoding/binary"
    "io"
    "os"
    "sync"
    "time"

    "github.com/kervanserver/kervan/internal/audit"
    "github.com/kervanserver/kervan/internal/vfs"
)

const (
    sftpProtocolVersion = 3

    // Packet types
    SSH_FXP_INIT          = 1
    SSH_FXP_VERSION       = 2
    SSH_FXP_OPEN          = 3
    SSH_FXP_CLOSE         = 4
    SSH_FXP_READ          = 5
    SSH_FXP_WRITE         = 6
    SSH_FXP_LSTAT         = 7
    SSH_FXP_FSTAT         = 8
    SSH_FXP_SETSTAT       = 9
    SSH_FXP_FSETSTAT      = 10
    SSH_FXP_OPENDIR       = 11
    SSH_FXP_READDIR       = 12
    SSH_FXP_REMOVE        = 13
    SSH_FXP_MKDIR         = 14
    SSH_FXP_RMDIR         = 15
    SSH_FXP_REALPATH      = 16
    SSH_FXP_STAT          = 17
    SSH_FXP_RENAME        = 18
    SSH_FXP_READLINK      = 19
    SSH_FXP_SYMLINK       = 20
    SSH_FXP_STATUS        = 101
    SSH_FXP_HANDLE        = 102
    SSH_FXP_DATA          = 103
    SSH_FXP_NAME          = 104
    SSH_FXP_ATTRS         = 105
    SSH_FXP_EXTENDED      = 200
    SSH_FXP_EXTENDED_REPLY = 201

    // Status codes
    SSH_FX_OK                = 0
    SSH_FX_EOF               = 1
    SSH_FX_NO_SUCH_FILE      = 2
    SSH_FX_PERMISSION_DENIED = 3
    SSH_FX_FAILURE           = 4
    SSH_FX_OP_UNSUPPORTED    = 8
)

type SFTPHandler struct {
    rw          io.ReadWriter
    vfs         *vfs.UserVFS
    username    string
    clientIP    string
    audit       *audit.Engine
    handles     map[string]handle
    handleSeq   uint64
    mu          sync.Mutex
    maxPacket   uint32
}

type handle struct {
    file  vfs.File
    isDir bool
    path  string
    dirRead bool // Has ReadDir been called
}

func NewSFTPHandler(rw io.ReadWriter, fs *vfs.UserVFS, username, clientIP string, auditEngine *audit.Engine) *SFTPHandler {
    return &SFTPHandler{
        rw:        rw,
        vfs:       fs,
        username:  username,
        clientIP:  clientIP,
        audit:     auditEngine,
        handles:   make(map[string]handle),
        maxPacket: 34000,
    }
}

func (h *SFTPHandler) Serve() {
    for {
        pktType, payload, err := h.readPacket()
        if err != nil {
            return
        }

        switch pktType {
        case SSH_FXP_INIT:
            h.handleInit(payload)
        case SSH_FXP_OPEN:
            h.handleOpen(payload)
        case SSH_FXP_CLOSE:
            h.handleClose(payload)
        case SSH_FXP_READ:
            h.handleRead(payload)
        case SSH_FXP_WRITE:
            h.handleWrite(payload)
        case SSH_FXP_LSTAT:
            h.handleLstat(payload)
        case SSH_FXP_FSTAT:
            h.handleFstat(payload)
        case SSH_FXP_SETSTAT:
            h.handleSetstat(payload)
        case SSH_FXP_OPENDIR:
            h.handleOpendir(payload)
        case SSH_FXP_READDIR:
            h.handleReaddir(payload)
        case SSH_FXP_REMOVE:
            h.handleRemove(payload)
        case SSH_FXP_MKDIR:
            h.handleMkdir(payload)
        case SSH_FXP_RMDIR:
            h.handleRmdir(payload)
        case SSH_FXP_REALPATH:
            h.handleRealpath(payload)
        case SSH_FXP_STAT:
            h.handleStat(payload)
        case SSH_FXP_RENAME:
            h.handleRename(payload)
        case SSH_FXP_READLINK:
            h.handleReadlink(payload)
        case SSH_FXP_SYMLINK:
            h.handleSymlink(payload)
        case SSH_FXP_EXTENDED:
            h.handleExtended(payload)
        }
    }
}

// Packet I/O

func (h *SFTPHandler) readPacket() (byte, []byte, error) {
    var lenBuf [4]byte
    if _, err := io.ReadFull(h.rw, lenBuf[:]); err != nil {
        return 0, nil, err
    }
    length := binary.BigEndian.Uint32(lenBuf[:])
    if length > h.maxPacket+4 {
        return 0, nil, ErrPacketTooLarge
    }

    payload := make([]byte, length)
    if _, err := io.ReadFull(h.rw, payload); err != nil {
        return 0, nil, err
    }

    return payload[0], payload[1:], nil
}

func (h *SFTPHandler) writePacket(pktType byte, payload []byte) error {
    length := uint32(1 + len(payload))
    var header [5]byte
    binary.BigEndian.PutUint32(header[:4], length)
    header[4] = pktType

    h.mu.Lock()
    defer h.mu.Unlock()

    if _, err := h.rw.Write(header[:]); err != nil {
        return err
    }
    _, err := h.rw.Write(payload)
    return err
}

func (h *SFTPHandler) sendStatus(id uint32, code uint32, msg string) {
    payload := marshalUint32(id)
    payload = append(payload, marshalUint32(code)...)
    payload = append(payload, marshalString(msg)...)
    payload = append(payload, marshalString("")...) // language tag
    h.writePacket(SSH_FXP_STATUS, payload)
}

func (h *SFTPHandler) sendHandle(id uint32, handle string) {
    payload := marshalUint32(id)
    payload = append(payload, marshalString(handle)...)
    h.writePacket(SSH_FXP_HANDLE, payload)
}

// Handle management

func (h *SFTPHandler) newHandle(f vfs.File, path string, isDir bool) string {
    h.mu.Lock()
    defer h.mu.Unlock()
    h.handleSeq++
    id := fmt.Sprintf("h%d", h.handleSeq)
    h.handles[id] = handle{file: f, isDir: isDir, path: path}
    return id
}

func (h *SFTPHandler) getHandle(id string) (handle, bool) {
    h.mu.Lock()
    defer h.mu.Unlock()
    hnd, ok := h.handles[id]
    return hnd, ok
}

func (h *SFTPHandler) closeHandle(id string) {
    h.mu.Lock()
    hnd, ok := h.handles[id]
    delete(h.handles, id)
    h.mu.Unlock()
    if ok && hnd.file != nil {
        hnd.file.Close()
    }
}

// Packet handlers (abbreviated — full implementations follow same pattern)

func (h *SFTPHandler) handleInit(payload []byte) {
    // Client sends version
    // We respond with our version
    resp := marshalUint32(sftpProtocolVersion)
    // Add extensions
    resp = append(resp, marshalString("posix-rename@openssh.com")...)
    resp = append(resp, marshalString("1")...)
    resp = append(resp, marshalString("statvfs@openssh.com")...)
    resp = append(resp, marshalString("2")...)
    h.writePacket(SSH_FXP_VERSION, resp)
}

func (h *SFTPHandler) handleOpen(payload []byte) {
    id, rest := unmarshalUint32(payload)
    path, rest := unmarshalString(rest)
    pflags, rest := unmarshalUint32(rest)
    attrs, _ := unmarshalAttrs(rest)

    goFlags := sftpFlagsToOS(pflags)
    perm := os.FileMode(0644)
    if attrs.Permissions != 0 {
        perm = attrs.Permissions
    }

    file, err := h.vfs.Open(path, goFlags, perm)
    if err != nil {
        h.sendStatus(id, statusFromError(err), err.Error())
        return
    }

    handle := h.newHandle(file, path, false)
    h.sendHandle(id, handle)
}

func (h *SFTPHandler) handleRead(payload []byte) {
    id, rest := unmarshalUint32(payload)
    handleID, rest := unmarshalString(rest)
    offset, rest := unmarshalUint64(rest)
    length, _ := unmarshalUint32(rest)

    hnd, ok := h.getHandle(handleID)
    if !ok {
        h.sendStatus(id, SSH_FX_FAILURE, "invalid handle")
        return
    }

    if length > h.maxPacket-9 {
        length = h.maxPacket - 9
    }

    buf := make([]byte, length)
    n, err := hnd.file.(io.ReaderAt).ReadAt(buf, int64(offset))

    if n == 0 {
        if err == io.EOF {
            h.sendStatus(id, SSH_FX_EOF, "EOF")
        } else {
            h.sendStatus(id, SSH_FX_FAILURE, err.Error())
        }
        return
    }

    resp := marshalUint32(id)
    resp = append(resp, marshalString(string(buf[:n]))...)
    h.writePacket(SSH_FXP_DATA, resp)
}

func (h *SFTPHandler) handleWrite(payload []byte) {
    id, rest := unmarshalUint32(payload)
    handleID, rest := unmarshalString(rest)
    offset, rest := unmarshalUint64(rest)
    data, _ := unmarshalString(rest)

    hnd, ok := h.getHandle(handleID)
    if !ok {
        h.sendStatus(id, SSH_FX_FAILURE, "invalid handle")
        return
    }

    _, err := hnd.file.(io.WriterAt).WriteAt([]byte(data), int64(offset))
    if err != nil {
        h.sendStatus(id, SSH_FX_FAILURE, err.Error())
        return
    }

    h.sendStatus(id, SSH_FX_OK, "")
}

func (h *SFTPHandler) handleClose(payload []byte) {
    id, rest := unmarshalUint32(payload)
    handleID, _ := unmarshalString(rest)
    h.closeHandle(handleID)
    h.sendStatus(id, SSH_FX_OK, "")
}

func (h *SFTPHandler) handleStat(payload []byte) {
    id, rest := unmarshalUint32(payload)
    path, _ := unmarshalString(rest)

    info, err := h.vfs.Stat(path)
    if err != nil {
        h.sendStatus(id, statusFromError(err), err.Error())
        return
    }

    resp := marshalUint32(id)
    resp = append(resp, marshalFileInfo(info)...)
    h.writePacket(SSH_FXP_ATTRS, resp)
}

func (h *SFTPHandler) handleRealpath(payload []byte) {
    id, rest := unmarshalUint32(payload)
    path, _ := unmarshalString(rest)

    resolved := cleanPath(path)

    resp := marshalUint32(id)
    resp = append(resp, marshalUint32(1)...) // count = 1
    resp = append(resp, marshalString(resolved)...)
    resp = append(resp, marshalString(resolved)...) // longname
    resp = append(resp, marshalDirAttrs()...)
    h.writePacket(SSH_FXP_NAME, resp)
}

// Extended operations (posix-rename, statvfs)
func (h *SFTPHandler) handleExtended(payload []byte) {
    id, rest := unmarshalUint32(payload)
    extName, rest := unmarshalString(rest)

    switch extName {
    case "posix-rename@openssh.com":
        oldPath, rest := unmarshalString(rest)
        newPath, _ := unmarshalString(rest)
        if err := h.vfs.Rename(oldPath, newPath); err != nil {
            h.sendStatus(id, statusFromError(err), err.Error())
            return
        }
        h.audit.Emit(audit.Event{
            Type:       audit.FileRename,
            Protocol:   "sftp",
            Username:   h.username,
            Path:       oldPath,
            TargetPath: newPath,
        })
        h.sendStatus(id, SSH_FX_OK, "")

    case "statvfs@openssh.com":
        path, _ := unmarshalString(rest)
        stat, err := h.vfs.Statvfs(path)
        if err != nil {
            h.sendStatus(id, SSH_FX_FAILURE, err.Error())
            return
        }
        resp := marshalUint32(id)
        resp = append(resp, marshalStatVFS(stat)...)
        h.writePacket(SSH_FXP_EXTENDED_REPLY, resp)

    default:
        h.sendStatus(id, SSH_FX_OP_UNSUPPORTED, "unsupported extension")
    }
}
```

---

## 7. AUDIT ENGINE

```go
// internal/audit/engine.go
package audit

import (
    "sync"
    "time"
)

type Engine struct {
    outputs  []Output
    eventCh  chan Event
    wg       sync.WaitGroup
    closed   chan struct{}
}

type Output interface {
    Write(event Event) error
    Close() error
}

func NewEngine(bufferSize int) *Engine {
    e := &Engine{
        eventCh: make(chan Event, bufferSize),
        closed:  make(chan struct{}),
    }
    e.wg.Add(1)
    go e.processLoop()
    return e
}

func (e *Engine) AddOutput(output Output) {
    e.outputs = append(e.outputs, output)
}

func (e *Engine) Emit(event Event) {
    if event.ID == "" {
        event.ID = generateULID()
    }
    if event.Timestamp.IsZero() {
        event.Timestamp = time.Now().UTC()
    }

    select {
    case e.eventCh <- event:
    default:
        // Buffer full — drop event (log warning)
    }
}

func (e *Engine) processLoop() {
    defer e.wg.Done()

    for {
        select {
        case event := <-e.eventCh:
            for _, out := range e.outputs {
                _ = out.Write(event)
            }
        case <-e.closed:
            // Drain remaining events
            for {
                select {
                case event := <-e.eventCh:
                    for _, out := range e.outputs {
                        _ = out.Write(event)
                    }
                default:
                    return
                }
            }
        }
    }
}

func (e *Engine) Close() error {
    close(e.closed)
    e.wg.Wait()
    for _, out := range e.outputs {
        out.Close()
    }
    return nil
}
```

---

## 8. WEBUI EMBEDDING

### 8.1 Embed Handler

```go
// internal/webui/embed.go
package webui

import "embed"

//go:embed dist/*
var assets embed.FS
```

```go
// internal/webui/handler.go
package webui

import (
    "io/fs"
    "net/http"
    "strings"
)

// Handler serves the embedded React SPA
func Handler() http.Handler {
    distFS, _ := fs.Sub(assets, "dist")
    fileServer := http.FileServer(http.FS(distFS))

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Check if file exists
        path := r.URL.Path
        if path == "/" {
            path = "/index.html"
        }

        // Try to serve static file
        if _, err := fs.Stat(distFS, strings.TrimPrefix(path, "/")); err == nil {
            fileServer.ServeHTTP(w, r)
            return
        }

        // SPA fallback: serve index.html for all other routes
        r.URL.Path = "/"
        fileServer.ServeHTTP(w, r)
    })
}
```

---

## 9. ENTRY POINT

### 9.1 Main with CLI

```go
// cmd/kervan/main.go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/kervanserver/kervan/internal/build"
    "github.com/kervanserver/kervan/internal/config"
    "github.com/kervanserver/kervan/internal/server"
)

func main() {
    if len(os.Args) < 2 {
        runServer()
        return
    }

    switch os.Args[1] {
    case "version":
        fmt.Println(build.Info())
    case "init":
        cmdInit()
    case "keygen":
        cmdKeygen()
    case "admin":
        cmdAdmin()
    case "user":
        cmdUser()
    case "check":
        cmdCheck()
    case "migrate":
        cmdMigrate()
    case "status":
        cmdStatus()
    default:
        // Might be --config flag
        runServer()
    }
}

func runServer() {
    cfgPath := "kervan.yaml"
    for i, arg := range os.Args {
        if arg == "--config" && i+1 < len(os.Args) {
            cfgPath = os.Args[i+1]
        }
    }

    liveCfg, err := config.NewLiveConfig(cfgPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
        os.Exit(1)
    }

    srv, err := server.New(liveCfg)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating server: %v\n", err)
        os.Exit(1)
    }

    if err := srv.Start(); err != nil {
        fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
        os.Exit(1)
    }

    // Watch for SIGHUP (config reload)
    liveCfg.WatchSignals()

    // Wait for shutdown signal
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()

    fmt.Println("\nShutting down...")
    cfg := liveCfg.Get()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.GracefulShutdownTimeout)
    defer cancel()

    if err := srv.Shutdown(shutdownCtx); err != nil {
        fmt.Fprintf(os.Stderr, "Shutdown error: %v\n", err)
        os.Exit(1)
    }
    fmt.Println("Server stopped.")
}
```

### 9.2 Server Orchestrator

```go
// internal/server/server.go
package server

import (
    "context"
    "crypto/tls"
    "fmt"
    "net/http"

    "github.com/kervanserver/kervan/internal/api"
    "github.com/kervanserver/kervan/internal/audit"
    "github.com/kervanserver/kervan/internal/auth"
    "github.com/kervanserver/kervan/internal/cobalt"
    "github.com/kervanserver/kervan/internal/config"
    "github.com/kervanserver/kervan/internal/protocol/ftp"
    "github.com/kervanserver/kervan/internal/protocol/sftp"
    "github.com/kervanserver/kervan/internal/session"
    "github.com/kervanserver/kervan/internal/webui"
)

type Server struct {
    cfg         *config.LiveConfig
    store       *cobalt.Store
    authEngine  *auth.Engine
    sessionMgr  *session.Manager
    auditEngine *audit.Engine
    ftpServer   *ftp.Server
    sftpServer  *sftp.Server
    httpServer  *http.Server
}

func New(cfg *config.LiveConfig) (*Server, error) {
    c := cfg.Get()

    // Open CobaltDB
    store, err := cobalt.Open(c.Server.DataDir)
    if err != nil {
        return nil, fmt.Errorf("opening database: %w", err)
    }

    // Initialize audit engine
    auditEngine := audit.NewEngine(10000)
    // Add configured outputs...

    // Initialize auth engine
    userRepo := auth.NewUserRepository(store)
    authEngine := auth.NewEngine(userRepo, c.Auth)

    // Session manager
    sessionMgr := session.NewManager()

    // TLS config for FTPS
    var tlsConfig *tls.Config
    if c.FTPS.Enabled {
        tlsConfig, err = buildTLSConfig(c.FTPS)
        if err != nil {
            return nil, fmt.Errorf("TLS config: %w", err)
        }
    }

    // SSH host keys
    hostKeys, err := loadOrGenerateHostKeys(c.SFTP.HostKeyDir, c.SFTP.HostKeyAlgorithms)
    if err != nil {
        return nil, fmt.Errorf("host keys: %w", err)
    }

    s := &Server{
        cfg:         cfg,
        store:       store,
        authEngine:  authEngine,
        sessionMgr:  sessionMgr,
        auditEngine: auditEngine,
    }

    // FTP Server
    if c.FTP.Enabled {
        s.ftpServer = ftp.NewServer(&c.FTP, &c.FTPS, authEngine, sessionMgr, auditEngine, tlsConfig)
    }

    // SFTP/SCP Server
    if c.SFTP.Enabled {
        s.sftpServer = sftp.NewServer(&c.SFTP, &c.SCP, authEngine, sessionMgr, auditEngine, hostKeys)
    }

    // WebUI + API HTTP Server
    if c.WebUI.Enabled {
        mux := http.NewServeMux()

        // API routes
        apiRouter := api.NewRouter(authEngine, sessionMgr, auditEngine, store)
        mux.Handle("/api/", apiRouter)

        // WebSocket
        mux.HandleFunc("/api/v1/ws", api.WebSocketHandler(auditEngine, sessionMgr))

        // WebUI (SPA)
        mux.Handle("/", webui.Handler())

        s.httpServer = &http.Server{
            Addr:    fmt.Sprintf("%s:%d", c.WebUI.BindAddress, c.WebUI.Port),
            Handler: mux,
        }
    }

    return s, nil
}

func (s *Server) Start() error {
    if s.ftpServer != nil {
        if err := s.ftpServer.Start(); err != nil {
            return fmt.Errorf("FTP server: %w", err)
        }
    }

    if s.sftpServer != nil {
        if err := s.sftpServer.Start(); err != nil {
            return fmt.Errorf("SFTP server: %w", err)
        }
    }

    if s.httpServer != nil {
        go func() {
            c := s.cfg.Get()
            if c.WebUI.TLS {
                s.httpServer.ListenAndServeTLS(c.FTPS.CertFile, c.FTPS.KeyFile)
            } else {
                s.httpServer.ListenAndServe()
            }
        }()
    }

    return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
    if s.httpServer != nil {
        s.httpServer.Shutdown(ctx)
    }
    if s.ftpServer != nil {
        s.ftpServer.Stop()
    }
    if s.sftpServer != nil {
        s.sftpServer.Stop()
    }
    s.auditEngine.Close()
    s.store.Close()
    return nil
}
```

---

## 10. IMPLEMENTATION SEQUENCE

Build order optimized for incremental testing:

### Phase 1 — Foundation
1. Config system (load, validate, defaults, env overlay)
2. CobaltDB integration (store layer, user repository)
3. VFS interfaces + path resolver
4. Local filesystem backend
5. Build system + entry point skeleton

### Phase 2 — FTP Core
6. FTP server listener + connection handler
7. FTP auth (USER/PASS via auth engine)
8. FTP navigation (PWD, CWD, LIST, MLSD)
9. FTP transfer (RETR, STOR, passive mode)
10. FTP file ops (DELE, MKD, RMD, RNFR/RNTO)
11. FTPS (AUTH TLS, implicit mode)

### Phase 3 — SSH Protocols
12. SSH server foundation (host keys, auth callbacks)
13. SFTP handler (init, open, read, write, close)
14. SFTP directory ops (opendir, readdir, mkdir, rmdir)
15. SFTP file ops (stat, setstat, rename, remove)
16. SFTP extensions (posix-rename, statvfs)
17. SCP handler (source + sink modes)

### Phase 4 — Advanced Features
18. User permission enforcement
19. Quota tracking + enforcement
20. Bandwidth throttling
21. Session management
22. Audit engine + outputs (file, syslog, webhook)
23. Brute-force protection + IP banning

### Phase 5 — S3 Backend
24. S3 client (SigV4 signing)
25. S3 VFS backend (CRUD, listing, multipart)
26. S3 file metadata layer
27. Mount table (multi-backend per user)
28. Memory backend

### Phase 6 — WebUI & API
29. REST API router + JWT auth
30. User CRUD API endpoints
31. Session management API
32. Audit query API
33. File browser API
34. WebSocket live events
35. React 19 WebUI build + embed
36. Dashboard page
37. Users management page
38. Sessions page
39. File browser page
40. Audit log page
41. Configuration page

### Phase 7 — Operations
42. ACME client (auto-cert)
43. Prometheus metrics
44. Health check endpoint
45. LDAP integration
46. TOTP 2FA
47. MCP server
48. CLI commands (init, keygen, admin, migrate)
49. Migration tools (vsftpd, ProFTPD)
50. Docker build + systemd unit

---

## 11. CRITICAL IMPLEMENTATION NOTES

### 11.1 Buffer Pool
```go
var bufPool = sync.Pool{
    New: func() any {
        buf := make([]byte, 32*1024) // 32 KB
        return &buf
    },
}
```
Use everywhere for transfer buffers to minimize GC pressure under load.

### 11.2 Graceful Shutdown
Track all active transfers with `sync.WaitGroup`. On SIGTERM: stop accepting new connections, wait for active transfers (with timeout), then exit.

### 11.3 Zero-Copy Transfers
On Linux, use `syscall.Sendfile` for local-backend → FTP/SFTP data channel transfers. Fall back to userspace copy on other platforms.

### 11.4 S3 Write File Pattern
Buffer small writes (< multipart threshold) in memory, flush to S3 on Close(). For large files, start multipart upload after threshold exceeded, streaming parts as they accumulate.

### 11.5 Cross-Platform Considerations
- `sendfile` / `splice`: Linux only (use `golang.org/x/sys`)
- `statvfs`: Linux/macOS (different syscall paths)
- File permissions: Unix only (no-op on Windows)
- Windows service: Use `golang.org/x/sys/windows/svc`

### 11.6 Testing S3 Backend
Use MinIO in Docker for integration tests:
```bash
docker run -d --name minio -p 9000:9000 -p 9001:9001 \
  minio/minio server /data --console-address ":9001"
```

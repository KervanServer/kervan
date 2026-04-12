# KERVAN — Unified File Transfer Server

## SPECIFICATION v1.0

**Tagline:** "One Binary. Every Protocol. Total Control."

**Project:** Kervan (Turkish: *Caravan* — carries files across protocols, just as caravans carried goods across trade routes)

**Owner:** ECOSTACK TECHNOLOGY OÜ

**Language:** Go 1.23+

**License:** Open Source (MIT or Apache 2.0)

**Domain:** kervanserver.com

**GitHub:** github.com/kervanserver/kervan

---

## 1. VISION & POSITIONING

### 1.1 Problem Statement

Modern file transfer infrastructure is fragmented:

- **vsftpd** — FTP only, C codebase, CVE history, no WebUI, config-file hell
- **ProFTPD** — Bloated, module-based C, complex directives
- **OpenSSH** — SFTP/SCP as afterthought, no virtual users, no audit trail, no WebUI
- **FileZilla Server** — Windows-first, GUI-only management, limited automation
- **GoAnywhere / MOVEit** — Enterprise-only, $$$, proprietary, recent critical CVEs (MOVEit 2023)

**No single binary exists that unifies FTP + FTPS + SFTP + SCP with a modern WebUI, virtual filesystem, S3 backend, and zero-config deployment.**

### 1.2 Solution

**Kervan** is a single-binary, multi-protocol file transfer server written in pure Go:

- **4 protocols in 1 binary**: FTP, FTPS (explicit + implicit), SFTP, SCP
- **WebUI**: Embedded React dashboard for user/transfer/audit management
- **Virtual Filesystem**: Abstraction layer — local disk, S3-compatible, or memory
- **User Isolation**: Virtual users, per-user chroot, quota enforcement
- **Audit Everything**: Every file operation logged with structured events
- **S3 Backend**: Seamlessly store files on S3/MinIO/R2 behind FTP/SFTP interface
- **Zero External Dependencies**: Pure Go (only `golang.org/x/crypto` for SSH primitives)
- **Single Binary**: WebUI embedded via `embed.FS`, deploy with `./kervan`

### 1.3 Target Users

- DevOps / SREs replacing legacy vsftpd/ProFTPD
- Organizations needing audit-compliant file transfer
- Teams wanting S3-backed FTP/SFTP without commercial MFT products
- Developers needing quick local file transfer with management UI
- MSPs managing multi-tenant file transfer

### 1.4 Non-Goals

- Full SSH shell server (only SFTP/SCP subsystem)
- FTPS certificate authority / PKI management
- Built-in file sync / replication (use rsync, rclone)
- Email notifications (webhook-based, integrate externally)
- GUI desktop client

---

## 2. ARCHITECTURE OVERVIEW

```
┌──────────────────────────────────────────────────────────┐
│                      KERVAN BINARY                        │
│                                                          │
│  ┌─────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │   FTP   │  │  FTPS    │  │  SFTP    │  │   SCP    │  │
│  │ :2121   │  │  :990    │  │  :2222   │  │  :2222   │  │
│  │ RFC 959 │  │ RFC 4217 │  │ SSH Sub  │  │ SSH Sub  │  │
│  └────┬────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘  │
│       │            │             │              │         │
│       └────────────┴──────┬──────┴──────────────┘         │
│                           │                               │
│                    ┌──────┴──────┐                        │
│                    │   Session   │                        │
│                    │   Manager   │                        │
│                    └──────┬──────┘                        │
│                           │                               │
│       ┌───────────────────┼───────────────────┐          │
│       │                   │                   │          │
│  ┌────┴─────┐  ┌─────────┴────────┐  ┌──────┴──────┐   │
│  │  Auth    │  │  Virtual         │  │  Audit      │   │
│  │  Engine  │  │  Filesystem      │  │  Engine     │   │
│  │          │  │  (VFS)           │  │             │   │
│  └────┬─────┘  └─────────┬────────┘  └──────┬──────┘   │
│       │                  │                   │          │
│  ┌────┴─────┐     ┌──────┴──────┐     ┌─────┴──────┐   │
│  │ CobaltDB │     │  Storage    │     │ Structured │   │
│  │ (Users,  │     │  Backends   │     │ Log Output │   │
│  │  Config) │     │             │     │ (JSON/CEF) │   │
│  └──────────┘     │ ┌─────────┐ │     └────────────┘   │
│                   │ │  Local  │ │                       │
│                   │ │   FS    │ │                       │
│                   │ ├─────────┤ │                       │
│                   │ │   S3    │ │                       │
│                   │ │ Compat  │ │                       │
│                   │ ├─────────┤ │                       │
│                   │ │ Memory  │ │                       │
│                   │ └─────────┘ │                       │
│                   └─────────────┘                       │
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │              WebUI (embed.FS)                     │    │
│  │   React 19 · Dashboard · Users · Transfers · Logs │    │
│  │              REST API · WebSocket Live Events      │    │
│  └──────────────────────────────────────────────────┘    │
│                                                          │
│  ┌──────────────────────────────────────────────────┐    │
│  │              MCP Server (stdio)                    │    │
│  │   list_users · transfer_stats · audit_query        │    │
│  └──────────────────────────────────────────────────┘    │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

### 2.1 Core Modules

| Module | Responsibility |
|--------|----------------|
| `protocol/ftp` | FTP server (RFC 959), active/passive mode, MLSD/MLST |
| `protocol/ftps` | FTPS wrapper — explicit (AUTH TLS) + implicit (:990) |
| `protocol/sftp` | SFTP subsystem over SSH (draft-ietf-secsh-filexfer) |
| `protocol/scp` | SCP subsystem over SSH (legacy `scp` command compat) |
| `session` | Unified session manager, connection tracking, rate limiting |
| `auth` | Authentication engine — local, LDAP, OIDC, public key |
| `vfs` | Virtual filesystem interface + path resolution + chroot |
| `storage/local` | Local filesystem backend |
| `storage/s3` | S3-compatible backend (AWS, MinIO, Cloudflare R2) |
| `storage/memory` | In-memory backend (testing, temp storage) |
| `audit` | Structured event logging, query engine |
| `quota` | Per-user/group disk quota enforcement |
| `webui` | Embedded React 19 dashboard |
| `api` | REST API for management |
| `config` | YAML configuration + env override + runtime-safe reload |
| `mcp` | MCP server for AI/LLM integration |
| `cobalt` | CobaltDB integration for metadata persistence |

---

## 3. PROTOCOL SPECIFICATIONS

### 3.1 FTP Server (RFC 959 + Extensions)

**Port:** Default 2121 (unprivileged), configurable to 21

**Standards Compliance:**

| RFC | Feature | Status |
|-----|---------|--------|
| RFC 959 | Core FTP | Full |
| RFC 2228 | FTP Security Extensions | AUTH TLS |
| RFC 2389 | FEAT command | Full |
| RFC 2428 | EPSV / EPRT (IPv6) | Full |
| RFC 3659 | MLSD / MLST / SIZE / MDTM | Full |
| RFC 4217 | Securing FTP with TLS | Full |
| RFC 7151 | HOST command (virtual hosting) | Full |
| RFC 2640 | UTF-8 Support | Full |

**Supported Commands:**

```
Connection:    USER, PASS, QUIT, REIN, NOOP, SYST, FEAT, OPTS, HOST
Navigation:    CWD, CDUP, PWD, LIST, NLST, MLSD, MLST
Transfer:      RETR, STOR, STOU, APPE, REST, ABOR, TYPE, MODE, STRU
File Ops:      DELE, RMD, MKD, RNFR, RNTO, SITE, CHMOD, SIZE, MDTM
Passive Mode:  PASV, EPSV
Active Mode:   PORT, EPRT
Security:      AUTH, PBSZ, PROT, CCC
```

**Passive Port Range:** Configurable (default 50000–50100), critical for firewall/NAT environments.

**Transfer Modes:**
- Stream mode (default)
- Binary (Image) and ASCII type support
- REST (resume) for interrupted transfers

**Virtual Hosting:** HOST command support — multiple domains on same IP with per-host user namespaces.

### 3.2 FTPS (FTP over TLS)

**Two Modes:**

| Mode | Port | Behavior |
|------|------|----------|
| Explicit FTPS | 2121 (same as FTP) | Client sends `AUTH TLS` to upgrade |
| Implicit FTPS | 990 | TLS from connection start |

**TLS Configuration:**

```yaml
ftps:
  enabled: true
  mode: both           # explicit | implicit | both
  implicit_port: 990
  min_tls_version: "1.2"
  max_tls_version: "1.3"
  cert_file: "/etc/kervan/cert.pem"
  key_file: "/etc/kervan/key.pem"
  client_auth: none    # none | request | require
  client_ca_file: ""
  cipher_suites:       # empty = Go defaults (secure)
    - "TLS_AES_128_GCM_SHA256"
    - "TLS_AES_256_GCM_SHA384"
    - "TLS_CHACHA20_POLY1305_SHA256"
  auto_cert:
    enabled: false
    domains: ["ftp.example.com"]
    acme_email: "admin@example.com"
    acme_dir: "/etc/kervan/acme"
```

**Auto-TLS:** Built-in ACME client for Let's Encrypt / ZeroSSL (from-scratch, no external dep).

### 3.3 SFTP Server

**Transport:** SSH protocol (RFC 4253), from-scratch SSH server using `golang.org/x/crypto/ssh`.

**Port:** Default 2222, configurable to 22

**SFTP Protocol:** Implements draft-ietf-secsh-filexfer-02 (version 3, most widely compatible).

**Supported Operations:**

```
SSH_FXP_INIT, SSH_FXP_VERSION
SSH_FXP_OPEN, SSH_FXP_CLOSE, SSH_FXP_READ, SSH_FXP_WRITE
SSH_FXP_LSTAT, SSH_FXP_FSTAT, SSH_FXP_SETSTAT, SSH_FXP_FSETSTAT
SSH_FXP_OPENDIR, SSH_FXP_READDIR
SSH_FXP_REMOVE, SSH_FXP_MKDIR, SSH_FXP_RMDIR
SSH_FXP_REALPATH, SSH_FXP_STAT, SSH_FXP_RENAME
SSH_FXP_READLINK, SSH_FXP_SYMLINK
SSH_FXP_EXTENDED: posix-rename@openssh.com, statvfs@openssh.com,
                  hardlink@openssh.com, fsync@openssh.com
```

**Authentication Methods:**

| Method | Description |
|--------|-------------|
| Password | Virtual user password (bcrypt/argon2id hashed) |
| Public Key | Ed25519, RSA (2048+), ECDSA authorized keys |
| Keyboard-Interactive | Custom challenge/response (2FA support) |
| Certificate | SSH certificate authority |

**Host Keys:** Auto-generated on first run (Ed25519 + RSA 4096), stored in data directory.

**Shell Access:** Disabled by default. Only SFTP and SCP subsystems exposed. Optional restricted shell with configurable allowed commands.

### 3.4 SCP Server

**Transport:** Same SSH server as SFTP.

**Support:** Legacy `scp` command compatibility (source and sink modes).

```
scp file.txt user@kervan-host:/uploads/
scp user@kervan-host:/uploads/file.txt ./
scp -r user@kervan-host:/uploads/dir/ ./
```

**Features:**
- Recursive directory copy (`-r`)
- Preserve timestamps/permissions (`-p`)
- Bandwidth limiting (`-l`)
- Compression (`-C`, SSH-level)
- Operates through same VFS as SFTP

**Note:** SCP is deprecated in favor of SFTP by OpenSSH 9.0+, but many legacy systems and scripts still use it. Kervan maintains full compatibility.

---

## 4. VIRTUAL FILESYSTEM (VFS)

### 4.1 VFS Interface

The VFS is the central abstraction that decouples protocols from storage:

```go
type FileSystem interface {
    // File operations
    Open(name string, flags int, perm os.FileMode) (File, error)
    Stat(name string) (FileInfo, error)
    Lstat(name string) (FileInfo, error)
    Rename(oldname, newname string) error
    Remove(name string) error
    Mkdir(name string, perm os.FileMode) error
    MkdirAll(path string, perm os.FileMode) error
    ReadDir(name string) ([]DirEntry, error)
    Symlink(oldname, newname string) error
    Readlink(name string) (string, error)

    // Metadata
    Chmod(name string, mode os.FileMode) error
    Chown(name string, uid, gid int) error
    Chtimes(name string, atime, mtime time.Time) error

    // Capacity
    Statvfs(path string) (*StatVFS, error)
}

type File interface {
    io.Reader
    io.Writer
    io.Seeker
    io.Closer
    Sync() error
    Stat() (FileInfo, error)
    ReadDir(n int) ([]DirEntry, error)
    Truncate(size int64) error
}
```

### 4.2 Path Resolution & Chroot

Every user session operates within a **virtual root**:

```
Physical:  /data/users/john/uploads/report.pdf
User sees: /uploads/report.pdf

Physical:  /data/shared/public/readme.txt
User sees: /shared/readme.txt (if mounted)
```

**Path Security:**
- Symlink resolution within chroot only (no escape)
- Path traversal prevention (`../` normalized before resolution)
- Case sensitivity configurable per-backend
- Maximum path depth limit (default: 256 components)
- Forbidden characters enforcement

### 4.3 Mount Points

Users can have multiple storage backends mounted at different paths:

```yaml
users:
  - username: john
    home_dir: /
    mounts:
      - path: /                    # Root → local filesystem
        backend: local
        options:
          root: /data/users/john
      - path: /archive             # /archive → S3 bucket
        backend: s3
        options:
          bucket: john-archive
          prefix: files/
      - path: /shared              # /shared → shared local dir (read-only)
        backend: local
        options:
          root: /data/shared
          read_only: true
```

### 4.4 Storage Backends

#### 4.4.1 Local Filesystem Backend

```yaml
backend: local
options:
  root: /data/files         # Base directory
  create_root: true         # Create if missing
  permissions: "0755"       # Default dir permissions
  file_permissions: "0644"  # Default file permissions
  uid: 1000                 # OS-level file owner
  gid: 1000                 # OS-level file group
  allocate: true            # Pre-allocate disk space (fallocate)
  sync_writes: false        # fsync after every write
  temp_dir: ""              # Temp dir for atomic writes (same fs)
```

**Features:**
- Atomic writes via temp file + rename
- Sparse file support
- Extended attribute passthrough (optional)
- Disk space pre-allocation

#### 4.4.2 S3-Compatible Backend

```yaml
backend: s3
options:
  endpoint: "s3.amazonaws.com"    # Or MinIO/R2 endpoint
  region: "us-east-1"
  bucket: "my-files"
  prefix: "uploads/"              # Key prefix
  access_key: "${S3_ACCESS_KEY}"  # Env var expansion
  secret_key: "${S3_SECRET_KEY}"
  use_path_style: false           # true for MinIO
  disable_ssl: false
  multipart_threshold: "64MB"     # Switch to multipart above this
  multipart_chunk_size: "16MB"
  max_retries: 3
  upload_concurrency: 4
  download_concurrency: 4
  storage_class: "STANDARD"       # STANDARD | IA | GLACIER
  server_side_encryption: ""      # AES256 | aws:kms
  acl: "private"
```

**S3 VFS Mapping:**

| VFS Operation | S3 Operation |
|---------------|--------------|
| Open (read) | GetObject |
| Open (write) | PutObject / CreateMultipartUpload |
| Stat | HeadObject |
| ReadDir | ListObjectsV2 (delimiter=/) |
| Remove | DeleteObject |
| Mkdir | PutObject (key/) — empty marker |
| Rename | CopyObject + DeleteObject |
| Symlink | Not supported (error) |

**S3 Challenges Handled:**
- Directory emulation via `/` delimiter + empty markers
- Atomic rename via copy+delete (non-atomic, documented)
- Append operations via multipart upload continuation
- Large file streaming without full buffering (chunked upload)
- ETag-based consistency verification

#### 4.4.3 Memory Backend

```yaml
backend: memory
options:
  max_size: "256MB"         # Maximum total size
  max_files: 10000          # Maximum file count
```

For testing, temporary file staging, and ephemeral transfer areas.

### 4.5 File Metadata Layer

Since S3 doesn't have native POSIX metadata, Kervan maintains a metadata sidecar in CobaltDB:

```go
type FileMeta struct {
    Path        string      `json:"path"`
    Backend     string      `json:"backend"`
    Size        int64       `json:"size"`
    ModTime     time.Time   `json:"mod_time"`
    Permissions os.FileMode `json:"permissions"`
    Owner       string      `json:"owner"`
    Group       string      `json:"group"`
    Checksum    string      `json:"checksum"`   // SHA-256
    ContentType string      `json:"content_type"`
    Tags        map[string]string `json:"tags"`
}
```

---

## 5. AUTHENTICATION & USER MANAGEMENT

### 5.1 User Types

| Type | Storage | Use Case |
|------|---------|----------|
| Virtual | CobaltDB | Default, managed via WebUI/API |
| System | OS passwd/shadow | Map to OS users (Linux only) |
| LDAP | External LDAP/AD | Enterprise directory integration |
| OIDC | External IdP | SSO via OIDC (WebUI login only) |

### 5.2 Virtual User Schema

```go
type User struct {
    ID           string            `json:"id"`           // ULID
    Username     string            `json:"username"`
    PasswordHash string            `json:"password_hash"` // argon2id
    PublicKeys   []string          `json:"public_keys"`   // SSH authorized keys
    Email        string            `json:"email"`
    Status       UserStatus        `json:"status"`        // active | disabled | locked
    Role         Role              `json:"role"`          // admin | user | readonly
    HomeDir      string            `json:"home_dir"`
    Mounts       []MountConfig     `json:"mounts"`
    Permissions  UserPermissions   `json:"permissions"`
    Quota        QuotaConfig       `json:"quota"`
    RateLimit    RateLimitConfig   `json:"rate_limit"`
    AllowedIPs   []string          `json:"allowed_ips"`   // CIDR notation
    Protocols    []string          `json:"protocols"`     // ["ftp","sftp","scp","webui"]
    MaxSessions  int               `json:"max_sessions"`  // 0 = unlimited
    ExpiresAt    *time.Time        `json:"expires_at"`    // Account expiry
    Metadata     map[string]string `json:"metadata"`
    CreatedAt    time.Time         `json:"created_at"`
    UpdatedAt    time.Time         `json:"updated_at"`
    LastLoginAt  *time.Time        `json:"last_login_at"`
}

type UserPermissions struct {
    Upload       bool     `json:"upload"`
    Download     bool     `json:"download"`
    Delete       bool     `json:"delete"`
    Rename       bool     `json:"rename"`
    CreateDir    bool     `json:"create_dir"`
    ListDir      bool     `json:"list_dir"`
    Chmod        bool     `json:"chmod"`
    Overwrite    bool     `json:"overwrite"`
    DeniedExts   []string `json:"denied_exts"`    // [".exe", ".bat"]
    AllowedExts  []string `json:"allowed_exts"`   // Empty = all allowed
    MaxFileSize  int64    `json:"max_file_size"`   // Per-file limit (bytes)
}

type QuotaConfig struct {
    MaxStorage   int64 `json:"max_storage"`    // Total bytes
    MaxFiles     int64 `json:"max_files"`      // Total file count
    MaxBandwidth int64 `json:"max_bandwidth"`  // Bytes/second
}

type RateLimitConfig struct {
    MaxUploadKBps   int `json:"max_upload_kbps"`    // 0 = unlimited
    MaxDownloadKBps int `json:"max_download_kbps"`
    MaxConnections  int `json:"max_connections"`     // Per-user concurrent
    MaxLoginAttempts int `json:"max_login_attempts"` // Per minute
}
```

### 5.3 Group System

```go
type Group struct {
    ID          string          `json:"id"`
    Name        string          `json:"name"`
    Description string          `json:"description"`
    SharedDirs  []SharedDir     `json:"shared_dirs"`
    Permissions UserPermissions `json:"permissions"`   // Inherited by members
    Quota       QuotaConfig     `json:"quota"`         // Group-level quota
    Members     []string        `json:"members"`       // User IDs
}

type SharedDir struct {
    Path     string `json:"path"`     // Virtual path in each member's namespace
    Backend  string `json:"backend"`
    Options  map[string]string `json:"options"`
    ReadOnly bool   `json:"read_only"`
}
```

### 5.4 Authentication Flow

```
Client → Protocol Handler → Auth Engine → Provider Chain → Result
                                             │
                                    ┌────────┼────────┐
                                    │        │        │
                                  Local    LDAP    PublicKey
                                (CobaltDB)  (AD)    (SSH)
```

**Multi-Factor:** Keyboard-Interactive SFTP + TOTP (via authenticator app). WebUI supports TOTP natively.

**Account Locking:** After N failed attempts (configurable), account is locked for configurable duration. IP-based blocking after M failed attempts across all accounts.

### 5.5 LDAP Integration

```yaml
auth:
  ldap:
    enabled: true
    url: "ldaps://ldap.example.com:636"
    bind_dn: "cn=kervan,ou=services,dc=example,dc=com"
    bind_password: "${LDAP_BIND_PASSWORD}"
    base_dn: "ou=users,dc=example,dc=com"
    user_filter: "(&(objectClass=person)(sAMAccountName=%s))"
    group_filter: "(&(objectClass=group)(member=%s))"
    username_attribute: "sAMAccountName"
    email_attribute: "mail"
    group_attribute: "memberOf"
    group_mapping:
      "CN=FTP-Admins,OU=Groups,DC=example,DC=com": "admin"
      "CN=FTP-Users,OU=Groups,DC=example,DC=com": "user"
    default_home_dir: "/home/{username}"
    default_permissions:
      upload: true
      download: true
      delete: false
    cache_ttl: "5m"
    connection_pool_size: 10
    tls_skip_verify: false
```

---

## 6. AUDIT SYSTEM

### 6.1 Audit Events

Every file operation generates a structured audit event:

```go
type AuditEvent struct {
    ID          string            `json:"id"`           // ULID
    Timestamp   time.Time         `json:"timestamp"`
    EventType   AuditEventType    `json:"event_type"`
    Protocol    string            `json:"protocol"`     // ftp|ftps|sftp|scp|webui|api
    Username    string            `json:"username"`
    SessionID   string            `json:"session_id"`
    ClientIP    string            `json:"client_ip"`
    ClientPort  int               `json:"client_port"`
    ServerIP    string            `json:"server_ip"`
    Action      string            `json:"action"`       // upload|download|delete|rename|mkdir|login|...
    Path        string            `json:"path"`         // Virtual path
    TargetPath  string            `json:"target_path"`  // For rename operations
    Size        int64             `json:"size"`          // Bytes transferred
    Duration    time.Duration     `json:"duration"`
    StatusCode  int               `json:"status_code"`  // Protocol-specific
    Error       string            `json:"error"`
    Checksum    string            `json:"checksum"`     // SHA-256 of transferred file
    UserAgent   string            `json:"user_agent"`   // Client identification
    Metadata    map[string]string `json:"metadata"`
}
```

**Event Types:**

```
AUTH_LOGIN_SUCCESS    AUTH_LOGIN_FAILURE    AUTH_LOGOUT
AUTH_KEY_ACCEPTED     AUTH_KEY_REJECTED     AUTH_ACCOUNT_LOCKED

FILE_UPLOAD_START     FILE_UPLOAD_COMPLETE  FILE_UPLOAD_FAILED
FILE_DOWNLOAD_START   FILE_DOWNLOAD_COMPLETE FILE_DOWNLOAD_FAILED
FILE_DELETE           FILE_RENAME           FILE_CHMOD
FILE_MKDIR            FILE_RMDIR            FILE_SYMLINK

SESSION_OPEN          SESSION_CLOSE         SESSION_TIMEOUT
SESSION_RATE_LIMITED  SESSION_QUOTA_EXCEEDED

ADMIN_USER_CREATE     ADMIN_USER_UPDATE     ADMIN_USER_DELETE
ADMIN_CONFIG_CHANGE   ADMIN_SERVER_RESTART
```

### 6.2 Audit Output

Multiple concurrent outputs:

```yaml
audit:
  outputs:
    - type: file
      path: "/var/log/kervan/audit.json"  # JSON lines
      rotation:
        max_size: "100MB"
        max_age: "90d"
        max_backups: 10
        compress: true

    - type: syslog
      network: "udp"
      address: "syslog.example.com:514"
      facility: "local0"
      format: "cef"              # CEF (ArcSight compatible)

    - type: webhook
      url: "https://hooks.example.com/kervan"
      method: "POST"
      headers:
        Authorization: "Bearer ${WEBHOOK_TOKEN}"
      batch_size: 100
      flush_interval: "5s"
      retry_count: 3

    - type: database                # CobaltDB internal (for WebUI queries)
      retention: "365d"
      max_records: 10000000
```

### 6.3 Compliance Features

- **Immutable Logs:** Append-only log files with HMAC chain (each entry signs previous hash)
- **File Integrity:** SHA-256 checksum computed during transfer, stored in audit
- **Session Recording:** Optional full command recording for SFTP/FTP sessions
- **Retention Policies:** Configurable per-output, auto-purge old records
- **Export:** CSV/JSON export via WebUI and API

---

## 7. SESSION MANAGEMENT

### 7.1 Session Model

```go
type Session struct {
    ID            string        `json:"id"`             // ULID
    Username      string        `json:"username"`
    Protocol      string        `json:"protocol"`
    ClientIP      string        `json:"client_ip"`
    ClientPort    int           `json:"client_port"`
    ServerPort    int           `json:"server_port"`
    TLSVersion    string        `json:"tls_version"`    // If encrypted
    CipherSuite   string        `json:"cipher_suite"`
    AuthMethod    string        `json:"auth_method"`
    ConnectedAt   time.Time     `json:"connected_at"`
    LastActivity  time.Time     `json:"last_activity"`
    BytesUploaded int64         `json:"bytes_uploaded"`
    BytesDownloaded int64       `json:"bytes_downloaded"`
    FileCount     int           `json:"file_count"`
    CurrentDir    string        `json:"current_dir"`
    State         SessionState  `json:"state"`          // active | idle | transferring
}
```

### 7.2 Connection Management

- **Global Limits:** Maximum total connections (default: 1000)
- **Per-IP Limits:** Maximum connections from single IP (default: 10)
- **Per-User Limits:** Configurable per user/group
- **Idle Timeout:** Auto-disconnect after inactivity (default: 300s)
- **Transfer Timeout:** Maximum time for single transfer (default: 3600s)
- **Graceful Disconnect:** Finish active transfer before disconnecting on shutdown
- **Admin Kill:** Force-disconnect any session via WebUI/API

### 7.3 Bandwidth Throttling

Per-user, per-group, and global bandwidth limits:

```go
type Throttler struct {
    globalUpload    *rate.Limiter
    globalDownload  *rate.Limiter
    userUpload      map[string]*rate.Limiter
    userDownload    map[string]*rate.Limiter
}
```

Token bucket algorithm, configurable burst size. Rate limits apply across all protocols for the same user.

---

## 8. WebUI

### 8.1 Technology

- **Frontend:** React 19, embedded via `embed.FS`
- **API:** REST JSON API (same binary, same port or separate)
- **Auth:** JWT tokens (access + refresh), TOTP 2FA support
- **Real-time:** WebSocket for live events (transfers, connections)
- **Port:** Default 8443 (HTTPS) / 8080 (HTTP)

### 8.2 Dashboard Pages

| Page | Features |
|------|----------|
| **Dashboard** | Active sessions, transfer rates, storage usage, recent events, protocol breakdown chart |
| **Users** | CRUD users, bulk import/export, group management, permission matrix, quota settings |
| **Sessions** | Live session list, kill session, bandwidth per session, geo-IP display |
| **Transfers** | Active/completed/failed transfers, search/filter, retry failed, checksum verify |
| **File Browser** | Browse VFS per-user, upload/download via browser, preview (images/text), share links |
| **Audit Log** | Searchable audit trail, date range filter, export CSV/JSON, event type filter |
| **Configuration** | Edit server config, validate patches, and trigger runtime-safe reloads |
| **Monitoring** | CPU/memory/disk/network graphs, protocol stats, error rates, connection graph |
| **API Keys** | Manage API keys for automation, per-key permissions, usage stats |

### 8.3 WebUI File Browser

The WebUI includes a full file browser allowing admins to:

- Browse any user's VFS (admin only)
- Upload files via drag-and-drop (chunked upload, resume support)
- Download files (streaming, zip for directories)
- Preview files (images, text, PDF, video)
- Create/delete/rename files and directories
- Edit text files inline (CodeMirror)
- Generate temporary share links (configurable expiry)
- View file checksums and metadata

### 8.4 REST API

**Base URL:** `https://kervan-host:8443/api/v1`

**Authentication:** Bearer token (JWT) or API key

**Endpoints:**

```
# Server
GET    /api/v1/server/status
GET    /api/v1/server/config
PUT    /api/v1/server/config
POST   /api/v1/server/reload

# Users
GET    /api/v1/users
POST   /api/v1/users
GET    /api/v1/users/{id}
PUT    /api/v1/users/{id}
DELETE /api/v1/users/{id}
POST   /api/v1/users/{id}/disable
POST   /api/v1/users/{id}/enable
POST   /api/v1/users/{id}/reset-password
POST   /api/v1/users/import        # Bulk CSV/JSON import
GET    /api/v1/users/export        # Bulk export

# Groups
GET    /api/v1/groups
POST   /api/v1/groups
GET    /api/v1/groups/{id}
PUT    /api/v1/groups/{id}
DELETE /api/v1/groups/{id}

# Sessions
GET    /api/v1/sessions
GET    /api/v1/sessions/{id}
DELETE /api/v1/sessions/{id}        # Kill session

# Transfers
GET    /api/v1/transfers
GET    /api/v1/transfers/{id}

# Audit
GET    /api/v1/audit/events
GET    /api/v1/audit/events/{id}
GET    /api/v1/audit/export          # CSV/JSON export

# Files (Admin file browser API)
GET    /api/v1/files/{user}/ls?path=/
GET    /api/v1/files/{user}/stat?path=/file.txt
GET    /api/v1/files/{user}/download?path=/file.txt
POST   /api/v1/files/{user}/upload?path=/
DELETE /api/v1/files/{user}/rm?path=/file.txt
POST   /api/v1/files/{user}/mkdir?path=/newdir
POST   /api/v1/files/{user}/rename?from=/old&to=/new
POST   /api/v1/files/{user}/share?path=/file.txt&ttl=24h

# API Keys
GET    /api/v1/apikeys
POST   /api/v1/apikeys
DELETE /api/v1/apikeys/{id}

# Monitoring
GET    /api/v1/metrics               # Prometheus format
GET    /api/v1/health                 # Health check
GET    /api/v1/stats                  # Summary stats
```

### 8.5 WebSocket Events

```
ws://kervan-host:8443/api/v1/ws

Events:
  session.connected    { session_id, username, protocol, client_ip }
  session.disconnected { session_id, username, duration }
  transfer.started     { session_id, username, path, direction }
  transfer.progress    { session_id, path, bytes, total, percent }
  transfer.completed   { session_id, path, size, duration, checksum }
  transfer.failed      { session_id, path, error }
  auth.failed          { username, client_ip, reason }
  quota.warning        { username, usage_percent }
  server.alert         { level, message }
```

---

## 9. SECURITY

### 9.1 Password Hashing

Default: Argon2id with secure parameters:

```go
// Default parameters
const (
    ArgonTime    = 3
    ArgonMemory  = 64 * 1024  // 64 MB
    ArgonThreads = 4
    ArgonKeyLen  = 32
    ArgonSaltLen = 16
)
```

Bcrypt supported for migration compatibility.

### 9.2 TLS/SSH Security

**TLS Defaults:**
- Minimum TLS 1.2
- Preferred TLS 1.3
- Only AEAD cipher suites
- OCSP stapling support
- Auto-cert via ACME

**SSH Defaults:**
- Host key algorithms: Ed25519, RSA (4096-bit)
- Key exchange: curve25519-sha256, diffie-hellman-group16-sha512
- Ciphers: chacha20-poly1305, aes256-gcm, aes128-gcm
- MACs: hmac-sha2-256-etm, hmac-sha2-512-etm
- No legacy algorithms (3DES, SHA1, MD5 disabled)

### 9.3 IP-Based Security

```yaml
security:
  allowed_ips: []              # Global whitelist (empty = all)
  denied_ips: []               # Global blacklist
  geo_blocking:
    enabled: false
    mode: allow                # allow | deny
    countries: ["US", "EU"]    # ISO 3166-1 alpha-2
    geoip_db: "/etc/kervan/GeoLite2-Country.mmdb"
  brute_force:
    enabled: true
    max_attempts: 5            # Per username
    lockout_duration: "15m"
    ip_ban_threshold: 20       # Per IP across all users
    ip_ban_duration: "1h"
    whitelist_ips: []           # Never ban these
```

### 9.4 File Security

- **Antivirus Integration:** ClamAV socket scanner (optional, scan on upload)
- **File Type Validation:** Magic byte verification (not just extension)
- **Filename Sanitization:** Strip/reject dangerous characters, null bytes
- **Hidden Files:** Configurable visibility of dotfiles
- **Symlink Policy:** Follow | restrict-to-chroot | deny

---

## 10. CONFIGURATION

### 10.1 Configuration File

Default path: `/etc/kervan/kervan.yaml` or `./kervan.yaml`

```yaml
# kervan.yaml — Complete configuration reference

server:
  name: "Kervan File Server"
  listen_address: "0.0.0.0"
  pid_file: "/var/run/kervan.pid"
  data_dir: "/var/lib/kervan"        # CobaltDB + metadata
  log_level: "info"                   # debug | info | warn | error
  log_format: "json"                  # json | text
  log_file: "/var/log/kervan/kervan.log"
  graceful_shutdown_timeout: "30s"

ftp:
  enabled: true
  port: 2121
  banner: "Welcome to Kervan File Server"
  passive_port_range: "50000-50100"
  passive_ip: ""                      # Public IP for passive mode (auto-detect if empty)
  active_mode: true                   # Allow active mode
  ascii_transfer: true                # Allow ASCII mode
  max_connections: 500
  idle_timeout: "300s"
  transfer_timeout: "3600s"

ftps:
  enabled: true
  mode: both
  implicit_port: 990
  min_tls_version: "1.2"
  cert_file: ""
  key_file: ""
  auto_cert:
    enabled: false
    domains: []
    acme_email: ""

sftp:
  enabled: true
  port: 2222
  host_key_dir: "/var/lib/kervan/host_keys"
  host_key_algorithms: ["ed25519", "rsa"]
  max_connections: 500
  idle_timeout: "300s"
  max_packet_size: 34000              # SFTP packet size
  disable_shell: true                 # No shell access

scp:
  enabled: true                       # Shares port with SFTP

webui:
  enabled: true
  port: 8443
  tls: true                           # Auto-uses FTPS cert or generates self-signed
  bind_address: "0.0.0.0"
  admin_username: "admin"
  admin_password: ""                  # Set on first run, forced change
  session_timeout: "24h"
  totp_enabled: false
  cors_origins: []

auth:
  default_provider: "local"           # local | ldap
  password_hash: "argon2id"           # argon2id | bcrypt
  min_password_length: 8
  require_special_char: false
  ldap: {}                            # See LDAP section

storage:
  default_backend: "local"
  backends:
    local:
      root: "/data/kervan"
      create_root: true
    s3:
      endpoint: ""
      region: ""
      bucket: ""
      access_key: ""
      secret_key: ""

quota:
  enabled: true
  default_max_storage: "1GB"
  default_max_files: 100000
  check_interval: "60s"              # Periodic quota recalculation

audit:
  enabled: true
  outputs: []                         # See Audit section

security:
  allowed_ips: []
  denied_ips: []
  brute_force:
    enabled: true
    max_attempts: 5
    lockout_duration: "15m"

mcp:
  enabled: true
  transport: "stdio"
```

### 10.2 Environment Variable Override

Every config key can be overridden via environment variable:

```
KERVAN_FTP_PORT=21
KERVAN_SFTP_PORT=22
KERVAN_WEBUI_ADMIN_PASSWORD=secretpass
KERVAN_STORAGE_S3_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
```

Pattern: `KERVAN_{SECTION}_{KEY}` (uppercase, underscores).

### 10.3 Runtime Config Reload

`POST /api/v1/server/reload` reloads the config file from disk, validates it,
and applies only runtime-safe settings immediately.

Currently reloadable without restart:

- `auth.min_password_length`
- `webui.session_timeout`
- `webui.totp_enabled`
- `webui.cors_origins`
- `security.brute_force.enabled`
- `security.brute_force.max_attempts`
- `security.brute_force.lockout_duration`

The response reports `applied_paths` and `restart_paths` so operators can see
which edits took effect immediately and which still require a restart.

**Restart required:** listener/port changes, storage backend changes, protocol
enable/disable, TLS material changes, and other server bootstrap settings.

---

## 11. MCP SERVER

### 11.1 Tools

```
kervan_list_users         List all users with status, quota usage
kervan_get_user           Get user details by username
kervan_create_user        Create new virtual user
kervan_update_user        Update user settings
kervan_list_sessions      List active sessions
kervan_kill_session       Force-disconnect a session
kervan_transfer_stats     Get transfer statistics (period, protocol)
kervan_audit_query        Search audit events
kervan_server_status      Get server health and metrics
kervan_list_files         Browse user's VFS
kervan_quota_report       Get quota usage report
```

### 11.2 Resources

```
kervan://server/status        Server health and metrics
kervan://server/config        Current configuration (redacted secrets)
kervan://users                User list
kervan://sessions             Active sessions
kervan://audit/recent         Recent audit events
kervan://transfers/active     Active transfers
```

---

## 12. DEPLOYMENT

### 12.1 Single Binary

```bash
# Download
curl -L https://github.com/kervanserver/kervan/releases/latest/download/kervan-linux-amd64 -o kervan
chmod +x kervan

# Run with defaults (creates config on first run)
./kervan

# Run with config
./kervan --config /etc/kervan/kervan.yaml

# Init config (generates default config file)
./kervan init --config /etc/kervan/kervan.yaml

# Generate host keys
./kervan keygen --output /var/lib/kervan/host_keys/

# Create admin user
./kervan admin create --username admin --password secret

# Version info
./kervan version
```

### 12.2 Docker

```dockerfile
FROM scratch
COPY kervan /kervan
EXPOSE 2121 990 2222 8443 50000-50100
VOLUME ["/data", "/etc/kervan"]
ENTRYPOINT ["/kervan"]
CMD ["--config", "/etc/kervan/kervan.yaml"]
```

```yaml
# docker-compose.yml
services:
  kervan:
    image: ghcr.io/kervanserver/kervan:latest
    ports:
      - "2121:2121"       # FTP
      - "990:990"         # FTPS Implicit
      - "2222:2222"       # SFTP/SCP
      - "8443:8443"       # WebUI
      - "50000-50100:50000-50100"  # Passive FTP
    volumes:
      - ./config:/etc/kervan
      - ./data:/data
    environment:
      - KERVAN_WEBUI_ADMIN_PASSWORD=changeme
```

### 12.3 systemd Service

```ini
[Unit]
Description=Kervan File Transfer Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/kervan --config /etc/kervan/kervan.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
User=kervan
Group=kervan
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/kervan /var/log/kervan /data

[Install]
WantedBy=multi-user.target
```

### 12.4 Supported Platforms

| OS | Architecture | Status |
|----|-------------|--------|
| Linux | amd64, arm64 | Primary |
| macOS | amd64 (Intel), arm64 (Apple Silicon) | Full |
| Windows | amd64 | Full (no systemd, Windows Service support) |
| FreeBSD | amd64, arm64 | Best-effort |

---

## 13. PERFORMANCE TARGETS

| Metric | Target |
|--------|--------|
| Concurrent connections | 10,000+ |
| FTP command latency | < 1ms |
| SFTP packet processing | < 0.5ms |
| Transfer throughput (local) | Near wire speed (10 Gbps with sufficient IO) |
| Transfer throughput (S3) | Limited by S3 API / network |
| Memory per connection | < 256 KB (idle) |
| Startup time | < 500ms |
| Binary size | < 30 MB |
| WebUI bundle | < 2 MB (gzipped) |

### 13.1 Optimization Strategies

- **Zero-copy:** `sendfile(2)` / `splice(2)` for local → socket transfers on Linux
- **Buffer pooling:** `sync.Pool` for read/write buffers to minimize GC pressure
- **Connection pooling:** Reuse S3 HTTP connections
- **Async audit:** Non-blocking audit event channel with batched writes
- **mmap:** Optional memory-mapped file access for local backend
- **Goroutine per-connection:** Leveraging Go's lightweight concurrency model

---

## 14. TESTING STRATEGY

| Level | Scope | Tools |
|-------|-------|-------|
| Unit | VFS, auth, quota, path resolution | Go testing, table-driven |
| Integration | Protocol compliance, S3 backend | TestContainers (MinIO), real FTP/SFTP clients |
| E2E | Full transfer workflows | FileZilla, `sftp`, `scp`, `curl`, `lftp` |
| Compliance | RFC conformance | Dedicated FTP/SFTP test suites |
| Security | Pen testing, fuzzing | `go-fuzz`, custom protocol fuzzers |
| Performance | Load testing | Custom Go load generator, `iperf` |

### 14.1 Protocol Compliance Matrix

| Client | FTP | FTPS | SFTP | SCP |
|--------|-----|------|------|-----|
| FileZilla | ✓ | ✓ | ✓ | — |
| WinSCP | ✓ | ✓ | ✓ | ✓ |
| OpenSSH sftp | — | — | ✓ | ✓ |
| curl | ✓ | ✓ | ✓ | ✓ |
| lftp | ✓ | ✓ | ✓ | — |
| Cyberduck | ✓ | ✓ | ✓ | — |
| Transmit (macOS) | ✓ | ✓ | ✓ | — |
| Python paramiko | — | — | ✓ | — |
| Python ftplib | ✓ | ✓ | — | — |

---

## 15. DIRECTORY STRUCTURE

```
kervan/
├── cmd/
│   └── kervan/
│       └── main.go                 # Entry point, CLI commands
├── internal/
│   ├── config/
│   │   ├── config.go               # YAML config struct + parsing
│   │   ├── defaults.go             # Default values
│   │   ├── validate.go             # Config validation
│   │   └── env.go                  # Environment variable overlay
│   ├── protocol/
│   │   ├── ftp/
│   │   │   ├── server.go           # FTP listener + connection handler
│   │   │   ├── handler.go          # Command dispatcher
│   │   │   ├── commands.go         # FTP command implementations
│   │   │   ├── transfer.go         # Data connection (active/passive)
│   │   │   ├── tls.go              # FTPS (AUTH TLS, implicit)
│   │   │   └── mlst.go             # MLSD/MLST formatting
│   │   ├── sftp/
│   │   │   ├── server.go           # SSH server + SFTP subsystem
│   │   │   ├── handler.go          # SFTP packet handler
│   │   │   ├── packets.go          # SFTP packet types + encoding
│   │   │   └── extensions.go       # OpenSSH extensions
│   │   └── scp/
│   │       ├── server.go           # SCP subsystem handler
│   │       ├── source.go           # SCP source mode (server → client)
│   │       └── sink.go             # SCP sink mode (client → server)
│   ├── vfs/
│   │   ├── vfs.go                  # VFS interface definitions
│   │   ├── resolver.go             # Path resolution + chroot
│   │   ├── mount.go                # Mount table management
│   │   └── metadata.go             # File metadata layer
│   ├── storage/
│   │   ├── local/
│   │   │   └── local.go            # Local filesystem backend
│   │   ├── s3/
│   │   │   ├── client.go           # From-scratch S3 client (SigV4)
│   │   │   ├── backend.go          # S3 VFS implementation
│   │   │   └── multipart.go        # Multipart upload handler
│   │   └── memory/
│   │       └── memory.go           # In-memory backend
│   ├── auth/
│   │   ├── auth.go                 # Auth engine + provider interface
│   │   ├── local.go                # CobaltDB user provider
│   │   ├── ldap.go                 # LDAP/AD provider
│   │   ├── publickey.go            # SSH public key auth
│   │   ├── totp.go                 # TOTP 2FA
│   │   └── password.go             # Argon2id / bcrypt hashing
│   ├── session/
│   │   ├── manager.go              # Session lifecycle
│   │   ├── throttle.go             # Bandwidth throttling
│   │   └── limiter.go              # Connection limits
│   ├── audit/
│   │   ├── engine.go               # Audit event dispatcher
│   │   ├── event.go                # Event types + schema
│   │   ├── file.go                 # File output (JSON lines)
│   │   ├── syslog.go               # Syslog output (CEF format)
│   │   ├── webhook.go              # Webhook output (batched)
│   │   └── db.go                   # CobaltDB output (queryable)
│   ├── quota/
│   │   └── quota.go                # Quota tracking + enforcement
│   ├── api/
│   │   ├── router.go               # REST API router
│   │   ├── middleware.go           # JWT auth, CORS, rate limit
│   │   ├── handlers_users.go       # User CRUD handlers
│   │   ├── handlers_sessions.go    # Session handlers
│   │   ├── handlers_transfers.go   # Transfer handlers
│   │   ├── handlers_audit.go       # Audit handlers
│   │   ├── handlers_files.go       # File browser handlers
│   │   ├── handlers_server.go      # Server status/config
│   │   └── websocket.go           # WebSocket event stream
│   ├── webui/
│   │   ├── embed.go                # embed.FS for React build
│   │   └── handler.go              # SPA handler (fallback to index.html)
│   ├── mcp/
│   │   ├── server.go               # MCP server (stdio)
│   │   ├── tools.go                # Tool definitions
│   │   └── resources.go            # Resource definitions
│   ├── acme/
│   │   └── acme.go                 # ACME client (Let's Encrypt)
│   ├── crypto/
│   │   ├── argon2.go               # Argon2id (from-scratch or x/crypto)
│   │   ├── tls.go                  # TLS configuration builder
│   │   └── ssh.go                  # SSH key generation
│   └── cobalt/
│       └── store.go                # CobaltDB integration
├── webui/                          # React 19 frontend source
│   ├── src/
│   │   ├── pages/
│   │   │   ├── Dashboard.tsx
│   │   │   ├── Users.tsx
│   │   │   ├── Sessions.tsx
│   │   │   ├── Transfers.tsx
│   │   │   ├── FileBrowser.tsx
│   │   │   ├── AuditLog.tsx
│   │   │   ├── Configuration.tsx
│   │   │   ├── Monitoring.tsx
│   │   │   └── ApiKeys.tsx
│   │   ├── components/
│   │   ├── hooks/
│   │   ├── api/
│   │   └── App.tsx
│   ├── package.json
│   └── vite.config.ts
├── scripts/
│   ├── build.sh                    # Cross-compile for all platforms
│   └── generate_webui.go           # Build React + embed
├── kervan.example.yaml             # Example configuration
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── README.md
└── LICENSE
```

---

## 16. DEPENDENCY POLICY

### Allowed External Dependencies

| Dependency | Purpose | Justification |
|-----------|---------|---------------|
| `golang.org/x/crypto` | SSH protocol, Argon2id, curve25519 | Standard Go extended lib, crypto primitives |
| `golang.org/x/sys` | OS-level syscalls (sendfile, splice) | Standard Go extended lib |

### Everything Else: From Scratch

| Component | Built In-House |
|-----------|----------------|
| FTP server | Full RFC 959 implementation |
| FTPS | TLS wrapper using `crypto/tls` |
| SFTP | Using `golang.org/x/crypto/ssh` |
| SCP | SSH subsystem handler |
| S3 client | SigV4 signing, HTTP client |
| ACME client | RFC 8555 implementation |
| HTTP router | Custom router for REST API |
| WebSocket | RFC 6455 implementation |
| JWT | Token generation/validation |
| LDAP client | Basic LDAP bind + search |
| TOTP | RFC 6238 implementation |
| Syslog client | RFC 5424 / CEF format |
| CobaltDB | Embedded database (existing) |
| YAML parser | `gopkg.in/yaml.v3` (only if needed) |

---

## 17. CLI COMMANDS

```bash
kervan                              # Start server (default config)
kervan --config /path/to/config.yaml  # Start with specific config
kervan init                         # Generate default config
kervan init --config /etc/kervan/kervan.yaml
kervan keygen                       # Generate SSH host keys
kervan keygen --type ed25519 --output /path/to/keys/
kervan admin create                 # Create admin user (interactive)
kervan admin create --username admin --password pass
kervan admin reset-password --username admin
kervan user list                    # List all users
kervan user create --username john --password secret
kervan user delete --username john
kervan user import --file users.csv
kervan user export --format json --output users.json
kervan status                       # Show server status (connects to running instance)
kervan version                      # Version + build info
kervan check                        # Validate config file
kervan migrate                      # Migrate from vsftpd/ProFTPD config
```

### 17.1 Migration Tools

```bash
# Import vsftpd virtual users
kervan migrate vsftpd --user-db /etc/vsftpd/virtual_users.db

# Import ProFTPD users
kervan migrate proftpd --config /etc/proftpd/proftpd.conf

# Import OpenSSH authorized_keys for SFTP users
kervan migrate ssh-keys --authorized-keys-dir /home/*/.ssh/
```

---

## 18. MONITORING & OBSERVABILITY

### 18.1 Prometheus Metrics

Exposed at `GET /api/v1/metrics`:

```
# Connections
kervan_connections_total{protocol="ftp|ftps|sftp|scp"} counter
kervan_connections_active{protocol="ftp|ftps|sftp|scp"} gauge
kervan_connections_rejected_total{reason="limit|auth|ip"} counter

# Transfers
kervan_transfers_total{direction="upload|download",protocol="..."} counter
kervan_transfers_active gauge
kervan_transfer_bytes_total{direction="upload|download"} counter
kervan_transfer_duration_seconds histogram
kervan_transfer_errors_total{type="timeout|io|quota"} counter

# Auth
kervan_auth_attempts_total{result="success|failure",method="password|key|ldap"} counter
kervan_auth_locked_accounts gauge

# Storage
kervan_storage_bytes_used{backend="local|s3",user="..."} gauge
kervan_storage_files_total{backend="local|s3"} gauge
kervan_quota_usage_ratio{user="..."} gauge

# System
kervan_uptime_seconds gauge
kervan_goroutines gauge
kervan_memory_bytes gauge
```

### 18.2 Health Check

```
GET /api/v1/health

{
  "status": "healthy",
  "checks": {
    "ftp": { "status": "up", "port": 2121 },
    "ftps": { "status": "up", "port": 990 },
    "sftp": { "status": "up", "port": 2222 },
    "storage_local": { "status": "up", "free_bytes": 107374182400 },
    "storage_s3": { "status": "up", "latency_ms": 45 },
    "cobaltdb": { "status": "up" }
  },
  "version": "1.0.0",
  "uptime": "72h15m30s"
}
```

---

## 19. VERSIONING & RELEASE

- **Versioning:** Semantic versioning (semver)
- **Release Cycle:** Monthly minor releases, patches as needed
- **Binaries:** Cross-compiled for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- **Container:** Multi-arch Docker image (amd64 + arm64)
- **Checksums:** SHA-256 for all release artifacts
- **Signing:** GPG-signed releases

---

## 20. FUTURE CONSIDERATIONS (v2+)

- **FTPS Client Auth:** Mutual TLS with client certificates
- **Cluster Mode:** Multi-node Kervan with shared CobaltDB (Raft)
- **AS2 Protocol:** EDI file transfer (enterprise B2B)
- **WebDAV:** HTTP-based file access
- **Event Hooks:** Pre/post transfer scripts/webhooks
- **Encryption at Rest:** Transparent file encryption per-user
- **Geo-Replication:** S3 cross-region replication coordination
- **WASM Plugins:** Custom auth/transform logic via wazero

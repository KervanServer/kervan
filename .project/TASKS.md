# KERVAN â€” TASKS

## Implementation Task List v1.0

**Total Tasks:** 127
**Estimated Duration:** 12â€“14 weeks (solo developer, full-time)

---

## PHASE 1 â€” FOUNDATION (Tasks 1â€“18)

> Config, database, VFS core, local backend, build system

### Task 1: Project Scaffold
- [ ] `go mod init github.com/kervanserver/kervan`
- [ ] Create full directory structure per SPECIFICATION Â§15
- [ ] `go get golang.org/x/crypto@latest && go get golang.org/x/sys@latest`
- [ ] Create `internal/build/build.go` with Version/Commit/Date ldflags
- [ ] Create `Makefile` with build/test/release/clean targets
- [ ] Create `.gitignore` (bin/, internal/webui/dist/, *.db)

### Task 2: Config Struct Definitions
- [ ] Define all config structs in `internal/config/config.go`: ServerConfig, FTPConfig, FTPSConfig, SFTPConfig, SCPConfig, WebUIConfig, AuthConfig, LDAPConfig, StorageConfig, BackendConfig, QuotaConfig, AuditConfig, AuditOutput, RotationConfig, SecurityConfig, GeoBlockConfig, BruteForceConfig, MCPConfig
- [ ] Implement `time.Duration` YAML unmarshaling for string durations ("300s", "15m", "24h")
- [ ] Implement byte size parsing for string sizes ("1GB", "256MB", "100KB")

### Task 3: Config Defaults
- [ ] Implement `DefaultConfig()` in `internal/config/defaults.go`
- [ ] All default values per SPECIFICATION Â§10.1
- [ ] FTP port 2121, SFTP port 2222, WebUI port 8443
- [ ] Default passive range 50000â€“50100
- [ ] Default Argon2id, min password length 8
- [ ] Default brute force: 5 attempts, 15m lockout

### Task 4: Config Loader
- [ ] Implement `Load(path)` â€” read YAML file, expand env vars, unmarshal, validate
- [ ] Implement `expandEnvVars()` â€” `${VAR_NAME}` pattern with `os.Expand`
- [ ] Implement `OverlayEnv()` â€” `KERVAN_SECTION_KEY` env var override pattern
- [ ] Support `--config` CLI flag and fallback to `./kervan.yaml`
- [ ] Generate default config file on `kervan init`

### Task 5: Config Validation
- [ ] Implement `Validate()` in `internal/config/validate.go`
- [ ] Validate port ranges (1â€“65535)
- [ ] Validate passive port range format and bounds (1024â€“65535, start â‰¤ end)
- [ ] Validate FTPS mode enum (explicit|implicit|both)
- [ ] Validate cert_file required when FTPS enabled and auto_cert disabled
- [ ] Validate password_hash enum (argon2id|bcrypt)
- [ ] Validate min_password_length â‰¥ 4
- [ ] Validate IP/CIDR notation in allowed/denied lists
- [ ] Validate log_level enum (debug|info|warn|error)
- [ ] Return aggregated error list (not fail-fast)

### Task 6: Runtime Config Reload
- [x] Expose `POST /api/v1/server/reload` for admin-triggered config reload
- [x] Return `applied_paths` / `restart_paths` to distinguish safe runtime changes
- [x] Apply runtime-safe settings without restart (`webui.*`, `auth.min_password_length`, `security.brute_force.*`)
- [x] Validate config from disk before applying updates
- [ ] Expand reload coverage to more subsystems without reintroducing startup-only state

### Task 7: Structured Logger
- [ ] Implement structured logger in `internal/logger/logger.go`
- [ ] Support JSON and text output formats
- [ ] Log levels: debug, info, warn, error
- [ ] Fields: timestamp, level, component, message, extra key-value pairs
- [ ] Log file output with configurable path
- [ ] Stderr fallback when log file unavailable
- [ ] Thread-safe (no external dep â€” use `sync.Mutex` + `encoding/json`)

### Task 8: ULID Generator
- [ ] Implement ULID generation in `internal/id/ulid.go`
- [ ] Monotonic within same millisecond
- [ ] crypto/rand entropy source
- [ ] Crockford Base32 encoding
- [ ] No external dependency

### Task 9: CobaltDB Store Layer
- [ ] Implement `internal/cobalt/store.go` â€” Open, Close, Put, Get, Delete, List
- [ ] Collection-based key prefix pattern: `{collection}:{key}`
- [ ] JSON marshaling/unmarshaling for values
- [ ] `PrefixScan` for listing collection entries
- [ ] `Query` with filter function for complex lookups
- [ ] Secondary index support pattern: `{collection}:idx:{field}:{value}` â†’ primary key

### Task 10: User Model
- [ ] Define `User` struct in `internal/auth/user.go` per SPECIFICATION Â§5.2
- [ ] Define `UserPermissions` struct (upload, download, delete, rename, createDir, listDir, chmod, overwrite, deniedExts, allowedExts, maxFileSize)
- [ ] Define `QuotaConfig` struct (maxStorage, maxFiles, maxBandwidth)
- [ ] Define `RateLimitConfig` struct
- [ ] Define `UserStatus` enum (active, disabled, locked)
- [ ] Define `Role` enum (admin, user, readonly)
- [ ] `CanUseProtocol(proto string) bool` method

### Task 11: User Repository
- [ ] Implement `UserRepository` in `internal/auth/user_repo.go`
- [ ] `Create(user)` â€” ULID generation, unique username check, store + index
- [ ] `GetByID(id)` â€” direct lookup
- [ ] `GetByUsername(username)` â€” index lookup â†’ ID â†’ get
- [ ] `Update(user)` â€” update timestamp, store
- [ ] `Delete(id)` â€” remove index + record
- [ ] `List()` â€” prefix scan, deserialize all users
- [ ] `UpdateLastLogin(id)` â€” set LastLoginAt timestamp
- [ ] `Count()` â€” total user count

### Task 12: Group Model & Repository
- [ ] Define `Group` struct per SPECIFICATION Â§5.3
- [ ] Define `SharedDir` struct
- [ ] Implement `GroupRepository` â€” CRUD + member management
- [ ] `AddMember(groupID, userID)`, `RemoveMember(groupID, userID)`
- [ ] `GetGroupsForUser(userID)` â€” reverse lookup

### Task 13: VFS Interface Definitions
- [ ] Define `FileSystem` interface in `internal/vfs/vfs.go` â€” all methods per IMPLEMENTATION Â§4.1
- [ ] Define `File` interface (Reader, ReaderAt, Writer, WriterAt, Seeker, Closer, Stat, Sync, Truncate, ReadDir, Name)
- [ ] Define `StatVFS` struct
- [ ] Define `FileInfo` wrapper struct with all fields
- [ ] Define VFS error types: ErrPathEscape, ErrPathTooDeep, ErrForbiddenChar, ErrForbiddenExtension, ErrNoMount, ErrReadOnly, ErrQuotaExceeded

### Task 14: Path Resolver
- [ ] Implement `Resolver` in `internal/vfs/resolver.go`
- [ ] `Resolve(virtualPath)` â€” clean, validate, depth check, forbidden char check
- [ ] `ResolvePair(from, to)` â€” for rename ops
- [ ] Max depth constant (256)
- [ ] Null byte rejection
- [ ] `path.Clean("/" + virtualPath)` normalization
- [ ] Test: `../` escape attempts, deep nesting, null bytes, empty paths, double slashes

### Task 15: Mount Table
- [ ] Implement `MountTable` in `internal/vfs/mount.go`
- [ ] `Mount(virtualPath, backend, readOnly)` â€” add + sort by path length desc
- [ ] `Lookup(virtualPath)` â€” longest prefix match, return (backend, relativePath, readOnly)
- [ ] `ListMountPoints(dir)` â€” child mounts visible at directory level
- [ ] Thread-safe (RWMutex)
- [ ] Test: nested mounts, root mount, overlapping paths

### Task 16: User VFS (Composite)
- [ ] Implement `UserVFS` in `internal/vfs/user_vfs.go`
- [ ] Wire resolver â†’ mount table â†’ backend for every operation
- [ ] Permission checks: upload, download, delete, rename, createDir, listDir, chmod
- [ ] Read-only mount enforcement
- [ ] File extension filtering (allowed/denied lists)
- [ ] Max file size check on write
- [ ] Quota file wrapper (track bytes written)
- [ ] Cross-mount rename (copy + delete)
- [ ] ReadDir merging (backend entries + child mount points)

### Task 17: Local Filesystem Backend
- [ ] Implement `Backend` in `internal/storage/local/local.go`
- [ ] `physicalPath(name)` â€” join root + name, `filepath.Abs`, `isSubPath` escape check
- [ ] Open, Stat, Lstat, Rename, Remove, RemoveAll, Mkdir, MkdirAll, ReadDir
- [ ] Symlink, Readlink (convert physical â†’ VFS path on readlink)
- [ ] Chmod, Chown, Chtimes
- [ ] `Statvfs` â€” Linux: `syscall.Statfs`, macOS: `syscall.Statfs`, Windows: stub
- [ ] `localFile` wrapper with optional sync-on-write
- [ ] CreateRoot option (MkdirAll on init)
- [ ] Configurable file/dir permissions, uid/gid
- [ ] Safety: never remove root directory
- [ ] Test: path escape, basic CRUD, concurrent access

### Task 18: Memory Backend
- [ ] Implement `Backend` in `internal/storage/memory/memory.go`
- [ ] In-memory file tree (map-based)
- [ ] Max size limit, max file count limit
- [ ] All VFS interface methods
- [ ] Thread-safe (RWMutex)
- [ ] Useful for testing and ephemeral storage

---

## PHASE 2 â€” FTP SERVER (Tasks 19â€“36)

> Full FTP + FTPS implementation

### Task 19: FTP Server Listener
- [ ] Implement `Server` in `internal/protocol/ftp/server.go`
- [ ] TCP listener on configured port
- [ ] `acceptLoop()` â€” accept, check connection limit, spawn handler goroutine
- [ ] Connection counting with `atomic.Int64`
- [ ] `sync.WaitGroup` for graceful shutdown
- [ ] Context-based cancellation
- [ ] `Start()` and `Stop()` methods

### Task 20: FTP Connection Handler
- [ ] Implement `connectionHandler` in `internal/protocol/ftp/handler.go`
- [ ] Buffered reader/writer (4KB)
- [ ] `serve()` â€” banner â†’ command loop with idle timeout
- [ ] `parseCommand(line)` â€” split CMD ARGS
- [ ] `dispatch(cmd, args)` â€” route to handler methods
- [ ] Pre-auth commands: USER, PASS, AUTH, FEAT, QUIT, SYST, NOOP, OPTS, PBSZ, PROT, HOST
- [ ] Post-auth command routing
- [ ] `reply(code, message)` â€” single-line response
- [ ] `replyMultiline(code, lines)` â€” multi-line response
- [ ] `close()` â€” cleanup data connections, passive listeners

### Task 21: FTP Session State
- [ ] Define `ftpSession` struct â€” id, username, authenticated, vfs, cwd, dataConn, passiveListener, dataType, renameFrom, restOffset, tlsUpgraded, lastActivity, ctx/cancel
- [ ] Session ID generation (ULID)
- [ ] Register/deregister with session manager

### Task 22: FTP Authentication Commands
- [ ] `handleUSER(args)` â€” store username, reply 331
- [ ] `handlePASS(args)` â€” authenticate via auth engine, setup UserVFS, audit event
- [ ] Auth failure: audit event, reply 530
- [ ] Protocol permission check (`user.CanUseProtocol("ftp")`)
- [ ] Session setup on success: vfs, cwd="/", reply 230

### Task 23: FTP Navigation Commands
- [ ] `handlePWD()` â€” reply 257 with quoted CWD
- [ ] `handleCWD(args)` â€” resolve path, stat, verify isDir, update CWD
- [ ] `handleCDUP()` â€” delegate to CWD("..")
- [ ] `resolvePath(p)` â€” absolute vs relative against CWD

### Task 24: FTP Data Connection (Passive Mode)
- [ ] Passive port range tracking with `atomic.Int64` round-robin
- [ ] `handlePASV()` â€” listen on next passive port, reply 227 with h1,h2,h3,h4,p1,p2
- [ ] `handleEPSV()` â€” listen on next passive port, reply 229 with (|||port|)
- [ ] Passive IP auto-detection (configurable override for NAT)
- [ ] `openDataConnection()` â€” accept from passive listener with 30s timeout
- [ ] Cleanup: close passive listener after accept

### Task 25: FTP Data Connection (Active Mode)
- [ ] `handlePORT(args)` â€” parse h1,h2,h3,h4,p1,p2, connect to client
- [ ] `handleEPRT(args)` â€” parse |proto|addr|port|, connect to client
- [ ] Active mode security: optional disable, IP validation (only to client IP)
- [ ] `openDataConnection()` â€” return active connection if set

### Task 26: FTP Directory Listing (LIST)
- [ ] `handleLIST(args)` â€” ReadDir from VFS, format Unix ls -l, send via data connection
- [ ] `formatLIST(info)` â€” permission string + links + owner + group + size + date + name
- [ ] Handle `-a`, `-l` flags (ignore, list all)
- [ ] Reply 150 before data, 226 after complete

### Task 27: FTP Machine-Readable Listing (MLSD/MLST)
- [ ] `handleMLSD(args)` â€” ReadDir, format facts, send via data connection
- [ ] `handleMLST(args)` â€” single entry, send in control channel (reply 250)
- [ ] `formatMLST(info)` â€” `type=file;size=12345;modify=20240101120000;perm=rfwdcm; filename`
- [ ] Fact types: type (file/dir/cdir/pdir), size, modify, perm, unique
- [ ] Permission mapping: `r`=read, `w`=write, `f`=rename, `d`=delete, `c`=createDir, `m`=chmod

### Task 28: FTP File Download (RETR)
- [ ] `handleRETR(args)` â€” open file, open data conn, stream file â†’ data conn
- [ ] Resume support: apply `restOffset` with Seek before copy
- [ ] Reset restOffset after use
- [ ] Reply 150 with filename and size, reply 226 on complete
- [ ] Audit event: FileDownloadComplete (size, duration) or FileDownloadFailed
- [ ] Transfer timeout enforcement

### Task 29: FTP File Upload (STOR/STOU/APPE)
- [ ] `handleSTOR(args)` â€” create/truncate file, stream data conn â†’ file
- [ ] `handleSTOU()` â€” unique filename generation, reply 150 with generated name
- [ ] `handleAPPE(args)` â€” open with O_APPEND, stream data conn â†’ file
- [ ] Quota check before/during upload
- [ ] Audit event: FileUploadComplete or FileUploadFailed
- [ ] Transfer timeout enforcement

### Task 30: FTP File Operations
- [ ] `handleDELE(args)` â€” vfs.Remove + audit event
- [ ] `handleMKD(args)` â€” vfs.Mkdir + reply 257 with quoted path
- [ ] `handleRMD(args)` â€” vfs.Remove (directory)
- [ ] `handleRNFR(args)` â€” store renameFrom, reply 350
- [ ] `handleRNTO(args)` â€” vfs.Rename(renameFrom, to) + audit event, clear renameFrom
- [ ] `handleSITE(args)` â€” SITE CHMOD support

### Task 31: FTP Metadata Commands
- [ ] `handleSIZE(args)` â€” vfs.Stat, reply 213 with size
- [ ] `handleMDTM(args)` â€” vfs.Stat, reply 213 with YYYYMMDDhhmmss
- [ ] `handleTYPE(args)` â€” set A (ASCII) or I (Binary)
- [ ] `handleREST(args)` â€” parse offset, store in session
- [ ] `handleABOR()` â€” close active data connection

### Task 32: FTP Feature Negotiation
- [ ] `handleFEAT()` â€” list all supported extensions (AUTH TLS, MLSD, SIZE, MDTM, REST STREAM, EPSV, EPRT, HOST, UTF8)
- [ ] `handleOPTS(args)` â€” handle OPTS UTF8 ON
- [ ] `handleSYST()` â€” reply 215 "UNIX Type: L8"
- [ ] `handleNOOP()` â€” reply 200
- [ ] `handleHOST(args)` â€” virtual hosting support (store hostname in session)

### Task 33: FTP NLST Command
- [ ] `handleNLST(args)` â€” ReadDir, send filenames only (no metadata), one per line
- [ ] Used by legacy clients and scripts

### Task 34: FTPS â€” Explicit Mode (AUTH TLS)
- [ ] `handleAUTH(args)` â€” accept TLS/SSL, reply 234, upgrade conn with `tls.Server`
- [ ] Reset reader/writer after TLS upgrade
- [ ] Track `tlsUpgraded` state
- [ ] `handlePBSZ(args)` â€” reply 200 "PBSZ=0" (required for PROT)
- [ ] `handlePROT(args)` â€” P (private) or C (clear) data channel protection

### Task 35: FTPS â€” Implicit Mode
- [ ] Separate `tls.Listen` on implicit port (default 990)
- [ ] `acceptLoop` with implicitTLS=true flag
- [ ] Connection handler auto-sets `tlsUpgraded = true`
- [ ] Same command handling as explicit mode

### Task 36: FTPS â€” TLS Configuration
- [ ] Build `tls.Config` from FTPSConfig: min/max version, cipher suites, cert/key
- [ ] Certificate loading from file
- [ ] Client auth modes: none, request, require (with CA file)
- [ ] TLS version mapping: "1.2" â†’ `tls.VersionTLS12`, "1.3" â†’ `tls.VersionTLS13`
- [ ] Data channel TLS wrapping for passive/active connections when PROT P

---

## PHASE 3 â€” SSH PROTOCOLS (Tasks 37â€“52)

> SFTP + SCP over SSH

### Task 37: SSH Host Key Management
- [ ] Implement `internal/crypto/ssh.go`
- [ ] `GenerateEd25519Key()` â€” generate + marshal to PEM
- [ ] `GenerateRSAKey(bits)` â€” generate 4096-bit RSA + marshal to PEM
- [ ] `LoadHostKeys(dir)` â€” read all key files from directory
- [ ] `LoadOrGenerate(dir, algorithms)` â€” load existing or generate on first run
- [ ] Key file naming: `host_key_ed25519`, `host_key_rsa`
- [ ] File permissions check (warn if not 0600)

### Task 38: SSH Server Foundation
- [ ] Implement SSH server in `internal/protocol/sftp/server.go`
- [ ] `ssh.ServerConfig` with PasswordCallback, PublicKeyCallback, KeyboardInteractiveCallback
- [ ] Algorithm configuration: key exchange, ciphers, MACs per SPECIFICATION Â§9.2
- [ ] TCP listener on SFTP port
- [ ] `ssh.NewServerConn()` handshake
- [ ] Discard global requests
- [ ] Channel handler: accept "session" type, reject others

### Task 39: SSH Authentication â€” Password
- [ ] PasswordCallback: delegate to auth engine `Authenticate(username, password)`
- [ ] Store user ID in `ssh.Permissions.Extensions`
- [ ] Audit events: AuthLoginSuccess / AuthLoginFailure
- [ ] Protocol permission check

### Task 40: SSH Authentication â€” Public Key
- [ ] PublicKeyCallback: delegate to auth engine `AuthenticatePublicKey(username, key)`
- [ ] `AuthenticatePublicKey` implementation: lookup user, compare authorized keys
- [ ] SSH public key parsing (Ed25519, RSA, ECDSA)
- [ ] `authorized_keys` format parsing and matching
- [ ] Audit events: AuthKeyAccepted / AuthKeyRejected

### Task 41: SSH Authentication â€” Keyboard-Interactive
- [ ] KeyboardInteractiveCallback implementation
- [ ] Password prompt as first challenge
- [ ] Optional TOTP prompt as second challenge (when user has 2FA enabled)
- [ ] Challenge/response flow via `ssh.KeyboardInteractiveChallenge`

### Task 42: SSH Session Handling
- [ ] `handleSession()` â€” process channel requests
- [ ] "subsystem" request â†’ route "sftp" to SFTP handler
- [ ] "exec" request â†’ detect SCP command, route to SCP handler
- [ ] "shell" request â†’ reject when DisableShell=true
- [ ] Build UserVFS for authenticated user
- [ ] Proper channel closure after handler completes

### Task 43: SFTP Packet I/O
- [ ] Implement `readPacket()` â€” 4-byte length header + type byte + payload
- [ ] Implement `writePacket(type, payload)` â€” length prefix + type + payload
- [ ] Max packet size enforcement (default 34000)
- [ ] Packet too large error handling
- [ ] Binary marshaling helpers: marshalUint32, marshalUint64, marshalString, marshalAttrs
- [ ] Binary unmarshaling helpers: unmarshalUint32, unmarshalUint64, unmarshalString, unmarshalAttrs

### Task 44: SFTP Init/Version
- [ ] `handleInit(payload)` â€” read client version
- [ ] Reply SSH_FXP_VERSION with server version (3)
- [ ] Advertise extensions: posix-rename@openssh.com, statvfs@openssh.com, hardlink@openssh.com, fsync@openssh.com

### Task 45: SFTP Handle Management
- [ ] `newHandle(file, path, isDir)` â€” generate handle string, store in map
- [ ] `getHandle(id)` â€” lookup by handle string
- [ ] `closeHandle(id)` â€” remove from map, close file
- [ ] Sequential handle ID generation (`h1`, `h2`, ...)
- [ ] Mutex-protected handle map

### Task 46: SFTP File Operations â€” Open/Close/Read/Write
- [ ] `handleOpen(payload)` â€” unmarshal path + pflags + attrs, convert flags to `os.O_*`, open via VFS, return handle
- [ ] `handleClose(payload)` â€” close handle, audit on write handles
- [ ] `handleRead(payload)` â€” unmarshal handle + offset + length, ReadAt, return SSH_FXP_DATA or SSH_FX_EOF
- [ ] `handleWrite(payload)` â€” unmarshal handle + offset + data, WriteAt, return status
- [ ] SFTP flags â†’ Go flags mapping: SSH_FXF_READ, SSH_FXF_WRITE, SSH_FXF_APPEND, SSH_FXF_CREAT, SSH_FXF_TRUNC, SSH_FXF_EXCL

### Task 47: SFTP Stat Operations
- [ ] `handleStat(payload)` â€” vfs.Stat, return SSH_FXP_ATTRS
- [ ] `handleLstat(payload)` â€” vfs.Lstat, return SSH_FXP_ATTRS
- [ ] `handleFstat(payload)` â€” file.Stat via handle, return SSH_FXP_ATTRS
- [ ] `handleSetstat(payload)` â€” unmarshal attrs, apply chmod/chown/chtimes
- [ ] `handleFsetstat(payload)` â€” same via file handle
- [ ] `marshalFileInfo(os.FileInfo)` â€” convert to SFTP attrs binary format
- [ ] Attrs flags: SSH_FILEXFER_ATTR_SIZE, SSH_FILEXFER_ATTR_UIDGID, SSH_FILEXFER_ATTR_PERMISSIONS, SSH_FILEXFER_ATTR_ACMODTIME

### Task 48: SFTP Directory Operations
- [ ] `handleOpendir(payload)` â€” vfs.Open (as dir), return handle
- [ ] `handleReaddir(payload)` â€” ReadDir via handle, return SSH_FXP_NAME with entries, SSH_FX_EOF when done
- [ ] Long name format for READDIR: permissions + links + owner + group + size + date + name
- [ ] Handle dirRead state (only read once, then EOF)
- [ ] `handleMkdir(payload)` â€” vfs.Mkdir, return status
- [ ] `handleRmdir(payload)` â€” vfs.Remove, return status

### Task 49: SFTP Path Operations
- [ ] `handleRealpath(payload)` â€” resolve path via VFS resolver, return SSH_FXP_NAME with single entry
- [ ] `handleRename(payload)` â€” vfs.Rename + audit event
- [ ] `handleRemove(payload)` â€” vfs.Remove + audit event
- [ ] `handleReadlink(payload)` â€” vfs.Readlink, return SSH_FXP_NAME
- [ ] `handleSymlink(payload)` â€” vfs.Symlink, return status

### Task 50: SFTP Extensions
- [ ] `handleExtended(payload)` â€” route by extension name
- [ ] `posix-rename@openssh.com` â€” atomic rename via VFS + audit
- [ ] `statvfs@openssh.com` â€” VFS.Statvfs, marshal StatVFS response
- [ ] `hardlink@openssh.com` â€” if backend supports it
- [ ] `fsync@openssh.com` â€” file.Sync via handle
- [ ] Unknown extension: SSH_FX_OP_UNSUPPORTED

### Task 51: SCP Source Mode (Server â†’ Client)
- [ ] Implement `internal/protocol/scp/source.go`
- [ ] Parse SCP command flags: `-f` (from/source), `-r` (recursive), `-p` (preserve times)
- [ ] Single file send: `C<mode> <size> <name>\n` + data + `\0`
- [ ] Directory send: `D<mode> 0 <name>\n` ... `E\n`
- [ ] Timestamp preservation: `T<mtime> 0 <atime> 0\n`
- [ ] Wait for ACK (0x00) after each protocol message
- [ ] Audit events for each file downloaded

### Task 52: SCP Sink Mode (Client â†’ Server)
- [ ] Implement `internal/protocol/scp/sink.go`
- [ ] Parse SCP command flags: `-t` (to/sink), `-r` (recursive), `-d` (directory)
- [ ] Receive `C` command: parse mode/size/name, read exact bytes, write to VFS
- [ ] Receive `D` command: create directory, recurse
- [ ] Receive `E` command: pop directory level
- [ ] Receive `T` command: store timestamps for next file
- [ ] Send ACK (0x00) after each successful operation
- [ ] Send error (0x01 + message) on failure
- [ ] Quota enforcement during receive
- [ ] Audit events for each file uploaded

---

## PHASE 4 â€” SECURITY & SESSION (Tasks 53â€“69)

> Auth engine, quota, throttling, brute force, audit outputs

### Task 53: Password Hashing â€” Argon2id
- [ ] Implement `internal/auth/password.go`
- [ ] `HashPassword(password)` â€” Argon2id with params: time=3, memory=64MB, threads=4, keyLen=32, saltLen=16
- [ ] `VerifyPassword(hash, password)` â€” parse stored params + salt, recompute, constant-time compare
- [ ] Encoded format: `$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>` (PHC string format)
- [ ] `crypto/rand` for salt generation

### Task 54: Password Hashing â€” Bcrypt
- [ ] `HashPasswordBcrypt(password)` â€” using `golang.org/x/crypto/bcrypt`
- [ ] `VerifyPasswordBcrypt(hash, password)` â€” bcrypt.CompareHashAndPassword
- [ ] Auto-detect hash format (bcrypt vs argon2id) in verify

### Task 55: Auth Engine
- [ ] Implement `internal/auth/engine.go`
- [ ] `Authenticate(username, password)` â€” provider chain: local â†’ LDAP
- [ ] `AuthenticatePublicKey(username, key)` â€” public key lookup
- [ ] `GetUserByID(id)` â€” from repository
- [ ] Provider interface: `type Provider interface { Authenticate(username, password) (*User, error) }`
- [ ] Chain multiple providers, first success wins
- [ ] Account status check (active only, not disabled/locked)
- [ ] Account expiry check (ExpiresAt)
- [ ] Allowed IP check (AllowedIPs CIDR matching)

### Task 56: VFS Builder
- [ ] Implement `buildUserVFS(user)` function
- [ ] Parse user mount configs â†’ instantiate backends
- [ ] Create MountTable, mount all configured paths
- [ ] Apply group shared directories (merge group mounts)
- [ ] Create QuotaTracker for user
- [ ] Return assembled UserVFS

### Task 57: Quota Tracker
- [ ] Implement `internal/quota/quota.go`
- [ ] `QuotaTracker` â€” track bytes used + file count per user
- [ ] `Check(additionalBytes)` â€” return error if would exceed quota
- [ ] `Add(bytes)` / `Remove(bytes)` â€” update usage
- [ ] `Usage()` â€” return current usage stats
- [ ] Periodic recalculation (walk VFS, update stored usage)
- [ ] Quota file wrapper: intercept Write() calls, update tracker

### Task 58: Bandwidth Throttler
- [ ] Implement `internal/session/throttle.go`
- [ ] Token bucket algorithm (from scratch, no `golang.org/x/time/rate`)
- [ ] Global upload/download limits
- [ ] Per-user upload/download limits
- [ ] `ThrottledReader` â€” wraps `io.Reader`, blocks when rate exceeded
- [ ] `ThrottledWriter` â€” wraps `io.Writer`, blocks when rate exceeded
- [ ] Configurable burst size (default: 2Ã— rate)
- [ ] Apply at data connection level in FTP and at SFTP read/write

### Task 59: Session Manager
- [ ] Implement `internal/session/manager.go`
- [ ] `Register(session)` â€” add to active map
- [ ] `Deregister(id)` â€” remove from active map
- [ ] `Get(id)` â€” lookup by session ID
- [ ] `List()` â€” return all active sessions
- [ ] `ListByUser(username)` â€” filter by user
- [ ] `ListByIP(ip)` â€” filter by client IP
- [ ] `Kill(id)` â€” force close session (cancel context)
- [ ] `Count()` â€” total active sessions
- [ ] Global connection limit enforcement
- [ ] Per-IP connection limit enforcement
- [ ] Per-user connection limit enforcement

### Task 60: Brute Force Protection
- [ ] Implement `internal/security/bruteforce.go`
- [ ] Per-username attempt tracking (sliding window, last N minutes)
- [ ] Account lockout after max_attempts: set user status to "locked"
- [ ] Auto-unlock after lockout_duration
- [ ] Per-IP attempt tracking across all usernames
- [ ] IP ban after ip_ban_threshold: add to dynamic deny list
- [ ] IP ban auto-expire after ip_ban_duration
- [ ] Whitelist IPs exempt from banning
- [ ] Thread-safe (sync.Map or mutex)

### Task 61: IP Access Control
- [ ] Implement `internal/security/ipfilter.go`
- [ ] Global allowed/denied IP lists (CIDR matching)
- [ ] Per-user allowed IP list
- [ ] `Check(ip, username)` â†’ allow/deny
- [ ] CIDR parsing via `net.ParseCIDR` / `net.IP.Mask`
- [ ] Denied list takes precedence over allowed list
- [ ] Integrate with connection accept (reject before handshake)

### Task 62: Audit Engine Core
- [ ] Implement `internal/audit/engine.go`
- [ ] Buffered event channel (configurable size, default 10000)
- [ ] Async `processLoop()` goroutine â€” drain channel, write to all outputs
- [ ] `Emit(event)` â€” non-blocking send (drop on full buffer with warning)
- [ ] `Close()` â€” signal shutdown, drain remaining events, close outputs
- [ ] Event struct per SPECIFICATION Â§6.1

### Task 63: Audit Output â€” JSON File
- [ ] Implement `internal/audit/file.go`
- [ ] JSON Lines format (one JSON object per line)
- [ ] Log rotation: max_size, max_age, max_backups, compress
- [ ] Size-based rotation: check size before write, rotate if exceeded
- [ ] Age-based rotation: track creation time, rotate if expired
- [ ] Gzip compression of rotated files
- [ ] Backup cleanup (keep max_backups)

### Task 64: Audit Output â€” Syslog
- [ ] Implement `internal/audit/syslog.go`
- [ ] UDP and TCP syslog client (from scratch)
- [ ] RFC 5424 format support
- [ ] CEF (Common Event Format) for ArcSight compatibility
- [ ] Configurable facility and severity mapping
- [ ] Connection retry on failure

### Task 65: Audit Output â€” Webhook
- [ ] Implement `internal/audit/webhook.go`
- [ ] HTTP POST with JSON body
- [ ] Batch mode: accumulate events, flush at batch_size or flush_interval
- [ ] Configurable headers (for auth tokens)
- [ ] Retry with exponential backoff (up to retry_count)
- [ ] Non-blocking: buffer events internally

### Task 66: Audit Output â€” CobaltDB
- [ ] Implement `internal/audit/db.go`
- [ ] Write events to CobaltDB for WebUI query
- [ ] Retention policy: auto-delete events older than configured duration
- [ ] Max records limit: delete oldest when exceeded
- [ ] Query support: by date range, event type, username, path

### Task 67: Audit Immutable Chain
- [ ] HMAC chain: each event includes hash of previous event
- [ ] `sha256(prev_hash + event_json)` chaining
- [ ] First event: prev_hash = all zeros
- [ ] Verify chain integrity API endpoint
- [ ] Detect tampering via chain break

### Task 68: File Integrity â€” SHA-256
- [ ] Compute SHA-256 during file transfer (streaming, no re-read)
- [ ] `HashingReader` / `HashingWriter` â€” tee data through hash
- [ ] Store checksum in audit event
- [ ] Store checksum in file metadata (CobaltDB)
- [ ] Verify API: recompute and compare

### Task 69: TOTP 2FA
- [ ] Implement `internal/auth/totp.go` per RFC 6238
- [ ] HMAC-SHA1 based OTP generation
- [ ] 30-second time step, 6-digit code
- [ ] Secret key generation (160-bit, base32 encoded)
- [ ] QR code URL generation (`otpauth://totp/Kervan:{username}?secret={secret}&issuer=Kervan`)
- [ ] Clock skew tolerance (Â±1 step)
- [ ] Used code tracking (prevent replay within window)
- [ ] Integrate with keyboard-interactive SSH and WebUI login

---

## PHASE 5 â€” S3 BACKEND (Tasks 70â€“82)

> S3-compatible storage backend

### Task 70: S3 Client â€” Core
- [ ] Implement `internal/storage/s3/client.go`
- [ ] HTTP client with connection pooling (`http.Transport`)
- [ ] URL building: virtual-hosted style vs path style
- [ ] Error response parsing (XML body â†’ Go error)
- [ ] Retry logic with exponential backoff
- [ ] Request timeout configuration

### Task 71: S3 Client â€” SigV4 Signing
- [ ] `signRequest(req, payload)` â€” full AWS Signature V4 implementation
- [ ] Step 1: Canonical request (method, path, query, headers, payload hash)
- [ ] Step 2: String to sign (algorithm, date, credential scope, canonical hash)
- [ ] Step 3: Signing key derivation (HMAC chain: secret â†’ date â†’ region â†’ service â†’ signing)
- [ ] Step 4: Signature (HMAC-SHA256)
- [ ] Step 5: Authorization header construction
- [ ] `x-amz-date` and `x-amz-content-sha256` headers
- [ ] Test against known AWS test vectors

### Task 72: S3 Client â€” Object Operations
- [ ] `GetObject(bucket, key)` â†’ response body + metadata
- [ ] `PutObject(bucket, key, body, size, contentType)` â†’ error
- [ ] `HeadObject(bucket, key)` â†’ content-length, last-modified, etag
- [ ] `DeleteObject(bucket, key)` â†’ error
- [ ] `CopyObject(srcBucket, srcKey, dstBucket, dstKey)` â†’ error
- [ ] `DeleteObjects(bucket, keys)` â†’ batch delete (up to 1000)

### Task 73: S3 Client â€” Listing
- [ ] `ListObjectsV2(bucket, prefix, delimiter, maxKeys)` â†’ objects + common prefixes
- [ ] `ListObjectsV2WithToken(bucket, prefix, delimiter, maxKeys, token)` â†’ pagination
- [ ] Response XML parsing: Contents (Key, Size, LastModified, ETag), CommonPrefixes, IsTruncated, NextContinuationToken
- [ ] From-scratch XML parser for S3 responses (minimal, targeted)

### Task 74: S3 Client â€” Multipart Upload
- [ ] `CreateMultipartUpload(bucket, key, contentType)` â†’ uploadID
- [ ] `UploadPart(bucket, key, uploadID, partNumber, body)` â†’ ETag
- [ ] `CompleteMultipartUpload(bucket, key, uploadID, parts)` â†’ error
- [ ] `AbortMultipartUpload(bucket, key, uploadID)` â†’ error
- [ ] Part tracking: store partNumber â†’ ETag for completion

### Task 75: S3 VFS Backend â€” Read Operations
- [ ] `Open(name, O_RDONLY)` â†’ S3ReadFile wrapping GetObject response body
- [ ] `Stat(name)` â†’ HeadObject, fallback to directory check via ListObjectsV2
- [ ] `ReadDir(name)` â†’ ListObjectsV2 with delimiter, paginate, merge files + directories
- [ ] Directory detection: trailing `/` key or has children via ListObjectsV2

### Task 76: S3 VFS Backend â€” Write Operations
- [ ] `Open(name, O_WRONLY|O_CREATE)` â†’ S3WriteFile (buffer in temp â†’ PutObject on Close)
- [ ] `Open(name, O_APPEND)` â†’ S3AppendFile (download existing + append + re-upload)
- [ ] S3WriteFile: track written bytes, multipart upload if exceeds threshold
- [ ] S3WriteFile.Close(): flush buffer to S3 (PutObject or CompleteMultipartUpload)
- [ ] Temp file or in-memory buffer decision based on size

### Task 77: S3 VFS Backend â€” Mutations
- [ ] `Mkdir(name)` â€” PutObject with trailing `/` key (empty marker)
- [ ] `Remove(name)` â€” DeleteObject
- [ ] `RemoveAll(name)` â€” ListObjectsV2 + DeleteObjects (batch, paginated)
- [ ] `Rename(old, new)` â€” CopyObject + DeleteObject (NOT atomic, documented)
- [ ] `Lstat` â†’ alias for Stat (no symlinks on S3)
- [ ] `Symlink` â†’ return os.ErrInvalid
- [ ] `Chmod/Chown/Chtimes` â†’ no-op (store in metadata layer if needed)
- [ ] `Statvfs` â†’ return effectively unlimited values

### Task 78: S3 File Metadata Layer
- [ ] Store POSIX-like metadata in CobaltDB for S3-backed files
- [ ] Key: `file_meta:{backend}:{path}` â†’ FileMeta struct
- [ ] Update on write: permissions, owner, group, checksum, content_type
- [ ] Read on stat: merge S3 metadata (size, modtime) with stored metadata
- [ ] Cleanup: remove metadata on delete
- [ ] Optional: skip metadata layer for pure S3 usage

### Task 79: S3 Streaming Upload
- [ ] Implement chunked upload without full buffering
- [ ] Buffer configurable chunk size (default 16MB)
- [ ] Start multipart upload on first chunk
- [ ] Upload parts concurrently (configurable concurrency, default 4)
- [ ] Complete multipart on Close()
- [ ] Abort multipart on error
- [ ] Track total bytes for quota enforcement

### Task 80: S3 Error Handling
- [ ] Parse S3 XML error responses: Code, Message, RequestId
- [ ] Map S3 errors to VFS errors: NoSuchKey â†’ os.ErrNotExist, AccessDenied â†’ os.ErrPermission
- [ ] Retry on 5xx errors and throttling (503 SlowDown, 429)
- [ ] Log S3 request IDs for debugging

### Task 81: S3 Connection Testing
- [ ] `TestConnection(bucket)` â€” HeadBucket or ListObjectsV2 with maxKeys=1
- [ ] Called on startup to verify S3 config
- [ ] Called on config reload to verify new S3 config
- [ ] Report clear error message on auth failure, bucket not found, network error

### Task 82: Multi-Backend Mount Integration
- [ ] Wire storage backend factory: `NewBackend(type, options)` â†’ FileSystem
- [ ] Support `local`, `s3`, `memory` types
- [ ] User mount config â†’ instantiate backend + mount in MountTable
- [ ] Cross-mount rename: detect different backends, copy file stream + delete source
- [ ] ReadDir merge: backend entries + visible child mount points
- [ ] Test: user with `/` â†’ local, `/archive` â†’ S3, `/shared` â†’ local read-only

---

## PHASE 6 â€” WEBUI & API (Tasks 83â€“112)

> REST API, WebSocket, React 19 WebUI

### Task 83: HTTP Router
- [ ] Implement custom HTTP router in `internal/api/router.go`
- [ ] Method-based routing (GET, POST, PUT, DELETE)
- [ ] Path parameter extraction (`/users/{id}`)
- [ ] Route grouping with prefix (`/api/v1/`)
- [ ] Middleware chain support
- [ ] 404 and 405 default handlers

### Task 84: JWT Authentication Middleware
- [ ] Implement JWT in `internal/api/jwt.go`
- [ ] Token generation: access token (15min) + refresh token (7d)
- [ ] HMAC-SHA256 signing (from scratch â€” header.payload.signature)
- [ ] Claims: sub (user ID), role, exp, iat, jti
- [ ] Token validation middleware: extract from Authorization Bearer header
- [ ] Refresh endpoint: validate refresh token, issue new access token
- [ ] Token revocation: store revoked JTIs in CobaltDB with TTL

### Task 85: API Key Authentication
- [ ] API key model: id, key_hash, name, permissions, user_id, created_at, last_used
- [ ] Key generation: `kvn_` prefix + 32 bytes crypto/rand (base64)
- [ ] Store hash only (SHA-256)
- [ ] Auth middleware: check `X-API-Key` header or `api_key` query param
- [ ] Per-key permission scoping

### Task 86: API Middleware Stack
- [ ] CORS middleware: configurable origins, methods, headers
- [ ] Rate limiting middleware: token bucket per-IP
- [ ] Request logging middleware: method, path, status, duration
- [ ] Recovery middleware: catch panics, return 500
- [ ] Content-Type enforcement (application/json)

### Task 87: User CRUD API
- [ ] `GET /api/v1/users` â€” list all users (admin only), pagination
- [ ] `POST /api/v1/users` â€” create user (admin only), validate input
- [ ] `GET /api/v1/users/{id}` â€” get user details
- [ ] `PUT /api/v1/users/{id}` â€” update user
- [ ] `DELETE /api/v1/users/{id}` â€” delete user
- [ ] `POST /api/v1/users/{id}/disable` â€” disable user account
- [ ] `POST /api/v1/users/{id}/enable` â€” enable user account
- [ ] `POST /api/v1/users/{id}/reset-password` â€” reset password
- [ ] Input validation: username format, email, password strength
- [ ] Response: JSON with appropriate HTTP status codes

### Task 88: User Bulk Operations API
- [ ] `POST /api/v1/users/import` â€” bulk create from CSV or JSON upload
- [ ] CSV format: username, password, email, role, home_dir, quota
- [ ] JSON format: array of user objects
- [ ] Validation per-row, skip invalid with error report
- [ ] `GET /api/v1/users/export` â€” export all users as CSV or JSON
- [ ] Content-Disposition header for download

### Task 89: Group CRUD API
- [ ] `GET /api/v1/groups` â€” list all groups
- [ ] `POST /api/v1/groups` â€” create group
- [ ] `GET /api/v1/groups/{id}` â€” get group details with member list
- [ ] `PUT /api/v1/groups/{id}` â€” update group
- [ ] `DELETE /api/v1/groups/{id}` â€” delete group
- [ ] `POST /api/v1/groups/{id}/members` â€” add member
- [ ] `DELETE /api/v1/groups/{id}/members/{userId}` â€” remove member

### Task 90: Session Management API
- [ ] `GET /api/v1/sessions` â€” list active sessions (with filters: protocol, user, ip)
- [ ] `GET /api/v1/sessions/{id}` â€” session details
- [ ] `DELETE /api/v1/sessions/{id}` â€” kill session (force disconnect)
- [ ] Session info: id, username, protocol, client_ip, connected_at, bytes_up/down, state

### Task 91: Transfer Tracking API
- [ ] `GET /api/v1/transfers` â€” list active + recent transfers
- [ ] `GET /api/v1/transfers/{id}` â€” transfer details
- [ ] Filters: direction (upload/download), protocol, user, status (active/complete/failed)
- [ ] Pagination with cursor-based approach
- [ ] Transfer info: session_id, path, direction, size, progress, speed, duration

### Task 92: Audit Query API
- [ ] `GET /api/v1/audit/events` â€” search audit events
- [ ] Query params: event_type, username, protocol, path, date_from, date_to, client_ip
- [ ] Pagination: offset + limit or cursor-based
- [ ] Sort: timestamp desc (default)
- [ ] `GET /api/v1/audit/events/{id}` â€” single event detail
- [ ] `GET /api/v1/audit/export` â€” export as CSV or JSON with same filters
- [ ] Content-Disposition for download

### Task 93: File Browser API
- [ ] `GET /api/v1/files/{user}/ls?path=/` â€” list directory contents
- [ ] `GET /api/v1/files/{user}/stat?path=/file.txt` â€” file metadata
- [ ] `GET /api/v1/files/{user}/download?path=/file.txt` â€” download file (streaming)
- [ ] `POST /api/v1/files/{user}/upload?path=/` â€” upload file (multipart form)
- [ ] `DELETE /api/v1/files/{user}/rm?path=/file.txt` â€” delete file/directory
- [ ] `POST /api/v1/files/{user}/mkdir?path=/newdir` â€” create directory
- [ ] `POST /api/v1/files/{user}/rename?from=/old&to=/new` â€” rename/move
- [ ] Admin only: browse any user's VFS
- [ ] Regular user: browse own VFS only

### Task 94: Share Link API
- [ ] `POST /api/v1/files/{user}/share?path=/file.txt&ttl=24h` â€” create share link
- [ ] Generate random token (32 bytes, URL-safe base64)
- [ ] Store in CobaltDB: token â†’ {user, path, expires_at, download_count, max_downloads}
- [ ] `GET /api/v1/share/{token}` â€” public download endpoint (no auth)
- [ ] Expiry enforcement, max download count
- [ ] Admin: list/revoke share links

### Task 95: Server Status API
- [x] `GET /api/v1/server/status` â€” uptime, version, enabled protocols, connection counts
- [x] `GET /api/v1/server/config` â€” current config (secrets redacted)
- [x] `PUT /api/v1/server/config` â€” update config (partial, admin only)
- [x] `POST /api/v1/server/reload` â€” trigger runtime-safe config reload

### Task 96: API Keys Management API
- [x] `GET /api/v1/apikeys` â€” list user's API keys
- [x] `POST /api/v1/apikeys` â€” create new API key (return key once, store hash)
- [x] `DELETE /api/v1/apikeys/{id}` â€” revoke key
- [x] Per-key: name, permissions (read-only, read-write, admin), last_used, created_at

### Task 97: Prometheus Metrics Endpoint
- [x] `GET /api/v1/metrics` â€” Prometheus text format
- [ ] Connection metrics: total, active, rejected (by protocol)
- [ ] Transfer metrics: total, active, bytes total, duration histogram, errors
- [ ] Auth metrics: attempts by result and method, locked accounts
- [ ] Storage metrics: bytes used, files total, quota usage ratio
- [x] System metrics: uptime, goroutines, memory
- [x] From-scratch Prometheus exposition format (no external lib)

### Task 98: Health Check Endpoint
- [x] `GET /api/v1/health` â€” JSON health status
- [x] Check each protocol listener (is it accepting?)
- [ ] Check storage backends (local: disk free, S3: connectivity)
- [ ] Check CobaltDB (is it readable/writable?)
- [x] Overall status: healthy / degraded / unhealthy
- [x] Include version and uptime

### Task 99: WebSocket Event Stream
- [x] Implement WebSocket upgrade handler at `/api/v1/ws`
- [x] From-scratch WebSocket (RFC 6455): handshake, frame encoding/decoding
- [ ] JWT auth via query parameter or first message
- [ ] Event types per SPECIFICATION Â§8.5
- [ ] Broadcast pattern: audit engine â†’ WebSocket hub â†’ connected clients
- [x] Client subscription filtering (by event type)
- [ ] Ping/pong keepalive (30s interval)
- [x] Graceful disconnect handling

### Task 100: WebUI Embedding
- [x] `embed.FS` for React build output in `internal/webui/embed.go`
- [x] SPA handler: serve static files, fallback to index.html for client routes
- [x] Correct Content-Type headers for JS/CSS/images
- [x] Cache-Control headers: immutable for hashed assets, no-cache for index.html
- [ ] Gzip compression middleware

### Task 101: WebUI â€” React 19 Project Setup
- [x] Initialize React 19 + TypeScript + Vite in `webui/` directory
- [x] Tailwind CSS v4 setup
- [x] React Router v7 for client-side routing
- [ ] API client layer (fetch wrapper with JWT, auto-refresh)
- [x] WebSocket client hook
- [x] Dark/light mode support
- [x] Responsive layout (mobile-friendly)

### Task 102: WebUI â€” Login Page
- [x] Username/password form
- [x] TOTP prompt (conditional, after password success)
- [x] JWT token storage (memory, not localStorage)
- [x] Auto-redirect to dashboard on auth
- [ ] Session timeout handling (auto-logout)

### Task 103: WebUI â€” Dashboard Page
- [ ] Active sessions count (per protocol)
- [ ] Current transfer rates (upload/download, real-time via WebSocket)
- [ ] Storage usage (bar chart per backend)
- [ ] Recent events timeline (last 20 events, live updates)
- [ ] Protocol breakdown pie chart
- [ ] Quick stats: total users, total transfers today, failed logins today

### Task 104: WebUI â€” Users Page
- [ ] User table: username, email, role, status, last login, quota usage
- [ ] Search/filter by name, role, status
- [ ] Create user dialog (form with all fields)
- [ ] Edit user dialog (inline edit)
- [x] Delete user (confirmation dialog)
- [x] Disable/enable toggle
- [ ] Reset password action
- [ ] Bulk import dialog (CSV upload)
- [x] Export button (CSV/JSON download)
- [ ] Permission matrix editor
- [ ] Mount configuration editor
- [ ] Quota settings (with visual bar)

### Task 105: WebUI â€” Sessions Page
- [ ] Active sessions table: user, protocol, IP, connected time, bytes up/down, state
- [x] Real-time updates via WebSocket
- [x] Kill session button (confirmation)
- [x] Filter by protocol, user, IP
- [ ] Sort by connected time, bytes transferred
- [ ] Session detail panel: full info, transfer history

### Task 106: WebUI â€” File Browser Page
- [ ] Directory tree navigation (left panel)
- [ ] File list (right panel): name, size, modified, permissions
- [ ] Admin: user selector dropdown to browse any user's VFS
- [x] Breadcrumb navigation
- [ ] Upload: drag-and-drop zone + file picker (chunked upload with progress)
- [ ] Download: click to download (streaming)
- [ ] Context menu: rename, delete, share, properties
- [ ] Create folder dialog
- [ ] File preview: images (inline), text (CodeMirror), PDF (iframe)
- [x] Generate share link dialog (TTL selector)
- [ ] Multi-select + bulk actions (delete, download as zip)

### Task 107: WebUI â€” Audit Log Page
- [ ] Event table: timestamp, type, user, protocol, path, IP, status
- [ ] Date range picker
- [ ] Event type filter (multi-select)
- [ ] Username search
- [ ] Path search
- [ ] IP search
- [ ] Live event feed toggle (WebSocket)
- [ ] Event detail panel (all fields)
- [x] Export button (CSV/JSON with applied filters)
- [ ] Pagination (infinite scroll or page numbers)

### Task 108: WebUI â€” Transfers Page
- [ ] Active transfers: progress bar, speed, ETA
- [ ] Completed transfers: file, size, duration, speed, checksum
- [ ] Failed transfers: error message, retry action
- [ ] Filter by direction, protocol, user, status
- [ ] Real-time progress via WebSocket
- [ ] Transfer detail: full audit trail

### Task 109: WebUI â€” Configuration Page
- [x] Current config display (read-only, secrets masked)
- [ ] Edit sections: FTP, FTPS, SFTP, SCP, WebUI, Auth, Security
- [x] Form-based editing with validation
- [x] Reload button (POST /api/v1/server/reload)
- [x] Restart required indicator for non-hot-reloadable settings
- [ ] TLS certificate info display (expiry, issuer, SANs)
- [ ] Test connection button for S3 backend

### Task 110: WebUI â€” Monitoring Page
- [ ] CPU/memory/goroutine graphs (polling /api/v1/stats)
- [ ] Connection count over time (per protocol)
- [ ] Transfer throughput over time
- [ ] Error rate over time
- [ ] Top users by transfer volume
- [ ] Storage usage breakdown
- [ ] Configurable time range (1h, 6h, 24h, 7d)

### Task 111: WebUI â€” API Keys Page
- [x] API key table: name, permissions, created, last used
- [x] Create key dialog: name, permission level
- [x] Show generated key once (copy button, warning: shown once only)
- [x] Revoke key button (confirmation)

### Task 112: WebUI Build Integration
- [x] `go run ./scripts` â€” run `npm ci`, build the WebUI, then sync `internal/webui/dist/`
- [x] Copy `webui/dist/` â†’ `internal/webui/dist/`
- [x] Makefile target: `webui` before `build`
- [ ] Decide whether `internal/webui/dist/` should stay committed or become a CI-only artifact
- [ ] Evaluate a `//go:generate` alternative for local developer ergonomics

---

## PHASE 7 â€” OPERATIONS & EXTRAS (Tasks 113â€“127)

> ACME, LDAP, MCP, CLI, migration, Docker

### Task 113: ACME Client
- [ ] Implement `internal/acme/acme.go` â€” RFC 8555
- [ ] Account creation with Let's Encrypt / ZeroSSL
- [ ] HTTP-01 challenge solver (serve on /.well-known/acme-challenge/)
- [ ] TLS-ALPN-01 challenge solver (alternative)
- [ ] Certificate request (CSR generation)
- [ ] Certificate download and storage
- [ ] Auto-renewal (check expiry daily, renew at 30 days before)
- [ ] Certificate hot-swap (update TLS config without restart)
- [ ] Fallback to self-signed on ACME failure

### Task 114: LDAP Client
- [ ] Implement `internal/auth/ldap.go` â€” from-scratch LDAP client
- [ ] TCP + TLS connection to LDAP server
- [ ] LDAP Bind operation (authenticate service account)
- [ ] LDAP Search operation (find user by filter)
- [ ] Attribute extraction (username, email, groups)
- [ ] Group mapping: LDAP group â†’ Kervan role
- [ ] Connection pooling (configurable pool size)
- [ ] Cache: authenticated user info cached for TTL
- [ ] TLS certificate verification (skip option for self-signed)

### Task 115: MCP Server â€” Core
- [x] Implement `internal/mcp/server.go` â€” stdio transport
- [x] JSON-RPC 2.0 message handling
- [x] `initialize` â†’ server capabilities + info
- [x] `tools/list` â†’ tool definitions
- [x] `tools/call` â†’ dispatch to tool handlers
- [x] `resources/list` â†’ resource definitions
- [x] `resources/read` â†’ resource content

### Task 116: MCP Server â€” Tools
- [ ] `kervan_list_users` â€” list users with status, quota usage %
- [ ] `kervan_get_user` â€” user details by username
- [ ] `kervan_create_user` â€” create virtual user
- [ ] `kervan_update_user` â€” update user settings
- [ ] `kervan_list_sessions` â€” active sessions with protocol, IP, duration
- [ ] `kervan_kill_session` â€” force disconnect by session ID
- [ ] `kervan_transfer_stats` â€” statistics by period and protocol
- [ ] `kervan_audit_query` â€” search audit events (type, user, date range)
- [ ] `kervan_server_status` â€” health, connections, version
- [ ] `kervan_list_files` â€” browse user VFS (path, list)
- [ ] `kervan_quota_report` â€” per-user quota usage

### Task 117: MCP Server â€” Resources
- [ ] `kervan://server/status` â€” real-time server health
- [ ] `kervan://server/config` â€” current config (redacted)
- [ ] `kervan://users` â€” user list summary
- [ ] `kervan://sessions` â€” active sessions
- [ ] `kervan://audit/recent` â€” last 50 audit events
- [ ] `kervan://transfers/active` â€” active transfers

### Task 118: CLI â€” Init Command
- [x] `kervan init` â€” generate default config file
- [x] `kervan init --config /path/to/kervan.yaml` â€” custom path
- [ ] Interactive mode: prompt for admin password, data directory, ports
- [x] Create data directory structure
- [x] Set secure file permissions on config (0600)

### Task 119: CLI â€” Keygen Command
- [x] `kervan keygen` â€” generate SSH host keys
- [x] `--type ed25519|rsa|both` â€” key algorithm selection
- [x] `--output /path/to/keys/` â€” output directory
- [x] `--force` â€” overwrite existing keys
- [x] Display key fingerprint after generation

### Task 120: CLI â€” Admin Commands
- [ ] `kervan admin create` â€” create admin user (interactive: prompt for username/password)
- [x] `kervan admin create --username admin --password secret` â€” non-interactive
- [ ] `kervan admin reset-password --username admin` â€” reset (interactive prompt)
- [x] `kervan admin list` â€” list admin users

### Task 121: CLI â€” User Commands
- [ ] `kervan user list` â€” table output: username, role, status, last login
- [x] `kervan user create --username john --password pass` â€” create user
- [ ] `kervan user delete --username john` â€” delete user (confirmation prompt)
- [x] `kervan user import --file users.csv` â€” bulk import
- [x] `kervan user export --format json --output users.json` â€” bulk export
- [x] `--json` flag for machine-readable output

### Task 122: CLI â€” Status & Check Commands
- [x] `kervan status` â€” connect to running instance API, display server status
- [x] `kervan check` â€” validate config file without starting server
- [x] `kervan check --config /path/to/config.yaml`
- [x] Exit code 0 on success, 1 on error

### Task 123: Migration â€” vsftpd
- [x] `kervan migrate vsftpd --user-db /path/to/virtual_users.db`
- [ ] Parse vsftpd Berkeley DB or plain text virtual user file
- [x] Extract username + password pairs
- [x] Create Kervan users with matching home directories
- [ ] Map vsftpd config to Kervan config suggestions
- [x] Report: migrated users, skipped, errors

### Task 124: Migration â€” ProFTPD
- [x] `kervan migrate proftpd --config /path/to/proftpd.conf`
- [x] Parse ProFTPD config format
- [x] Extract virtual users from AuthUserFile
- [ ] Map directory limits to Kervan mount configs
- [ ] Map permission directives to Kervan user permissions
- [x] Report: migrated settings, unsupported directives

### Task 125: Migration â€” SSH Keys
- [x] `kervan migrate ssh-keys --authorized-keys-dir /home/*/.ssh/`
- [x] Glob and parse authorized_keys files
- [x] Create/update Kervan users with public keys
- [x] Map system username to Kervan username
- [x] Report: imported keys per user

### Task 126: Docker Build
- [ ] Multi-stage Dockerfile: Go build â†’ scratch image
- [x] WebUI build stage (Node.js)
- [x] Go build stage (CGO_ENABLED=0, static binary)
- [ ] Final stage: scratch + binary + CA certs
- [ ] EXPOSE ports: 2121, 990, 2222, 8443, 50000-50100
- [ ] VOLUME for /data and /etc/kervan
- [x] docker-compose.yml with all port mappings
- [ ] Multi-arch build (amd64 + arm64)
- [ ] GitHub Container Registry push

### Task 127: systemd Service & Packaging
- [x] systemd unit file per SPECIFICATION Â§12.3
- [x] Security hardening: NoNewPrivileges, ProtectSystem, ProtectHome, ReadWritePaths
- [x] `CAP_NET_BIND_SERVICE` for privileged ports
- [x] `ExecReload=/bin/kill -HUP $MAINPID`
- [ ] LimitNOFILE=65535
- [ ] `kervan` user/group creation script
- [ ] Makefile target: `install` (binary + config + service + user)
- [x] README.md with quick start, Docker, configuration reference

---

## TASK DEPENDENCY GRAPH

```
Phase 1 (Foundation)
  â”œâ”€â”€ T1 (scaffold)
  â”œâ”€â”€ T2-T6 (config) â† T1
  â”œâ”€â”€ T7 (logger) â† T1
  â”œâ”€â”€ T8 (ULID) â† T1
  â”œâ”€â”€ T9 (CobaltDB) â† T1
  â”œâ”€â”€ T10-T12 (users/groups) â† T8, T9
  â”œâ”€â”€ T13-T16 (VFS) â† T10
  â””â”€â”€ T17-T18 (backends) â† T13

Phase 2 (FTP) â† Phase 1
  â”œâ”€â”€ T19-T21 (server/handler/session) â† T7, T16
  â”œâ”€â”€ T22 (auth) â† T55
  â”œâ”€â”€ T23-T27 (nav/listing) â† T21
  â”œâ”€â”€ T28-T29 (transfer) â† T24/T25
  â”œâ”€â”€ T30-T33 (file ops) â† T21
  â””â”€â”€ T34-T36 (FTPS) â† T19

Phase 3 (SSH) â† Phase 1
  â”œâ”€â”€ T37 (host keys) â† T1
  â”œâ”€â”€ T38-T42 (SSH server) â† T37, T55
  â”œâ”€â”€ T43-T50 (SFTP handler) â† T42
  â””â”€â”€ T51-T52 (SCP) â† T42

Phase 4 (Security) â† Phase 1
  â”œâ”€â”€ T53-T55 (auth engine) â† T10
  â”œâ”€â”€ T56 (VFS builder) â† T16, T17
  â”œâ”€â”€ T57-T58 (quota/throttle) â† T13
  â”œâ”€â”€ T59-T61 (session/security) â† T8
  â”œâ”€â”€ T62-T67 (audit) â† T8, T9
  â”œâ”€â”€ T68 (integrity) â† T62
  â””â”€â”€ T69 (TOTP) â† T53

Phase 5 (S3) â† Phase 1, Phase 4
  â”œâ”€â”€ T70-T74 (S3 client) â† T1
  â”œâ”€â”€ T75-T77 (S3 VFS) â† T13, T70-T74
  â”œâ”€â”€ T78 (metadata) â† T9, T75
  â”œâ”€â”€ T79 (streaming) â† T74
  â”œâ”€â”€ T80-T81 (error/test) â† T70
  â””â”€â”€ T82 (multi-mount) â† T15, T17, T75

Phase 6 (WebUI/API) â† Phase 4
  â”œâ”€â”€ T83-T86 (HTTP infra) â† T7, T84
  â”œâ”€â”€ T87-T98 (API endpoints) â† T83, T55, T59, T62
  â”œâ”€â”€ T99 (WebSocket) â† T62
  â”œâ”€â”€ T100 (embed) â† T112
  â””â”€â”€ T101-T112 (React WebUI) â† T87-T99

Phase 7 (Ops) â† Phase 2, Phase 3, Phase 6
  â”œâ”€â”€ T113 (ACME) â† T36
  â”œâ”€â”€ T114 (LDAP) â† T55
  â”œâ”€â”€ T115-T117 (MCP) â† T55, T59, T62
  â”œâ”€â”€ T118-T122 (CLI) â† T4, T11
  â”œâ”€â”€ T123-T125 (migration) â† T11
  â””â”€â”€ T126-T127 (deploy) â† all
```

---

## TIME ESTIMATES

| Phase | Tasks | Estimated Duration |
|-------|-------|--------------------|
| Phase 1 â€” Foundation | T1â€“T18 | 1.5 weeks |
| Phase 2 â€” FTP Server | T19â€“T36 | 2 weeks |
| Phase 3 â€” SSH Protocols | T37â€“T52 | 2 weeks |
| Phase 4 â€” Security & Session | T53â€“T69 | 1.5 weeks |
| Phase 5 â€” S3 Backend | T70â€“T82 | 1.5 weeks |
| Phase 6 â€” WebUI & API | T83â€“T112 | 3 weeks |
| Phase 7 â€” Operations | T113â€“T127 | 1.5 weeks |
| **Total** | **127 tasks** | **~13 weeks** |

---

## MVP SCOPE (Minimal Viable Product)

For fastest usable release, implement in this order:

**MVP-1 (Week 1â€“4): FTP + SFTP with Local Backend**
- Phase 1 (all)
- Phase 4: T53â€“T56, T59, T62â€“T63 (core auth, session, file audit)
- Phase 2: T19â€“T31 (FTP core, no FTPS yet)
- Phase 3: T37â€“T50 (SFTP core, no SCP yet)

**MVP-2 (Week 5â€“7): WebUI + API**
- Phase 6: T83â€“T90, T92â€“T93, T95, T97â€“T98, T100â€“T104, T107, T112

**MVP-3 (Week 8â€“10): Security + FTPS + SCP**
- Phase 2: T34â€“T36 (FTPS)
- Phase 3: T51â€“T52 (SCP)
- Phase 4: T57â€“T61, T64â€“T69 (full security stack)

**MVP-4 (Week 11â€“13): S3 + Polish**
- Phase 5 (all)
- Phase 6: remaining pages
- Phase 7: T113, T118â€“T122, T126â€“T127

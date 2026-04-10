# Kervan — Unified File Transfer Server

> **One Binary. Every Protocol. Total Control.**

**Kervan** (Turkish for *caravan*) is a single-binary, multi-protocol file transfer
server written in pure Go. It unifies FTP, FTPS, SFTP and SCP behind a shared
virtual filesystem, user store, session manager, audit engine and management API
— deployable as one static binary with no external runtime dependencies.

Owner: **ECOSTACK TECHNOLOGY OÜ** · Module: `github.com/kervanserver/kervan`

See [.project/SPECIFICATION.md](.project/SPECIFICATION.md) for the full product
specification. This README documents the **current shippable skeleton** and
marks aspirational items from the spec as *planned*.

---

## Highlights

- **4 protocols in 1 binary** — FTP, FTPS (explicit + implicit), SFTP and SCP
  share a single auth engine, VFS layer and audit pipeline.
- **Virtual Filesystem (VFS)** — per-user chroot, path traversal protection,
  pluggable backends (local disk, in-memory; S3 planned).
- **Local user store** — Argon2id / bcrypt password hashing, admin flag, enable
  /disable, stored in an embedded JSON-backed store under `data_dir`.
- **Session & transfer tracking** — live session registry and transfer manager
  surfaced through REST and Prometheus-style `/metrics`.
- **JSONL audit log** — every significant action is appended to a structured
  audit file (`data/audit.jsonl` by default).
- **Config system** — YAML file + environment overlay + defaults + validation,
  with a `reload` hook wired for future hot-reload.
- **Embedded WebUI** — React 19 + Tailwind CSS 4.1 + shadcn/ui + lucide-react
  admin panel with dark/light theme and responsive layout, embedded from
  `internal/webui/dist`.
- **REST API** — auth, users, sessions, files, transfers, audit, server status
  and metrics behind JWT-style bearer tokens.
- **Zero external runtime deps** — only `golang.org/x/crypto` (SSH/Argon2id) and
  `gopkg.in/yaml.v3` as direct dependencies.

---

## Requirements

- **Go 1.26.1+** (toolchain pinned to `go1.26.2` in [go.mod](go.mod))
- Linux, macOS or Windows (tested on Windows 11, Linux amd64/arm64)

---

## Quick Start

```bash
# 1. Generate a default kervan.yaml
go run ./cmd/kervan init

# 2. Create the first admin user (required before logging in to the WebUI/API)
go run ./cmd/kervan admin create --username admin --password 'StrongPass123!'

# 3. Start the server (writes kervan.yaml on first run if missing)
go run ./cmd/kervan
```

Default listeners (see [kervan.example.yaml](kervan.example.yaml)):

| Service | Port  | Notes                                         |
|---------|-------|-----------------------------------------------|
| FTP     | 2121  | Passive range `50000-50100`                   |
| FTPS    | 2121 / 990 | Disabled by default; needs cert + key    |
| SFTP    | 2222  | Ed25519 + RSA host keys in `data/host_keys/`  |
| SCP     | 2222  | Shares the SFTP SSH listener                  |
| WebUI   | 8080  | Also exposes REST `/api/*` and `/metrics`     |

---

## CLI Commands

```bash
# Run the server
go run ./cmd/kervan                               # default kervan.yaml
go run ./cmd/kervan -config /etc/kervan/kervan.yaml

# Print version / build info
go run ./cmd/kervan version

# Generate a default config (refuses to overwrite unless --force)
go run ./cmd/kervan init
go run ./cmd/kervan init --config /etc/kervan/kervan.yaml --force

# Generate SSH host keys (ed25519 or rsa-4096)
go run ./cmd/kervan keygen --type ed25519 --output ./data/host_keys
go run ./cmd/kervan keygen --type rsa     --output ./data/host_keys

# Admin user management
go run ./cmd/kervan admin create         --username admin --password 'StrongPass123!'
go run ./cmd/kervan admin reset-password --username admin --password 'NewStrongPass123!'
```

> *Planned CLI (per Spec §17):* `user list/create/delete/import/export`,
> `status`, `check`, `migrate vsftpd|proftpd|ssh-keys`.

---

## Build & Test

```bash
# Run the full test suite
go test ./...

# Build the React WebUI and copy it to internal/webui/dist
./scripts/generate-webui.sh

# Build a static binary
go build -o kervan ./cmd/kervan

# Or via the provided Makefile targets
make build
make test
```

---

## Protocols

### FTP (RFC 959 + extensions)

- Authentication, navigation (`CWD`, `PWD`, `LIST`, `NLST`), upload (`STOR`,
  `APPE`), download (`RETR`), rename, delete, mkdir / rmdir.
- Active and passive data channels (`PORT`, `PASV`). Configurable passive port
  range and advertised passive IP for NAT/firewall scenarios.
- ASCII and binary (`TYPE I`) transfer modes.
- Per-connection idle and transfer timeouts.

### FTPS (RFC 4217)

- Explicit FTPS via `AUTH TLS`, `PBSZ` and `PROT`.
- Optional implicit FTPS listener on `ftps.implicit_port` (default `990`).
- Mode selector: `explicit`, `implicit`, or `both`.
- Configurable minimum / maximum TLS version and optional client-certificate
  authentication (`none` / `request` / `require` with `client_ca_file`).
- Enable by setting `ftps.enabled: true` and providing `cert_file` + `key_file`.

### SFTP (SSH File Transfer Protocol)

- SSH transport built on `golang.org/x/crypto/ssh` with Ed25519 and RSA-4096
  host keys auto-generated on first run (or via `kervan keygen`).
- SFTP subsystem handler covering open/close/read/write, stat, readdir,
  mkdir/rmdir, remove, rename and realpath.
- Password authentication against the local user store (public-key auth is
  stubbed out per the spec roadmap).

### SCP (OpenSSH-compatible)

- Source and sink modes for file and directory copies.
- Shares the SSH listener with SFTP; no separate port.
- Operates through the same VFS and audit pipeline as SFTP.

---

## Virtual Filesystem

All protocols share a common VFS layer ([internal/vfs](internal/vfs)):

- **Per-user chroot** — every session is resolved against the user's home
  directory, with `..` normalization and symlink containment.
- **Mount resolver** — directory roots can be composed from multiple backends
  (foundation for the multi-mount model in Spec §4.3).
- **Backends shipped:**
  - `local` — filesystem directory rooted at `storage.backends.local.options.root`.
  - `memory` — in-process backend used for tests and ephemeral workloads.
- **Planned backends:** S3-compatible (AWS / MinIO / R2) with SigV4, multipart
  upload, directory emulation and metadata sidecar (Spec §4.4.2).

---

## Authentication & Users

- **Local provider** — users stored in the embedded JSON store under
  `server.data_dir`, protected by Argon2id (default) or bcrypt password hashes.
- **Admin flag** — admin users can manage other users through the REST API.
- **Brute-force lockout** — configurable `max_attempts` and `lockout_duration`
  enforced by the auth engine.
- **Per-user home directory** — surfaced to every protocol as the VFS chroot.
- **Planned:** LDAP/AD provider, OIDC WebUI SSO, SSH public-key auth, TOTP 2FA,
  per-user quotas, rate limiting, IP allowlists, groups and account expiry
  (Spec §5).

---

## Session & Transfer Tracking

- `internal/session` tracks live sessions (protocol, client IP, bytes in/out,
  last activity) and exposes them via `/api/v1/sessions`.
- `internal/transfer` records in-flight and recent transfers with bytes and
  duration; surfaced via `/api/v1/transfers` and `/metrics`.
- `/metrics` returns a Prometheus-style text exposition covering active
  sessions, transfer counters and server uptime.

---

## Audit Log

- Structured events (login, upload, download, delete, rename, mkdir, session
  open/close) are written as JSON lines to `data/audit.jsonl` by default.
- Output path is configurable via `audit.outputs[].path` in `kervan.yaml`.
- Events are also queryable over the REST API at `/api/v1/audit/events`.
- *Planned outputs:* syslog (RFC 5424 / CEF), batched webhooks, CobaltDB-backed
  queryable store, HMAC-chained immutable mode (Spec §6).

---

## REST API

The management API is served from the same process as the WebUI (default
`:8080`). All non-login endpoints require a bearer token obtained from
`POST /api/v1/auth/login`.

| Method | Path                     | Description                                   |
|--------|--------------------------|-----------------------------------------------|
| `GET`  | `/health`                | Unauthenticated liveness + subsystem checks   |
| `GET`  | `/metrics`               | Prometheus-style text metrics                 |
| `POST` | `/api/v1/auth/login`         | Exchange username + password for a token  |
| `GET`  | `/api/v1/server/status`      | Server status snapshot                    |
| `GET`  | `/api/v1/server/config`      | Redacted runtime config (admin)           |
| `PUT`  | `/api/v1/server/config`      | Update config with JSON patch (admin)     |
| `POST` | `/api/v1/server/config/validate` | Validate config patch without write (admin) |
| `POST` | `/api/v1/server/reload`      | Validate/reload config file (admin)       |
| `GET`  | `/api/v1/users`              | List users (admin)                        |
| `POST` | `/api/v1/users`              | Create user (admin)                       |
| `DELETE` | `/api/v1/users?id=…`       | Delete user (admin)                       |
| `GET`  | `/api/v1/apikeys`            | List API keys (current user)              |
| `POST` | `/api/v1/apikeys`            | Create API key (shown once)               |
| `DELETE` | `/api/v1/apikeys?id=…`     | Revoke API key                            |
| `GET`  | `/api/v1/sessions`           | Active session list                       |
| `GET`  | `/api/v1/files/{user}/ls`    | List directory contents                   |
| `GET`  | `/api/v1/files/{user}/stat`  | File or directory metadata                |
| `POST` | `/api/v1/files/{user}/mkdir` | Create directory                          |
| `POST` | `/api/v1/files/{user}/rename`| Rename or move file/directory            |
| `POST` | `/api/v1/files/{user}/upload`| Upload file content                       |
| `GET`  | `/api/v1/files/{user}/download` | Stream file download                   |
| `DELETE` | `/api/v1/files/{user}/rm`  | Remove file or directory                  |
| `GET`  | `/api/v1/transfers`          | Transfer registry (active + recent)      |
| `GET`  | `/api/v1/audit/events`       | Paginated audit events                    |
| `GET`  | `/api/v1/ws?token=...&types=server,sessions,transfers,audit` | WebSocket live snapshots |

The full `/api/v1/...` surface from Spec §8.4 still has planned gaps (groups,
bulk import/export, share links, advanced server config editing).
Current reload endpoint validates and reloads config from disk, but runtime
apply still requires restart for most subsystems.

---

## WebUI

The binary embeds a React 19 WebUI from [webui](webui) at runtime through
`embed.FS`:

- Tailwind CSS 4.1 design system with shadcn/ui component patterns
- lucide-react icon set
- Dark/light theme switch via `next-themes`
- Responsive navigation and page layouts for desktop/mobile
- API-integrated pages: dashboard, users, sessions, files, transfers, audit, monitoring, API keys

---

## Configuration

The full configuration schema with defaults lives in
[kervan.example.yaml](kervan.example.yaml). On first run, if `kervan.yaml` does
not exist, the server writes a default copy and continues startup. Every config
key can be overridden with a `KERVAN_<SECTION>_<KEY>` environment variable
(Spec §10.2).

Key sections: `server`, `ftp`, `ftps`, `sftp`, `scp`, `webui`, `auth`,
`storage`, `quota`, `audit`, `security`, `mcp`.

### FTPS notes

- Set `ftps.enabled: true` and provide both `ftps.cert_file` and
  `ftps.key_file` (auto-cert via ACME is planned, Spec §3.2).
- Pick `ftps.mode` as `explicit`, `implicit`, or `both`.
- Implicit mode listens on `ftps.implicit_port` (default `990`).
- `ftps.min_tls_version` / `ftps.max_tls_version` accept `"1.2"` / `"1.3"`.

### SFTP notes

- Host keys are stored under `sftp.host_key_dir` and auto-generated on first
  start if missing. Use `kervan keygen` to pre-create them.
- Set `sftp.disable_shell: true` to only expose the SFTP and SCP subsystems.

---

## Project Layout

```
cmd/kervan/              # Entry point + CLI subcommands (init, keygen, admin, version)
internal/
  api/                   # REST API router, JWT-style tokens, file/audit handlers
  audit/                 # Event schema + JSONL file sink
  auth/                  # Auth engine, user repo, Argon2id/bcrypt hashing
  build/                 # Version & build metadata
  config/                # Loader, defaults, env overlay, validation, reload hook
  crypto/                # TLS config builder, SSH host-key generation
  protocol/
    ftp/                 # FTP (and FTPS wrapper) server
    sftp/                # SFTP + SCP subsystem over SSH
  server/                # Top-level Application wiring all subsystems
  session/               # Session manager
  storage/
    local/               # Local filesystem backend
    memory/              # In-memory backend
  store/                 # Embedded JSON-backed key-value store
  transfer/              # Transfer tracker
  util/                  # Logging, ULID generation helpers
  vfs/                   # VFS interface, resolver, mount registry, user VFS
  webui/                 # embed.FS handler for the bundled dashboard
kervan.example.yaml      # Reference configuration
.project/SPECIFICATION.md  # Full product specification
```

---

## Roadmap

Tracked against [.project/SPECIFICATION.md](.project/SPECIFICATION.md):

- **Storage:** S3-compatible backend with multipart upload + metadata sidecar.
- **Auth:** LDAP/AD, OIDC WebUI SSO, SSH public-key auth, TOTP 2FA, groups,
  per-user quotas, rate limiting, IP allow/deny, geo-blocking.
- **Protocols:** ACME auto-TLS (Let's Encrypt), MLSD/MLST, virtual hosting
  (`HOST` command), keyboard-interactive SFTP auth, SSH certificate auth.
- **WebUI:** React 19 dashboard, live WebSocket events, file-share links,
  chunked resumable uploads, inline editor.
- **API:** Full `/api/v1/...` surface (groups, API keys, bulk import/export,
  hot-reload endpoint, WebSocket stream).
- **Audit:** Syslog (CEF), webhook batching, CobaltDB-backed queryable store,
  HMAC-chained immutable logs, session recording.
- **Ops:** Hot reload on `SIGHUP`, Prometheus metrics parity with Spec §18.1,
  systemd unit + Dockerfile + multi-arch releases, migration tools
  (`migrate vsftpd|proftpd|ssh-keys`).
- **MCP server:** `stdio` MCP server exposing users, sessions, transfers and
  audit queries for AI/LLM integration.

---

## License

Open source — MIT or Apache 2.0 (to be finalized per Spec §0).

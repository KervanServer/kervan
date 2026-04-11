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

## Requirements

- **Go 1.26.1+** (toolchain pinned to `go1.26.2` in [go.mod](go.mod))
- Linux, macOS or Windows (tested on Windows 11, Linux amd64/arm64)

---

## Quick Start

```bash
# 1. Generate a default kervan.yaml
go run ./cmd/kervan init

# 2. Create the first admin user (required before starting the WebUI/API securely)
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
| Debug   | 6060  | Disabled by default; localhost-only `pprof`   |

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

# Create or restore an operational backup archive
go run ./cmd/kervan backup create  --config ./kervan.yaml --output ./backups/kervan-backup.zip
go run ./cmd/kervan backup restore --config ./kervan.yaml --input  ./backups/kervan-backup.zip --force
go run ./cmd/kervan backup verify  --input  ./backups/kervan-backup.zip

# Validate config and inspect a running instance
go run ./cmd/kervan check  --config ./kervan.yaml
go run ./cmd/kervan status --config ./kervan.yaml

# User management
go run ./cmd/kervan user list   --config ./kervan.yaml
go run ./cmd/kervan user create --config ./kervan.yaml --username alice --password 'StrongPass123!' --home-dir /uploads
go run ./cmd/kervan user delete --config ./kervan.yaml --username alice

# Discover API key presets and supported scopes
go run ./cmd/kervan apikey scopes
go run ./cmd/kervan apikey presets
```

API key scopes currently include surfaces such as `server:read`,
`files:read`, `files:write`, `share:write`, `audit:read`,
`sessions:write`, and `transfers:read`. Presets (`read-only`,
`automation`, `operations`, `read-write`) expand to curated scope sets.

> Additional CLI management for API key create/list/revoke is not yet exposed;
> the WebUI/API remains the primary management surface.

---

## Build & Test

```bash
# Run the full test suite
go test ./...

# Run the same backend checks enforced in CI
go vet ./...
staticcheck ./...

# Build the React WebUI and copy it to internal/webui/dist
./scripts/generate-webui.sh

# Build a static binary
go build -o kervan ./cmd/kervan

# Or via the provided Makefile targets
make build
make test
make compose-config
```

GitHub Actions CI runs backend formatting/vet/staticcheck/tests/build and also
verifies that `webui` source changes are reflected in the committed
`internal/webui/dist` assets.

## Logging

Kervan logs through Go's structured `slog` pipeline in either `json` or `text`
format. By default logs go to stdout, which is the recommended mode for
containers and supervised environments.

If `server.log_file` is set, Kervan now performs built-in size-based log
rotation:

```yaml
server:
  log_file: ./data/logs/kervan.log
  log_max_size_mb: 100
  log_max_backups: 5
```

Behavior:

- Rotation happens when the current file would grow past `log_max_size_mb`.
- Rotated files are kept as `kervan.log.1`, `kervan.log.2`, and so on.
- Only `log_max_backups` old files are retained.
- The log writer is closed cleanly during process shutdown.

For release builds, the repository now ships with [GoReleaser](.goreleaser.yml).
Tagged pushes like `v0.1.0` trigger the release workflow, rebuild the embedded
WebUI, publish platform archives and attach a `checksums.txt` file to a draft
GitHub Release.

```bash
# Validate the release config locally
make release-check

# Produce local snapshot artifacts in ./dist
make release-snapshot
```

## Docker

```bash
# Build the production image
docker build -t kervan:dev .

# Start with the bundled example config and bootstrap the first admin user
docker run --rm \
  -p 2121:2121 \
  -p 2222:2222 \
  -p 8080:8080 \
  -p 50000-50100:50000-50100 \
  -e KERVAN_ADMIN_PASSWORD='StrongPass123!' \
  -v kervan-data:/var/lib/kervan/data \
  kervan:dev
```

Container defaults:

- Runs as a non-root `kervan` user (`uid/gid 10001`).
- Uses `/var/lib/kervan/kervan.yaml` as the default config path.
- Persists runtime state under `/var/lib/kervan/data`.
- Exposes FTP (`2121`), FTPS implicit (`990`), SFTP/SCP (`2222`), WebUI/API
  (`8080`) and the passive FTP range (`50000-50100`).
- Ships with a `HEALTHCHECK` against `http://127.0.0.1:8080/health`.
- Does not expose the optional debug/`pprof` listener by default.

Optional environment variables:

- `KERVAN_CONFIG` — override the config path used by the entrypoint.
- `KERVAN_ADMIN_USERNAME` — bootstrap admin username when using
  `KERVAN_ADMIN_PASSWORD`.
- `KERVAN_ADMIN_PASSWORD` — creates the first admin user on container startup if
  it does not already exist.
- Any `KERVAN_*` config override already supported by the binary, such as
  `KERVAN_SERVER__DATA_DIR` or `KERVAN_FTP__PORT`.

### Docker Compose

The repository now ships with a ready-to-run [docker-compose.yml](docker-compose.yml)
plus an [.env.example](.env.example) template.

```bash
# 1. Create local runtime files (both are gitignored)
cp .env.example .env
cp kervan.example.yaml kervan.yaml

# 2. Validate and start
make compose-config
make compose-up
```

Compose defaults:

- Builds the local image if it does not already exist.
- Mounts `./kervan.yaml` into the container as the active config file.
- Persists runtime state in the named volume `kervan-data`.
- Reads bootstrap credentials from `.env`.

### systemd

For Linux hosts running the released binary directly, an example unit file is
included at [deploy/systemd/kervan.service](deploy/systemd/kervan.service).

Typical installation flow:

```bash
sudo install -d -m 0750 -o root -g root /etc/kervan
sudo install -d -m 0750 -o kervan -g kervan /var/lib/kervan
sudo install -m 0644 deploy/systemd/kervan.service /etc/systemd/system/kervan.service
sudo install -m 0640 kervan.example.yaml /etc/kervan/kervan.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now kervan
```

The unit is hardened for a non-root `kervan` user, includes automatic restart
on failure, and grants only `CAP_NET_BIND_SERVICE` so the service can bind
privileged ports like `990` without running as root.

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
  sessions, transfer counters, server uptime, and HTTP request counters /
  latency aggregates grouped by normalized route + status code.
- The HTTP middleware emits structured access logs with request ID, route,
  status, duration and authenticated username for every completed request.
- Incoming `Traceparent` headers are preserved in responses and logs; if a
  client does not send one, Kervan generates a fresh trace context and exposes
  it via `Traceparent` and `X-Trace-ID`.

## Debug & Profiling

Kervan now supports an optional, separate debug listener for Go `pprof`.
It is disabled by default and binds to `127.0.0.1:6060` when enabled, so it
does not share the public WebUI/API port.

Example config:

```yaml
debug:
  enabled: true
  bind_address: 127.0.0.1
  port: 6060
  pprof: true
```

Available endpoints when enabled:

- `/health` — lightweight debug listener health probe
- `/debug/pprof/` — pprof index
- `/debug/pprof/heap`
- `/debug/pprof/goroutine`
- `/debug/pprof/profile`
- `/debug/pprof/trace`

The main `/health` response also reports the debug listener as a separate
subsystem check.

---

## Audit Log

- Structured events (login, upload, download, delete, rename, mkdir, session
  open/close) are written as JSON lines to `data/audit.jsonl` by default.
- `audit.outputs[]` supports `file`, `http` and `webhook` sinks.
- File output path is configurable via `audit.outputs[].path` in `kervan.yaml`.
- HTTP/webhook outputs support custom headers, batch size, flush interval and
  retry count for downstream audit collectors.
- Events are also queryable over the REST API at `/api/v1/audit/events`.
- `kervan backup create` packages the embedded store, its `.bak` recovery copy,
  the audit log and the active config into a ZIP archive for offline recovery.
- `kervan backup verify` validates the ZIP structure and, when a manifest is
  present, verifies all recorded file sizes and SHA-256 checksums.
- `kervan backup restore` restores those files back into the current
  `server.data_dir`; use it with the server stopped for the cleanest recovery.
- Backup manifests now carry per-file SHA-256 checksums, and restore verifies
  them before writing recovered files back to disk.

Example:

```yaml
audit:
  enabled: true
  outputs:
    - type: file
      path: ./data/audit.jsonl
    - type: webhook
      url: https://audit.example.com/events
      headers:
        Authorization: Bearer change-me
      batch_size: 50
      flush_interval: 5s
      retry_count: 3
```
*Still planned:* syslog (RFC 5424 / CEF), CobaltDB-backed
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
| `GET`  | `/api/v1/apikeys`            | List API keys plus supported scopes/presets |
| `POST` | `/api/v1/apikeys`            | Create scoped API key (shown once)        |
| `DELETE` | `/api/v1/apikeys?id=…`     | Revoke API key                            |
| `GET`  | `/api/v1/sessions`           | Active session list                       |
| `GET`  | `/api/v1/files/{user}/ls`    | List directory contents                   |
| `GET`  | `/api/v1/files/{user}/stat`  | File or directory metadata                |
| `POST` | `/api/v1/files/{user}/mkdir` | Create directory                          |
| `POST` | `/api/v1/files/{user}/rename`| Rename or move file/directory            |
| `POST` | `/api/v1/files/{user}/share` | Create file share link                    |
| `POST` | `/api/v1/files/{user}/upload`| Upload file content                       |
| `GET`  | `/api/v1/files/{user}/download` | Stream file download                   |
| `DELETE` | `/api/v1/files/{user}/rm`  | Remove file or directory                  |
| `GET`  | `/api/v1/share`              | List own share links                      |
| `DELETE` | `/api/v1/share?token=…`    | Revoke a share link                       |
| `GET`  | `/api/v1/share/{token}`      | Public share download                     |
| `GET`  | `/api/v1/transfers`          | Transfer registry (active + recent)      |
| `GET`  | `/api/v1/audit/events`       | Paginated audit events                    |
| `GET`  | `/api/v1/ws?types=server,sessions,transfers,audit` | WebSocket live snapshots |

WebSocket clients should present the bearer token through the
`Sec-WebSocket-Protocol` offer (the bundled WebUI uses `kervan.v1` plus an
`auth.<token>` subprotocol) instead of putting tokens into the URL.
Bearer token signing keys are persisted under `data_dir`, so valid sessions now
survive normal process restarts as long as the same data directory is reused.

The full `/api/v1/...` surface from Spec §8.4 still has planned gaps (groups,
bulk import/export, advanced server config editing).
The reload endpoint now applies runtime-safe API settings immediately
(`webui.session_timeout`, `webui.totp_enabled`, `webui.cors_origins`,
`security.brute_force.enabled`, `security.brute_force.max_attempts`,
`security.brute_force.lockout_duration`) and returns `applied_paths` /
`restart_paths` so callers can see what still needs a restart.

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

For secure first startup, `webui.admin_password` now defaults to empty and
cross-origin access is disabled by default. Either create the admin explicitly
with `kervan admin create` before starting, or set a strong
`webui.admin_password` for the first boot. If you need browser access from a
different origin, set `webui.cors_origins` to an explicit allowlist. The API
listener also ships with bounded `webui.read_timeout`, `webui.read_header_timeout`,
`webui.write_timeout` and `webui.idle_timeout` defaults so slow clients do not
hold the management port open indefinitely.

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
- **Audit:** Syslog (CEF), CobaltDB-backed queryable store, HMAC-chained
  immutable logs, session recording.
- **Ops:** Hot reload on `SIGHUP`, Prometheus metrics parity with Spec §18.1,
  migration tools
  (`migrate vsftpd|proftpd|ssh-keys`).
- **MCP server:** `stdio` MCP server exposing users, sessions, transfers and
  audit queries for AI/LLM integration.

---

## License

Open source — MIT or Apache 2.0 (to be finalized per Spec §0).

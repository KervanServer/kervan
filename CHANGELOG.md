# Changelog

## v0.4.0 (2026-10-09)

### Added

- **audit:** tamper-evident file logs, on by default
  (`audit.integrity`). Each record is HMAC-chained to the previous one,
  using a per-installation key in `<data_dir>/audit.key`.
  - `kervan audit verify` detects modified, deleted, inserted and
    reordered records, and exits non-zero on any finding.
  - The line format stays backward compatible: the chain is an extra
    `chain` field.
  - The chain continues across restarts, and backups include the key.

## v0.3.0 (2026-10-08)

### Added

- **FTP:** virtual hosts through RFC 7151 `HOST` (or the TLS server name
  for FTPS). Each host has its own banner and an optional group
  restriction, so several tenants can share one address.

- **audit:** `syslog` output (RFC 5424) over UDP, TCP, TLS or Unix sockets.
  It supports `format: cef` for SIEMs, configurable facility, RFC 6587
  framing with automatic reconnects, and escaping that prevents log
  injection.

### Fixed

- **FTP:** `FEAT` reply lines start with a space as RFC 2389 requires. They
  were previously stripped, which some clients reject.

### Changed

- **WebUI live updates are event-driven.** Sessions, transfers and audit
  events now push a WebSocket snapshot within about 200 ms of a change.
  Bursts collapse into one frame, and an idle connection gets a 15 s
  heartbeat instead of a frame every 2 s. The message format is unchanged.

## v0.2.0 (2026-10-08)

### Added

- **OIDC single sign-on** for the WebUI with any OpenID Connect provider.
  Uses discovery, the authorization-code flow with PKCE, and JWKS-verified
  ID tokens (RS/PS/ES/EdDSA; `none` and HMAC are rejected); the
  implementation is standard library only.
  - Accounts are provisioned on first sign-in and bound to the provider
    subject. Local and LDAP accounts cannot be taken over.
  - `allowed_groups` restricts who may sign in, `admin_groups` syncs the
    role, and `group_mapping` maps provider groups to Kervan groups.
  - The session reaches the SPA through a one-time code, never in a URL.
  - Verified against Dex in a real browser.
- **API:** `Start` now binds the WebUI/API port synchronously, so a busy
  port fails startup instead of only being logged.

- **groups:** permission and storage-quota templates. A user's primary group
  supplies permissions and quota for every protocol and the API, with
  per-user overrides (`custom_permissions`, `max_storage`). Per-user quotas
  are new: previously every user had `quota.default_max_storage`.
  - Managed through `/api/v1/groups`, `kervan group list|create|delete`
    (and `kervan user create --group`), and a new WebUI Groups page.
  - The Users page gains an Edit dialog for group, quota and permissions.
  - Renames carry memberships along; deleting a group that has members
    needs `force`.
  - User import/export carries `primary_group` and `max_storage`.

## v0.1.1 (2026-10-08)

### Added

- **SCP:** recursive legacy copies (`scp -O -r`) in both directions. Upload
  follows OpenSSH semantics: a missing target becomes the copied directory,
  and an existing one receives it as a subdirectory. Directory times are
  kept with `-p`, a directory is refused without `-r`, and nesting is capped
  at 128 levels. Covered by an OpenSSH interop test.
- **release:** the Release workflow publishes multi-arch (`linux/amd64`,
  `linux/arm64`) images to `ghcr.io/kervanserver/kervan`. The Dockerfile
  cross-compiles on the build host, so only the final stage is emulated.

## v0.1.0 (2026-10-07)

Verified end to end against a running server with real clients: OpenSSH
`sftp`/`scp` (SFTP and legacy modes), curl (FTP in EPSV/PASV/PORT/EPRT, FTPS
explicit/implicit, SFTP, SCP), a headless browser for the WebUI, and the
Docker image. Those client runs are now part of the test suite
(`internal/server/interop_test.go`).

### Fixed

- **SFTP:** reverted the v0.0.2 DATA/NAME packet-type swap. The draft and
  OpenSSH use DATA=103 and NAME=104; v0.0.2 broke every real client
  ("Expected SSH2_FXP_NAME(104) packet, got 103") while the wire tests,
  which encoded the same misreading, stayed green.
- **SFTP:** SETSTAT/FSETSTAT are implemented (truncate; best-effort
  permissions/times). OpenSSH 9+ `scp` failed every upload with
  "remote fsetstat: Operation unsupported".
- **SFTP:** the session sends `exit-status`, so `scp` no longer exits 1 after
  a successful transfer.
- **SSH:** authorized_keys entries with a comment or options now match.
  Before this, only keys imported through `migrate ssh-keys` worked.
- **SCP:** shell-quoted targets are unquoted, so curl and libssh2 uploads no
  longer land in a file literally named `'/path'`. `-p` sends the `T` time
  record that libssh2 requires and applies incoming times. Completed
  transfers are no longer failed when libssh2 closes without the final ack.
  Ambiguous multi-word targets are refused, as OpenSSH does.
- **store:** the JSON store is safe to share between the server and CLI
  commands. Writes are locked read-modify-writes of the file, and reads pick
  up external changes. Before this, a user created with `kervan user create`
  against a running server could not log in, and the server's next write
  silently deleted it.
- **WebUI:** live updates work in browsers. The server rejected the
  `auth.<token>` WebSocket subprotocol that the WebUI (and README) use, so
  the dashboard was stuck in snapshot mode.
- **WebUI:** page content now starts at the top of the viewport instead of
  one screen height down, and the compact sidebar links are styled again.
  A Radix tooltip had stringified NavLink's className callback.
- **config:** `ftps.enabled: false` disables FTPS even when a certificate is
  configured. `audit.enabled: false` disables the audit sinks.
- **CI:** fixed the gofmt and staticcheck failures on master. The
  cross-protocol tests use ephemeral ports instead of fixed ones.

### Added

- **security:** `security.allowed_ips` / `denied_ips` are enforced on FTP,
  FTPS, SFTP/SCP and the API, and can be reloaded at runtime.
  `ftp.max_connections` / `sftp.max_connections` are enforced. Over-limit
  FTP clients get `421` and the rejection is audited.
- **security:** a per-address login ban (`ip_ban_threshold`,
  `ip_ban_duration`, `whitelist_ips`) shared by FTP, SFTP and the API, with
  IPv6 grouped per /64. `brute_force.enabled` now also controls account
  lockout, and every `brute_force.*` key is reloadable.
- **security:** `auth.require_special_char` is enforced, CSP (hash-pinned
  inline script) and HSTS (on TLS) headers are sent, and `ftps.client_auth`
  / `client_ca_file` add client-certificate verification for FTPS.
- **FTP:** `EPSV`, `PORT`/`EPRT` (with `ftp.active_mode` and bounce
  protection), `REST` resume for `RETR`/`STOR`, `CDUP`.
- **ops:** `server.pid_file` is written and removed on shutdown, and a
  startup warning is logged for settings that have no effect. Session
  `last_seen_at` is updated on activity, accept loops back off on errors,
  and the health check `cobaltdb` was renamed to `store`.
- **CI:** a `go test -race` job.

### Security

- **deps:** Go toolchain go1.26.2 -> go1.26.8 and x/crypto v0.57.0,
  x/net v0.59.0, x/text v0.42.0, x/sys v0.48.0. govulncheck had reported
  22 reachable vulnerabilities, including in the SSH stack and in
  crypto/tls, crypto/x509 and net/http; it now reports 0.
- **WebUI deps:** react-router 7.18 (the only affected runtime package),
  vite and related build tooling, and vitest 5. npm audit had reported 14
  advisories (2 critical); it now reports 0.

### Changed

- **CI:** GitHub Actions CI runs only on manual dispatch
  (`gh workflow run CI`). Use `make check` locally for the same checks and
  `make audit` for vulnerability scans.

### Removed

- Build and coverage artifacts that had been committed (`dist/`,
  `kervan.exe`, root-level coverage profiles).

## v0.0.2 (2026-09-25)

Seven post-release defects fixed by bug-hunt rounds 26-48. Every fix was
proven with a failing reproduction (red) and a durable regression test
(green); the full suite is green (24 packages) on this tree.
(Commit 527caf2.)

### Fixed
- **SFTP:** the idle deadline is renewed on session activity — `idle_timeout`
  acted as an absolute lifetime cap that killed active transfers at the
  timeout.
- **SFTP:** the SSH_FXP_NAME/SSH_FXP_DATA packet type constants are corrected
  (they were swapped against the wire spec, wire-breaking READ replies and
  READDIR/REALPATH listings).
- **storage:** the memory backend is shared per user across protocols and
  requests — per-call instantiation isolated every session's data.
- **config:** the default audit sink path is anchored to `server.data_dir`
  instead of the CWD-relative `./data` default.
- **FTP:** the explicit-FTPS data-TLS handshake is performed lazily — RFC 4217
  client ordering no longer deadlocks PROT P transfers.
- **FTP:** the advertised MLST command is implemented (RFC 3659 single-entry
  listing on the control channel).
- **FTP:** zero-I/O protected transfers complete their lazy TLS handshake —
  empty MLSD listings and zero-byte RETR aborted mid-handshake.

## v0.0.1 (2026-09-25)

First tagged release: 16 defects fixed by a 25-round proof-driven bug hunt.
Every fix was proven with a failing reproduction (red) and a durable
regression test (green); the full set was reviewed together for interactions.
(Commit 9b63cc7 — go vet clean, go test 24/24 packages green.)

### Fixed

- **memory backend:** closing a file whose path was removed or renamed while
  open no longer panics.
- **SFTP:** ATTRS replies are wire-correct — clients misparsed a spurious
  length prefix on every STAT/READDIR/REALPATH response.
- **SFTP:** READDIR no longer emits truncated NAME packets; unstatable
  entries are skipped.
- **FTP:** FEAT advertises the first and last features (RFC 2389 framing).
- **FTP:** mid-connection re-login no longer orphans the previous session.
- **FTP:** commands pipelined with AUTH TLS are honored across the upgrade.
- **FTP:** control-channel lines bounded at 64 KiB (pre-auth exhaustion).
- **SCP:** protocol lines (sink header, ack messages) bounded at 64 KiB.
- **MCP:** declared frame sizes bounded at 16 MiB before allocation.
- **LDAP:** declared BER message sizes bounded at 16 MiB before allocation.
- **auth:** out-of-range argon2id parameters rejected at import and
  verification (login-path OOM).
- **config:** removed the unwired sftp.max_packet_size knob.
- **config:** webui.port validated (1-65535) when the web UI is enabled.
- **S3:** ReadDir follows ListObjectsV2 pagination — entries beyond the
  first 1000 are no longer invisible.
- **API:** /api/audit streams the audit log with bounded pages instead of
  replaying the whole file per request.
- **API:** unmatched HTTP paths no longer create unbounded metric series.

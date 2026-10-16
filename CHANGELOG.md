# Changelog

## Unreleased

### Fixed

- **Quotas are shared by all of a user's sessions.** Every FTP/SFTP/SCP
  connection and every WebUI/API file request used to get its own quota
  tracker, each measured from storage when it was created. Parallel
  sessions could therefore together store more than the quota, and every
  API file request walked the user's whole directory tree. One tracker per
  user is now shared (`quota.Registry`). A changed quota reaches open
  sessions at the user's next sign-in or request. Trackers follow the
  account's ID, so a rename keeps the usage and a deleted account
  re-created under the same name starts fresh. Trackers of deleted
  accounts are dropped by the 30-second access sweep. When an FTP/SFTP/SCP
  connection or an API request ends, uploads it left open stop counting
  as in progress, so one dropped upload cannot block re-measurement.

### Changed

- **`quota.check_interval` takes effect.** A user's usage is re-measured
  from storage at sign-in once the last measurement is older than this
  (default `60s`; `0` or a negative value also means `60s`, never
  "re-measure on every request"), and only while none of their uploads,
  deletes or renames is in progress. The setting no longer triggers the
  "no effect" startup warning.

- **License: MIT.** Added `LICENSE`. It ships in the release archives and
  in the container image (`/usr/share/licenses/kervan/LICENSE`, with the
  `org.opencontainers.image.licenses` label).

## v0.9.0 (2026-10-14)

### Security

- **Go 1.26.9 and `golang.org/x/net` v0.60.0.** `govulncheck` reported ten
  standard-library vulnerabilities reachable from Kervan's code: HTTP/2 and
  HTTP/1 desynchronisation and DoS issues in `net/http`, unbounded Range
  header parsing, MIME header memory limits, and malformed TLS ECH
  extensions. All are fixed by these versions; the container build uses
  Go 1.26.9 too.

- **Concurrent sign-ins could exhaust memory.** Every password check runs
  Argon2id with 64 MiB, and nothing limited how many ran at once. 300
  simultaneous wrong-password logins for a known username drove the server
  to 13.7 GB before any lockout or IP ban reacted. Argon2 work now runs
  within a 256 MiB budget; further checks wait up to 30 s, then fail. The
  same flood now peaks at about 540 MB (about 410 MB with
  `GOMEMLIMIT=400MiB`).

### Added

- **File browser.** Download and preview buttons: text and code files up
  to 1 MiB, images up to 20 MiB. Several files can be uploaded at once by
  picker or by dropping them on the browser, with a progress counter.
  Sizes are human-readable and a Modified column was added.

### Fixed

- **Regular users got a broken WebUI.** Their landing page was the admin
  dashboard, which stayed on loading placeholders, and the menu offered
  admin pages (Users, Groups, Monitoring, Configuration) that could only
  fail. Non-admins now land on Files, see only the pages they can use, and
  are redirected from admin routes. Two-factor setup moved from the
  dashboard to **My Account**, so every password user can reach it.

### Changed

- `/api/v1/audit/events` and `/api/v1/transfers` include `pagination` even
  when auditing or transfer tracking is off.
- `/api/v1/auth/totp` answers 405 for methods it does not support while
  two-factor auth is disabled (it returned 400).

## v0.8.0 (2026-10-13)

### Added

- **Password-protected share links.** Links can require a password (at
  least 6 characters, stored as an Argon2id hash) and the WebUI now sets
  the download limit too. Share URLs open a public landing page
  (`/s/<token>`) that shows the file name, size, expiry and remaining
  downloads, and asks for the password when one is set. Wrong passwords
  count toward the per-address login ban. Scripts can use
  `/api/v1/share/<token>` with an `X-Share-Password` header. Creating,
  revoking and downloading through share links is audited (`share.*`);
  the audit record holds only a short token prefix.
- **Audit filters.** `/api/v1/audit/events` and `/api/v1/audit/export`
  accept `from` and `to` (RFC 3339 or `YYYY-MM-DD`). The Audit page now
  filters by date, event type, status, user and text, pages through
  results, shows user, protocol, address and details for each event, and
  exports with the same filters.

- **MCP tools.** `get_user` (sign-in restrictions, SSH key fingerprints,
  effective policy), `list_groups` and `verify_audit` (checks the
  tamper-evident chain). `audit_query` now also filters by `status`,
  `from` and `to`. The README gains an MCP section.
- **Fuzz targets** for the SFTP packet handler, SCP sink and exec parser,
  the FTP command loop and its path/command parsers (`go test -fuzz`).
  `go test` runs their seed corpora, including the crashers found so far.
- **Interop suite.** `make interop` runs lftp (FTP, explicit and implicit
  FTPS, SFTP), rclone (FTP, FTPS, SFTP) and paramiko against a running
  server: 26 checks.
- **Docs.** `docs/openapi.yaml` (OpenAPI 3.1 reference for the REST API),
  `CONTRIBUTING.md` (workflow, architecture map, conventions, release
  steps), and an up-to-date `.project/ROADMAP.md`.

### Fixed

- **FTPS uploads from lftp (GnuTLS) failed** with `426` and empty files.
  Under TLS 1.3 the server sent session tickets on the data connection that
  the client never read, so its close turned into a TCP reset that dropped
  the upload. Data connections no longer issue session tickets.
- **An SFTP write at a huge offset crashed the server** when the user's
  storage was the memory backend: the backend tried to allocate the whole
  gap. The memory backend now refuses gaps over 64 MiB and files over
  4 GiB, writes in place instead of copying the file on every write, and
  rejects overflowing seeks.
- **A panic in an SFTP/SCP channel ended the whole process**, because the
  channel goroutine had no recovery. It now ends only that channel and is
  logged.
- **SFTP packets declaring a large length** (up to 16 MiB) were allocated
  in full before any data arrived. Buffers above 512 KiB now grow only as
  the data actually arrives.
- Failed FTP transfers are now logged with the underlying error.
- **Admin file actions were audited under the wrong user.** When an admin
  worked on another user's files through `/api/v1/files/{user}/…`, the
  audit record named the owner as the actor. It now names the admin, with
  the owner in `meta.target_user`.
- **The WebSocket ignored sign-in restrictions.** It now applies expiry,
  allowed addresses and allowed protocols at connect and before every
  snapshot, so a disabled or expired account loses its live stream too.
- **Client-sent `X-Auth-*` headers** are now stripped before routing. They
  were already overwritten on authenticated routes, but public routes could
  see them.
- **Password-hash export** (`/api/v1/users/export?include_password_hashes=1`)
  now requires a signed-in admin session; a `users:read` API key gets 403.
  Every user export is audited (`admin.user.export`).
- **Config patches** can no longer change fields the config view redacts,
  such as `storage.backends.*.options.access_key`.
- **Existence probing.** Another user's session or share link now returns
  404, like a missing one, instead of 403. Deleting an unknown API key
  returns 404 (was 400). An admin naming an unknown user under
  `/api/v1/files/{user}/` gets 404 (was 500).
- `GET /api/v1/server/status` rejects other methods with 405.
- Audit export validates its filters before reading the log, and streams
  the log, keeping only matching events. A CSV export cut short no longer
  tries to write a JSON error after the headers were sent.
- `completed_at` is omitted for transfers still in progress (it showed the
  zero time).

### Changed

- Share-link `share_url` is now the landing page (`/s/<token>`). The direct
  download is `download_url`. Existing `/api/v1/share/<token>` links keep
  working.

## v0.7.0 (2026-10-11)

### Added

- **Per-user sign-in restrictions.** Each user can have an expiry time,
  allowed IP/CIDR ranges, allowed protocols (`ftp`, `ftps`, `sftp`, `scp`,
  `webui`) and a cap on concurrent FTP/SFTP/SCP sessions. Set them from the
  Users page, the users API, or the new `kervan user access` command. They
  apply on every protocol and to existing WebUI/API sessions. Every 30 s,
  open FTP/SFTP/SCP connections of expired, disabled or deleted accounts,
  or from addresses no longer allowed, are ended and audited as
  `session.revoked`.
- **Per-address connection limit.** `security.max_connections_per_ip`
  caps concurrent FTP and SSH connections from one address, counted across
  both protocols.

- **Wider audit coverage.** Sign-ins through the WebUI and OIDC (both
  success and failure), password changes, user, group and API-key
  management, config updates and reloads, session kills, and file
  operations in the WebUI file browser are now audited. Directory
  create/delete and renames are audited over FTP and SFTP too, and renames
  record the destination in `meta.to`. Admin events name the affected
  account in `meta.target_user`; passwords are never logged.

- **FTP commands.** New: `ABOR` (including during a transfer, Telnet- or
  urgent-data-framed), `STOU`, `MFMT`, `SITE CHMOD`/`SITE HELP`, `STAT`,
  `HELP`, `MODE`, `STRU`, `ALLO`, and the `X*` legacy aliases. FTP
  upload/download audit events now include `meta.bytes`, and failed or
  aborted transfers are audited.
- **SFTP extensions.** `posix-rename`, `statvfs`/`fstatvfs` (`df` shows the
  quota), `fsync` and `limits` (OpenSSH clients switch to 255 KiB
  requests). Local storage now reports real disk space.

### Changed

- **SFTP sessions are capped at 1024 open handles.**
- **One SFTP audit event per transfer.** SFTP used to write a `file.write`
  event for every WRITE packet (thousands for one large upload). It now
  writes one `file.write` or `file.read` event when the handle closes, with
  the byte count in `meta.bytes`. A transfer cut off by a dropped
  connection is recorded with status `incomplete`.

## v0.6.0 (2026-10-10)

### Added

- **File-count quota.** `quota.default_max_files` is now enforced, with
  per-group and per-user `max_files` overrides (API, CLI
  `--max-files`, WebUI). Only regular files count, and overwriting a file
  does not.

- **Bandwidth limits.** `max_bandwidth` can be set per user and per group,
  with `bandwidth.default_user_rate` as the default and
  `bandwidth.max_total` as a server-wide cap. Limits apply to FTP, SFTP,
  SCP and API transfers, are shared across a user's parallel connections,
  and reload at runtime. Measured at 1.0 MiB/s for a 1 MiB/s limit over FTP
  and SFTP.

- **Self-service account.** A new WebUI **My Account** page and
  `/api/v1/account` endpoints show a user's effective policy, and let them
  change their password (local accounts) and manage their SSH public keys.
  Previously neither was possible without the server CLI.
- **Admin password reset and SSH key editing** from the WebUI Users dialog
  and `PUT /api/v1/users` (`password`, `authorized_keys`).

### Changed

- **`quota.default_max_files` now defaults to 0 (unlimited).** Configs
  generated by older `kervan init` versions contain
  `default_max_files: 100000`, which had no effect before and now applies.
  Set it to `0` to keep the previous behavior.

### Security

- **Session revocation:** session tokens now carry `iat`, and a password
  change or reset revokes every WebUI/API session issued before it. An
  admin reset also ends the user's protocol sessions.
- **SSH keys:** keys with `authorized_keys` options are rejected by the API,
  and skipped with a warning by `kervan migrate ssh-keys`. Kervan does not
  enforce the options, so a `from=` restriction would have been silently
  lost.

### Fixed

- **quota:** renaming a file onto an existing one now releases the
  replaced file's bytes. Before, they stayed counted against the user's
  quota.

- **WebUI:** an expired or revoked session now returns to the sign-in screen
  with an explanation, instead of failing every request with "Session
  expired".

## v0.5.0 (2026-10-09)

### Added

- **audit:** size-based rotation and retention for file outputs
  (`max_size_mb`, default 100; `max_backups`, default 10). Previously the
  audit log grew without bound.
  - The HMAC chain continues across rotated files. Retention seals an
    `audit.pruned` record before deleting a file, so `kervan audit verify`
    can tell retention from tampering.
  - The API, export, MCP, verify and backup/restore cover the rotated
    files. Restore validates rotated file names.

### Fixed

- **audit:** export and the MCP recent-events reader no longer load the
  whole log into memory. `kervan mcp` now reads the configured audit path
  instead of always `<data_dir>/audit.jsonl`.

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

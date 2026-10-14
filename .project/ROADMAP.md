# Project Roadmap

> **Status (2026-10-13, after v0.7.0):** the original seven-phase plan from
> 2026-04-11 is complete except where noted below. This file now tracks
> what is left before v1.0. Release-by-release detail lives in
> `CHANGELOG.md`.

## Where the project stands

Kervan runs FTP, FTPS (explicit and implicit), SFTP and SCP on one process,
with a REST API, an embedded React WebUI and an MCP server. Measured on a
single host (loopback, local storage):

| Spec §13 target | Target | Measured |
|---|---|---|
| Concurrent connections | 10,000+ | 10,000 FTP connections held |
| Memory per idle connection | < 256 KB | ~10 KB |
| FTP command latency | < 1 ms | ~21 µs with 10,000 connections open |
| Startup time | < 500 ms | 13 ms to a healthy `/health` |
| Binary size | < 30 MB | 12 MB |
| WebUI bundle (gzip) | < 2 MB | ~200 KB |
| Throughput | near wire speed | FTP 1.4 GB/s up, 2.3 GB/s down; SFTP ~340 MB/s per stream (cipher-bound), 580 MiB/s across 16 streams |
| Authenticated SFTP sessions | — | 2,000 sessions in 2.4 s, ~71 KB each |
| S3 storage (SeaweedFS) | limited by S3 | SFTP 260 MiB/s across 8 streams; FTP 171 MiB/s up, 320 MiB/s down; server RSS ≤ 151 MB |
| Sign-in flood | — | 300 simultaneous wrong-password logins: ~540 MB peak (Argon2 budget) |

## Original plan: done

**Phase 1, critical fixes:** API-key auth, enforced IP filters and
connection caps, password policy, honest store naming, `status --insecure`.

**Phase 2, core completion:**
- Groups with permission, quota and bandwidth templates.
- OIDC WebUI sign-in.
- FTP active mode and extensions.
- MCP tools for users, groups, audit queries and audit-chain verification.
- The JSON store adopted as the persistence layer.

**Phase 3, hardening:**
- Request-body limits.
- CSP and HSTS.
- Live `last_seen_at`.
- Dead-config warnings.
- S3 uploads staged on disk instead of in memory.

**Phase 4, testing:**
- Protocol packages at 66–79% coverage.
- API integration tests.
- WebUI component tests.
- Race detector in `make check`.
- Real-client interop tests.
- Fuzz targets.

**Phase 5, performance:**
- Event-driven WebSocket updates.
- Route-level code splitting.
- The targets in the table above.

**Phase 6, documentation:**
- README rewritten around shipped features.
- `CONTRIBUTING.md` with an architecture map.
- OpenAPI reference (`docs/openapi.yaml`).

**Phase 7, release:**
- goreleaser binaries and a multi-arch GHCR image.
- Backup and restore.
- A documented release procedure.

Shipped beyond the original v1.0 scope:
- Tamper-evident (HMAC-chained) audit log with rotation.
- Syslog and HTTP audit sinks.
- Per-user sign-in restrictions.
- Bandwidth limits and file-count quotas.
- Password-protected share links.
- Self-service accounts.

## Interop matrix (spec §14.1)

Verified with `make interop` and the protocol test suites:

| Client | FTP | FTPS | SFTP | SCP |
|---|---|---|---|---|
| OpenSSH `sftp` / `scp` | — | — | ✓ | ✓ |
| curl | ✓ | ✓ | ✓ | ✓ |
| lftp | ✓ | ✓ (explicit + implicit) | ✓ | — |
| rclone | ✓ | ✓ | ✓ | — |
| Python paramiko | — | — | ✓ | — |
| Python ftplib | ✓ | ✓ (explicit) | — | — |

Not yet verified: FileZilla, WinSCP, Cyberduck and Transmit. These are
GUI clients, so checking them needs a manual session.

## Before v1.0

- [ ] Manual check with FileZilla and WinSCP (FTP, FTPS, SFTP).
- [x] Longer fuzzing campaign: 90 minutes, ~230 million executions across
      the five FTP/SFTP/SCP targets, no crashes or hangs (2026-10-14).
- [x] Load test with authenticated SFTP sessions and S3 storage (see the
      table above). It found an unbounded Argon2 memory use under
      concurrent sign-ins, which is now fixed.
- [x] License: MIT (`LICENSE`).

## After v1.0

- Queryable audit store and a database-backed metadata store for large
  installations (deferred by decision; the JSON store and JSONL audit
  files remain the supported setup).
- File-browser preview and drag-and-drop upload in the WebUI.
- Shared or clustered control plane.
- Zero-copy (`sendfile`/`splice`) transfers for the local backend.

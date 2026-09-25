# Changelog

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

# Contributing to Kervan

Thanks for helping. This guide covers the local workflow, the layout of the
code, and the conventions changes are expected to follow.

## Prerequisites

- Go (the version in `go.mod`'s `toolchain` line; `go` downloads it).
- Node.js 22+ for the WebUI.
- `staticcheck`: `go install honnef.co/go/tools/cmd/staticcheck@latest`.
- A C toolchain for `go test -race`.
- Optional: OpenSSH (`sftp`, `scp`, `ssh-keygen`) and `curl`, which some
  protocol tests use when present, and Docker for `make interop`.

## Everyday workflow

```bash
go run ./cmd/kervan init --config ./kervan.yaml   # first run only
go run ./cmd/kervan admin create --username admin --password 'StrongPass123!'
go run ./cmd/kervan --config ./kervan.yaml

cd webui && npm install && npm run dev            # WebUI with hot reload
```

Before you commit, run:

```bash
make check
```

It runs gofmt, `go vet`, staticcheck, the Go tests (also under `-race`), the
WebUI tests, and a check that `internal/webui/dist` matches `webui/src`.
**CI does not run on push or pull requests** (see the README's *Continuous
integration* section), so `make check` is the gate. Commit the regenerated
`internal/webui/dist` together with any WebUI change; `make check` rebuilds
it, and its last step fails until the new bundle is committed.

Other targets:

| Target | What it does |
|---|---|
| `make build` | WebUI bundle plus the `kervan` binary |
| `make interop` | lftp, rclone and paramiko against a running server (see `scripts/interop/run.sh`) |
| `make audit` | `govulncheck` and `npm audit` |
| `make release-snapshot` | Local goreleaser build without publishing |

### Fuzzing

The protocol parsers have Go fuzz targets. `go test` runs their seed corpora
(including past crashers under `testdata/fuzz`). To fuzz for real:

```bash
go test ./internal/protocol/sftp -run '^$' -fuzz '^FuzzSFTPHandler$' -fuzztime 5m
go test ./internal/protocol/sftp -run '^$' -fuzz '^FuzzSCPSink$'     -fuzztime 2m
go test ./internal/protocol/sftp -run '^$' -fuzz '^FuzzParseSCPExec$' -fuzztime 1m
go test ./internal/protocol/ftp  -run '^$' -fuzz '^FuzzFTPCommands$' -fuzztime 5m
go test ./internal/protocol/ftp  -run '^$' -fuzz '^FuzzFTPParsers$'  -fuzztime 1m
```

Commit any new file the fuzzer writes under `testdata/fuzz/` along with the
fix, so the input stays a regression test.

## Architecture

One process runs every listener. They all share a single auth engine,
session manager, audit engine and user-VFS builder, which
`internal/server` wires together.

```
             ┌───────────── internal/server (App: wiring, reload, sweeper) ─────────────┐
 clients ──► │ protocol/ftp   protocol/sftp (+SCP)   api (REST, WebUI, WebSocket)   mcp │
             │        │               │                     │                          │
             │        └──── netguard (IP filter, per-IP and per-protocol limits) ──────│
             │        auth (users, groups, LDAP, OIDC accounts, access policy)         │
             │        session · transfer · events · audit · throttle · quota           │
             │        vfs (mounts, chroot, permissions, quota/throttle wrappers)       │
             │        storage/local · storage/memory · storage/s3                      │
             │        store (embedded JSON document store under data_dir)              │
             └──────────────────────────────────────────────────────────────────────────┘
```

| Package | Responsibility |
|---|---|
| `cmd/kervan` | Entry point and CLI subcommands (`init`, `user`, `group`, `apikey`, `audit`, `backup`, `migrate`, `mcp`, …) |
| `internal/server` | Builds every subsystem from the config, starts listeners, applies runtime reloads, runs the session sweeper |
| `internal/protocol/ftp` | FTP/FTPS control loop, data channels, virtual hosts, ABOR watcher |
| `internal/protocol/sftp` | SSH server, SFTP v3 handler and extensions, SCP source/sink |
| `internal/api` | REST API, auth middleware, share links, OIDC flow, WebSocket snapshots; serves the WebUI |
| `internal/auth` | Users, groups, password hashing, LDAP, external (OIDC) accounts, SSH keys, per-user access policy, IP throttle |
| `internal/oidc` | OpenID Connect relying party (discovery, PKCE, ID-token verification); `oidctest` is an in-process provider for tests |
| `internal/vfs` | Mount table, path resolution/chroot, `UserVFS` permission checks, quota and bandwidth wrappers |
| `internal/storage/*` | Backends: local disk, in-memory, S3 |
| `internal/quota`, `internal/throttle` | Storage/file-count quota tracking; token-bucket bandwidth limits |
| `internal/netguard` | IP allow/deny lists, connection limiters, accept backoff |
| `internal/session`, `internal/transfer` | Live session and transfer registries |
| `internal/audit` | Event types, async engine, file (HMAC-chained, rotating), HTTP and syslog sinks, verification |
| `internal/events` | In-process change notifications feeding the WebSocket |
| `internal/store` | Embedded JSON store, safe for concurrent CLI and server use |
| `internal/config` | YAML loading, defaults, env overrides, validation |
| `internal/crypto`, `internal/acme` | TLS and host-key helpers; ACME certificates for FTPS |
| `internal/mcp` | MCP server over stdio |
| `internal/webui` | Embeds `internal/webui/dist` and sets the CSP |
| `webui/` | React 19 + Vite + Tailwind source of the WebUI |
| `scripts/` | WebUI build/copy helper, Docker entrypoint, interop suite |

Where to look for common changes:

- **New config key:** `internal/config/config.go` (struct and defaults),
  `validate.go`, `kervan.example.yaml`, and, if it is reloadable,
  `applyRuntimeConfig` in `internal/server/server.go`.
- **New audit event:** add the type in `internal/audit/event.go`, emit it
  where the action happens, and mention it in the README's *Audit Log*.
- **New REST endpoint:** register it in `internal/api/server.go`, decide its
  API-key scope in `apiKeyPermissionAllowsRequest`, and document it in
  `docs/openapi.yaml` and the README's API table.
- **Anything a user can do:** it must go through `vfs.UserVFS`. That is
  where permissions, chroot, quota and bandwidth are enforced; never use a
  storage backend directly from a protocol.

## Conventions

- **Tests come with the change.** Protocol behaviour gets a wire-level test
  (see `wire_types_test.go`, `extra_commands_test.go`). Security checks get a
  test that proves the refusal, not only the happy path.
- **Validate input at the boundary.** Bound sizes and lengths, and never let
  a client-controlled number size an allocation directly. The fuzzers exist
  because of exactly that kind of bug.
- **Errors to clients stay generic, logs and audit carry the detail.** For
  example, an expired account gets "invalid credentials" while the audit
  record says why.
- **Never log or audit secrets:** passwords, tokens and keys. Share tokens
  appear only as a short prefix.
- **Docs are part of the change:** README, `CHANGELOG.md` (under
  *Unreleased*), `kervan.example.yaml`, and `docs/openapi.yaml` when the API
  changes.
- **Commit messages:** `type(scope): summary` (`feat`, `fix`, `docs`, `test`,
  `refactor`, `chore`), with a body explaining why when it isn't obvious.

## Releasing

1. Move the *Unreleased* CHANGELOG section under `## vX.Y.Z (date)` and
   commit it as `docs: vX.Y.Z release notes`.
2. `git tag vX.Y.Z && git push origin vX.Y.Z`. The Release workflow builds
   the binaries and the multi-arch GHCR image, and drafts the release.
3. Check a downloaded artifact against `checksums.txt` and run
   `kervan version`, then publish the draft with that CHANGELOG section as
   its notes.

## Reporting security issues

Do not open a public issue for a vulnerability. Contact the maintainers
privately first, and allow time for a fix before disclosure.

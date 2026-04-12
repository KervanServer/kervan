# Project Analysis Report

> Auto-generated comprehensive analysis of Kervan
> Generated: 2026-04-11

## 1. Executive Summary

Kervan is a modular-monolith file transfer server written in Go. It combines FTP, FTPS, SFTP, SCP, an embedded React WebUI, a REST API, an MCP stdio server, local/LDAP auth, share links, backup/restore tooling, and local/memory/S3-backed storage in a single deployable binary.

Key measured metrics:

| Metric | Value |
|---|---:|
| Total repository files | 7,761 |
| Go files | 108 |
| Go packages | 24 |
| Go LOC | 23,384 |
| Frontend source files | 26 |
| Frontend LOC | 2,790 |
| Test files | 48 |
| Overall Go coverage | 53.6% |
| Direct Go deps | 2 |
| Frontend runtime deps | 13 |
| Registered HTTP route patterns | 45 |
| TODO/FIXME/HACK markers | 0 |

Overall health: **6.5/10**.

Top strengths:
- Extremely small backend dependency surface and clean static analysis: `go vet ./...` and `staticcheck ./...` both pass.
- Real operational depth: Docker, systemd, CI, Goreleaser, health, metrics, debug server, backups, migrations.
- Broad backend test presence: every Go package has tests, and `go test ./... -count=1` passes.

Top concerns:
- The documented architecture and the actual architecture diverge sharply. The spec is CobaltDB-centric; the implementation uses a JSON file store in `internal/store/store.go`.
- The MCP/CLI/deployment task matrix still overstates what is fully shipped. The MCP server exists, but the tool/resource surface is still a narrow MVP and several admin/packaging items remain partial.
- Several declared config/security controls are not enforced at runtime, including IP allow/deny lists and connection limits.

## 2. Architecture Analysis

### 2.1 High-Level Architecture

Architecture style: **single-process modular monolith**.

Text data flow:

```text
FTP / FTPS / SFTP / SCP / WebUI / REST / MCP clients
                |
                v
          cmd/kervan/main.go
                |
                v
         internal/server.App
                |
                +-- auth.Engine + UserRepository
                +-- store.Store (kervan-store.json + .bak)
                +-- session.Manager
                +-- transfer.Manager
                +-- audit.Engine + file/http sinks
                +-- VFS mount/resolver/user wrapper
                +-- storage backend (local|memory|s3)
                +-- api.Server
                +-- ftp.Server
                +-- sftp.Server
                +-- mcp.Server
```

Concurrency model:
- FTP and SFTP listeners manage goroutines with `sync.WaitGroup`.
- Session and transfer registries are mutex/atomic based.
- Audit HTTP sink batches through a background flush loop.
- WebSocket “live” updates are actually 2-second polling snapshots (`internal/api/websocket.go:139-149`), not event-driven pushes.

### 2.2 Package Structure Assessment

| Package | Responsibility | Assessment |
|---|---|---|
| `cmd/kervan` | CLI entrypoint, user/admin/migration/backup commands | Coherent but getting large. |
| `internal/server` | App composition, lifecycle, config patching | Central and oversized. |
| `internal/api` | HTTP API, middleware, metrics, WebSocket, file/user/share handlers | Biggest hotspot; too much in one file. |
| `internal/auth` | Local auth, password hashing, TOTP, LDAP, SSH public key auth | Strong cohesion. |
| `internal/vfs` | Mounts, path resolution, permissions, quota wrapper | Good abstraction boundary. |
| `internal/storage/*` | Local, memory, S3 backends | Sensible backend separation. |
| `internal/session` | Active session registry | Clean and focused. |
| `internal/transfer` | Transfer registry and counters | Clean and focused. |
| `internal/audit` | Audit events and sinks | Good separation. |
| `internal/mcp` | stdio MCP server | Useful MVP, small surface. |
| `internal/webui` | Embedded SPA serving | Small and focused. |

Boundary quality is generally good. There are no obvious circular-dependency problems. The real structural debt is file-level concentration, especially `internal/api/server.go` (2,385 LOC) and `internal/server/server.go` (1,085 LOC).

### 2.3 Dependency Analysis

Go modules from `go.mod`:

| Dependency | Version | Purpose | Notes |
|---|---|---|---|
| `golang.org/x/crypto` | `v0.50.0` | SSH, password hashing, ACME/autocert | Appropriate, not a stdlib candidate. |
| `gopkg.in/yaml.v3` | `v3.0.1` | YAML config load/write | Appropriate. |
| `golang.org/x/net` | `v0.52.0` indirect | Transitive networking | Present transitively. |
| `golang.org/x/sys` | `v0.43.0` indirect | OS/syscalls | Present transitively. |
| `golang.org/x/text` | `v0.36.0` indirect | Text helpers | Present transitively. |

Measured dependency hygiene:
- `govulncheck ./...`: **No vulnerabilities found**
- `go list -m -u all`: only minor transitive/tooling updates available

Frontend dependencies:
- React 19.2, React Router 7.14, Radix UI primitives, Tailwind 4.1, Vite 8, TypeScript 6.
- `npm audit --omit=dev --json`: **0 production vulnerabilities**
- `npm outdated --json`: only minor drift surfaced for `@vitejs/plugin-react` and `tailwindcss`.

Verdict: dependency hygiene is a strength. The bigger risk lies in custom code, not third-party libraries.

### 2.4 API & Interface Design

Primary implemented API surface:

| Method | Path | Notes |
|---|---|---|
| `GET` | `/health`, `/api/v1/health` | Structured health payload |
| `GET` | `/metrics`, `/api/v1/metrics` | Prometheus-style text |
| `POST` | `/api/login`, `/api/v1/auth/login` | Login |
| `GET`,`DELETE` | `/api/v1/auth/totp` | TOTP status/disable |
| `POST` | `/api/v1/auth/totp/setup` | TOTP setup |
| `POST` | `/api/v1/auth/totp/enable` | TOTP enable |
| `GET` | `/api/ws`, `/api/v1/ws` | WebSocket snapshots |
| `GET` | `/api/v1/server/status` | Admin only |
| `GET`,`PUT` | `/api/v1/server/config` | Admin only |
| `POST` | `/api/v1/server/config/validate` | Admin only |
| `POST` | `/api/v1/server/reload` | Admin only |
| `GET`,`POST`,`PUT`,`DELETE` | `/api/v1/users` | Admin only |
| `POST` | `/api/v1/users/import` | Admin only |
| `GET` | `/api/v1/users/export` | Admin only |
| `GET`,`POST`,`DELETE` | `/api/v1/apikeys` | CRUD only |
| `GET` | `/api/v1/sessions` | Filterable |
| `GET`,`DELETE` | `/api/v1/sessions/{id}` | Ownership/admin scoped |
| `GET`,`DELETE` | `/api/v1/share`, `/api/v1/share/{token}` | List/revoke/public download |
| `GET` | `/api/v1/audit/events` | Filterable |
| `GET` | `/api/v1/audit/export` | CSV/JSON export |
| `GET` | `/api/v1/transfers` | Active + recent |
| path-based | `/api/v1/files/{user}/{action}` | `ls`, `mkdir`, `rm`, `rename`, `share`, `upload`, `download`, `stat` |

Assessment:
- The API is practical and usable.
- Legacy `/api/...` and newer `/api/v1/...` routes coexist, which adds surface-area clutter.
- Files endpoints are split between legacy query-driven routes and a newer path-driven v1 route family.
- The API supports bearer tokens and API keys, but the auth surface is still narrower than the full original spec.

## 3. Code Quality Assessment

### 3.1 Go Code Quality

Strengths:
- Consistent style and naming.
- `gofmt`, `go vet`, and `staticcheck` are clean.
- Config loading/validation/reload is one of the best parts of the codebase.
- Structured logging and log rotation exist.

Weaknesses:
- `internal/api/server.go` and `internal/server/server.go` are oversized feature hubs.
- Panic recovery logs a panic value but no stack trace (`internal/api/server.go:1665-1693`).
- Environment overrides are much narrower than documented. `internal/config/loader.go:48-95` supports only a small subset of keys.

Concrete issues:
- `cmd/kervan/cli_commands.go:118-126` can disable TLS verification for the `status` command when `--insecure` is supplied.

### 3.2 Frontend Code Quality

Strengths:
- Small, readable React app.
- Clear page-based structure.
- Modern React functional components and TypeScript.

Weaknesses:
- The frontend test suite is focused on page/component flows and still lacks browser-level E2E coverage.
- Route-level code splitting exists, but the shared shell/vendor chunks still dominate the bundle.
- Transfer typing drifts from backend reality:
  - backend: `bytes_done`, `error_message` in `internal/transfer/manager.go:24-36`
  - frontend: `bytes_transferred`, `speed_bps`, `error` in `webui/src/lib/types.ts:69-81`

Bundle result from `npm run build`:
- JS: 338.85 kB
- CSS: 20.99 kB

### 3.3 Concurrency & Safety

Good:
- Mutex-protected session and transfer managers.
- WaitGroup-based listener shutdown in FTP/SFTP.
- Audit batching is isolated and asynchronous.

Risks:
- WebSocket updates are polling snapshots, not event-based.
- S3 `PutObject` reads the full body into memory (`internal/storage/s3/client.go:139-153`).
- `go test -race` could not be run here because the environment lacked CGO support.

### 3.4 Security Assessment

Positive:
- Argon2id/bcrypt password hashing
- TOTP support
- LDAP filter escaping
- SSH public-key auth
- Basic secure headers
- Zero findings from `govulncheck` and `npm audit --omit=dev`

Important gaps:
- `security.allowed_ips` / `security.denied_ips` are validated but not enforced anywhere.
- `max_connections` config fields are present but unused.
- No CSP or HSTS headers.
- Request-body size limits are inconsistent; only multipart user import applies a hard size cap.

## 4. Testing Assessment

### 4.1 Test Coverage

Measured results:
- `go test ./... -count=1`: **pass**
- `go test ./... -coverprofile=coverage -covermode=atomic`: **pass**
- overall statement coverage: **53.6%**
- packages with zero tests: **0**

Coverage by risk area:

| Package | Coverage |
|---|---:|
| `internal/config` | 89.2% |
| `internal/auth` | 70.2% |
| `internal/session` | 98.3% |
| `internal/webui` | 94.7% |
| `internal/api` | 52.1% |
| `internal/server` | 44.5% |
| `internal/protocol/ftp` | 18.5% |
| `internal/protocol/sftp` | 8.7% |

### 4.2 Test Infrastructure

Strengths:
- Every Go package has tests.
- Good use of in-memory/fake backends.
- CI runs formatting, vet, staticcheck, tests, frontend build, and Docker build.

Weaknesses:
- No Playwright/Cypress.
- No benchmarks or fuzzing.
- `go test -race` is not currently executable in this environment.

## 5. Specification vs Implementation Gap Analysis

### 5.1 Feature Completion Matrix

| Planned Feature | Status | Notes |
|---|---|---|
| Single binary FTP/FTPS/SFTP/SCP | ⚠️ Partial | Core works, but FTP active mode and several extensions are missing. |
| Embedded React 19 WebUI | ⚠️ Partial | Present and embedded, but far simpler than planned. |
| REST API v1 surface | ⚠️ Partial | Useful, but groups and several planned resources are absent. |
| Local auth | ✅ Complete | Implemented and tested. |
| LDAP auth | ⚠️ Partial | Works, but some planned config/runtime depth is missing. |
| OIDC WebUI SSO | ❌ Missing | Not implemented. |
| SSH public-key auth | ✅ Complete | Implemented. |
| TOTP MFA | ✅ Complete | Implemented for WebUI/API login. |
| Keyboard-interactive SSH MFA | ❌ Missing | Not implemented. |
| API keys for automation auth | ⚠️ Partial | API key auth exists for the HTTP API, but the broader automation story is still narrower than the original spec. |
| Groups/group API | ❌ Missing | Not implemented. |
| Quotas | ⚠️ Partial | Byte quota + file size limit only. |
| S3 backend + metadata sidecar | ⚠️ Partial | Backend exists; planned metadata DB layer does not. |
| Audit sinks (file/http/syslog/db) | ⚠️ Partial | File + HTTP/webhook only. |
| MCP server | ⚠️ Partial | Only 3 tools and 3 resources. |
| Docker scratch image | ❌ Missing | Final image is Alpine. |

Estimated spec completion: **~62%**.

### 5.2 Architectural Deviations

Major deviations from the spec:
- Planned CobaltDB-centric persistence became a JSON file store.
- Planned richer MCP surface became a narrow MVP.
- Planned security/network controls are only partly realized.
- WebSocket “live events” are implemented as snapshot polling.

Some deviations are simplifications, not regressions. The real issue is that the docs were not updated after those simplifications.

### 5.3 Task Completion Assessment

Measured directly from `.project/TASKS.md`:

| Metric | Value |
|---|---:|
| Checked tasks | 104 |
| Unchecked tasks | 683 |

Literal task completion: **~13.2%**.

Observed implementation progress: **roughly 55-60%** of task themes are materially done or in progress.

Conclusion: `TASKS.md` is stale and cannot be treated as an execution-truth artifact.

### 5.4 Scope Creep Detection

Useful additions beyond the obvious base scope:
- backup/restore CLI
- vsftpd / ProFTPD migration commands
- authorized_keys migration
- debug server / pprof
- TLS certificate monitoring
- docker-compose and systemd support

This is mostly good scope creep. It improves operability.

### 5.5 Missing Critical Components

Highest-impact missing or partial components:
1. group model + group API
2. OIDC
3. enforced IP/network security controls
4. CobaltDB/queryable audit layer promised by docs
5. richer FTP RFC support
6. browser-level frontend E2E coverage

## 6. Performance & Scalability

Performance observations:
- S3 uploads buffer full bodies in memory.
- WebSocket snapshots are rebuilt every 2 seconds per client.
- Audit reads are file scans, not indexed queries.
- Frontend uses route-level lazy loading, but the shared chunks are still fairly large.

Scalability observations:
- Good fit for a single-node deployment.
- Poor fit for horizontal scaling because state is partly file-backed and partly in-process (`session.Manager`, `transfer.Manager`, audit live state).

## 7. Developer Experience

Onboarding is decent because the repo has a real README, Makefile, Dockerfile, docker-compose, Goreleaser, CI, and test suite.

DX problems:
- The docs overstate support for several features.
- There is no OpenAPI/Swagger or dedicated API reference.
- `TASKS.md` is stale enough to mislead contributors.

## 8. Technical Debt Inventory

### 🔴 Critical

1. `internal/config/loader.go:48-95` plus runtime code
   Config advertises knobs that do not do anything.

2. `internal/store/store.go:23-36` vs docs/health payloads
   Persistence architecture is mislabeled and under-documented.

3. `internal/storage/s3/client.go:139-153`
   Large uploads are buffered in memory.

### 🟡 Important

1. `cmd/kervan/cli_commands.go:118-126`
   TLS verification can be bypassed in the status command via `--insecure`.

2. `webui/`
   The test suite is now real, but it still stops at component/page scope.

### 🟢 Minor

1. `internal/api/websocket.go:139-149`
   Polling snapshots instead of event push.

2. `internal/api/server.go`, `internal/server/server.go`
   Large files need decomposition.

3. `internal/webui/embed.go`
   No compression for static assets.

## 9. Metrics Summary Table

| Metric | Value |
|---|---|
| Total Go Files | 108 |
| Total Go LOC | 23,384 |
| Total Frontend Files | 26 |
| Total Frontend LOC | 2,790 |
| Test Files | 48 |
| Test Coverage (measured) | 53.6% |
| External Go Dependencies | 2 direct / 3 indirect in `go.mod` |
| External Frontend Dependencies | 13 runtime / 7 dev |
| Open TODOs/FIXMEs | 0 |
| API Endpoints | 45 registered route patterns |
| Spec Feature Completion | ~62% |
| Task Completion | ~13.2% tracked / ~55-60% observed |
| Overall Health Score | 6.5/10 |

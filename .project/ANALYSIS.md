# Project Analysis Report

> Auto-generated comprehensive analysis of KervanServer
> Generated: 2026-04-11
> Analyzer: Codex - Full Codebase Audit

## 1. Executive Summary

KervanServer is a modular monolith written in Go that aims to unify FTP, FTPS, SFTP, SCP, a REST API, a React 19 Web UI, basic observability, migration tooling, and an MCP endpoint into one deployable binary. The codebase is real and substantial, not a stub project, but it is still materially behind the ambition expressed in `.project/SPECIFICATION.md`. The implemented system is best described as an advanced prototype or pre-production control plane for virtual file transfer, not a production-ready secure appliance.

- Total files in repo: `7700`
- First-party Go files: `78`
- First-party Go LOC: `16292`
- First-party frontend source files: `26`
- First-party frontend LOC: `2504`
- Go test files: `25`
- Go packages: `23`
- Packages with zero tests: `10`
- Go dependencies in `go.mod`: `5` modules total, `2` direct + `3` indirect
- Frontend dependencies: `13` runtime + `7` dev = `20`
- Registered HTTP routes: `45`
- Markdown docs found: `4`

Overall health score: **5/10**.

Justification: the repository has coherent structure, passes `go build ./cmd/kervan`, `go test ./... -count=1`, `go vet ./...`, `staticcheck ./...`, `govulncheck ./...`, and `npm run build`. That is the good news. The bad news is more important for production readiness: the project auto-creates an admin user with a hardcoded default password at `internal/server/server.go:455`, relies on an in-process ephemeral API token secret at `internal/api/server.go:96-120`, defaults CORS to wildcard and only emits the first configured origin at `internal/api/server.go:1447-1451`, has no CI, Docker, release automation, or migration framework, and falls well short of the original specification in authentication breadth, protocol completeness, deployment readiness, and operational hardening.

Top 3 strengths:

- Strong modular decomposition. `internal/auth`, `internal/api`, `internal/protocol`, `internal/vfs`, `internal/storage`, `internal/server`, and `internal/webui` have clear boundaries.
- Broad functional surface already exists. FTP, FTPS, SFTP, limited SCP, REST API, Web UI, health/metrics, audit export, API keys, share links, and migration commands are all present.
- Developer ergonomics are better than average for an unfinished system. The binary builds, tests pass, the frontend builds, and the README is relatively honest about planned gaps.

Top 3 concerns:

- Security posture is not production-safe. Hardcoded fallback admin password, wildcard-friendly CORS behavior, bearer token support in WebSocket query strings, no rate limiting, no secure headers, no CSRF, and thin auth/session controls are all serious issues.
- The implementation is materially behind the spec. OIDC is absent, groups/shared directory semantics are mostly absent, deployment artifacts are absent, MCP is minimal, FTP/SFTP/SCP compatibility is partial, and hot reload is effectively write-and-restart.
- State persistence is fragile. The user/session/key/share repositories sit on top of a JSON file store rewritten wholesale on each mutation in `internal/store/store.go:40-48` and `internal/store/store.go:114-121`.

## 2. Architecture Analysis

### 2.1 High-Level Architecture

Architecture style: **modular monolith**.

Text data flow:

1. Client connects through FTP, FTPS, SFTP, SCP, REST, or WebSocket.
2. Protocol/API layer authenticates through `internal/auth`.
3. Session and transfer state is tracked in-memory via `internal/session` and `internal/transfer`.
4. Filesystem requests are routed through `internal/vfs`, then into `internal/storage/local`, `internal/storage/memory`, or `internal/storage/s3`.
5. User metadata, API keys, and share links persist through `internal/store`.
6. Audit events are queued through `internal/audit` and written as JSONL.
7. Monitoring endpoints synthesize runtime, auth, user, transfer, storage, and TLS information.

Component interaction map:

- `cmd/kervan` wires CLI entrypoints, health checks, migration/import/export, and standalone MCP mode.
- `internal/server` is the composition root. It loads config, provisions auth/store/audit/session/transfer managers, chooses storage backends, creates protocol/API servers, and starts/shuts down listeners.
- `internal/api` exposes REST, file operations, WebSocket snapshots, metrics, health, config patching, users, sessions, transfers, audit, share links, and API keys.
- `internal/protocol/ftp` and `internal/protocol/sftp` perform protocol handling and depend on the same auth/VFS/audit/session/transfer subsystems.
- `internal/webui` embeds the built React SPA using `embed.FS`.

Concurrency model:

- Per-listener accept loops run in goroutines.
- FTP and SFTP create per-connection goroutines.
- Audit engine creates one background goroutine with a buffered channel of `1024` events in `internal/audit/engine.go:25-35`.
- WebSocket live updates are not event-driven; they use a `2 * time.Second` ticker at `internal/api/websocket.go:132`.
- Session and transfer managers use mutexes and atomics but remain single-process, in-memory state holders.

Goroutine management assessment:

- Reasonable for a small monolith, but not rigorous. There is no comprehensive supervisor model or lifecycle registry.
- `internal/server/server.go` even retains an unused `wg` field flagged by staticcheck at `internal/server/server.go:57`.
- Audit shutdown may drop queued events because `Close()` cancels first, closes the channel, then waits; the loop exits on `ctx.Done()` without guaranteed draining in `internal/audit/engine.go:54-79`.

### 2.2 Package Structure Assessment

Go packages and responsibilities:

| Package | Responsibility | Assessment |
|---|---|---|
| `cmd/kervan` | Main binary, CLI commands, migration tools, status/check commands | Good cohesion |
| `internal/acme` | AutoCert wrapper for HTTP-01/TLS config | Small, focused |
| `internal/api` | REST API, metrics, health, auth endpoints, WebSocket, admin endpoints | Large and central; beginning to sprawl |
| `internal/audit` | Event model, async queue, JSONL sink | Focused but minimal |
| `internal/auth` | Users, password hashing, lockout, LDAP, TOTP, public-key auth | Strong but overloaded |
| `internal/build` | Build metadata | Fine |
| `internal/config` | Config schema, defaults, validation, loader, signal reload | Good cohesion |
| `internal/crypto` | TLS config, SSH key generation, certificate inspection | Focused |
| `internal/mcp` | Standalone stdio MCP server, tools/resources | Minimal but coherent |
| `internal/protocol/ftp` | FTP/FTPS server implementation | Large single-file hotspot |
| `internal/protocol/sftp` | SSH/SFTP/SCP server logic | Large but still understandable |
| `internal/quota` | Quota tracker and measurement | Small, focused |
| `internal/server` | Composition root and lifecycle management | Correct place, but heavy |
| `internal/session` | In-memory session tracking and kill support | Small, focused |
| `internal/storage/local` | Local filesystem backend | Focused |
| `internal/storage/memory` | In-memory filesystem backend | Focused |
| `internal/storage/s3` | S3-compatible backend and custom client | Significant complexity lives here |
| `internal/store` | JSON-file KV persistence | Simple, but too primitive for production |
| `internal/transfer` | In-memory transfer tracking/stats | Small, focused |
| `internal/util/log` | `slog` logger factory | Fine |
| `internal/util/ulid` | ULID-like identifier generator | Fine, but custom |
| `internal/vfs` | Virtual filesystem abstraction, mounts, permissions, user FS | Core abstraction, well placed |
| `internal/webui` | Embedded SPA handler | Focused |

Package cohesion:

- Mostly good. The clearest hotspots are `internal/api/server.go`, `internal/protocol/ftp/server.go`, and `internal/protocol/sftp/handler.go`, where too much behavior accumulates in single files.
- `internal/auth` is doing a lot: local users, LDAP provider, TOTP, lockout, and public key auth. It still hangs together, but it is already a mini-subsystem.

Circular dependency risk:

- No obvious circular imports today.
- Risk grows around `internal/server` because it knows about nearly every subsystem and pushes config into all of them.

Internal vs public package separation:

- Good discipline. There is no premature `pkg/` sprawl.
- The project is intentionally application-scoped, and `internal/` usage is appropriate.

### 2.3 Dependency Analysis

Go dependencies from `go.mod`:

| Dependency | Version | Purpose | Maintenance status | Stdlib replaceable? |
|---|---|---|---|---|
| `golang.org/x/crypto` | `v0.50.0` | bcrypt, ssh, acme/autocert, crypto helpers | Actively maintained by Go team | No practical full replacement |
| `gopkg.in/yaml.v3` | `v3.0.1` | YAML config parsing | Mature, stable | Not with stdlib |
| `golang.org/x/net` | `v0.52.0` | Indirect networking helpers | Active | Partly, but not worth it |
| `golang.org/x/sys` | `v0.43.0` | OS/syscall support | Active | Partly, but standard companion package |
| `golang.org/x/text` | `v0.36.0` | Indirect text/encoding support | Active | Not fully |

Dependency hygiene:

- Go dependency count is low, which is good.
- No known vulnerabilities were found by `govulncheck ./...`.
- The flip side is that the codebase reimplements complex things from scratch: LDAP protocol handling, S3 SigV4 client behavior, WebSocket framing, FTP server, SFTP server, and SCP handling. The project avoided third-party dependencies at the cost of a much larger correctness and maintenance burden.
- ACME is not actually from scratch despite the spec claiming it should be. It is a thin wrapper over `autocert` in `internal/acme/acme.go`.

Frontend dependencies from `webui/package.json`:

Runtime dependencies:

- `@radix-ui/react-slot` - UI composition primitive.
- `class-variance-authority` - variant-based class helpers.
- `clsx` - conditional classes.
- `lucide-react` - icon set.
- `next-themes` - theme handling.
- `react` - UI runtime.
- `react-dom` - DOM renderer.
- `react-router-dom` - client routing.
- `tailwind-merge` - Tailwind class merging.
- `tailwindcss` - utility CSS.
- `tw-animate-css` - animation helpers.
- `vaul` - drawer/sheet primitive.
- `zod` - runtime schema validation.

Dev dependencies:

- `@eslint/js`
- `@types/react`
- `@types/react-dom`
- `@vitejs/plugin-react`
- `eslint`
- `typescript`
- `vite`

Frontend dependency assessment:

- Modern and mainstream stack.
- React is current (`19.2.0`) and Vite/Tailwind setup is healthy.
- `zod` is installed but not meaningfully used for request/form validation in the UI, which is a missed opportunity.
- Bundle size from `npm run build`: `338.85 kB` JS, `20.99 kB` CSS before gzip. Acceptable for an admin UI, but there is no route-level code splitting.

### 2.4 API & Interface Design

Route inventory from `internal/api/server.go:128-182`:

| Methods | Paths | Handler | Notes |
|---|---|---|---|
| `GET` | `/health`, `/api/v1/health` | `handleHealth` | Public |
| `GET` | `/metrics`, `/api/v1/metrics` | `handleMetrics` | Public |
| `POST` | `/api/login`, `/api/v1/auth/login` | `handleLogin` | Public |
| `GET,DELETE` | `/api/v1/auth/totp` | `handleTOTP` | Authenticated |
| `POST` | `/api/v1/auth/totp/setup` | `handleTOTPSetup` | Authenticated |
| `POST` | `/api/v1/auth/totp/enable` | `handleTOTPEnable` | Authenticated |
| `GET` | `/api/ws`, `/api/v1/ws` | `handleWebSocket` | Token via query or bearer header |
| `GET` | `/api/server/status`, `/api/v1/server/status` | `handleServerStatus` | Authenticated |
| `GET,PUT` | `/api/v1/server/config` | `handleServerConfig` | Authenticated, admin behavior inside |
| `POST` | `/api/v1/server/config/validate` | `handleServerConfigValidate` | Authenticated |
| `POST` | `/api/v1/server/reload` | `handleServerReload` | Authenticated |
| `GET,POST,PUT,DELETE` | `/api/users`, `/api/v1/users` | `handleUsers` | Admin-oriented |
| `POST` | `/api/users/import`, `/api/v1/users/import` | `handleUsersImport` | Admin-oriented |
| `GET` | `/api/users/export`, `/api/v1/users/export` | `handleUsersExport` | Admin-oriented |
| `GET,POST,DELETE` | `/api/apikeys`, `/api/v1/apikeys` | `handleAPIKeys` | Current user |
| `GET` | `/api/sessions`, `/api/v1/sessions` | `handleSessions` | Scoped for non-admin |
| `GET,DELETE` | `/api/sessions/{id}`, `/api/v1/sessions/{id}` | `handleSessionByID` | Scoped for non-admin |
| `GET` | `/api/files/list` | `handleFilesList` | Legacy current-user API |
| `POST` | `/api/files/mkdir` | `handleFilesMkdir` | Legacy |
| `DELETE` | `/api/files/delete` | `handleFilesDelete` | Legacy |
| `POST` | `/api/files/rename` | `handleFilesRename` | Legacy |
| `POST` | `/api/files/upload` | `handleFilesUpload` | Legacy raw-body upload |
| `GET` | `/api/files/download` | `handleFilesDownload` | Legacy |
| multiple | `/api/v1/files/{user}/...` | `handleFilesV1` | Current-user and admin file operations |
| `GET,DELETE` | `/api/share`, `/api/v1/share` | `handleShareLinks` | Current user |
| `GET` | `/api/share/{token}`, `/api/v1/share/{token}` | `handleShareDownload` | Public |
| `GET` | `/api/audit`, `/api/v1/audit/events` | `handleAudit` | Scoped for non-admin |
| `GET` | `/api/audit/export`, `/api/v1/audit/export` | `handleAuditExport` | Scoped export |
| `GET` | `/api/transfers`, `/api/v1/transfers` | `handleTransfers` | Scoped for non-admin |

API consistency:

- Naming is mostly coherent and JSON responses are generally predictable.
- There is duplication between legacy `/api/...` and newer `/api/v1/...` routes.
- Handler methods dispatch manually instead of using a router with explicit method constraints, which increases accidental complexity.
- Error responses are plain and simple, but not standardized beyond ad hoc `{error: ...}` payloads.

Authentication and authorization model:

- Web/API auth uses bearer tokens signed with a random per-process secret generated at `internal/api/server.go:96-120`.
- All tokens become invalid on restart. That is simple, but operationally rough.
- WebSocket also accepts tokens in the query string at `internal/api/websocket.go:45-47`, which is a security footgun because URLs leak more easily into logs, proxies, and browser tooling.
- Authorization is primarily role or ownership based. Session, transfer, and audit endpoints do scope non-admin users correctly in tests and code.

Rate limiting, CORS, validation:

- No API rate limiting found.
- No request size limiting beyond some handler-specific behavior.
- CORS middleware only emits the first configured origin at `internal/api/server.go:1447-1451`; with defaults this is `*`.
- No CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, or panic-recovery middleware is present.

## 3. Code Quality Assessment

### 3.1 Go Code Quality

Code style consistency:

- Generally strong. The code reads as `gofmt`-compliant and naming is mostly conventional.
- The repo passed `go vet ./...`.
- `staticcheck ./...` found four issues, all low severity:
  - `internal/protocol/sftp/server.go:253` should use `String()` instead of `fmt.Sprintf`
  - `internal/quota/quota.go:156` unused type `dirEntryInfo`
  - `internal/server/server.go:57` unused field `wg`
  - `internal/vfs/user_vfs.go:455` simplifiable struct conversion

Error handling:

- Reasonably consistent in API and CLI layers.
- Many errors are surfaced cleanly to callers.
- There is no unified domain error taxonomy; this is application-style direct handling.

Context usage:

- Present in major server start paths and auth/audit integrations.
- Inconsistent deeper in protocol loops and storage logic. For example, some long-lived connection operations do not meaningfully consult cancellation after setup.

Logging:

- Structured logging exists through `slog` in `internal/util/log/log.go`.
- Coverage of important events is uneven.
- There is no request ID propagation or structured request logging middleware.

Configuration management:

- Good schema and validation layer in `internal/config`.
- Environment overlay exists, but only as a partial key-path mechanism via `internal/config/loader.go:19-43`.
- Runtime config patching exists, but live reload is overstated. Most config mutations return `requires_restart: true` from `internal/server/server.go:267`, `277`, `299`, `319`.

Magic numbers and hardcoded values:

- Hardcoded default admin password `admin123!` at `internal/server/server.go:455`.
- Audit queue size `1024` in `internal/audit/engine.go:29`.
- WebSocket snapshot interval `2 * time.Second` in `internal/api/websocket.go:132`.
- Store filename `kervan-store.json` at `internal/store/store.go:25`.

TODO/FIXME/HACK comments:

- None found by repository-wide search.
- Absence of TODOs here is not a sign of completeness; it is mostly a sign that unfinished work is tracked in docs rather than inline.

### 3.2 Frontend Code Quality

React patterns:

- Functional components throughout.
- State management is local `useState` and `useEffect`; simple and readable.
- React 19 is present but the code does not use notable React 19 patterns or modern concurrency APIs.

TypeScript strictness:

- `tsconfig` is strict, which is good.
- Type coverage in UI data models is shallow in places. `ServerStatus` and `AuditEvent` are typed as `Record<string, unknown>` in `webui/src/lib/types.ts:25` and `84`, which weakens UI correctness.

Component structure:

- Reasonably consistent: pages under `webui/src/pages`, primitives under `components/ui`, app shell separated cleanly.
- The UI is an admin panel, not a design system; that is fine.

CSS approach:

- Tailwind 4 + shadcn-style component patterns.
- Visual consistency is good.

Bundle size and build quality:

- Production build succeeds.
- JS bundle is `338.85 kB` before gzip. Not alarming, but no lazy loading is present.

Accessibility and UX:

- Mixed.
- Good: labels exist on login fields and theme toggle has `aria-label`.
- Weak: many destructive or important actions rely on `window.confirm` and `window.prompt` in `files-page.tsx`, `users-page.tsx`, `sessions-page.tsx`, and `apikeys-page.tsx`.
- Weak: TOTP setup shows raw shared secret and `otpauth_url` directly in the dashboard at `webui/src/pages/dashboard-page.tsx:135-145`.
- Weak: auth state is kept only in memory at `webui/src/app.tsx:24-57`, so a page refresh logs the user out.
- Weak: no frontend test files exist.

### 3.3 Concurrency & Safety

Positive patterns:

- Session and transfer managers use mutexes and atomics appropriately.
- Server start/stop paths are at least consciously structured.

Specific risks:

- Audit engine drops events when the queue fills at `internal/audit/engine.go:45-50`.
- Audit shutdown may lose queued events because cancellation wins over draining at `internal/audit/engine.go:54-79`.
- API tokens are process-local because the signing secret is generated at startup at `internal/api/server.go:96-120`.
- WebSocket live state is implemented as periodic full snapshots, which is simple but noisy and potentially expensive as user/session/audit counts grow.

Graceful shutdown:

- Present but not comprehensive.
- `internal/server/server.go` does close FTP, SFTP, API, ACME, audit, and store resources.
- There is no evidence of careful draining of in-flight transfers or websocket clients beyond listener shutdown.

### 3.4 Security Assessment

Input validation:

- Better than average on config and some auth paths.
- Thin on API request payloads and UI form handling.

Injection risks:

- No SQL, so SQL injection is not relevant.
- No obvious command injection paths found.
- Path traversal handling is better than many file servers because VFS/local backend normalize and constrain paths.

Secrets management:

- No obvious committed secret files found.
- Severe exception: default admin fallback password in `internal/server/server.go:453-456`.

TLS/HTTPS:

- TLS config exists for FTP/Web UI integration.
- ACME support exists through `autocert`, not a bespoke ACME client.
- There is no mTLS or advanced hardening.

CORS:

- Weak default posture. Defaults include `cors_origins: ["*"]` and the middleware simply reflects the first configured origin at `internal/api/server.go:1447-1451`.

Authentication/authorization quality:

- Local auth, public key auth, LDAP shadowing, and TOTP are real features.
- No OIDC.
- No persistent session store.
- No token rotation, revocation list, idle timeout enforcement at token layer, or refresh flow.

Known vulnerability patterns found:

- Hardcoded default admin password: `internal/server/server.go:455`
- Bearer token allowed in query string: `internal/api/websocket.go:45-47`
- No rate limiting on login or API abuse paths
- No CSRF protection for browser-facing authenticated APIs
- No secure response headers
- Public share links are bearer-by-URL and depend entirely on token secrecy

## 4. Testing Assessment

### 4.1 Test Coverage

Counts:

- Go source files excluding tests: `53`
- Go test files: `25`
- Source-to-test-file ratio: about `47%`
- Frontend test files: `0`

Command results:

- `go build ./cmd/kervan` -> passed
- `go test ./... -count=1` -> passed
- `go vet ./...` -> passed
- `staticcheck ./...` -> passed with 4 low-severity findings
- `govulncheck ./...` -> no vulnerabilities found
- `go test -race ./...` -> could not run because `CGO_ENABLED=0`
- `go test ./... -coverprofile=coverage.out` -> partially produced package coverage, then failed with Go toolchain version mismatch (`go1.26.2` vs `go1.26.1`) during instrumentation

Packages with explicit coverage output before the coverprofile failure:

- `cmd/kervan` `59.3%`
- `internal/acme` `65.2%`
- `internal/api` `39.3%`
- `internal/auth` `70.2%`
- `internal/config` `49.1%`
- `internal/crypto` `36.5%`
- `internal/mcp` `62.8%`
- `internal/protocol/sftp` `8.7%`
- `internal/quota` `45.6%`
- `internal/server` `18.9%`
- `internal/storage/s3` `57.1%`
- `internal/transfer` `71.0%`
- `internal/vfs` `29.9%`

Estimated total coverage:

- **Roughly mid-30% overall**, with especially weak coverage around FTP, server composition, embedded Web UI serving, persistence, and real end-to-end protocol behavior.

Packages with zero test files:

- `internal/audit`
- `internal/build`
- `internal/protocol/ftp`
- `internal/session`
- `internal/storage/local`
- `internal/storage/memory`
- `internal/store`
- `internal/util/log`
- `internal/util/ulid`
- `internal/webui`

Test types present:

- Unit tests: yes
- Integration-style HTTP handler tests: yes
- Protocol parser tests: limited
- E2E tests: absent
- Benchmarks: absent
- Fuzzing: absent
- Frontend component tests: absent

Test quality:

- Better than average for admin API and auth utilities.
- Thin where it matters most for production confidence: FTP server, SFTP correctness, SCP interoperability, persistence durability, listener lifecycle, and browser workflows.

### 4.2 Test Infrastructure

- Go tests are easy to run locally and mostly use temp dirs plus in-process dependencies.
- No CI pipeline exists in `.github/workflows`.
- No browser test setup, Vitest, Playwright, Cypress, or Jest setup exists.
- No load, soak, or protocol interoperability matrix exists despite the product domain demanding it.

## 5. Specification vs Implementation Gap Analysis

This is the most important section of the audit. The codebase has meaningful implementation, but the specification remains significantly ahead of reality.

### 5.1 Feature Completion Matrix

| Planned Feature | Spec Section | Implementation Status | Files/Packages | Notes |
|---|---|---|---|---|
| FTP server with full compatibility | Spec Sec. 3.1 | Partial | `internal/protocol/ftp` | Passive mode works; active mode (`PORT`/`EPRT`), `EPSV`, `REST`, `ABOR`, and real `MLST` support are missing while `FEAT` advertises `MLST` at `internal/protocol/ftp/server.go:268-270`. |
| FTPS explicit + implicit | Spec Sec. 3.2 | Partial | `internal/protocol/ftp`, `internal/crypto`, `internal/acme` | Explicit and implicit TLS exist, but enablement depends on TLS config and mode rather than clearly honoring `cfg.FTPS.Enabled` in `internal/protocol/ftp/server.go:160-167`. |
| SFTP subsystem | Spec Sec. 3.3 | Partial | `internal/protocol/sftp` | Core read/write/list/stat operations exist; many extended operations are unsupported. |
| SCP compatibility | Spec Sec. 3.4 | Partial | `internal/protocol/sftp/scp.go` | Basic source/sink file support exists; recursive directories and full compatibility are absent. |
| Virtual filesystem with multi-backend mounts | Spec Sec. 4 | Partial | `internal/vfs`, `internal/storage/*` | Local, memory, and S3 backends exist; cross-backend rename falls back to deny, not copy-delete; `Statvfs` is mostly stubbed. |
| Local auth + lockout | Spec Sec. 5 | Complete | `internal/auth/engine.go`, `internal/auth/password.go` | Reasonable implementation. |
| SSH public-key auth | Spec Sec. 5 | Complete | `internal/auth/engine.go` | Real support exists, contrary to README's "planned" wording. |
| TOTP 2FA | Spec Sec. 5 | Complete | `internal/auth/totp.go`, `internal/api` | Real setup/enable/disable and login enforcement exists, also beyond README wording. |
| LDAP/AD integration | Spec Sec. 5.5 | Partial | `internal/auth/ldap.go` | Basic custom LDAP bind/search/group mapping exists; enterprise completeness is not there. |
| OIDC WebUI SSO | Spec Sec. 5 | Missing | none | No OIDC code found. |
| Groups/shared directories | Spec Sec. 4, Sec. 5 | Mostly missing | config only | Group/shared-dir models from the spec are not materially implemented. |
| REST API full surface | Spec Sec. 8.4 | Partial | `internal/api` | Many endpoints exist, but not the full spec surface, and several are simplified. |
| WebSocket live events | Spec Sec. 8.5, Sec. 7 | Partial | `internal/api/websocket.go`, `webui/src/lib/use-live-snapshot.ts` | Implemented as periodic snapshots, not a true event stream. |
| React 19 embedded WebUI | Spec Sec. 7 | Partial | `webui`, `internal/webui` | Real SPA exists and is embedded; functionality is broad but still admin-basic and untested. |
| API keys | Spec Sec. 8 | Complete | `internal/api/apikeys.go` | Working CRUD for current user. |
| Share links | Spec Sec. 8 | Complete | `internal/api/sharelinks.go`, file endpoints | Implemented for files; no directory sharing. |
| Audit log and export | Spec Sec. 6, Sec. 8 | Partial | `internal/audit`, `internal/api` | Real JSONL audit sink/export exists; event schema is narrow and scalability is poor. |
| Health and Prometheus metrics | Spec Sec. 18 | Partial | `internal/api/monitoring.go` | Present, but checks are synthetic and no tracing/alerts/pprof exist. |
| MCP server | Spec Sec. 11 | Partial | `internal/mcp` | Only 3 tools and 3 resources. |
| ACME auto-cert | Spec Sec. 3.2, Sec. 12 | Partial | `internal/acme` | Works via `autocert`; not from scratch and not production-hardened. |
| Docker/systemd/release automation | Spec Sec. 12 | Missing | none | No `Dockerfile`, no compose, no `.goreleaser.yml`, no CI/CD. |

### 5.2 Architectural Deviations

- Spec says CobaltDB-style persistence; implementation uses a single JSON file store in `internal/store/store.go`. This is a regression for durability and concurrency.
- Spec describes from-scratch ACME; implementation wraps `autocert`. This is an improvement in risk reduction, but a deviation from stated architecture.
- Spec implies deeper protocol completeness. FTP/SFTP/SCP implementations are deliberately narrower than promised.
- Spec suggests hot reload; implementation mostly validates/writes config and tells the caller restart is required.
- Spec positions groups/shared mounts as first-class. Implementation is user-centric and lacks that model.
- Spec describes richer MCP. Implementation is intentionally tiny.

### 5.3 Task Completion Assessment

Raw `TASKS.md` checkbox status:

- Checkbox items found: `788`
- Checked items: `0`
- Raw completion: `0%`

Assessment:

- `TASKS.md` is not maintained as an execution log.
- The raw checkbox percentage is not a useful indicator of actual implementation state.
- Based on code actually present, practical feature completion is closer to **55-60%** of the roadmap, heavily skewed toward backend foundations rather than production hardening.

Blocked or abandoned looking task clusters:

- OIDC and broader auth policy work
- Group/shared directory model
- Richer SCP compatibility
- Deployment/packaging infrastructure
- Frontend testing and UX hardening
- Full MCP surface

### 5.4 Scope Creep Detection

Scope creep is limited. Most implemented features map back to the spec. Small additions beyond the core spec emphasis include:

- Focused migration commands for `vsftpd`, `proftpd`, and `authorized_keys`
- Richer server status/config inspection than a minimal MVP would need
- TLS certificate introspection in monitoring

These additions are mostly valuable, not wasteful.

### 5.5 Missing Critical Components

Highest-impact missing items:

1. Production-grade security hardening: rate limiting, secure headers, CSRF posture, safer token model.
2. Persistent durable state model: migrations, transactions, backup/restore, non-JSON-file storage.
3. Deployment assets: CI, Docker, release automation, environment-specific deployment guidance.
4. OIDC and richer identity/group model.
5. Real protocol compatibility and interoperability coverage, especially FTP active mode and SCP recursion.

## 6. Performance & Scalability

### 6.1 Performance Patterns

Potential bottlenecks:

- `internal/store/store.go` rewrites the entire JSON file on every `Put` and `Delete`.
- `internal/api/server.go:1683-1727` reads the entire audit file into memory for listing/export.
- `internal/storage/s3/backend.go:313` reads full object bodies into memory, and `internal/storage/s3/backend.go:543` writes full buffered object contents on close.
- WebSocket snapshots send full state every 2 seconds rather than incremental events.

Positive notes:

- In-memory session and transfer tracking is cheap.
- The frontend bundle is not excessive.
- Transfer counters use atomics where appropriate.

### 6.2 Scalability Assessment

- Horizontal scaling: **poor** today.
- Why: API auth tokens are process-local, sessions are in-memory, transfer state is in-memory, audit is local file JSONL, and the user/key/share store is local JSON.
- There is no shared cache, queue, or database abstraction that enables multi-instance coordination.
- Connection pooling and back-pressure are mostly not relevant yet because the app does not have a DB tier, but the S3 client and protocol handlers do not expose strong resource controls.

## 7. Developer Experience

### 7.1 Onboarding Assessment

- Clone/build/run path is fairly approachable.
- README gives build steps and explains high-level structure.
- No dev container, compose stack, or CI preview workflow exists.
- Web UI build scripts are present in `scripts/generate-webui.sh` and `.ps1`.

### 7.2 Documentation Quality

- `README.md` is honest and reasonably useful.
- `.project/SPECIFICATION.md` and `.project/IMPLEMENTATION.md` are thorough but aspirational.
- `TASKS.md` is detailed but stale as status tracking.
- No `ARCHITECTURE.md`, `API.md`, `CHANGELOG.md`, `CONTRIBUTING.md`, or `LICENSE` exist.

### 7.3 Build & Deploy

- Build complexity is moderate, not extreme.
- Cross-platform awareness exists in Go code.
- Container readiness is absent because there is no Docker artifact.
- CI/CD maturity is effectively zero.

## 8. Technical Debt Inventory

### Critical

- `internal/server/server.go:453-456` - Auto-created admin falls back to `admin123!`. Suggested fix: require explicit bootstrap secret or one-time random credential. Effort: `2-4h`.
- `internal/api/server.go:96-120` - API bearer token signing secret is generated per process, making session continuity and cluster use impossible. Suggested fix: persistent signing key with rotation support. Effort: `8-16h`.
- `internal/api/server.go:1447-1451` - CORS is simplistic and defaults unsafe in practice. Suggested fix: explicit production origin allowlist, credentials model, secure headers middleware. Effort: `6-12h`.
- `internal/store/store.go:40-48`, `114-121` - Full-file rewrite persistence is fragile and not production-grade. Suggested fix: real embedded DB or append-only/journaled store with migrations. Effort: `40-80h`.
- `internal/protocol/ftp/server.go` - No FTP active mode and incomplete command surface. Suggested fix: finish protocol support or narrow product claims. Effort: `24-40h`.

### Important

- `internal/api/websocket.go:45-47` - Token allowed in query string. Suggested fix: header-only or secure negotiated session channel. Effort: `2-4h`.
- `internal/audit/engine.go:45-50`, `54-79` - Audit event dropping and shutdown loss risk. Suggested fix: drain-on-close and backpressure strategy. Effort: `6-10h`.
- `internal/api/server.go:1683-1727` - Audit listing/export scales poorly with file size. Suggested fix: streamed reader or indexed/event-store approach. Effort: `8-16h`.
- `internal/storage/s3/backend.go:313`, `543` - Whole-object buffering can explode memory on larger files. Suggested fix: multipart and streaming uploads/downloads. Effort: `24-48h`.
- `webui/src/pages/*.tsx` - heavy use of `window.confirm`/`window.prompt`. Suggested fix: proper dialogs/forms and stronger validation. Effort: `8-16h`.
- `webui/src/app.tsx:24-57` - auth state is in-memory only. Suggested fix: deliberate session persistence model or explicit non-persistence UX. Effort: `4-8h`.

### Minor

- `internal/server/server.go:57` - unused `wg` field.
- `internal/quota/quota.go:156` - unused type.
- `internal/vfs/user_vfs.go:455` and `internal/protocol/sftp/server.go:253` - staticcheck simplifications.
- README and spec wording are behind the code in a few places, especially public-key auth and TOTP.

## 9. Metrics Summary Table

| Metric | Value |
|---|---|
| Total Go Files | 78 |
| Total Go LOC | 16292 |
| Total Frontend Files | 26 |
| Total Frontend LOC | 2504 |
| Test Files | 25 |
| Test Coverage (estimated) | ~35% |
| External Go Dependencies | 5 |
| External Frontend Dependencies | 20 |
| Open TODOs/FIXMEs | 0 |
| API Endpoints Registered | 45 |
| Spec Feature Completion | ~58% |
| Task Completion (`TASKS.md` raw) | 0% |
| Overall Health Score | 5/10 |

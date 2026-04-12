# Production Readiness Assessment

> Comprehensive evaluation of whether Kervan is ready for production deployment.
> Assessment Date: 2026-04-11
> Verdict: 🟡 CONDITIONALLY READY

## Overall Verdict & Score

**Production Readiness Score: 64/100**

| Category | Score | Weight | Weighted Score |
|---|---:|---:|---:|
| Core Functionality | 7/10 | 20% | 14.0 |
| Reliability & Error Handling | 7/10 | 15% | 10.5 |
| Security | 5/10 | 20% | 10.0 |
| Performance | 6/10 | 10% | 6.0 |
| Testing | 7/10 | 15% | 10.5 |
| Observability | 8/10 | 10% | 8.0 |
| Documentation | 4/10 | 5% | 2.0 |
| Deployment Readiness | 6/10 | 5% | 3.0 |
| **TOTAL** |  | **100%** | **64.0/100** |

This is a conditional-ready system, not a clean-ready system. The code works, the tests pass, and the operational tooling is real. The problem is that the project still claims more than it truly delivers.

## 1. Core Functionality Assessment

### 1.1 Feature Completeness

- ✅ **Working** — local auth, LDAP auth, TOTP, SSH public-key auth, local/memory/S3 storage backends, share links, user import/export, backup/restore, metrics/health, embedded WebUI, MCP basics
- ⚠️ **Partial** — FTP/FTPS, SFTP/SCP, quotas, WebSocket live updates, API surface completeness, audit sinks
- ❌ **Missing** — groups, OIDC, keyboard-interactive SSH auth, SSH certificate auth, syslog/CobaltDB audit backends, several declared security controls
- 🐛 **Misleading** — some planning docs and health labels still imply a CobaltDB-backed design even though the runtime uses `kervan-store.json`

Estimated feature completeness against the full specification: **~62%**.

### 1.2 Critical Path Analysis

The main happy path is viable:

1. Start server
2. Bootstrap/admin login
3. Create users
4. Transfer files via FTP/SFTP/SCP
5. Observe sessions/transfers/audit
6. Back up and restore state

That is enough for a constrained production deployment.

Where confidence drops:
- some protocol claims exceed actual support
- frontend coverage is focused rather than end-to-end
- some config/security controls are decorative rather than real

### 1.3 Data Integrity

- Data is consistently persisted to the embedded JSON store and backup file.
- Backup/restore tooling includes manifest checksums.
- Store recovery from missing/corrupt primary is tested.
- There is no database migration framework because the app is not using a structured DB.

Verdict: acceptable for a single-node embedded store design, but not aligned with the DB-backed architecture the docs describe.

## 2. Reliability & Error Handling

### 2.1 Error Handling Coverage

- Errors are generally surfaced cleanly to clients.
- Panic recovery exists in API middleware.
- Structured request logging is present.

Weak points:
- Panic logs do not include stack traces.
- The largest handlers are sprawling enough that edge-case behavior is hard to reason about.

### 2.2 Graceful Degradation

- Audit sinks are isolated enough that file and webhook outputs can fail independently.
- Health checks expose subsystem-level status.
- Local and LDAP auth paths are separate.

Missing:
- no circuit breakers
- no queue-backed retries for broader subsystems
- no multi-node/shared-state recovery strategy

### 2.3 Graceful Shutdown

- `SIGINT`/`SIGTERM` handling exists.
- API server uses `http.Server.Shutdown`.
- App-level close respects a configured shutdown timeout.

Limitations:
- protocol shutdown behavior is mostly listener/goroutine stop semantics, not an explicit “drain all in-flight transfers cleanly” model

### 2.4 Recovery

- Store backup recovery is tested.
- Backup/restore CLI is a meaningful recovery mechanism.
- System supervisor recovery is delegated to systemd/Docker/orchestration.

## 3. Security Assessment

### 3.1 Authentication & Authorization

- [x] Local auth exists
- [x] LDAP auth exists
- [x] TOTP exists for WebUI/API login
- [x] SSH public-key auth exists
- [x] Admin/self scoping exists on key endpoints
- [x] API-key authentication exists
- [ ] OIDC exists
- [ ] Keyboard-interactive SSH MFA exists
- [ ] SSH certificate auth exists

### 3.2 Input Validation & Injection

- [x] Config validation is strong
- [x] LDAP filter escaping exists
- [x] VFS/path normalization is thoughtful
- [x] No SQL layer, so SQL injection is not applicable
- [ ] Request-body size limits are consistently enforced
- [ ] Every declared security/network config is actually enforced

### 3.3 Network Security

- [x] FTPS and WebUI TLS support exist
- [x] CORS allowlist logic exists
- [x] Basic secure headers exist
- [ ] HSTS is configured
- [ ] CSP is configured
- [x] API-key auth exists for automation consumers

### 3.4 Secrets & Configuration

- [x] No hardcoded production secrets were found
- [x] Sensitive config values are redacted in config output
- [x] `.env.example` uses placeholders
- [ ] Docs accurately describe which config values actually do something

### 3.5 Security Vulnerabilities Found

Specific issues:

1. **Dead security/config knobs**
   Severity: High
   Examples: IP allow/deny lists, connection limits, some spec-level network controls.

2. **`kervan status --insecure` disables TLS verification**
   Severity: Medium
   File: `cmd/kervan/cli_commands.go:118-126`

3. **No CSP/HSTS**
   Severity: Medium
   File: `internal/api/server.go:1623-1630`

4. **S3 uploads buffer entire bodies in memory**
   Severity: Medium
   File: `internal/storage/s3/client.go:139-153`

Positive validation:
- `govulncheck ./...`: no findings
- `npm audit --omit=dev --json`: zero production vulnerabilities

## 4. Performance Assessment

### 4.1 Known Performance Issues

- S3 uploads buffer full request bodies in memory before PUT.
- WebSocket “live” updates are repeated snapshots every 2 seconds.
- Audit queries are flat-file scans, not indexed queries.
- Frontend still ships a moderate client bundle even after route-level lazy loading.

### 4.2 Resource Management

- Listener/goroutine cleanup is reasonable.
- Session and transfer registries are lightweight.
- The embedded store flushes on each mutation, which is simple but can become costly under metadata-heavy load.
- Race detection was not runnable here because `go test -race` required CGO.

### 4.3 Frontend Performance

`npm run build` produced:
- JS bundle: 338.85 kB
- CSS bundle: 20.99 kB

Route-level lazy loading is present, but the initial shell and vendor chunks are still substantial.

## 5. Testing Assessment

### 5.1 Test Coverage Reality Check

- The backend is genuinely tested.
- Every Go package has tests.
- Overall Go statement coverage is **53.6%**.
- FTP and SFTP/SCP are the weakest-covered high-risk areas.

### 5.2 Test Categories Present

- [x] Unit tests
- [x] API tests
- [x] Repository/storage/auth integration-style tests
- [x] Frontend component tests
- [ ] E2E tests
- [ ] Benchmark tests
- [ ] Fuzz tests
- [ ] Load tests

### 5.3 Test Infrastructure

- [x] `go test ./...` runs successfully
- [x] CI runs tests and static analysis
- [x] Backend tests are mostly self-contained
- [ ] Race-detector coverage exists
- [x] Frontend tests exist

## 6. Observability

### 6.1 Logging

- [x] Structured logging exists
- [x] Request IDs and trace IDs are logged
- [x] Log rotation exists
- [x] Sensitive config output is redacted
- [ ] Panic logs include stack traces

### 6.2 Monitoring & Metrics

- [x] Health endpoint exists and is reasonably rich
- [x] Prometheus-style metrics exist
- [x] Session, transfer, auth, and TLS certificate visibility exist
- [ ] Metrics parity with the full specification exists

### 6.3 Tracing

- [x] `traceparent` parsing/propagation exists
- [x] Correlation IDs exist
- [x] pprof can be exposed on the debug listener
- [ ] Distributed tracing export exists

## 7. Deployment Readiness

### 7.1 Build & Package

- [x] Go build passes
- [x] WebUI production build passes
- [x] Multi-platform release config exists
- [x] Dockerfile is functional
- [ ] Final image matches the planned scratch/distroless target

### 7.2 Configuration

- [x] YAML config exists
- [x] Validation exists
- [x] Runtime reload/update exists
- [ ] Every documented env override works
- [ ] Every documented security config is enforced

### 7.3 Database & State

- [x] Embedded persistence exists
- [x] Backup/restore exists
- [ ] Shared-state or HA story exists
- [ ] Architecture/docs match the actual persistence model

### 7.4 Infrastructure

- [x] CI/CD pipeline exists
- [x] Docker build is in CI
- [x] Goreleaser config exists
- [x] systemd unit exists
- [ ] End-to-end rollback/deployment docs exist
- [ ] Zero-downtime deployment story exists

## 8. Documentation Readiness

- [ ] README is fully accurate
- [ ] Setup docs align with implemented features
- [ ] API documentation is comprehensive
- [ ] Configuration reference is honest about supported keys
- [ ] Architecture docs match runtime reality
- [ ] TASKS.md reflects actual progress

Documentation volume is good. Documentation trustworthiness is poor.

## 9. Final Verdict

### 🚫 Production Blockers (MUST fix before any official/public deployment)

1. Security-related config and docs overpromise support for controls that runtime does not enforce.
2. Persistence/health/docs still imply a CobaltDB-backed system when the runtime is JSON-file-backed.
3. Security defaults are still lighter than a public-facing deployment should tolerate.

### ⚠️ High Priority (Should fix within first week of production)

1. Remove TLS verification bypass from `kervan status` or make it explicitly unsafe.
2. Add request size limits and fuller security headers.
3. Add deeper protocol integration tests and at least one browser-level smoke flow.
4. Rewrite docs around the implemented scope immediately.

### 💡 Recommendations (Improve over time)

1. Replace WebSocket polling snapshots with event-driven updates.
2. Decide whether the long-term persistence story is “simple JSON store” or “embedded DB”, then align code and docs.
3. Add race-detector coverage in CI on a CGO-capable runner.

### Estimated Time to Production Ready

- From current state: **3-5 weeks** of focused engineering for an honest, supportable v1
- Minimum viable production with critical fixes only: **5-7 working days**
- Full production readiness against the original full specification: **6-10 weeks**, depending on whether missing features are removed from scope or implemented

### Go/No-Go Recommendation

**CONDITIONAL GO**

Justification:

Kervan is good enough to run in a controlled environment if you narrow the contract to what the code actually supports today: local/LDAP auth, bearer-token API usage, core file transfer, embedded admin UI, and single-node persistence. It is not good enough to ship under the full promise set implied by the spec and README.

The practical risk is not that the server immediately falls over. The practical risk is that operators will trust configuration and documentation that describe capabilities the runtime does not yet have. Fix that truth gap, implement API-key auth, and tighten a few security defaults, and this becomes a credible v1. Leave the scope drift unresolved, and production incidents will be caused as much by misunderstanding as by bugs.

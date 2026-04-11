# Project Roadmap

> Based on comprehensive codebase analysis performed on 2026-04-11
> This roadmap prioritizes work needed to bring the project to production quality.

## Current State Assessment

Kervan is a real, working server, not a stub. The Go build passes, the WebUI build passes, the Go test suite passes, and the repository already includes CI, Docker, Goreleaser, health checks, metrics, backup/restore, migration commands, and a usable embedded React admin UI.

The main blockers are trust and completeness:
- some promised features are missing or partial
- some security/config knobs exist but are not enforced
- frontend coverage is absent
- the project plan/docs do not accurately reflect the implementation

What is working well:
- local auth, LDAP auth, TOTP, SSH public-key auth
- local/memory/S3 backends
- share links, user import/export, backup/restore
- metrics, health, debug server, structured logging

## Phase 1: Critical Fixes (Week 1-2)

### Must-fix items blocking production trust

- [ ] Implement API-key authentication and permission enforcement.
  Affected files: `internal/api/apikeys.go`, `internal/api/server.go`
  Effort: 12-16h

- [ ] Enforce or remove unsupported config claims: `allowed_ips`, `denied_ips`, `max_connections`, LDAP pool size.
  Affected files: `internal/config/*`, `internal/server/server.go`, `internal/protocol/*`, `internal/api/server.go`
  Effort: 16-24h

- [ ] Resolve the persistence truth gap: stop labeling the JSON file store as “CobaltDB” unless the DB layer is actually implemented.
  Affected files: `internal/store/store.go`, `internal/api/monitoring.go`, `internal/server/server.go`, docs
  Effort: 4-8h

- [ ] Enforce password policy in create/import/reset flows.
  Affected files: `internal/auth/engine.go`, `cmd/kervan/cli_commands.go`, `internal/api/server.go`, `internal/api/users_bulk.go`
  Effort: 4-6h

- [ ] Remove the TLS verification bypass from `kervan status`, or gate it behind an explicit unsafe flag.
  Affected files: `cmd/kervan/cli_commands.go`
  Effort: 2-3h

## Phase 2: Core Completion (Week 3-6)

### Complete missing core features from specification

- [ ] Add a real group model and `/api/v1/groups` endpoints.
  Current gap: groups are promised but absent.
  Effort: 24-32h

- [ ] Decide whether OIDC is in or out for v1.0.
  If in: implement WebUI SSO.
  If out: remove it from the advertised scope.
  Effort: 4h to rescope, 24h+ to implement

- [ ] Implement FTP active-mode support and the missing extension coverage the docs still claim.
  Current gap: passive-mode-centric implementation.
  Effort: 24-36h

- [ ] Expand MCP to match the useful runtime surface: sessions, transfers, config summaries, maybe quota/report resources.
  Current gap: only 3 tools and 3 resources.
  Effort: 12-20h

- [ ] Decide on the long-term persistence strategy.
  Option A: formally adopt the JSON store and rewrite the spec.
  Option B: implement the originally planned embedded DB layer.
  Effort: 8h for rescoping, 40h+ for real DB work

## Phase 3: Hardening (Week 7-8)

### Security, error handling, edge cases

- [ ] Add request-body size limits to login, config update, file upload, and other JSON endpoints.
  Effort: 4-6h

- [ ] Add CSP and HSTS defaults for the WebUI/API.
  Effort: 4-6h

- [ ] Update session `last_seen_at` during real FTP/SFTP/SCP activity.
  Effort: 8-12h

- [ ] Stream S3 uploads instead of buffering entire request bodies in memory.
  Effort: 16-24h

- [ ] Audit all dead config fields and make each one either enforced or explicitly unsupported.
  Effort: 8-12h

## Phase 4: Testing (Week 9-10)

### Comprehensive test coverage

- [ ] Raise coverage in `internal/protocol/ftp` and `internal/protocol/sftp`.
  Current coverage: 18.5% / 8.7%
  Effort: 20-28h

- [ ] Add API integration tests for file operations, share links, config reload/update, and error cases.
  Effort: 12-16h

- [ ] Add frontend component tests with Vitest + React Testing Library.
  Effort: 12-16h

- [ ] Add Playwright smoke tests for login, files, sessions, users, and monitoring views.
  Effort: 16-24h

- [ ] Enable a race-detector job on a CGO-capable CI runner.
  Effort: 4-6h

## Phase 5: Performance & Optimization (Week 11-12)

### Performance tuning and optimization

- [ ] Replace 2-second WebSocket snapshots with more event-driven updates where practical.
  Effort: 16-24h

- [ ] Add route-level code splitting in the React app.
  Effort: 4-6h

- [ ] Add asset compression or document proxy-side compression as a deployment requirement.
  Effort: 4-6h

- [ ] Reassess JSON store write-amplification under heavy metadata churn.
  Effort: 6-10h

## Phase 6: Documentation & DX (Week 13-14)

### Documentation and developer experience

- [ ] Rewrite `README.md` around the implemented feature set, not the aspirational one.
  Effort: 8-10h

- [ ] Mark `TASKS.md` as archived/stale or update it to reflect actual progress.
  Effort: 4-6h

- [ ] Publish an API reference or OpenAPI document for the current endpoints.
  Effort: 12-20h

- [ ] Add `CONTRIBUTING.md` and a concise architecture map for new contributors.
  Effort: 6-8h

## Phase 7: Release Preparation (Week 15-16)

### Final production preparation

- [ ] Decide on the final container hardening target: Alpine accepted, or move to distroless/scratch as originally planned.
  Effort: 6-10h

- [ ] Add deployment examples for reverse-proxy TLS termination, object storage, and systemd.
  Effort: 6-8h

- [ ] Add rollback/recovery docs centered on backup/restore.
  Effort: 4-6h

- [ ] Add a release checklist that only includes features actually implemented.
  Effort: 4-6h

## Beyond v1.0: Future Enhancements

- [ ] OIDC WebUI SSO
- [ ] Full groups/policy model
- [ ] Event-driven WebSocket subscriptions
- [ ] Syslog / CEF / richer audit sinks
- [ ] Shared-state or clustered control plane
- [ ] Stronger metadata persistence if the JSON store becomes a bottleneck

## Effort Summary

| Phase | Estimated Hours | Priority | Dependencies |
|---|---:|---|---|
| Phase 1 | 40h | CRITICAL | None |
| Phase 2 | 80h | HIGH | Phase 1 |
| Phase 3 | 40h | HIGH | Phase 1 |
| Phase 4 | 55h | HIGH | Phases 1-3 |
| Phase 5 | 30h | MEDIUM | Phase 4 |
| Phase 6 | 30h | MEDIUM | Phase 2 |
| Phase 7 | 20h | HIGH | Phases 1-6 |
| **Total** | **295h** | | |

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|---|---|---|---|
| Operators trust unsupported config/docs and deploy with false assumptions | High | High | Rewrite docs and either implement or remove dead controls immediately |
| API-key feature ships as a false promise for automation users | High | High | Implement auth path or cut the feature from release scope |
| Large S3 uploads cause avoidable memory spikes | Medium | High | Stream uploads or stage them to disk |
| FTP/SFTP protocol regressions surface with real client diversity | Medium | High | Expand protocol integration coverage before broader rollout |
| Frontend regressions ship unnoticed | High | Medium | Add component and e2e tests |

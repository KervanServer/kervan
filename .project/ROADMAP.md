# Project Roadmap

> Based on comprehensive codebase analysis performed on 2026-04-11
> This roadmap prioritizes work needed to bring the project to production quality.

## Current State Assessment

KervanServer is past the toy stage and already delivers a meaningful single-binary file-transfer platform: FTP, FTPS, SFTP, limited SCP, REST API, Web UI, health/metrics, API keys, share links, LDAP basics, TOTP, and migration tooling are all present. The main blockers are not "does it do anything?" but "can this be trusted in production?" Right now the answer is no, mainly because security defaults are unsafe, persistence is weak, deployment infrastructure is absent, and the implementation still trails the specification in several core areas.

What is working well:

- The codebase is modular and understandable.
- Build, tests, vet, staticcheck, govulncheck, and frontend build all succeed.
- The admin surface is broader than the README's cautionary language suggests.

Key production blockers:

- Default admin bootstrap password
- Weak browser/API hardening
- Fragile JSON-file persistence
- No deployment pipeline or container packaging
- Partial protocol compatibility and limited interoperability confidence

## Phase 1: Critical Fixes (Week 1-2)

### Must-fix items blocking safe deployment

- [ ] Remove the hardcoded admin fallback at `internal/server/server.go:455`; require explicit bootstrap credentials or generate a one-time secret. Effort: `4h`
- [ ] Replace per-process API token secrets in `internal/api/server.go:96-120` with persistent signing material and explicit rotation support. Effort: `12h`
- [ ] Add secure HTTP middleware: panic recovery, request IDs, request logging, secure headers, stricter CORS, and basic abuse-rate limits. Effort: `20h`
- [ ] Remove token-in-query-string WebSocket auth in `internal/api/websocket.go:45-47`. Effort: `4h`
- [ ] Decide whether the product claim is "production-ready appliance" or "advanced beta", and align README/spec language accordingly until the hardening work lands. Effort: `4h`

## Phase 2: Core Completion (Week 3-6)

### Complete missing core features from specification

- [ ] Finish FTP compatibility gaps: `PORT`, `EPRT`, `EPSV`, `REST`, `ABOR`, and honest `FEAT` advertising in `internal/protocol/ftp/server.go`. Effort: `36h`
- [ ] Expand SFTP/SCP compatibility, especially recursive SCP and more complete file attribute operations. Effort: `40h`
- [ ] Implement or deliberately cut spec promises around groups/shared directories and account lifecycle features. Effort: `32h`
- [ ] Decide on OIDC scope. If keeping it in-spec, implement Web UI SSO; if not, move it out of v1 commitments. Effort: `40h`
- [ ] Expand MCP from the current 3 tools / 3 resources to a useful administrative surface aligned with Spec Sec. 11. Effort: `24h`

## Phase 3: Hardening (Week 7-8)

### Security, reliability, and data-safety work

- [ ] Replace `internal/store` JSON-file persistence with a durable embedded store plus schema/migration layer. Effort: `60h`
- [ ] Add startup config validation for all security-sensitive runtime combinations. Effort: `10h`
- [ ] Harden audit pipeline against event loss and improve retention/rotation strategy. Effort: `16h`
- [ ] Add stronger share-link controls: shorter defaults, optional one-time links, IP binding or signed metadata, and more explicit audit trails. Effort: `12h`
- [ ] Tighten TLS behavior and certificate error handling, especially around ACME failure modes. Effort: `12h`

## Phase 4: Testing (Week 9-10)

### Comprehensive test coverage

- [ ] Add tests for packages with zero coverage priority order: `internal/protocol/ftp`, `internal/store`, `internal/audit`, `internal/session`, `internal/storage/local`, `internal/webui`. Effort: `48h`
- [ ] Add protocol integration tests with real FTP/SFTP/SCP client interoperability. Effort: `40h`
- [ ] Add API end-to-end tests for login, TOTP, config patching, file flows, and share-link flows. Effort: `24h`
- [ ] Add frontend component or browser tests for the main admin workflows. Effort: `24h`
- [ ] Fix local environment so `go test -race ./...` is runnable and keep it in CI. Effort: `6h`

## Phase 5: Performance & Optimization (Week 11-12)

### Performance tuning and optimization

- [ ] Replace whole-file audit reads with streaming or indexed access. Effort: `12h`
- [ ] Rework S3 backend for multipart upload and streamed I/O instead of whole-object buffering. Effort: `40h`
- [ ] Reduce WebSocket full-snapshot polling by moving to incremental event messages or adaptive intervals. Effort: `16h`
- [ ] Review bundle splitting and route-level code splitting for the Web UI. Effort: `8h`
- [ ] Add large-file transfer benchmarks and memory profiling. Effort: `12h`

## Phase 6: Documentation & DX (Week 13-14)

### Documentation and developer experience

- [ ] Update `README.md` to reflect what is now implemented versus still planned. Effort: `6h`
- [ ] Add `ARCHITECTURE.md` with component diagrams and lifecycle notes. Effort: `8h`
- [ ] Add `API.md` or OpenAPI output for the current REST surface. Effort: `16h`
- [ ] Add `CONTRIBUTING.md`, `LICENSE`, and a realistic release process description. Effort: `8h`
- [ ] Convert `TASKS.md` from stale checkbox backlog into either ADRs + milestone issues or a generated status board. Effort: `6h`

## Phase 7: Release Preparation (Week 15-16)

### Final production preparation

- [ ] Add GitHub Actions CI for build, test, race, lint, and frontend build. Effort: `12h`
- [ ] Add `Dockerfile` and production image hardening. Effort: `10h`
- [ ] Add `.goreleaser.yml` or equivalent release automation. Effort: `8h`
- [ ] Add backup/restore documentation and operational runbooks. Effort: `10h`
- [ ] Add environment-specific config guidance for dev, staging, and prod. Effort: `8h`

## Beyond v1.0: Future Enhancements

- [ ] Multi-node/shared-state architecture instead of single-process in-memory coordination
- [ ] Better admin UX for file preview, audit exploration, and operational drill-down
- [ ] Policy engine for per-user, per-group, and per-path constraints
- [ ] Real event streaming and richer observability/tracing
- [ ] Optional external database-backed control plane

## Effort Summary

| Phase | Estimated Hours | Priority | Dependencies |
|---|---|---|---|
| Phase 1 | 44h | CRITICAL | None |
| Phase 2 | 172h | HIGH | Phase 1 |
| Phase 3 | 110h | HIGH | Phase 1 |
| Phase 4 | 142h | HIGH | Phase 1-3 |
| Phase 5 | 88h | MEDIUM | Phase 3-4 |
| Phase 6 | 44h | MEDIUM | Phase 2 |
| Phase 7 | 48h | HIGH | Phase 1-6 |
| **Total** | **648h** |  |  |

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|---|---|---|---|
| Security incident caused by weak auth/session defaults | High | High | Complete Phase 1 before any internet-facing deployment |
| Data corruption or loss from JSON-file persistence | Medium | High | Replace `internal/store` and add backup strategy |
| Protocol incompatibility with real-world clients | High | Medium | Add interoperability testing before release |
| Scope creep from trying to finish every spec item before stabilizing core | High | Medium | Freeze v1 scope after Phase 2 decisions |
| Operational fragility due to absent CI/CD and packaging | High | Medium | Implement Phase 7 before release candidates |

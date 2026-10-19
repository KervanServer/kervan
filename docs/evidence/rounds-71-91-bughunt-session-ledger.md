# Evidence ledger

Scope: proof-driven bug-hunt continuation, rounds 71–91 (whole-repository sweep with per-round depth)

## Setup evidence
- Repository instructions: CLAUDE.md (RTK tooling only — RTK is NOT installed in this environment, so all commands ran unfiltered); Makefile `test`/`check`.
- Baseline: rev bf945f4 (master, in sync with origin/master), clean tree. Closing commit: 0074c4a "fix: resolve 5 defects found by bug-hunt rounds 71-83b" (12 files, 428+/24−). Worktree clean after the commit.
- Toolchain: Go 1.26.1 (go.mod), toolchain go1.26.9. Test runner: `go test ./... -count=1`; race gate: `CGO_ENABLED=1 go test -race ./... -count=1`; lint: gofmt + go vet. `staticcheck` ABSENT in this environment (pre-existing; `make check` hard-fails there), and the npm WebUI step was not run.
- Verification runs on the closing commit 0074c4a: full suite exit 0 (28 packages ok), repo-wide race exit 0, `go vet ./...` exit 0, worktree clean.
- Prior-round fingerprints honored: r37 (quota Registry — re-verified fixed in buildUserFS), r42 (lazy data-TLS handshake), r46 (MLST), r51 (cross-protocol: no-defect), r63/r64/r65 (store), r9 (S3 pagination), r38 (audit path anchoring), r7-LDAP (BER size cap verified landed).

## Fixed rounds

### Round 71 — vfs Chtimes bypassed the extension/permission model (r71)
- Location: internal/vfs/user_vfs.go — `UserVFS.Chtimes`.
- Root cause: Chtimes enforced no permission, while Chmod (same file) gates on `permissions.Chmod` and the SFTP SETSTAT contract (internal/protocol/sftp/handler.go, handleSetstat doc) treats timestamps as chmod-gated metadata ("a user without the chmod permission gets OK with the change skipped"). FTP MFMT therefore applied timestamps for chmod-denied users (SITE CHMOD was refused 550) and SFTP SETSTAT/SCP -p silently applied them despite their documented skip policy.
- Proof (before): scratch `TestZZR82`-style in-package test — `upload of .exe succeeded (200)…` analog for MFMT: `MFMT without the chmod permission = "213 Modify=…", want 550`; control (allowed user) passed. See `.temp_files/r71` evidence log (removed after the round; full proof text in the session transcript).
- Patch: Chtimes returns `os.ErrPermission` without `permissions.Chmod`, mirroring Chmod. Consumers land on documented behavior: handleMFMT → 550; SFTP SETSTAT and scp.go swallow the error (skip policy); no API/WebUI callers.
- Regression tests: `TestUserVFSChtimesRequiresChmodPermission` (internal/vfs/user_vfs_test.go); `TestFTPMfmtSetsModificationTime` extended (allowed 213 + denied 550/untouched); `TestFTPMiscCommands` updated.
- Docs: README MFMT bullet updated to state the chmod requirement.

### Round 73 — SFTP CLOSE leaked its handle slot on close errors (r73)
- Location: internal/protocol/sftp/handler.go — `handleClose` (delete only on the success path).
- Root cause: when `v.file.Close()` failed (reachable via S3, which uploads on Close), the handler returned early without deleting `h.handles[handle]` — the closed file kept one of `maxOpenHandles` slots until the session ended, a retried CLOSE re-ran Close and re-emitted the audit event (auditFileClosed's contract is one event per opened file), and the stale handle answered READ/WRITE as a closed file.
- Proof (before): scratch in-package test with a stub FS whose Close fails — controls (successful close) passed, target failed: `CLOSE that reported a close error keeps handle "H0000000000000001" in the table`.
- Patch: delete the handle unconditionally before the type switch (draft-ietf-secsh-filexfer-02 §6.3; OpenSSH behaves the same). A retried CLOSE gets "invalid handle".
- Regression tests: `TestHandleCloseConsumesHandleEvenWhenCloseFails` / `TestHandleCloseConsumesHandleWhenCloseSucceeds` (internal/protocol/sftp/handler_test.go).

### Round 82 — API upload bypassed the extension policy via its staging name (r82)
- Location: internal/api/server.go — `handleFilesUpload`.
- Root cause: the staging file was `path + ".kervan-upload.tmp"` and `UserVFS.Open` applies AllowedExts/DeniedExts to the OPENED name — so DeniedExts never fired for the real extension (evil.exe landed via the un-checked rename) and an AllowedExts whitelist rejected every legitimate upload (.tmp not whitelisted). FTP STOR, SFTP and the SCP sink enforce the same policy via UserVFS.Open; the API upload was the lone bypass.
- Proof (before): scratch `TestZZR82*` — denied `.exe` upload returned 200 and created the file; allowed `.docx` upload returned 400. Controls passed.
- Patch: staging files are PREFIX siblings — `path.Join(path.Dir(p), "kervan-upload-"+path.Base(p))` — so the staging extension equals the target's and the existing check enforces exactly the target's policy both ways, keeping the atomic temp+rename (the prior truncation fix).
- Regression tests: `TestFileUploadEnforcesDeniedExtensions` / `TestFileUploadEnforcesAllowedExtensions` (internal/api/files_handlers_test.go); the temp-file control updated to the new staging name.

### Round 83 — bulk user import was invisible to the audit trail (r83)
- Location: internal/api/users_bulk.go — `handleUsersImport`.
- Root cause: the import created N users (admin session or `users:write` API key) and never called `emitAudit`, while every sibling admin mutation emits — single-user create (admin.user.create), update, delete, and even handleUsersExport in the same file.
- Proof (before): scratch `TestZZR83*` with the memorySink harness — a 2-user import produced 0 `admin.user.create` events; the report control (Created == 2) passed.
- Patch: one `audit.EventAdminUserCreate` per created user in the import loop, with target_user/admin/import meta, matching the single-create path's event shape.
- Regression test: `TestUserImportEmitsAuditEvents` (internal/api/audit_test.go).

### Round 83b — the TOTP lifecycle was the lone unaudited security-relevant account mutation (r83b)
- Location: internal/api/server.go — `handleTOTP` (disable), `handleTOTPEnable`, `handleTOTPSetup` (rotation of an ENABLED secret); enum: internal/audit/event.go.
- Root cause: no TOTP-related event type existed and none of the three handlers called `emitAudit` — disabling 2FA silently downgraded an account's protection with no trail, while password change (auth.password_change), own-key updates (admin.user.update) and all user/admin mutations were audited.
- Proof (before): scratch `TestZZR83b*` with the memorySink harness — a full setup→enable→disable flow produced 0 `auth.totp_change` events; all status controls passed.
- Patch: new `EventTOTPChange EventType = "auth.totp_change"`; emitted on enable ("enabled"), on disable of an ENABLED factor ("disabled"; clearing a never-enabled pending secret emits nothing), and on rotation of an enabled secret ("rotated"; first-time pending setup emits nothing). `mutateTOTP`'s optimistic-concurrency 409 guard closes the check-then-act windows.
- Regression test: `TestTOTPChangesAreAudited` (internal/api/audit_test.go).

## No-defect rounds (coverage)

- r72/r74–r91 (14 rounds): every production Go package walked at least once under the bug-hunter pattern table — ftp dispatch/data plane, sftp handler/server/scp, vfs (all files), quota, transfer, session, throttle, netguard (+ acquire/release balance across consumers), oidc (jwt/oidc/api handlers), api/server.go middleware+auth+all handler bodies, token.go, websocket.go, events broker, webui, util/log+ulid, build, full config package, auth engine+password+ldap (incl. the BER client), audit sinks (http/syslog) and the fail-closed CLI verify, monitoring (emitter + health builders), cmd/kervan (all commands), scripts tooling.
- Refuted candidates kept distinct from findings: audit REST scoping (parseAuditQuery applies viewerScopedUsername — refuted on source), wrapDataConn zero-deadline (NewServer normalizes), metrics label cardinality (metricPathLabel buckets to "unmatched"), v1 cross-user access (canAccessTargetUser admin-or-self), API-key self-escalation (documented preset/scope design), upload staging-name concurrency (pre-existing, confined to the owner's own file).
- Closed stale evidence: the r47 (2026-09-25) MLSD-over-TLS wedge no longer reproduces — its exact sequence is permanently covered by `TestFTPMlsdOverTLSAfterAuthUpgrade` (passes at bf945f4+); the round-4-era "re-login orphans the session" candidate is fixed at server.go:464-467; the r7-LDAP BER size cap is landed; round-4-era "re-login orphans the session" candidate is fixed at server.go:464-467; the stale CCC finding does not apply to the current tree (no CCC case; FEAT does not advertise it).

## Harness / environment notes
- RTK is not installed in this environment (exit 127); all commands ran unfiltered despite CLAUDE.md.
- `staticcheck` absent (pre-existing); `make check`'s npm step not run.
- `.temp_files/` scratch (proof scripts, zz_* tests, commit-message draft) was removed after each round; only `r47-20260925-ftps-mlst-edges/` (an earlier session's artifact) remains.

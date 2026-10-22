# Evidence ledger

Scope: proof-driven bug-hunt, rounds r02–r92 (2026-10-20 session, committed here) and r05–r10 (2026-10-22 session) — storage-backend contract sweep: the memory and S3 backends against the POSIX/local oracle, the API upload staging path, and S3 metadata truthfulness.

## Setup evidence

- Repository instructions: CLAUDE.md (RTK tooling only — RTK is NOT installed in this environment, so all commands ran unfiltered); Makefile `test`/`check`.
- Baseline: rev 417e400 (master, 2 ahead of origin) with the 2026-10-20 session's fixes uncommitted in the working tree; those fixes and this arc's rounds were committed as ten per-defect commits `b608b96..a01da78` and pushed (branch-protection bypass recorded by the remote on both pushes). Hosted CI on the closing commit `a01da78`: run `39769495247`, all five required checks (Backend Checks, Race Detector, Race Detector (SQLite backend), WebUI Checks, Windows Store Tests) completed/success; Docker Image Build skipped by its workflow_dispatch-only condition.
- Toolchain: Go 1.26.1 (go.mod), toolchain go1.26.9, linux/amd64. Test runner: `go test ./... -count=1`; race gate: `go test -race ./... -count=1` (exit 0 after every round's fix); lint: gofmt + go vet (clean each round); `staticcheck` absent locally but runs in hosted CI (green at `a01da78`).
- Proof protocol: each round wrote a scratch proof under `.temp_files/proof-driven-bug-hunter/<round-id>/` and ran it in-package through `go test -overlay` so shared-checkout files were never reverted; red was additionally reproduced for the promoted regression tests against an isolated unfixed copy (working tree minus the round's guard, or `git show HEAD:` when the tree was clean). Round scratch dirs r05–r10 were removed after each round's report per protocol; the durable regressions named below are the runnable artifacts. The prior session's dirs (r02/r03/r04/r92 dated 2026-10-20, plus 2026-09-25 r47) are retained.
- Prior-round fingerprints honored: r37 (quota Registry — re-verified fixed), r42 (lazy data-TLS), r46 (MLST), r51 (cross-protocol no-defect), r63–r65 (store), r9 (S3 ReadDir pagination), r38 (audit path anchoring), r71 (Chtimes chmod gate), r73 (SFTP CLOSE handle leak), r82 (upload staging extension policy), r83/83b (audit coverage).

## Fixed rounds

### Round r02 — memory backend exclusive create clobbered existing paths
- Location: internal/storage/memory/memory.go — `Backend.Open`.
- Root cause: `O_CREATE|O_EXCL` opened the existing node and `Close` wrote back through it, breaking the atomic no-overwrite contract that FTP STOU, SFTP `SSH_FXF_EXCL` and the API upload staging path rely on.
- Proof (before): scratch `TestBackendExclusiveCreatePreservesExisting` via `go test -overlay` — existing file, exclusive open, Close → existing content overwritten; local_control leg passed. Scratch retained at `.temp_files/proof-driven-bug-hunter/r02-20261020-exclusive-c84d/`.
- Patch: refuse with `os.ErrExist` when the path exists (guard before any mutation). Commit `b608b96`.
- Regression tests: `internal/storage/memory/exclusive_test.go`.

### Round r03 — memory backend directory rename stranded the subtree
- Location: internal/storage/memory/memory.go — `Backend.Rename`.
- Root cause: Rename moved only the directory node in the flat `nodes` map; descendants stayed keyed under the old paths (renamed directory listed empty, nested files unreachable).
- Proof (before): scratch `TestBackendDirectoryRenameMovesSubtree` — subtree files absent under the new name on memory, present on local_control. Scratch retained at `.temp_files/proof-driven-bug-hunter/r03-20261020-dir-move-e91a/`.
- Patch: same-path no-op, own-descendant rejection, re-key descendants under `b.mu`; handles keep their node pointers. Commit `3e18650`.
- Regression tests: `internal/storage/memory/rename_directory_test.go`.

### Round r04 — memory backend MkdirAll built under file components
- Location: internal/storage/memory/memory.go — `Backend.MkdirAll`.
- Root cause: a regular file at `/blocker` did not stop `MkdirAll("/blocker/child")`; the child was keyed under a non-directory.
- Proof (before): scratch `TestFileMkdirRejectsRegularFileComponents` over the API mkdir handler — 2xx with a broken tree on memory; local_control refused. Scratch retained at `.temp_files/proof-driven-bug-hunter/r04-20261020-mkdir-components-74cb/`.
- Patch: refuse with "path component is not a directory". Commit `770cdb8`.
- Regression tests: `internal/api/mkdir_components_test.go`.

### Round r92 — API upload staging name truncated and removed an unrelated file
- Location: internal/api/server.go — `handleFilesUpload`.
- Root cause: the staging file was a fixed sibling (`uploadTempPrefix+base`), so an unrelated user file with that name was truncated then removed by cleanup, and the `.tmp` extension bypassed Allowed/DeniedExts.
- Proof (before): scratch `TestFileUploadPreservesUnrelatedStagingSibling` — neighbor file destroyed by an upload whose target did not exist. Scratch retained at `.temp_files/proof-driven-bug-hunter/r92-20261020-sibling-b7e42/`.
- Patch: per-request staging sibling `kervan-upload-<rand.Text()>-<name>` opened `O_CREATE|O_EXCL`, keeping the target extension. Commit `4db2c1c`.
- Regression tests: `internal/api/upload_sibling_regression_test.go`; staging control updated in `internal/api/files_handlers_test.go`.

### Round r05 — memory backend rename overwrote existing destinations
- Location: internal/storage/memory/memory.go — `Backend.Rename` (destination side; r03's unaudited "existing-target replacement" lead).
- Root cause: no destination check — file-over-dir turned a directory into a regular file whose children stayed keyed (statable, invisible to listings, quota-leaked); dir-over-file deleted the file; dir-over-non-empty-dir discarded the destination subtree; anything could replace the root.
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r05-20261022-rename-dst-6f3a/overlay.json -run TestBackendRenameOntoExistingDirectoryIsRejected -count=1 ./internal/storage/memory/` → exit 1: three memory scenarios failed (impact lines: orphaned child still reachable, listing fails "not a directory"); local_control and controls passed. The promoted root-boundary test caught the first fix's `//` prefix hole (newP=="/" matched nothing) before commit.
- Patch: destination type/emptiness switch (file-over-dir, dir-over-file, dir-over-non-empty-dir rejected; root counts as never-empty). Commit `c8dd877`.
- Regression tests: `internal/storage/memory/rename_destination_test.go` (three tests; local_control legs cover the universal POSIX rejections).

### Round r06 — S3 backend rename copied without inspecting the destination
- Location: internal/storage/s3/backend.go — `Backend.Rename`.
- Root cause: copy-then-delete with no destination checks — a file object at the exact key shadowed a directory (Stat=file, ReadDir failed, children stranded); a directory rename onto a file stranded the moved subtree behind the file object; dir-over-non-empty-dir silently overwrote colliding destination keys (proven data loss: `/b/keep.txt` replaced); same-path rename self-copied then deleted every key under the directory.
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r06-20261022-s3-rename-dst-a91c/overlay.json -run TestS3RenameOntoExistingDestinationIsRejected -count=1 ./internal/storage/s3/` → exit 1 with the three scenarios and impact logs; controls passed. Promoted tests red against an isolated `git show HEAD:` baseline copy.
- Patch: same-path no-op, own-descendant rejection, destination `Stat` type/emptiness switch (the S3 analogue of r05). Commit `acb2d15`.
- Regression tests: `internal/storage/s3/rename_destination_test.go` (`TestS3RenameOntoExistingDestinationIsRejected`, `TestS3RenameBoundaryTargets`).

### Round r07 — S3 backend ignored O_EXCL on open
- Location: internal/storage/s3/backend.go — `Backend.Open`.
- Root cause: exclusive create of an existing object returned a writable handle: without `O_TRUNC` it downloaded the object and Close re-PUT the merged content (proof red: `/taken.txt` became `"clobberedcontent"`), with `O_TRUNC` it replaced the object wholesale; an existing directory was also accepted; missing paths returned nil where the other backends return `os.ErrNotExist`.
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r07-20261022-s3-excl-b4d2/overlay.json -run TestS3ExclusiveCreateRefusesExisting -count=1 ./internal/storage/s3/` → exit 1 (three defect scenarios + the sequential re-create contract); controls passed.
- Patch: pre-flight `Stat` guard returning `os.ErrExist`. Commit `4a1f4c5`.
- Regression tests: `internal/storage/s3/exclusive_create_test.go`.

### Round r08 — S3 Chtimes reported success it did not achieve
- Location: internal/storage/s3/backend.go — `Backend.Chtimes`.
- Root cause: returned nil unconditionally — FTP MFMT answered `213 Modify=...` that the next MDTM/MLSD/Stat read contradicted (proof red: ModTime 10:41 vs requested 08:41), and missing paths returned nil where memory/local return `ErrNotExist`. S3's Last-Modified is server-managed and cannot be set, so copy-with-metadata was rejected (needs an x-amz-meta side channel, client signature changes, harness modeling, Stat preference logic, and full-object rewrites that reset Last-Modified anyway); the truthful refusal lands every consumer on documented behavior (MFMT→550, SFTP SETSTAT→OK-with-skip per `isHardSetstatError`, SCP best-effort).
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r08-20261022-s3-chtimes-d7e5/overlay.json -run TestS3ChtimesDoesNotReportSuccessWithoutEffect -count=1 ./internal/storage/s3/` → exit 1 (both lies); memory control passed.
- Patch: Stat-check then `errors.New("s3 backend cannot set modification times")` — deliberately not `os.ErrNotExist` so SETSTAT stays a soft skip. Commit `4906645`.
- Regression tests: `internal/storage/s3/chtimes_test.go`.

### Round r09 — S3 Chmod reported success it did not achieve (Chown examined)
- Location: internal/storage/s3/backend.go — `Backend.Chmod` (and `Chown`).
- Root cause: same class as r08 — `SITE CHMOD 600` answered 200 while Stat/LIST/SFTP attrs always report 0644 (read-after-write contradiction); missing paths returned nil.
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r09-20261022-s3-chmod-c2f8/overlay.json -run TestS3ChmodDoesNotReportSuccessWithoutEffect -count=1 ./internal/storage/s3/` → exit 1 (nil + "mode remains 644"); memory control passed.
- Patch: refusal as in r08 (SITE CHMOD→550, SETSTAT soft skip). `Chown` deliberately unchanged: `UserVFS.Chown` has zero production callers and `memory.Backend.Chown` no-ops identically — nothing observable can contradict it, so no proof was possible and none was manufactured. Commit `aa12bab`.
- Regression tests: `internal/storage/s3/chmod_test.go`.

### Round r10 — S3 exclusive create closed the r07 TOCTOU with a conditional PUT
- Location: internal/storage/s3/client.go (`PutObjectIfNoneMatch`), backend.go (`file.Close`), backend_test.go (fake).
- Root cause: r07's guard is a pre-flight Stat, but `Close` committed with an unconditional `PutObject`, so an object created between the open and the Close was still clobbered (proof red, deterministic interleaving without sleeps: exclusive open on fresh `/race.txt`, racing plain write commits "winner" while the handle is open, exclusive Close succeeded and overwrote with "exclusive").
- Proof (before): `go test -overlay .temp_files/proof-driven-bug-hunter/r10-20261022-s3-condput-e6b1/overlay.json -run 'TestS3ExclusiveCreateRefusesObjectCreatedAfterOpen|TestS3ExclusiveCreateStillSucceedsWithoutRacer|TestS3PlainOverwriteStillReplaces' -count=1 ./internal/storage/s3/` → exit 1 on the race scenario, controls green; promoted test red against a HEAD (`aa12bab`) baseline copy.
- Patch: `ErrPreconditionFailed` (HTTP 412) in the client, `PutObjectIfNoneMatch` (`If-None-Match: *`), exclusive handles commit conditionally and map 412→`os.ErrExist`; the fake S3 server models the conditional-write behavior; the signer is untouched. Commit `a01da78`.
- Regression tests: `internal/storage/s3/exclusive_create_race_test.go`.

## Ruled-out candidates (coverage)

- `s3.Chown` silent no-op — unreachable (`UserVFS.Chown` has no production caller) and matches `memory.Backend.Chown`'s no-op convention; not a defect.
- `memory.Statvfs` all-zero `StatVFS` — the only consumer (`sftp.replyStatVFS`, handler.go) serializes the values; no division or crash path found. Not proven harmful.
- S3 multipart absence — documented limit (`file.Close` re-PUTs whole objects); a capacity tradeoff, not a contract violation.
- SFTP `SETSTAT`/SCP timestamp flows — r5/r71 fingerprints already cover them; re-verified as skip-policy consumers of the r08/r09 refusals.
- Coverage note: rounds here walked `memory.go`, `local.go`, `s3/backend.go` + `client.go` in full, `UserVFS` Open/Rename/Remove/Chmod/Chown/Chtimes, the FTP RNFR/RNTO/STOU/SITE/MFMT dispatch, SFTP `mapOpenFlags`/`handleSetstat`/close, and the API rename/upload handlers. The overlayfs-backed tempdir divergence (dir-over-empty-dir returns EEXIST where POSIX rename(2) succeeds) is recorded in the memory-backend regression as the reason that variant has no local_control leg. No recorded S3 or memory-backend leads remain open.

## Harness / environment notes

- RTK is not installed in this environment (exit 127); all commands ran unfiltered despite CLAUDE.md.
- `staticcheck` absent locally (pre-existing); hosted CI runs it and was green on every pushed commit of this arc, including the closing `a01da78`.
- Round scratch directories for r05–r10 were removed after each round's report (only disposable proof fixtures lived there); the red/green commands above are recorded exactly as run, and each round's durable regression lives in the package's normal test suite.

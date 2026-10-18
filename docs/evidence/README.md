# Evidence records

Durable engineering evidence — test results, stress runs, and proof of fixes —
that belongs in version control but does not belong in the source tree.

## Why this directory exists

`.gitignore` excludes both places agent evidence previously landed:

- `.temp_files/` — scratch by design; anything there is discarded with the session.
- `.wrongstack/` — ignored, then un-ignored file by file. Only
  `/.wrongstack/project.json` and `/.wrongstack/tool_use_dispatch_defect.md` are
  tracked; everything else stays ignored, including `memories/sage.db` and its
  open SQLite WAL. `scripts/check-gitignore-safety.sh` fails the build if that
  ever changes.

That left no tracked home for evidence records. `docs/evidence/` is that home: it
sits under the already-tracked `docs/`, and it fights neither ignore rule.

## What belongs here

- Stress and soak runs that CI cannot cheaply reproduce.
- Proof for a fix: the command, the revision, and the before/after result.
- Ledgers recording what a multi-round investigation found and ruled out.

## What does not belong here

- Build output, coverage reports, or logs — those stay ignored.
- Anything reproduced trivially by `make check`; link to the command instead.
- Credentials. Scan before committing; these files are permanent.

## Rules for a record

1. **Name the revision.** Every result binds to a commit SHA.
2. **State the command**, exactly as run, so a reader can re-execute it.
3. **Record what the evidence does *not* show.** A green result that exercises
   one platform or path is not evidence about another. See
   `store-stress-b2fe9f7.md` for the pattern: the run is clean, and it still
   cannot speak to the `windows-latest` locking path.
4. **Separate local and hosted runs.** They are different outcomes with
   different evidence.

## Files

| File | What it records |
|---|---|
| `store-stress-b2fe9f7.md` | 6,400 race-instrumented `internal/store` executions under 16× CPU load at `b2fe9f7`. |
| `rounds-63-67-internal-store-ledger.md` | Ledger for the `internal/store` investigation, rounds 63–67: invariants, findings, and ruled-out candidates. |
| `tool-use-dispatch-defect-snapshot.md` | Measured hash of the **tracked** `.wrongstack/tool_use_dispatch_defect.md`, anchoring runtime response time when memory records cannot be retired. |
| `gates-4dc1efa.md` | `make check` and `make check-sqlite` exit 0 at `4dc1efa`, agreeing with the five hosted contexts on the same SHA. |
| `hosted-windows-samples.md` | Per-revision hosted `Windows Store Tests` sample sizes (15/11/17 across three revisions) behind the `SQLITE_BUSY` question, and why they are not pooled. |

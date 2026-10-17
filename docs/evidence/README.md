# Evidence records

Durable engineering evidence — test results, stress runs, and proof of fixes —
that belongs in version control but does not belong in the source tree.

## Why this directory exists

`.gitignore` excludes both places agent evidence previously landed:

- `.temp_files/` — scratch by design; anything there is discarded with the session.
- `.wrongstack/` — ignored except `/.wrongstack/project.json`, so
  `.wrongstack/tool_use_dispatch_defect.md` is untracked.

That left no tracked home for evidence. `docs/evidence/` is that home: it sits
under the already-tracked `docs/`, and it fights neither ignore rule.

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
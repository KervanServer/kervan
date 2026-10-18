# Hosted Windows Store Tests samples — three revisions

Durable record of the hosted `windows-latest` sample sizes behind the
`SQLITE_BUSY` question, which previously lived only in project memory. Memory
proved an unreliable substrate here: those records all read
`anchorVerified=unknown`, and a supersede link records a correction without
deactivating the superseded entry, so stale counts keep injecting.

Counts below were re-read from the check-runs API for each exact SHA on
2026-10-17, not copied from memory.

## The measurement

Per revision, `check-runs` filtered to `name == "Windows Store Tests"`, counting
only `(status, conclusion) == ("completed", "success")`:

```console
$ gh api "repos/KervanServer/kervan/commits/$SHA/check-runs?per_page=100"
```

| Revision | Full SHA | total | completed/success | not-green | `0.75^N` |
|---|---|---|---|---|---|
| `b2fe9f7` | `b2fe9f74c4fa138d8dc0413b99d347433dac6bf9` | 15 | **15** | 0 | 1.34% |
| `11eab44` | `11eab447f9049fd980303d36492ca22f2bc2a239` | 11 | **11** | 0 | 4.22% |
| `81298a8` | `81298a89885c3f852aff71ae227b43644dbad5a3` | 17 | **17** | 0 | 0.75% |

Zero `not-green` at every revision: no failure, cancellation or skip appears in
any of the 43 runs.

## Why these are NOT pooled

`81298a8` differs from `11eab44` only by `docs/evidence/` — **no Go source
changed**. Those 17 runs exercise the same compiled artifact as the 11 before
them. Pooling to "43 runs" would present one artifact measured repeatedly across
three labels as three independent confirmations, which is weaker evidence than
it appears, not stronger. `0.75^N` is reported per revision for that reason.

## What this does NOT show

- **It does not show the flake is fixed.** The by-chance column is computed
  against a **pre-fix 25% failure rate estimated from only 4 observations**
  (`TestSQLiteUpdateIsAtomicAcrossProcesses`). Four samples cannot establish a
  rate, and 1.34% is not zero. A 25% baseline that is itself wrong makes every
  figure here wrong in the same direction.
- **It does not isolate `e7d5385`.** That commit bundled the `BEGIN IMMEDIATE`
  busy-retry with other changes; no experiment here separates their effects.
- **It says nothing about the local Ubuntu stress.** See
  [`store-stress-b2fe9f7.md`](store-stress-b2fe9f7.md): 6,400 race-instrumented
  executions with zero failures, but that is POSIX `fcntl` locking and cannot
  speak to the `LockFileEx` path where the flake appears. The two are
  complementary, **not** substitutes.
- **It is a sample of CI runs, not a controlled experiment.** Runs are sequential
  dispatches; runs were not randomised, blinded, or interleaved across
  revisions.

## Method notes worth keeping

- Runs were dispatched **sequentially**. The workflow sets
  `concurrency: cancel-in-progress: true`, so overlapping dispatches cancel each
  other and silently collapse N runs into fewer.
- Each conclusion was read from **check-runs at the exact head SHA**, after
  waiting for every check-run at that head to report `status=completed`.
- An earlier ad-hoc harness read `conclusion` immediately after
  `gh run watch --exit-status` returned and reported two green runs as "not
  green", briefly suggesting a `SQLITE_BUSY` failure that did not exist. That
  harness is now `scripts/ci-sample.sh`, which polls until settled and checks
  `status` alongside `conclusion`.

## Re-measuring

These counts are point-in-time. Re-read the API rather than trusting this table;
check-runs accumulate and later dispatches on the same SHA change the totals.
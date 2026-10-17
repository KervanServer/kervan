# Store stress evidence — `master` @ `b2fe9f7`

Recorded 2026-10-17. This is local evidence that hosted CI cannot cheaply produce —
and it bounds what may be claimed from it.

## Command and result

```
KERVAN_STORE_BACKEND=sqlite CGO_ENABLED=1 \
  go test -race ./internal/store/ -count=200 -timeout 90m
```

Run on 16 cores held at capacity by 16 concurrent busy loops, the repository's
stress method for timing-sensitive code.

| | |
|---|---|
| Planned executions | 6,400 (32 test functions × 200) |
| Exit code | **0** |
| Duration | 1,195.270 s (18:32:39 → 18:52:36 +03:00) |
| `--- FAIL` | **0** |
| `DATA RACE` | **0** |
| `database is locked` / `SQLITE_BUSY` | **0** |
| Timeout panic | **0** |

Package result: `ok github.com/kervanserver/kervan/internal/store 1195.270s`

## Related runs at the same head

- **Idle, no `-timeout` override:** 6,400 executions, exit 0, 555 s, same zero counts.
- **Loaded at Go's default timeout:** killed by `panic: test timed out after 10m0s`
  at 600 s with zero assertion failures. That was a harness limit, not a defect;
  `-timeout 90m` removed it. **Any repeat-count gate in this repository needs an
  explicit `-timeout`.**
- **Hosted Windows evidence at `b2fe9f7`:** 15 consecutive green
  `Windows Store Tests` runs — 1.3% by chance if the observed pre-fix 25%
  failure rate were unchanged.

## What this shows

The busy-retry in `internal/store/sqlite_backend.go` and the cross-process
atomicity tests in `internal/store/sqlite_backend_test.go` are race-clean and
contention-stable across 6,400 race-instrumented executions under CPU
saturation.

## What this does **not** show

Nothing about the `SQLITE_BUSY` flake observed only on `windows-latest`. SQLite
takes a different locking path per platform — POSIX `fcntl` byte-range locks on
Ubuntu versus mandatory `LockFileEx` locking on Windows — so this is evidence
about the **Ubuntu/POSIX path only**.

Do **not** cite it as clearing the Windows flake. The `Windows Store Tests` job
remains the only job able to reproduce it.
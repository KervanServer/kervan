# Local gate evidence — `make check` and `make check-sqlite` at `4dc1efa`

Both local targets exited **0** on the current default branch, and the hosted gate
agrees on the **same SHA**. Recorded because the merge gate (`scripts/merge-pr.sh`)
does not watch its own merge commit — see
[the `merge-pr` workflow record](01M5492BMP1GQBP2KN83G37G5E).

## Identity

```
branch        master
HEAD          4dc1efa3f5ff9916112f18b9f251859c471ecf20
origin/master 4dc1efa3f5ff9916112f18b9f251859c471ecf20   (equal)
worktree      clean (0 modified paths)
staticcheck   2026.2.2 (0.8.2) on PATH
```

Run **2026-10-17**, immediately after `PR #21` merged as `4dc1efa`.

## Commands and exit codes

```console
$ make check
MAKE_CHECK_EXIT=0        failure lines matched (FAIL|--- FAIL|Error:): 0

$ make check-sqlite
MAKE_SQLITE_EXIT=0       failure lines matched (FAIL|--- FAIL|Error:): 0
```

Exit codes were captured directly rather than through a pipe. That distinction is
not pedantic here: piping into `tail`/`sed` reports the *last command's* status, which
caused two wrong conclusions earlier in this effort.

A zero exit alone is not the evidence — the logs were also grepped for
`FAIL`, `--- FAIL` and `Error:` and matched nothing, so no step failed *and* no
step was silently skipped.

## Steps covered by `make check`

| Step | Result |
|---|---|
| `gofmt -l $(git ls-files '*.go')` | clean; non-empty would `exit 1` |
| `go vet ./...` | clean |
| `staticcheck ./...` | 0 findings; the target **hard-fails** if absent, it was present |
| `go test ./... -count=1` | all packages `ok` |
| `CGO_ENABLED=1 go test -race ./... -count=1` | all packages `ok` |
| `$(MAKE) check-sqlite` | see below |
| `cd webui && npm test` | `Test Files 17 passed (17)` |
| `go run ./scripts` | regenerates `internal/webui/dist` |
| `git diff --exit-code -- internal/webui/dist` | no drift |

112 `ok` package lines in the `make check` log — 28 packages across four test
invocations (JSON plain, JSON `-race`, SQLite plain, SQLite `-race`).

## SQLite backend

`check-sqlite` re-runs the suite under `KERVAN_STORE_BACKEND=sqlite`, plain and
`-race`, because the default gate only exercises the JSON backend:

```
ok  github.com/kervanserver/kervan/internal/store   0.365s   (plain)
ok  github.com/kervanserver/kervan/internal/store   4.461s   (-race)
```

56 `ok` lines, 0 failures.

## What this record is specifically evidence *of*

The `ci-sample` work landed in `4dc1efa` and was **exercised inside this full
gate, not in isolation**:

- `scripts/ci_sample_test.go` ran as part of `go test ./...` and again under
  `-race` and under the SQLite backend;
- `scripts/ci-sample.sh` is driven by that test against a fake `gh`, so the
  script's settled-poll loop and counting execute for real;
- the Makefile `ci-sample` target and its `.PHONY` entry are parsed by `make
  check` itself.

A narrower `go test ./scripts/` run proves the same tests pass but does not prove
the full gate — which also covers `gofmt`, `vet`, `staticcheck`, the race suite,
the WebUI suite and the `internal/webui/dist` drift check.

## Local and hosted agree on the same SHA

| Gate | Evidence |
|---|---|
| `make check` | exit 0 at `4dc1efa` |
| `make check-sqlite` | exit 0 at `4dc1efa` |
| Hosted CI | 5/5 required contexts `completed/success` at `4dc1efa`, read **by name** after `settled=SETTLED` |

Required contexts, by name: Backend Checks, Race Detector, Race Detector (SQLite
backend), WebUI Checks, Windows Store Tests. `Docker Image Build` is
`completed/skipped` — non-required by design, gated on the `docker: false` input.

The hosted run was read after waiting for every check-run at the head to report
`status=completed`, so these conclusions come from settled results, not from a
script's exit path.

## What this record does NOT show

- **Not a fix.** It records a green gate on one revision. Nothing here claims the
  intermittent Windows `SQLITE_BUSY` flake is resolved; that question rests on the
  separate per-revision Windows samples, which are kept unpooled.
- **Not a licence to skip CI.** A green local gate and a green hosted gate are
  different outcomes; both are reported here, neither substitutes for the other.
- **Single revision.** Re-run rather than trusting these exit codes at another
  SHA — three earlier recorded hashes in this directory have already gone stale.
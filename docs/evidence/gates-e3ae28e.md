# Local gate evidence — full `make check` at `e3ae28e`

## Revision

Bound to **`e3ae28e754eb2ed42ec56065d5997dfdd48c8096`** (`master`), worktree clean
before the run. Recorded 2026-10-18, `go1.26.9 linux/amd64`,
`staticcheck 2026.2.2 (0.8.2)`.

This is the merge commit for PR #28. Its only change against its parent `8418c97`
is `CHANGELOG.md`.

## Command

Each step was run directly with its exit code captured, not through a pipe:

```console
$ ./scripts/check-gitignore-safety.sh
$ gofmt -l $(git ls-files '*.go')
$ go vet ./...
$ staticcheck ./...
$ go test ./... -count=1
$ CGO_ENABLED=1 go test -race ./... -count=1
$ make check-sqlite
$ (cd webui && npm test)
$ go run ./scripts
$ git diff --exit-code -- internal/webui/dist
```

## Result — ten of ten green

| Step | Exit |
|---|---|
| `check-gitignore-safety` | 0 |
| `gofmt` (`git ls-files`) | 0 |
| `go vet ./...` | 0 |
| `staticcheck ./...` | 0 |
| `go test ./... -count=1` | 0 |
| `go test -race ./... -count=1` | 0 |
| `check-sqlite` | 0 |
| `webui npm test` | 0 |
| `go run ./scripts` | 0 |
| `internal/webui/dist` drift | 0 |

`check-sqlite` is a separate `make` target, not a sub-step: it re-runs the suite
under `KERVAN_STORE_BACKEND=sqlite`, plain and with `-race`. Both store backends
are covered.

## A first run reported `-race` exit 1. That was a broken measurement.

The first attempt wrote its logs to `/tmp/s1.log` … `/tmp/s10.log` — predictable,
shared names on this machine. Its failure scan reported:

```
--- FAIL: TestSFTPHandshakeAndInitRoundTrip (4.22s)
--- FAIL: TestScriptEngineLazyInitRaces (0.04s)
FAIL github.com/cachestorm/cachestorm/internal/command  0.081s
```

`github.com/cachestorm/cachestorm` **is not a dependency of this module** —
absent from `go.mod`, `go.sum`, and any `go.work`. `./internal/command` does not
exist in the repository. Those lines were foreign output from another process
writing to the same `/tmp` paths.

Re-running with a private log directory:

```console
$ D=$(mktemp -d); CGO_ENABLED=1 go test -race ./... -count=1 > "$D/race.log" 2>&1; echo $?
0
$ grep -cE '^(FAIL|--- FAIL)' "$D/race.log"
0
```

**No test in this repository failed at any point.** The red result was a
measurement artifact, not a defect, and it is recorded here rather than quietly
dropped because the correction is the transferable lesson: write logs to a private
path, never a predictable `/tmp` name.

## Control run on the parent commit

`TestSFTPHandshakeAndInitRoundTrip` under `-race`, three iterations in a detached
worktree at `8418c97`: exit 0 each time, no data races, `ok` in ~2.3 s per
iteration. The failing packages are byte-identical between `8418c97` and
`e3ae28e` — `git diff --name-only 8418c97..e3ae28e` returns `CHANGELOG.md` only —
so a nine-line prose insert could not have altered Go test behaviour.

## What this evidence does NOT show

- **Not a hosted result.** These are local runs. The five required CI contexts on
  `e3ae28e` are a separate outcome, read separately.
- **Not the `windows-latest` path.** This is `linux/amd64`. `internal/store/
  atomic_windows.go` is compiled and exercised only by the `Windows Store Tests`
  job; see `hosted-windows-samples.md` for the per-revision hosted sample.
- **Not a concurrency proof.** One `-race` pass over a mostly-idle developer box.
  `store-stress-b2fe9f7.md` is the load-bearing record for store concurrency.
- **No flake was characterised.** The red result above was misattributed output;
  nothing here establishes that either named test is flaky.
- **Bound to one revision.** `e3ae28e` only. It says nothing about any later
  commit, and says nothing about the `.wrongstack/` report whose hash moves
  independently of `master`.

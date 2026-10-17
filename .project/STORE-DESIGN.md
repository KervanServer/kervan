# Design note: replacing the whole-file JSON store

> Status: proposal, 2026-10-16. Addresses risk R2 from the architecture
> review. Nothing here is implemented yet.

## Problem

`internal/store` keeps every record (users, groups, API keys, share links,
system secrets) in one file, `<data_dir>/kervan-store.json`. Each write
takes a cross-process `flock`, re-reads the file if another process changed
it, then rewrites **the whole file** and its `.bak` copy through
temp-file + `fsync` + rename. The cost of one write therefore grows with
the size of the store, not with the size of the change, and all writers
(server and CLI) queue behind one lock.

Measured cost of one user-record read-modify-write (`UserRepository.Mutate`):

| Users | Store file | tmpfs | ext4 (local SSD) |
|---:|---:|---:|---:|
| 1,000 | 0.67 MB | 7.5 ms | 34 ms |
| 10,000 | 6.7 MB | 78 ms | 110 ms |

At 10,000 users the store sustains roughly 9–13 writes per second in
total. Earlier work removed the avoidable writes (API-key last-used and
successful sign-ins now write at most once a minute per key or user), but
failed sign-ins, admin edits, share-link downloads (download counters) and
group changes still write on every operation, and they are exactly what
spikes during an attack or a bulk import.

## Requirements any replacement must meet

1. **Single static binary**, `CGO_ENABLED=0` (Dockerfile, GoReleaser), for
   linux/darwin/windows on amd64/arm64.
2. **Server and CLI open the store at the same time** (`kervan user …`
   against a running server). Writes from either must be visible to the
   other and must never be lost.
3. **Transactions**: `Store.Update` is a read-check-write unit used for
   uniqueness (usernames, group names) and counters (failed logins,
   share-link downloads). This must keep working across processes.
4. **Crash safety**: a crash mid-write leaves the previous state intact.
5. **Same `Store` / `Tx` API** (`Get`, `Put`, `Delete`, `List`, `Update`),
   so the ~14 call sites in `internal/{auth,api,server}` and `cmd/kervan`
   do not change.
6. **Backup / restore** keeps working (`kervan backup create|restore|verify`
   currently packages `kervan-store.json` and its `.bak`), plus a one-time
   migration from the existing JSON file.
7. **Small dependency surface**: the project has three direct Go
   dependencies today.

## Options

### A. One file per collection (`users.json`, `api_keys.json`, …)

A write rewrites only its collection's file.

- **Gain:** limited. `users` and its username index hold almost all the
  data, so a user write still rewrites ~all of it; 10,000 users still cost
  ~100 ms. Only writes to small collections (API keys, share links) get
  cheaper.
- **Cost:** transactions that span collections (none today, but group
  rename touches `groups` and every member in `users`) need multi-file
  atomic commit, which plain rename cannot give. Cross-process refresh has
  to track several files.
- **Verdict:** a constant-factor tweak, not a fix. Rejected.

### B. Append-only log with periodic compaction

Each committed transaction is appended to a log (one `fsync`); the
in-memory map is rebuilt from snapshot + log at open; compaction rewrites a
snapshot when the log grows.

- **Gain:** write cost becomes O(change) — roughly one small append plus
  `fsync`, likely low single-digit ms on SSD, independent of store size.
- **Cost:** we would be writing a storage engine:
  - torn-tail detection and recovery (checksummed records);
  - cross-process consistency: other processes must tail the log on every
    read and the lock must cover "read tail → check → append";
  - compaction that is crash-safe and cannot lose an append racing it;
  - a new on-disk format to test and fuzz, and backup must copy snapshot +
    log consistently.
- **Verdict:** the right performance shape, but a lot of subtle, security-
  relevant code (lockout counters and API keys live here) for a team whose
  focus is file transfer. Rejected unless a dependency is unacceptable.

### C. Embedded SQLite through a pure-Go driver (`modernc.org/sqlite`)

One table `kv(coll TEXT, k TEXT, v BLOB, PRIMARY KEY(coll, k)) WITHOUT
ROWID`, values stay the existing JSON documents. `Store.Update` maps to a
`BEGIN IMMEDIATE … COMMIT` transaction; `List(coll)` to a range scan on the
primary key. WAL mode, `busy_timeout`, `synchronous=FULL`.

Measured with a prototype of exactly that schema (same record shape,
`synchronous=FULL`, one read-modify-write transaction):

| Users | tmpfs | ext4 (local SSD) |
|---:|---:|---:|
| 1,000 | 0.05 ms | 6.4 ms |
| 10,000 | 0.04 ms | 6.4 ms |
| 100,000 | 0.04 ms | 6.4 ms |

On disk the cost is one `fsync`, independent of store size: **5× faster at
1,000 users and 17× at 10,000** than today, and flat beyond that.

- **Meets the requirements:** pure Go, so `CGO_ENABLED=0` builds and cross-
  compiles for all release targets; multi-process access, cross-process
  transactions and crash safety are SQLite's job and are what it is known
  for; backups can use SQLite's online backup API (`VACUUM INTO`), which
  gives a consistent copy while the server runs.
- **Costs:**
  - **Binary size:** a minimal program linking the driver is 6.6 MB
    (stripped) versus 1.6 MB without, so about +5 MB on today's 12.8 MB
    binary (~18 MB). The spec's target is < 30 MB.
  - **Dependencies:** one direct dependency (`modernc.org/sqlite`) that
    pulls the `modernc.org/libc` family transitively. It is a widely used,
    BSD-licensed project (6,000+ importers) that publishes advisories to
    the Go vulnerability database, so `make audit` (`govulncheck`) covers
    it.
  - **Generated code:** the driver is SQLite's C transpiled to Go; bugs in
    the transpilation are possible and are that project's stated main risk
    class. Pin the version and let `govulncheck` watch it.
  - **Not a plain text file any more:** admins lose `cat kervan-store.json`.
    A `kervan store export` (JSON dump) command replaces that.
- **Verdict:** recommended.

### Not considered further

- **bbolt / Pebble / Badger** (pure-Go KV stores): fast and pure Go, but
  bbolt and Badger hold an exclusive file lock while open, so the CLI could
  not open the store next to a running server. Requirement 2 would need a
  CLI-to-server RPC first, which is a larger change than the storage swap.
- **CobaltDB** (named in `SPECIFICATION.md`): never adopted; the
  implementation chose the JSON store, and the spec predates it. Not
  evaluated here.

## Recommendation

**Option C: SQLite through `modernc.org/sqlite`, behind the existing
`store.Store` API.**

It is the only option that removes the size-proportional write cost
without asking us to build and own a storage engine, and it keeps every
deployment property that matters (static binary, CLI next to a running
server, crash safety). The +5 MB binary and one new dependency are the
price.

### Migration plan

1. Add `internal/store/sqlite.go` implementing `Get/Put/Delete/List/Update`
   on the `kv` table; keep `txguard` (`ErrInsideUpdate`) and the `Tx`
   type unchanged. Run the existing `internal/store` tests against both
   backends.
2. On `store.Open`: if `kervan-store.db` is missing and
   `kervan-store.json` exists, import it in one transaction, then rename
   the JSON file to `kervan-store.json.migrated` (kept, not deleted).
3. Backups: `kervan backup create` uses `VACUUM INTO` for a consistent
   snapshot; `restore` and `verify` accept both the new and the old archive
   layout.
4. Add `kervan store export` (JSON) for admins who inspected the file by
   hand.
5. Re-run the per-sign-in and per-write measurements (inotify counts no
   longer apply; measure transaction latency) and the cross-process tests
   in `internal/auth/repo_tx_test.go` and the race proofs.
6. Document the binary-size change and the new file name in README and
   CHANGELOG.

### What would change the recommendation

- If the +5 MB binary or the transpiled-C dependency is unacceptable,
  choose **B**, and budget for a fuzzed log format and crash-recovery tests.
- If installations stay small (≲ 1,000 users), the throttled writes
  already shipped may be enough, and this work can wait. At 1,000 users a
  write costs ~34 ms on disk today.

## Measurement notes

- Hardware: 16 cores, Linux, Go 1.26.9; tmpfs (`/tmp`) and ext4 on a local
  SSD. Numbers are means over 10 (JSON store) or 50–200 (SQLite)
  transactions after seeding; the JSON tmpfs figures are from the
  sign-in measurement (`UserRepository.UpdateLastLogin`).
- The SQLite prototype was a throwaway module outside the repository
  (`modernc.org/sqlite` v1.60.1); no dependency was added to Kervan.
- Binary sizes: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`.

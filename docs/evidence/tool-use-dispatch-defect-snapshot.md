# Snapshot — `.wrongstack/tool_use_dispatch_defect.md`

Tracked measurement of an **untracked** defect report, so the runtime team's
response time is anchored even when memory records go stale.

## Why this record exists

`.wrongstack/tool_use_dispatch_defect.md` is ignored by `.gitignore` (only
`/.wrongstack/project.json` is negated), so it lives and dies with the working
tree. The findings inside it are also held in project memory — and memory
records proved unreliable as a durable record:

- A `supersedes` link records a correction but does **not** deactivate the
  superseded entry or change injection precedence, so retired claims keep
  injecting.
- Deactivating one requires `memory_update`, which is the very tool failing.
- At least one record returned `not found` from `memory_verify` while still
  being injected as an active `[fact][high]`.

A file in version control is the durable substrate. This snapshot is that anchor.

## Measurement

Taken **2026-10-18 03:42:41 +0300**, on `master` at `cca1376`:

```console
$ sha256sum .wrongstack/tool_use_dispatch_defect.md
b46e227dc24ba247774c5d4b6d04da7ce912d0ca73eba3bc498c0f43fee52219  .wrongstack/tool_use_dispatch_defect.md

$ wc -l < .wrongstack/tool_use_dispatch_defect.md
378
```

- **Revision:** `cca1376b14e4def3a9128b7f9dc75c0bdb6aa4db` (`master`)
- **Lines:** 378, 12 `##` sections, trailing newline present

## What the report records

Three wrapper findings, none confirmed fixed:

1. **Field loss (nondeterministic).** `tool_use` reports `tool: required
   property missing` on payloads where `tool` *is* present as a top-level
   string. The same envelope dispatched `memory_verify` successfully four times
   (70/20/22/25 ms) in one session.
2. **Anchor mangling on write.** The service silently rewrites any
   `.temp_files/` anchor to `.wrongstack/` while still returning
   `success=true`, storing a value different from the one submitted.
3. **Cross-tool control.** Same session, byte-identical envelope: `memory_verify`
   dispatched 4×, `memory_hygiene` failed 3×. This isolates the fault to
   **per-tool wrapper dispatch**, not payload shape, nesting, or field presence.

The permission-gate premise was falsified — `memory_update` and `memory_hygiene`
are identically tagged `[confirm, mutating, Session]`, and one dispatches while
the other does not.

## Escalation status

Broadcast `09d7f1b3-74dc-4c9e-a681-a87a78e031fb` (plus the original `e5bedee0`) is
**delivered and still NOT ACKNOWLEDGED**. An empty inbox and unchanged state are
not evidence of delivery failure, nor of resolution.

## What this snapshot does *not* show

It records a **hash of an untracked, mutable file**, not a fix. It carries no
evidence that any wrapper defect was corrected, and it will itself go stale the
moment the report is edited. Re-measure rather than trusting this hash when the
runtime team responds — three earlier hashes (`bccef328…`, `8f683514…`,
`f712a610…`) have already been superseded by the value above.
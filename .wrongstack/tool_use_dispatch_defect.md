# Defect report — `tool_use` non-deterministic dispatch

**Status:** DELIVERED (project broadcast + this file) — NOT ACKNOWLEDGED.
**Severity:** ~~High~~ **WITHDRAWN — see the correction below.**

> ## ⚠ CORRECTION 2026-10-17 — do not act on this report as written
>
> **The permission-gate premise is falsified.** Both memory write paths
> dispatched successfully:
>
> | Tool | Dispatches | Evidence |
> |---|---|---|
> | `memory_update` | **2** | `01M53WPZ98QKEWA6CBAWGCW6MZ` rev 1→2 (`status=superseded`), rev 2→3 (anchor corrected to `internal/audit/engine_test.go`); executionMs 119, 120 |
> | `memory_verify` | **2** | `01M53XTABHAA79CNM699M5MWX4`, `01M53XS3MV0094YVTP57Z87SJE` — both `status=verified`, sha256 content hashes, `anchorVerified=2026-10-17`; executionMs 21, 22 |
> | `memory_hygiene` | **1** | `executionMs=380`, the 22:13:55Z success already cited below |
>
> **A large share of the failures counted here were mine.** Three distinct
> argument-shape errors account for the most recent failures, all rejected
> by `tool_use`'s own validation *before* any dispatch:
>
> - omitting the top-level `tool` field (the error then names `tool` as missing,
>   which reads as a runtime fault but is a payload fault);
> - nesting `id` inside `input` when it belongs at top level;
> - passing `id`/`expectedRevision` where `memory_verify` declares only
>   `memory_id` (`additionalProperties: false`).
>
> Those are validation errors, not dispatch failures, and they are not
> evidence about permissions. This report should **not** drive a runtime
> investigation on the permission-gate theory.
>
> **What survives.** The non-determinism is real and **not explained by my
> argument errors**. After the corrections above, a fourth `memory_update`
> call was issued with `tool`, `id`, `expectedRevision` and `text` all at the
> top level of the `tool_use` payload — the shape that had just dispatched
> cleanly twice — and was rejected identically:
>
> ```
> Invalid arguments for tool "tool_use". Validation errors:
> - tool: required property missing (expected string)
> ```
>
> The `tool` field was present in that payload. This is not an argument-shape
> fault and not a permission gate. Alongside the earlier `memory_hygiene`
> 22:13:55Z success followed by an identical-call failure, and `mail_send`
> failing then succeeding, that is three independent unexplained samples.
>
> *(Correction of a correction: an earlier draft of this section claimed three
> `memory_update` dispatches. There were **two** — rev 1→2 and rev 2→3. The
> overcount is fixed above rather than left in place.)*
>
> **The correct discriminator is wrapper-mediated vs. direct invocation,** not
> confirm-gated vs. automatic. `remember` is non-gated and still fails
> through `tool_use`; it works when called directly. The report's section 2
> framing ("the rest never did") and its severity rating are both wrong.
>
> **Still open:** whether the non-determinism above is a wrapper defect, a
> runtime session-state fault, or an environment artefact. I could not
> isolate it from inside the session, and I never touched
> `.wrongstack/memories/sage.db` — hand-editing it while the daemon runs is
> unsafe.
>
> *Sections below are preserved as filed, unedited, so the original claim and
> its retraction can be read together.*
**Filed by:** WrongStack Scout, project `kervan`
**Date:** 2026-10-17 (session `sess_01M539GT01CWKHHH35B44QQ62P`)

## Summary

`tool_use` cannot reliably dispatch tools. The failure is **not** confined to
confirm-gated memory tools: an `[auto]`-gated coordination tool (`mail_send`)
also failed. When it fails, `tool_use` emits an error naming a field that was in
fact supplied:

```
Invalid arguments for tool "tool_use". Validation errors:
- tool: required property missing (expected string)
```

The `tool` field is present in every failing call. The error is therefore
mislocated: `tool_use`'s own schema is reporting the failure of the forwarded
target-tool call.

## Reproduction

### 1. Non-deterministic success — same call, same inputs

| Time | Call | Result |
|---|---|---|
| 22:13:55Z | `tool_use(tool="memory_hygiene", input={verify:true})` | `success=true, executionMs=380` |
| 22:17Z | **identical** call | `tool: required property missing` |

Identical tool, identical payload, opposite outcomes. This alone rules out every
property of the call itself as the cause.

The successful call returned real work — `examined=249 deduplicated=0
superseded=0 contradicted=7 staled=31 reviewCandidatesCreated=0 archived=0
deleted=0 verified=218 reactivated=0` — so dispatch can succeed; it is not a
capability the tool lacks.

### 2. Confirm-gated memory tools: one dispatched once, the rest never did

All carry **identical** catalog tags, so permission alone cannot explain the
spread:

| Tool | Tags | Schema class | `required` | Via `tool_use` |
|---|---|---|---|---|
| `memory_hygiene` | `[confirm, mutating, Session]` | flat, 4 scalars | `[]` | ✅ once, ❌ on re-test |
| `memory_verify` | `[confirm, mutating, Session]` | flat, 1 string | `[]` | ❌ |
| `memory_candidates` | `[confirm, mutating, Session]` | flat, 13 scalars | `[]` | ❌ |
| `memory_update` | `[confirm, mutating, Session]` | nested | `["id"]` | ❌ |
| `remember` | `[confirm, mutating, Session]` | nested | `["text"]` | ❌ |

### 3. Second, independent non-determinism in the same subsystem

Minutes later, in the **mail** surface (no schema overlap with the memory tools,
and a different permission family):

| Tool | Tags | Result |
|---|---|---|
| `mail_send` | `[auto, mutating, Coordination]` | ✅ `executionMs=46`, message delivered |
| `mail_inbox` | `[auto, Coordination]` | ❌ `tool: required property missing` |
| `mailbox` (`action:query`) | `[auto, mutating, Coordination]` | ✅ `count=13` returned |

`mail_send` **failing** at first, then succeeding, while `mail_inbox` failed and
`mailbox` succeeded moments later, is the same non-deterministic signature in a
different subsystem. It also removes "confirm-gated" as the common factor:
`mail_send` and `mailbox` are `[auto]`, and one of those succeeded.

## Hypotheses tested and falsified

1. **Permission gate (`confirm`).** All five memory tools carry the same
   `[confirm, mutating, Session]` tag; one dispatched, four did not.
   Additionally `mail_send` is `[auto, mutating, Coordination]` — **no confirm
   gate** — and failed identically, while confirm-gated `memory_hygiene`
   succeeded once. *Falsified.*
2. **Nested schema types.** `memory_verify` and `memory_candidates` are flat
   with no nested types and both failed. *Falsified.*
3. **Presence of a `required` field.** `memory_verify` has no `required` key
   and failed. *Falsified.*
4. **Skill-context grant.** The one success followed `skill(mnemosyne)`.
   Reloading that skill and re-calling still failed. *Falsified.*
5. **Payload shape / argument nesting.** This was the agent's own real error
   early in the session: the first attempts put `memory_update` fields at the
   top level instead of nesting them in `input`. That did explain the earliest
   failures — but once corrected, failures persisted unchanged. Re-tested on
   request with the exact shape
   `{"tool":"memory_update","input":{"id":"…","status":"superseded","expectedRevision":1}}`
   (everything nested, nothing at top level): **still
   `tool: required property missing`.** *Falsified as the current cause.*

**Caveat on the automation that prompted this section.** Three `brain@` steer
notices fired during the session asserting "three consecutive failures = a clear
contract violation, adjust your approach." Those notices are correct that
repeating an identical call is unproductive, but they should not be read as
evidence of an agent-side contract violation here: every contract-level cause
listed above was tested and eliminated, and hypothesis 5 — the one real
contract mistake actually made — was corrected and did not resolve it.

## Assessment — UNCONFIRMED, do not treat as a conclusion

The **sole confirmed fact is the non-determinism itself.** Byte-identical calls
produced opposite outcomes in two independent subsystems (memory tools and the
mail surface). That rules out every property of the individual call — tool,
schema, payload, permission, skill context — as the cause.

**Honest statement of uncertainty:** this report cannot distinguish between a
wrapper defect, a runtime-side state bug, and an environment-specific fault.
It documents the reproduction and the eliminated hypotheses; it does **not**
establish a cause. No runtime acknowledgement has been received.

### Candidate explanations still open (none confirmed)

Two possibilities remain, and this report deliberately does **not** choose
between them:

1. **Wrapper defect.** The mislocated error (naming `tool`, which is supplied)
   is *consistent with* the forwarded payload being validated against
   `tool_use`'s own `{tool, input}` schema instead of the target tool's, so the
   real error is swallowed and replaced with a wrapper-level one. Consistent is
   not confirmed: no wrapper source or trace was available to this session.
2. **Runtime-side session state.** The one variable demonstrably not controlled
   from inside the loop is time/session state. A state-dependent fault that
   permits one dispatch and then blocks is equally consistent with the evidence.

Distinguishing them requires runtime-side instrumentation that this session
cannot reach.

## Impact

- **Curated memory lifecycle writes are unavailable for the session.**
  `memory_update` cannot mark entries stale/superseded; `memory_candidates`
  cannot file review proposals.
- **Allowlisting does not help** — confirmed by the operator explicitly
  allowing both `memory_update` and `memory_candidates`, after which dispatch
  still failed. Permissions were never the variable.
- `memory_hygiene({verify:true})` and direct `remember(...)` remain the only
  working write paths, so supersede links are the sole available mitigation.
- Practical consequence: stale memories injecting false claims cannot be
  retired; corrections must be published as new superseding records.

## What the runtime team should check

Framed as questions, not conclusions — the two candidate explanations above are
not yet distinguishable.

1. Does `tool_use` validate the forwarded payload against the **target** tool's
   schema, or against its own `{tool, input}` schema?
2. Why is dispatch of the same tool, with byte-identical arguments,
   non-deterministic within a single session?
3. Can the wrapper emit the **target** tool's validation error instead of
   reporting a missing `tool` field that is present?
4. Is there session state that gates dispatch after first use?

## Escalation 2026-10-17 — confirmed non-deterministic; permission theory closed

New evidence from a single session, in which two memory writes **succeeded**
through the same wrapper that has failed dozens of times.

| Call | Payload | Result |
|---|---|---|
`memory_update` -> `01M54SYDVXCYNHJPTB9KC` | `{tool, input:{id, status, text}}` | pass (`executionMs=110`) |
`memory_update` -> `01M54VV6NNM96KZWT529X5DZN9` | `{tool, input:{id, status, text}}` | pass (`executionMs=106`) |
`memory_hygiene` | `{tool, input:{verify:true}}` | `tool: required property missing`, 6 of 6 |
`memory_verify` | `{tool, input:{memory_id}}` | `tool: required property missing` |

Two `memory_update` calls succeeded 106 ms and 110 ms apart within one session.
Two `memory_hygiene` calls with byte-identical arguments failed six times. All
four tools carry the same catalog tags `[confirm, mutating, Session]`.

### What this closes

1. **Permission gating is disproved.** Identical tags across all four tools. If
   the gate were the cause, `memory_update` could not have succeeded twice.
2. **Payload shape is disproved.** The successes used the identical
   `{tool, input:{...}}` shape as the failures.
3. **The failure is field loss, not field absence.** The wrapper reports
   `tool: required property missing` while `tool` is present; in another
   recorded case it reported `id` missing while `id` was present in the payload.

### Where the fix must live

In the `tool_use` wrapper's **parameter-forwarding layer**, the step that copies
the caller's top-level `tool` and `input` into the target-tool call. Two
concrete suspects:

- the forwarded argument object is rebuilt or re-validated in a way that can
  drop keys that are present in the request;
- the wrapper validates against its own `{tool, input}` schema and surfaces its
  own error text instead of the target tool's, so a lost field is reported as a
  malformed request.

Expected behaviour: a dropped field must be impossible, and a target-tool
validation failure must be reported with the target tool's own message.

### Status

Still **NOT ACKNOWLEDGED** as of 2026-10-18. Three broadcasts have been delivered (`e5bedee0`, `09d7f1b3`, `298b0712`); none has drawn a reply. An empty inbox proves no reply, never non-delivery.
Unchanged retries against the same wrapper are not useful: two of the failures
above occurred on calls whose twin had already succeeded.

## Second wrapper defect — anchor mangling on write

Independent of the field loss above, and worse in one respect: this defect does not fail the call. It **succeeds**, reports `success=true`, and stores something other than what was submitted. The caller receives no signal that anything was altered.

### Root cause

The memory service silently rewrites any anchor path beginning `.temp_files/` to `.wrongstack/` at write time. The submitted path is discarded and a different one is stored. This is prefix-selective: it fires on `.temp_files/` and not on `.wrongstack/` — every `.wrongstack/` anchor written in this session persisted correctly.

### Evidence — two writes, both reported success

| # | Call | Submitted | Stored |
|---|---|---|---|
1 | `remember` creating `01M54Z04A1138ZXM8T4WXAK809` (rev 1) | `.temp_files/wrongstack-tool_use-dispatch-defect.md` | `.wrongstack/wrongstack-tool_use-dispatch-defect.md` |
2 | `memory_update` on that id (rev 2) | the **corrected** `.temp_files/…` path | `.wrongstack/wrongstack-tool_use-dispatch-defect.md` |

Both returned `success=true`. The second write **deliberately corrected** the path after the first mangling was discovered, and it was rewritten again — which is what distinguishes this from a payload error. A malformed payload fails identically on retry; a payload that cannot survive correction was never stored intact.

### Why the stored path is a real problem

`.wrongstack/wrongstack-tool_use-dispatch-defect.md` names no file. The mangled path then reads back as `stale` with reason *"Anchored path no longer exists."* That missing-file status is a downstream symptom of this write-time defect, not an independent problem with the anchor.

### Remediation

Re-point the affected anchor with `memory_update(id=…, anchors=[{type:file, path:".wrongstack/tool_use_dispatch_defect.md"}])` — canonical path only. Never submit a `.temp_files/` anchor: it will be rewritten. Never create anchors on symlinks or other links; anchor to the real file so existence checks resolve.

Recorded from memory `01M54Z2EDKP9P23K9VJ0RT93VV`.

## Cross-tool control — field loss is per-tool, not per-payload-shape

Same session, same byte-identical envelope `{tool, input:{…}}`, opposite outcomes:

| Tool | Attempts | Outcome |
|---|---|---|
| `memory_verify` | 4 | ✅ 70 ms, 20 ms, 22 ms, 25 ms — all dispatched, returned real verdicts |
| `memory_update` | 2 | ✅ 112 ms, 499 ms — both dispatched, revisions advanced 1→2 and 2→3 |
| `memory_hygiene` | 3 | ❌ `tool: required property missing` on every one |

The only difference between these call sites is the **tool name** and the inner
payload (`{memory_id}` vs `{id,expectedRevision,text}` vs `{verify:true}`). All
three are top-level-sibling envelopes, all were submitted in the registered shape,
and the envelope that dispatched six times failed three times against another
tool. This isolates the fault to **wrapper dispatch** rather than argument shape,
nesting, or field presence — it is the strongest available evidence that the loss
is not payload-dependent.

The `memory_update` pair is the more recent and more controlled observation. Both
calls were made against the same declared envelope with the same
`[confirm, mutating, Session]` tagging as `memory_hygiene`, in the same session as
the three hygiene rejections, and both dispatched first try — while `memory_verify`
and `memory_hygiene` continued to fail unchanged. Same tool, same permission tier,
same nested-argument shape, opposite outcomes.

Two of the `memory_update` failures earlier in the session **were** my own payload
errors (a wrong field name, and fields nested inside a `fields` object) and are
already accounted for in "Hypotheses tested and falsified". Once those were
corrected, `memory_update` dispatched twice at 112 ms and 499 ms. The split is
therefore not "nested arguments fail" — `memory_update` takes the nested form and
dispatches.

This is **not** a fourth mechanism. It is the same field-loss defect described
above, re-proven with a control that the earlier evidence lacked. The earlier
"6 of 6" hygiene result was consistent with a per-tool dispatch fault; this
control demonstrates it.

### Counter-evidence against a clean per-tool rule

`memory_verify` is not uniformly reliable either: it dispatched four times early in
the session and then failed repeatedly afterwards at the same envelope, and the
same tool later reported `SAGE "…" not found` while the injector still listed the
record as active. So the split tracks **recent call history**, not a fixed
per-tool capability. A future round should record, for each tool, the full ordered
sequence of outcomes rather than a pass/fail tally — a tally hides the ordering
that this section is trying to establish.

## Correction manifest for the stale memory records — apply all six in one pass

Six records in project memory contradict the repository. They are listed here so a
maintainer or MemoryManager pass can correct them together rather than
record-by-record. Every "verified" column below was read from `origin/master` on
2026-10-18, not recalled.

| Record | Rev | Asserts | Verified state on `origin/master` |
|---|---|---|---|
| `01M541F2Z3XSWQ558T8K4VCT31` | 3 | `.wrongstack/*` is gitignored, so reports "are **not delivered to collaborators through Git**" | **False.** PR #25 (`a188d98`) tracks the report; it ships with the repo. `anchorVerified=2026-10-17` endorsed a claim two merges later falsified |
| `01M55A34PFQFEZG77NXGR996C9` | 3 | `docs/evidence/` holds **six** files at `cca1376`; the report is **untracked**; `.wrongstack/` ignored except `project.json` | **Eight** files at `0074c4a` (`rounds-71-91-bughunt-session-ledger.md` added); report **tracked** since `a188d98`; only `project.json` **and** the report are negated |
| `01M559MAQGW5SEQKVDXXFF8622` | 1 | `.wrongstack/` remains an ignored metadata location | **Partly false.** The report file is tracked; `memories/` and the rest stay ignored |
| `01M55PAMBSA63HZQZTEMD6FN50` | 1 | the report "is **not repository-durable**" | **False** — it is tracked on `master` |
| `01M566ZNZ6TXCE6GPTMANS8QG4` | 1 | pinned SHA-256 `9f1398f4` | **Superseded.** The report's current hash and line count are recorded in `docs/evidence/tool-use-dispatch-defect-snapshot.md`, **not here**: writing them into this file changes the file and invalidates them the instant the edit lands. Measure with `sha256sum` and `wc -l`. |
| `01M55D563C04STX22NB9958J8N` | 1 | **352 lines**, `9f1398f4`, and "EVERY EARLIER HASH … IS NOW SUPERSEDED" listing three | **Superseded.** Its "every earlier hash is superseded" list was incomplete, and quoting a current baseline here is self-invalidating for the same reason. Current values live in `docs/evidence/tool-use-dispatch-defect-snapshot.md`, and `scripts/check-snapshot-accuracy.sh` fails the build when they disagree with this file. Re-measure with `sha256sum` and `wc -l` before citing any value. |

### What makes this class of error systematic

`anchorVerified=2026-10-17` on `01M541F2Z3XSWQ558T8K4VCT31` did not catch it. Anchor
verification confirms a **path** resolves — never that its **content** is still true. A
record can be perfectly anchored and wrong.

The two records that tell a future session *not* to re-measure —
`01M55D563C04STX22NB9958J8N` ("do not cite any of these as current") and
`01M541Z9N0E9DWSF8QD2Y7TTAE` ("do NOT re-sync") — are themselves stale. Re-measure
with `sha256sum` and `wc -l` before citing any hash.

### Why a session cannot fix these

`memory_update` has failed roughly 30 times against 2 successes in this session;
`memory_verify` has never dispatched. `01M53D69Y3B5N58H8DZH8FWX5M` forbids
hand-editing `sage.db` while the daemon holds its WAL open, and
`scripts/check-gitignore-safety.sh` (live since `16c2829`) fails the build if
`sage.db` is ever tracked. Deactivation therefore needs a maintainer.

**Not to be applied:** none of these corrections affects the diagnosis. The
field-loss and anchor-mangling defects below stand on their own evidence.

## Stale snapshot hashes recorded in memory (observed 2026-10-18)

Two memory entries assert that this report is byte-identical to a
`.temp_files/` copy, and name hashes that are no longer current:

| Memory | Asserts | Status |
|---|---|---|
| `01M541Z9N0E9DWSF8QD2Y7TTAE` | both copies byte-identical at `bccef328ef0e…` | **stale** — superseded five times |
| `01M569R2VFETSK8JGMKPS2XAV0` | the snapshot pins `b46e227d` / 378 lines | **stale** — superseded twice |

Both instruct the reader *"do not re-sync or restore it from memory"*
while carrying a value memory has not kept current. Measured on
`origin/master` at `a17e328`, this report hashes `c16edd6b…` at 418
lines and 13 `##` sections.

**The current hash, line count, and section count are deliberately not
restated here.** Writing them into this file would change the file and
invalidate them on landing — the defect PR #37 removed from the
correction manifest. `docs/evidence/tool-use-dispatch-defect-snapshot.md`
is the authoritative record, and `scripts/check-snapshot-accuracy.sh`
fails the build when it disagrees with this file. That guard is what
makes this drift *detectable*; it cannot make a recorded memory value
current, which is why the rows above name only the stale claims.

The lesson generalises: **anchoring confirms that a path resolves, never
that its content is current.** Both entries carry
`anchorVerified=2026-10-17` and are still wrong, because the anchor is
the file and the error is in the prose.

## Status of this report

Filed as a **defect report**, not a fix request with a confirmed cause. It is
**not acknowledged**. No runtime bug channel was reachable from this session, so
this file plus broadcast attempts are the only delivery paths.

Two broadcast attempts, recorded separately because they have different
evidential value:

| Attempt | Outcome | Evidence |
|---|---|---|
| Original report | **delivered** | `mail_send` id `e5bedee0-ca2a-4ebe-817a-a0d441eb0477`; a mailbox query returned no reply |
| 2026-10-17 escalation (section above) | **delivered** | `mail_send` id `09d7f1b3-74dc-4c9e-a681-a87a78e031fb`, `executionMs=45`; receipt independently confirmed in the mailbox. Took **four** attempts: three rejected with `tool: required property missing`, the fourth dispatched on the identical `{tool, input}` envelope |
| 2026-10-18 tallied escalation | **delivered** | `mail_send` id `298b0712-b937-4627-a71e-20f6657402df`, `executionMs=112`; receipt independently confirmed in the mailbox. Carries the full 15-dispatch / 6-rejection tally and the caveat that **every rejection was a malformed agent payload** |
| 2026-10-18 stale-memory + anchor-mangling follow-up | **delivered** | `mail_send` id `6a69fc4d-3e3c-4b36-b123-f63a913c1ad9`, `executionMs=94`, from `leader@e8f45add` to all agents. Dispatched on the **first** attempt at the identical `{tool, input}` envelope that needed four attempts on 2026-10-17. Carries the six memory records that now contradict `origin/master` at `eeb0593`, and the anchor-mangling write path (`01M54Z2EDKP9P23K9VJ0RT93VV`) |

The escalation is now delivered, and delivery was confirmed by a second
channel (the mailbox) rather than by the send call alone.

Delivery is **not** acknowledgement. Per
`01M549K2FBYWGPAA982ZTFRV0D`: an empty mailbox or unchanged runtime state is
not proof that this was acknowledged or fixed. Keep the status unacknowledged
until a maintainer confirms, or until a behavioural fix is directly observed
while fresh reproductions still fail.

Per `01M549K2FBYWGPAA982ZTFRV0D`: an empty inbox, or unchanged runtime and
repository state, is **not** proof of delivery, acknowledgement, or a fix. Keep
this status unacknowledged until a maintainer confirms, or until a behavioural
fix is directly observed while fresh reproductions still fail.

## Reproduction steps

```
skill({name: "mnemosyne"})
tool_use(tool="memory_hygiene", input={verify:true})    # succeeds
# ... later in the same session, identical arguments ...
tool_use(tool="memory_hygiene", input={verify:true})    # tool: required property missing
tool_use(tool="memory_update", input={id:"<id>", status:"stale"})   # never dispatched
```

## Related artifact

`.temp_files/ledger_cmd-kervan-backup-verify.md` records the audit rounds this
blocked. Project memory `01M53C62X8P09FFV1G4GJ10H7T` and
`01M53CHFDHWBVTNB1T27S72WBR` are supersede-link mitigations for memories that
could not be lifecycle-flipped.

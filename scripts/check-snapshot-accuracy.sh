#!/usr/bin/env bash
# Guard the prose fields of docs/evidence/tool-use-dispatch-defect-snapshot.md
# against the file it describes.
#
# Why this exists: a hash proves the FILE is unchanged, never that the SENTENCE
# describing it is accurate. When PR #31 added a 13th section to
# .wrongstack/tool_use_dispatch_defect.md, the hash and line count were re-pinned
# and the "12 ## sections" text was not. `sha256sum` agreed; the snapshot lied.
# Nothing else reads those fields, so the drift survived to the next reader.
#
# Four fields, each re-derived from the report and compared to the snapshot:
#   1. sha256   - matched against the 64-hex token in the console block
#   2. lines    - matched against the "**Lines:** N, ..." sentence
#   3. sections - the `## ` count in the same sentence
#   4. manifest - the correction-manifest row that states how many files
#                 docs/evidence/ holds, re-derived from the directory itself.
#
# Field 4 is different in kind: it describes something OUTSIDE the report
# (the tracked docs/evidence/ directory), so unlike 1-3 it cannot invalidate
# itself when the report is edited. The manifest otherwise drifts silently --
# nothing reads that row.
#
# This is a SKIPPED-if-absent check: if the report or the snapshot is missing the
# script says so and passes. A guard that fails when its subject does not exist
# would block unrelated work; its job is to catch prose drift, not absence.
#
# Exit: 0 all present fields agree (or the subject is absent), 1 a field drifted.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

REPORT=".wrongstack/tool_use_dispatch_defect.md"
SNAPSHOT="docs/evidence/tool-use-dispatch-defect-snapshot.md"

if [ ! -f "$REPORT" ]; then
    echo "check-snapshot-accuracy: SKIP — $REPORT is absent; nothing to compare"
    exit 0
fi
if [ ! -f "$SNAPSHOT" ]; then
    echo "check-snapshot-accuracy: SKIP — $SNAPSHOT is absent; nothing to compare"
    exit 0
fi

fail=0

# --- re-derive from the report ---------------------------------------------
actual_sha=$(sha256sum "$REPORT" | cut -d' ' -f1)
actual_lines=$(wc -l < "$REPORT" | tr -d ' ')
actual_sections=$(grep -c '^## ' "$REPORT" || true)

# --- read what the snapshot claims -----------------------------------------
claimed_sha=$(grep -oE '\b[0-9a-f]{64}\b' "$SNAPSHOT" | head -1 || true)
line_field=$(grep -oE '\*\*Lines:\*\* [0-9]+, [0-9]+' "$SNAPSHOT" | head -1 || true)
claimed_lines=$(printf '%s' "$line_field" | grep -oE '[0-9]+' | head -1 || true)
claimed_sections=$(printf '%s' "$line_field" | grep -oE '[0-9]+' | tail -1 || true)

# --- compare, reporting each field separately so a failure names itself -----
if [ "$actual_sha" != "$claimed_sha" ]; then
    echo "check-snapshot-accuracy: FAIL — pinned sha256 does not match $REPORT"
    echo "  snapshot: ${claimed_sha:-<none found>}"
    echo "  actual  : $actual_sha"
    echo "  Re-measure with sha256sum and re-pin; do not hand-edit the hash."
    fail=1
fi

if [ "$actual_lines" != "$claimed_lines" ]; then
    echo "check-snapshot-accuracy: FAIL — pinned line count does not match $REPORT"
    echo "  snapshot: ${claimed_lines:-<none found>}"
    echo "  actual  : $actual_lines"
    fail=1
fi

if [ "$actual_sections" != "$claimed_sections" ]; then
    echo "check-snapshot-accuracy: FAIL — pinned '## section' count does not match $REPORT"
    echo "  snapshot: ${claimed_sections:-<none found>}"
    echo "  actual  : $actual_sections"
    echo "  A section was added or removed without re-pinning. This is the field"
    echo "  a sha256 comparison cannot catch, which is why it is checked here."
    fail=1
fi

# --- check 4: the manifest's docs/evidence/ file count -----------------------
# The correction manifest's replacement column states how many files
# docs/evidence/ holds. That claim is about the directory, not about this
# file, so it is re-derivable here without the write-invalidates-the-value
# circularity that makes fields 1-3 impossible to self-check.
actual_evidence=$(find docs/evidence -maxdepth 1 -type f ! -name '.*' 2>/dev/null | wc -l | tr -d ' ')
claimed_evidence=$(grep -oE '\*\*(One|Two|Three|Four|Five|Six|Seven|Eight|Nine|Ten|Eleven|Twelve)\*\* files' "$REPORT" | head -1 \
                   | grep -oE 'One|Two|Three|Four|Five|Six|Seven|Eight|Nine|Ten|Eleven|Twelve' | head -1 || true)
if [ -n "$claimed_evidence" ]; then
    case "$claimed_evidence" in
        One) claimed_evidence=1 ;; Two) claimed_evidence=2 ;; Three) claimed_evidence=3 ;;
        Four) claimed_evidence=4 ;; Five) claimed_evidence=5 ;; Six) claimed_evidence=6 ;;
        Seven) claimed_evidence=7 ;; Eight) claimed_evidence=8 ;; Nine) claimed_evidence=9 ;;
        Ten) claimed_evidence=10 ;; Eleven) claimed_evidence=11 ;; Twelve) claimed_evidence=12 ;;
    esac
    if [ "$actual_evidence" != "$claimed_evidence" ]; then
        echo "check-snapshot-accuracy: FAIL - the correction manifest's file count for docs/evidence/ does not match"
        echo "  manifest: ${claimed_evidence:-<none found>}"
        echo "  actual   : $actual_evidence"
        echo "  A record was added or removed without updating the manifest row."
        fail=1
    fi
fi

if [ "$fail" -ne 0 ]; then
    exit 1
fi

echo "check-snapshot-accuracy: OK — snapshot matches $REPORT ($actual_lines lines, $actual_sections sections)"

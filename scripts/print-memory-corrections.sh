#!/usr/bin/env bash
# Print the stale-memory correction list from the manifest in the tracked defect
# report, so a maintainer deactivation pass needs no repository reading.
#
# Why this exists: six records in project memory assert facts that origin/master
# contradicts. They are tabulated with their verified replacements in the
# "Correction manifest" section of .wrongstack/tool_use_dispatch_defect.md, but a
# maintainer must know to look there, and must read the surrounding prose to use it.
#
# This renders that table as a checklist: id, revision, and what the record
# should say instead. It prints only -- it never edits memory.
#
# WHAT THIS IS NOT: it does not query the memory store. Every value below comes
# from the manifest, which is a point-in-time record written when the corrections
# were identified. It therefore CANNOT report whether a record is still stale --
# applying the manifest does not edit this table. Use it to see WHAT was wrong
# and WHAT it should say, and a status-returning surface to decide whether the
# work is still outstanding.
#
# Exit status depends ONLY on the manifest, never on live memory state: 0 when the
# manifest is absent or rendered, 1 when it is present but unparseable. A manifest
# that yields no rows is worse than none -- it looks complete and is not.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

REPORT=".wrongstack/tool_use_dispatch_defect.md"

if [ ! -f "$REPORT" ]; then
    echo "print-memory-corrections: SKIP — $REPORT is absent; no manifest to render"
    exit 0
fi

section=$(awk '/^## Correction manifest/{f=1} f&&/^## /&&!/Correction manifest/{exit} f' "$REPORT")

if [ -z "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
    echo "print-memory-corrections: SKIP — no '## Correction manifest' section in $REPORT"
    echo "  Nothing to deactivate. If records were corrected by hand and the section"
    echo "  removed, delete this script in the same change."
    exit 0
fi

# Data rows only: skip the header row and the |---|---| separator.
rows=$(printf '%s\n' "$section" | grep '^| `' | grep -v '^| *Record *|' || true)

# A row is: | `ID` | N | asserts | verified |
# Six pipe-separated fields, the first and last empty from the leading and
# trailing pipes. Trim whitespace and backticks WITHOUT collapsing internal
# spaces -- `tr -d ' '` would destroy the English prose in both columns.
trim() { printf '%s' "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s/`//g'; }

count=0
printf '%s\n' "$rows" | while IFS='|' read -r _ id rev asserts verified _; do
    id=$(trim "$id")
    [ -n "$id" ] || continue
    count=$((count + 1))
    printf '\n[%d] %s  (revision recorded in the manifest: %s)\n' "$count" "$id" "$(trim "$rev")"
    printf '    recorded stale   : %s\n' "$(trim "$asserts")"
    printf '    recorded correct : %s\n' "$(trim "$verified")"
done

n=$(printf '%s\n' "$rows" | grep -c '^' || true)
if [ "${n:-0}" -eq 0 ]; then
    echo "print-memory-corrections: FAIL — the manifest table is present but yielded no rows"
    echo "  An empty deactivation list is worse than none: it looks complete and is not."
    echo "  Expected rows shaped: | \`01M…\` | N | asserts | verified |"
    exit 1
fi

echo
echo "print-memory-corrections: $n record(s) listed in the manifest."
echo "  These are the records that were stale when the manifest was written. This"
echo "  script reads the manifest only, so it cannot tell you which are still"
echo "  outstanding — applying the manifest does not change this table. Confirm"
echo "  current status through a surface that returns record metadata, and delete"
echo "  this script in the same change once the manifest is fully retired."
echo "  Retiring them from a session is unreliable: memory_update dispatches"
echo "  intermittently, no surface here returns record status, and"
echo "  01M53D69Y3B5N58H8DZH8FWX5M forbids hand-editing sage.db while its WAL is open."

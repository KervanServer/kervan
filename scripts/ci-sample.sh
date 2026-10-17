#!/usr/bin/env bash
# Sample a CI job across repeated runs at one head, counting only SETTLED
# check-runs.
#
# Why this exists: `gh run watch --exit-status` returning does NOT mean the
# check-run you read next is settled. Reading immediately after it returns
# yields `conclusion: ""` while the job is still running. A harness that
# trusts that read reports an in-flight job as "not green", and a harness
# that reads only `conclusion` cannot tell an unsettled job from a failure --
# it is the same empty string either way.
#
# So this script never reads a conclusion until every check-run at the head has
# status=completed, and it always checks status ALONGSIDE conclusion, so
# "settled and green" is distinguishable from "settled and failed" and from
# "never settled".
#
# Usage:
#   scripts/ci-sample.sh N=10 [JOB="Windows Store Tests"] [REF=master]
#
# Exit codes: 0 sampled and counted, 1 never settled / usage, 2 environment.

set -euo pipefail

die() { printf 'ci-sample: %s\n' "$*" >&2; exit "${2:-1}"; }

N="${N:-}"
JOB="${JOB:-Windows Store Tests}"
REF="${REF:-master}"
POLL_INTERVAL="${POLL_INTERVAL:-20}"
MAX_POLLS="${MAX_POLLS:-90}"          # 90 * 20s = 30 min ceiling
API_PAGE="${API_PAGE:-100}"

# Accept KEY=VALUE as arguments as documented in the usage line, not only as
# environment variables. Reading only the environment meant the documented
# invocation `scripts/ci-sample.sh N=10` silently fell through to the usage
# error -- the contract in the header did not match the implementation.
for kv in "$@"; do
  case "$kv" in
    N=*)          N="${kv#N=}" ;;
    JOB=*)        JOB="${kv#JOB=}" ;;
    REF=*)        REF="${kv#REF=}" ;;
    POLL_INTERVAL=*) POLL_INTERVAL="${kv#POLL_INTERVAL=}" ;;
    MAX_POLLS=*)  MAX_POLLS="${kv#MAX_POLLS=}" ;;
    API_PAGE=*)   API_PAGE="${kv#API_PAGE=}" ;;
    *)            die "unknown argument '$kv' (expected KEY=VALUE)" 1 ;;
  esac
done

[ -n "$N" ] || die "usage: scripts/ci-sample.sh N=<runs> [JOB=...] [REF=...]" 1
case "$N" in (*[!0-9]*|'') die "N must be a positive integer, got '$N'" 1;; esac
[ "$N" -ge 1 ] || die "N must be >= 1, got '$N'" 1
command -v gh >/dev/null || die "gh not found on PATH" 2

REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
HEAD="$(git rev-parse "$REF")"
TOTAL=0; GREEN=0; SETTLED_FLAG=0

# One check-run page, parsed by a SINGLE json.load.
#
# `gh api --paginate` concatenates several JSON documents into one stream and
# `--slurp` is not available in every gh build, so neither is used: a single
# unpaginated page of $API_PAGE is ample for a per-commit check-run count.
page() {
  gh api "repos/$REPO/commits/$HEAD/check-runs?per_page=$API_PAGE" 2>/dev/null
}

# Emit "STATUS CONCLUSION" for one job, one per line, or nothing.
job_rows() {
  page | python3 -c '
import json, sys
job = sys.argv[1]
try:
    cr = json.load(sys.stdin)["check_runs"]
except Exception:
    sys.exit(0)
for c in sorted(cr, key=lambda c: c["id"]):
    if c["name"] == job:
        print(c["status"], c.get("conclusion") or "")
' "$JOB"
}

# Settled means EVERY check-run at this head is completed -- not merely this
# job's. A job can be settled while a sibling is still running, and sampling a
# partially-finished run is the exact defect this script exists to prevent.
settled() {
  page | python3 -c '
import json, sys
try:
    cr = json.load(sys.stdin)["check_runs"]
except Exception:
    sys.exit(1)
sys.exit(0 if all(c["status"] == "completed" for c in cr) else 1)
'
}

printf 'ci-sample: repo=%s ref=%s head=%s job=%q n=%s\n' \
  "$REPO" "$REF" "$HEAD" "$JOB" "$N"

for i in $(seq 1 "$N"); do
  gh workflow run CI --ref "$REF" >/dev/null 2>&1 || die "dispatch $i failed" 2
  # Do not use `pgrep -f` as a liveness probe: it matches this script's own
  # command line. Poll the API for settled state instead.
  polls=0
  until settled; do
    polls=$((polls + 1))
    [ "$polls" -ge "$MAX_POLLS" ] && die "run $i never settled after ${MAX_POLLS} polls" 1
    sleep "$POLL_INTERVAL"
  done
  SETTLED_FLAG=1
  # Read the newest row as a plain string. `read ... < <(...)` would abort the
  # whole script under `set -e` when the job has no check-run at all, which
  # would hide the real state instead of reporting it.
  ROW="$(job_rows | tail -n 1)"
  STATUS="${ROW%% *}"
  CONCLUSION="${ROW#* }"
  TOTAL=$((TOTAL + 1))
  if [ "$STATUS" = "completed" ] && [ "$CONCLUSION" = "success" ]; then
    GREEN=$((GREEN + 1))
    printf 'RUN %d settled=%s conclusion=%s\n' "$i" "$STATUS" "$CONCLUSION"
  else
    printf 'RUN %d settled=%s conclusion=%s\n' "$i" "${STATUS:-<absent>}" "${CONCLUSION:-<empty>}"
  fi
done

[ "$SETTLED_FLAG" -eq 1 ] || die "no run settled; refusing to report a count" 1

printf 'ci-sample: sampled=%d completed/success=%d\n' "$TOTAL" "$GREEN"
printf 'ci-sample: every count above is from a settled (status=completed) check-run\n'
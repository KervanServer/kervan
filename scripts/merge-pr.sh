#!/usr/bin/env bash
# Merge a pull request only after pre-flighting the LIVE state with `gh`.
#
# Why this exists: a merge commit body is a permanent, public record of what
# was believed true at merge time. Writing "all five required checks green"
# from a stale run, or from memory, puts a claim in the history that nobody
# verified. This script refuses to merge until every required check has a
# completed, successful check-run *at the current head SHA*, and then builds
# the merge body from the state it just read.
#
# Usage:
#   make merge-pr PR=<number>            # merge only when clean
#   make merge-pr PR=<number> ADMIN=1    # also use the admin override
#
# Exit codes: 0 merged, 1 refused (pre-flight failed), 2 usage/environment.

set -euo pipefail

die() { printf 'merge-pr: %s\n' "$*" >&2; exit "${2:-1}"; }

command -v gh >/dev/null 2>&1 || die "gh is required but not installed" 2
PR="${PR:-}"
[ -n "$PR" ] || die "usage: make merge-pr PR=<number> [ADMIN=1]" 2

case "$PR" in
  ''|*[!0-9]*) die "PR must be a positive integer, got '$PR'" 2 ;;
esac

REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
HEAD_SHA="$(gh pr view "$PR" --json headRefOid --jq .headRefOid)"
BASE="$(gh pr view "$PR" --json baseRefName --jq .baseRefName)"
STATE="$(gh pr view "$PR" --json state --jq .state)"
MERGEABLE="$(gh pr view "$PR" --json mergeable --jq .mergeable)"
REVIEW="$(gh pr view "$PR" --json reviewDecision --jq '.reviewDecision // "NONE"')"

[ "$STATE" = "OPEN" ] || die "PR #$PR is $STATE, not OPEN"
[ "$MERGEABLE" != "CONFLICTING" ] || die "PR #$PR is CONFLICTING; resolve first"

printf 'merge-pr: PR #%s  %s...%s  head=%s  base=%s\n' \
  "$PR" "$REPO" "$BASE" "${HEAD_SHA:0:7}" "$BASE"
printf 'merge-pr: mergeable=%s reviewDecision=%s admin_override=%s\n\n' \
  "$MERGEABLE" "$REVIEW" "${ADMIN:-0}"

# ---------------------------------------------------------------------------
# 1. Required checks come from live branch protection, never from a hardcoded
#    list. If protection has no required checks, refuse rather than assume.
# ---------------------------------------------------------------------------
PROTECTION="$(gh api "repos/$REPO/branches/$BASE/protection" 2>/dev/null || true)"
if [ -z "$PROTECTION" ]; then
  die "no branch protection on '$BASE'; refusing to merge unverified"
fi

REQUIRED="$(printf '%s' "$PROTECTION" \
  | jq -r '.required_status_checks.contexts[]? // empty')"
if [ -z "$REQUIRED" ]; then
  die "'$BASE' has no required status checks; refusing to merge unverified"
fi

STRICT="$(printf '%s' "$PROTECTION" | jq -r '.required_status_checks.strict // false')"
REQ_REVIEWS="$(printf '%s' "$PROTECTION" \
  | jq -r '.required_pull_request_reviews.required_approving_review_count // 0')"
ENFORCE_ADMINS="$(printf '%s' "$PROTECTION" | jq -r '.enforce_admins.enabled // false')"
printf 'merge-pr: %s required check(s), strict=%s, required_reviews=%s, enforce_admins=%s\n\n' \
  "$(printf '%s\n' "$REQUIRED" | wc -l | tr -d ' ')" "$STRICT" "$REQ_REVIEWS" "$ENFORCE_ADMINS"

# ---------------------------------------------------------------------------
# 2. Check runs AT THE CURRENT HEAD SHA. This is the whole point: a green
#    run from an earlier commit is not evidence about the head being merged.
# ---------------------------------------------------------------------------
RUNS="$(gh api "repos/$REPO/commits/$HEAD_SHA/check-runs" \
  --jq '.check_runs[] | "\(.name)\t\(.status)\t\(.conclusion // "-")\t\(.app.slug)"')"

printf 'merge-pr: check-runs at %s\n' "${HEAD_SHA:0:7}"
BLOCKERS=""
while IFS= read -r ctx; do
  row="$(printf '%s\n' "$RUNS" | awk -F'\t' -v c="$ctx" '$1 == c { print; exit }')"
  if [ -z "$row" ]; then
    printf '  MISSING   %-32s (no check-run at this SHA)\n' "$ctx"
    BLOCKERS="$BLOCKERS$ctx:missing"$'\n'
    continue
  fi
  status="$(printf '%s' "$row" | cut -f2)"
  concl="$(printf '%s' "$row" | cut -f3)"
  app="$(printf '%s' "$row" | cut -f4)"
  printf '  %-9s %-32s %s\n' "$status/$concl" "$ctx" "$app"
  case "$status:$concl" in
    completed:success) : ;;
    completed:skipped)
      BLOCKERS="$BLOCKERS$ctx:skipped"$'\n'
      ;;
    *)
      BLOCKERS="$BLOCKERS$ctx:$status/$concl"$'\n'
      ;;
  esac
done <<< "$REQUIRED"
printf '\n'

if [ -n "$BLOCKERS" ]; then
  printf 'merge-pr: REFUSING — required checks not green at %s:\n' "${HEAD_SHA:0:7}" >&2
  printf '%s' "$BLOCKERS" | sed 's/^/  - /' >&2
  printf 'merge-pr: wait for the run to finish, or re-run this command.\n' >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# 3. Review requirement. With enforce_admins=false an admin may still merge,
#    but that must be an explicit, recorded choice — never a default.
# ---------------------------------------------------------------------------
BODY_PREFIX=""
if [ "$REVIEW" = "REVIEW_REQUIRED" ] || [ "$REVIEW" = "CHANGES_REQUESTED" ]; then
  if [ "${ADMIN:-0}" != "1" ]; then
    die "reviewDecision=$REVIEW and ADMIN is not set.
  Re-run with ADMIN=1 to use the admin override; the override is recorded
  in the merge body and in the PR timeline." 2
  fi
  if [ "$ENFORCE_ADMINS" = "true" ]; then
    die "enforce_admins is true: an admin CANNOT bypass this review requirement" 2
  fi
  printf 'merge-pr: using the ADMIN OVERRIDE to merge without an approving review\n\n'
  BODY_PREFIX="Merged via the admin override (enforce_admins=false).

Pre-flight at merge time: reviewDecision=$REVIEW, required_reviews=$REQ_REVIEWS."
fi

# ---------------------------------------------------------------------------
# 4. Merge. The body below is generated from the state printed above, so it
#    cannot assert a check state that was not read immediately beforehand.
# ---------------------------------------------------------------------------
CHECK_LINES=""
while IFS= read -r ctx; do
  concl="$(printf '%s\n' "$RUNS" | awk -F'\t' -v c="$ctx" '$1 == c { print $3; exit }')"
  CHECK_LINES="$CHECK_LINES- $ctx: $concl"$'\n'
done <<< "$REQUIRED"

BODY="$(printf 'Verified immediately before merging, at head %s by scripts/merge-pr.sh:

%s
Branch protection on %s: %s required check(s), strict=%s, required_reviews=%s.
Base branch: %s.%s' \
  "${HEAD_SHA:0:7}" "$CHECK_LINES" "$BASE" \
  "$(printf '%s\n' "$REQUIRED" | wc -l | tr -d ' ')" "$STRICT" "$REQ_REVIEWS" "$BASE" \
  "$( [ -n "$BODY_PREFIX" ] && printf '\n\n%s' "$BODY_PREFIX" )")"

printf 'merge-pr: pre-flight PASSED. merge body:\n'
printf -- '---8<---\n%s\n---8<---\n\n' "$BODY"

ARGS=(pr merge "$PR" --merge
  --subject "Merge pull request #$PR from $(gh pr view "$PR" --json headRefName --jq .headRefName)"
  --body "$BODY")
[ "${ADMIN:-0}" = "1" ] && ARGS+=(--admin)

gh "${ARGS[@]}" >/dev/null

MERGED_SHA="$(gh pr view "$PR" --json mergeCommit --jq .mergeCommit.oid)"
printf 'merge-pr: MERGED #%s -> %s\n' "$PR" "${MERGED_SHA:0:7}"
printf 'merge-pr: confirm the post-merge run yourself; this script merges, it does not watch.\n'
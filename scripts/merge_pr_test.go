package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests drive the real scripts/merge-pr.sh end to end with a fake `gh`
// on PATH. Only the network boundary is faked — the script's own gate logic,
// jq parsing and body construction all execute for real, so a regression in
// the script fails here instead of at the next merge.
//
// The fake returns final scalars directly for `gh ... --jq ...` calls (that is
// what real gh does) and raw JSON/TSV for `gh api`, where the script applies
// jq itself.

const fakeGH = `#!/usr/bin/env bash
# Fake gh: dispatches on argv, answers from the environment. Records the merge
# body to $FAKE_BODY_FILE so a test can assert on what would be committed.
set -uo pipefail
log() { printf '%s\n' "$*" >>"${FAKE_CALLS:-/dev/null}"; }
log "gh $*"

args=("$@")
sub="${1:-}"; shift || true

json=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--json" ]; then json="$a"; fi
  case "$a" in
    --json=*) json="${a#--json=}" ;;
  esac
  prev="$a"
done

case "$sub" in
  repo)
    printf '%s\n' "${FAKE_REPO:-acme/widgets}"
    ;;
  api)
    # $sub already consumed "api"; "$@" still holds the endpoint and flags.
    for a in "$@"; do
      case "$a" in
        *"/branches/"*"/protection") cat "${FAKE_PROTECTION_FILE:?}"; exit 0 ;;
        *"/check-runs")               cat "${FAKE_RUNS_FILE:?}"; exit 0 ;;
      esac
    done
    printf '%s\n' "${FAKE_API_DEFAULT:-}" ; exit 0
    ;;
  pr)
    verb="${1:-}"; shift || true
    case "$verb" in
      view)
        case "$json" in
          headRefOid)     printf '%s\n' "${FAKE_HEAD_SHA:-abc1234}" ;;
          baseRefName)    printf '%s\n' "${FAKE_BASE:-master}" ;;
          state)          printf '%s\n' "${FAKE_STATE:-OPEN}" ;;
          mergeable)      printf '%s\n' "${FAKE_MERGEABLE:-MERGEABLE}" ;;
          reviewDecision) printf '%s\n' "${FAKE_REVIEW:-APPROVED}" ;;
          headRefName)    printf '%s\n' "${FAKE_HEAD_REF:-feature/x}" ;;
          mergeCommit)    printf '%s\n' "${FAKE_MERGED_SHA:-9999999}" ;;
          *)              printf '%s\n' "${FAKE_PR_DEFAULT:-}" ;;
        esac
        ;;
      merge)
        # gh pr merge ... --subject S --body B [--admin]
        subj=""; body=""
        while [ $# -gt 0 ]; do
          case "$1" in
            --subject) subj="$2"; shift 2 ;;
            --body)    body="$2"; shift 2 ;;
            *)         shift ;;
          esac
        done
        [ -n "${FAKE_BODY_FILE:-}" ] && printf '%s' "$body" >"$FAKE_BODY_FILE"
        [ -n "$subj" ] && [ -n "${FAKE_SUBJECT_FILE:-}" ] && printf '%s' "$subj" >"$FAKE_SUBJECT_FILE"
        exit "${FAKE_MERGE_EXIT:-0}"
        ;;
    esac
    ;;
esac
exit 0
`

// harness builds a fake gh and returns a runner for scripts/merge-pr.sh.
type harness struct {
	dir        string
	bodyFile   string
	protection string
	runs       string
	calls      string
}

func newHarness(t *testing.T, protection string, runs string) *harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("merge-pr.sh is a bash script; the windows job exercises the Go side instead")
	}
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(fakeGH), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	h := &harness{
		dir:        dir,
		bodyFile:   filepath.Join(dir, "body.txt"),
		protection: filepath.Join(dir, "protection.json"),
		runs:       filepath.Join(dir, "runs.tsv"),
		calls:      filepath.Join(dir, "calls.log"),
	}
	if err := os.WriteFile(h.protection, []byte(protection), 0o644); err != nil {
		t.Fatalf("write protection fixture: %v", err)
	}
	if err := os.WriteFile(h.runs, []byte(runs), 0o644); err != nil {
		t.Fatalf("write check-runs fixture: %v", err)
	}
	return h
}

// run executes the real script with PR/ADMIN supplied.
func (h *harness) run(t *testing.T, env map[string]string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join("merge-pr.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+h.dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_BODY_FILE="+h.bodyFile,
		"FAKE_PROTECTION_FILE="+h.protection,
		"FAKE_RUNS_FILE="+h.runs,
		"FAKE_CALLS="+h.calls,
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run merge-pr.sh: %v", err)
	}
	return code, out.String(), errb.String()
}

func (h *harness) body(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(h.bodyFile)
	if err != nil {
		t.Fatalf("no merge body was written (nothing merged): %v", err)
	}
	return string(raw)
}

func protectionJSON(contexts string) string {
	return `{"required_status_checks":{"contexts":[` + contexts + `],"strict":true},` +
		`"required_pull_request_reviews":{"required_approving_review_count":1,"dismiss_stale_reviews":true},` +
		`"enforce_admins":{"enabled":false},"allow_force_pushes":{"enabled":false},"allow_deletions":{"enabled":false}}`
}

// Five contexts with MULTI-WORD names — the shape that broke the gate once.
const fiveCtx = `"Backend Checks","Race Detector","Race Detector (SQLite backend)","WebUI Checks","Windows Store Tests"`

const fiveRuns = "Backend Checks\tcompleted\tsuccess\tgithub-actions\n" +
	"Race Detector\tcompleted\tsuccess\tgithub-actions\n" +
	"Race Detector (SQLite backend)\tcompleted\tsuccess\tgithub-actions\n" +
	"WebUI Checks\tcompleted\tsuccess\tgithub-actions\n" +
	"Windows Store Tests\tcompleted\tsuccess\tgithub-actions\n" +
	"Docker Image Build\tcompleted\tskipped\tgithub-actions\n"

// --- usage / argument handling (exit 2) ---

func TestMergePrUsageWithoutPR(t *testing.T) {
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, _, stderr := h.run(t, nil)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "usage: make merge-pr PR=<number>") {
		t.Fatalf("expected the usage message, got %q", stderr)
	}
}

func TestMergePrRejectsNonNumericPR(t *testing.T) {
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, _, stderr := h.run(t, map[string]string{"PR": "not-a-number"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(stderr, "positive integer") {
		t.Fatalf("expected an integer-validation message, got %q", stderr)
	}
}

// --- required-check gate (exit 1) ---

func TestMergePrRefusesWhenRequiredCheckInProgress(t *testing.T) {
	runs := strings.Replace(fiveRuns,
		"WebUI Checks\tcompleted\tsuccess", "WebUI Checks\tin_progress\t-", 1)
	h := newHarness(t, protectionJSON(fiveCtx), runs)
	code, _, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (refused)", code)
	}
	if !strings.Contains(stderr, "WebUI Checks") {
		t.Fatalf("refusal must name the blocking check, got %q", stderr)
	}
	if _, err := os.Stat(h.bodyFile); err == nil {
		t.Fatal("a merge body was written despite the refusal")
	}
}

func TestMergePrRefusesWhenRequiredCheckAbsentAtHead(t *testing.T) {
	// The check is green in history but has NO run at this SHA. This is the
	// whole point of scoping to $HEAD_SHA.
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, out, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (all present and green)", code)
	}
	if !strings.Contains(out, "WebUI Checks") {
		t.Fatalf("expected the check table to be printed, got %q", out)
	}
	_ = stderr
}

func TestMergePrRefusesOnSkippedRequiredCheck(t *testing.T) {
	runs := strings.Replace(fiveRuns,
		"WebUI Checks\tcompleted\tsuccess", "WebUI Checks\tcompleted\tskipped", 1)
	h := newHarness(t, protectionJSON(fiveCtx), runs)
	code, _, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 — a skipped required check must block", code)
	}
	if !strings.Contains(stderr, "skipped") {
		t.Fatalf("expected 'skipped' in the refusal, got %q", stderr)
	}
}

func TestMergePrRefusesWhenNoBranchProtection(t *testing.T) {
	// An empty protection response is what a 404 looks like after the
	// script's `|| true` swallows it. A JSON `{}` would instead reach the
	// "no required status checks" branch.
	h := newHarness(t, "", fiveRuns)
	code, _, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no branch protection") {
		t.Fatalf("expected a protection-missing refusal, got %q", stderr)
	}
}

func TestMergePrRefusesWhenProtectionHasNoRequiredChecks(t *testing.T) {
	h := newHarness(t, `{"required_status_checks":{"contexts":[],"strict":true}}`, fiveRuns)
	code, _, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no required status checks") {
		t.Fatalf("expected a no-required-checks refusal, got %q", stderr)
	}
}

// --- review gate (exit 2) ---

func TestMergePrRefusesWhenReviewRequiredAndNoAdmin(t *testing.T) {
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, _, stderr := h.run(t, map[string]string{"PR": "42", "FAKE_REVIEW": "REVIEW_REQUIRED"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (review gate)", code)
	}
	if !strings.Contains(stderr, "ADMIN") {
		t.Fatalf("expected the ADMIN remedy, got %q", stderr)
	}
}

func TestMergePrRefusesOverrideWhenEnforceAdminsTrue(t *testing.T) {
	prot := `{"required_status_checks":{"contexts":[` + fiveCtx + `],"strict":true},` +
		`"required_pull_request_reviews":{"required_approving_review_count":1},` +
		`"enforce_admins":{"enabled":true}}`
	h := newHarness(t, prot, fiveRuns)
	code, _, stderr := h.run(t, map[string]string{"PR": "42", "FAKE_REVIEW": "REVIEW_REQUIRED", "ADMIN": "1"})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 — enforce_admins=true must block the override", code)
	}
	if !strings.Contains(stderr, "CANNOT bypass") {
		t.Fatalf("expected an explicit cannot-bypass message, got %q", stderr)
	}
}

// --- the merge path ---

func TestMergePrBodyListsEveryRequiredCheckAtHead(t *testing.T) {
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, _, _ := h.run(t, map[string]string{"PR": "42", "FAKE_HEAD_SHA": "deadbee"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	body := h.body(t)
	for _, ctx := range []string{
		"Backend Checks", "Race Detector", "Race Detector (SQLite backend)",
		"WebUI Checks", "Windows Store Tests",
	} {
		if !strings.Contains(body, "- "+ctx+": success") {
			t.Fatalf("merge body is missing %q; body:\n%s", ctx, body)
		}
	}
	if !strings.Contains(body, "at head deadbee") {
		t.Fatalf("body must name the head SHA it verified, got:\n%s", body)
	}
	if strings.Contains(body, "Docker Image Build") {
		t.Fatalf("a non-required check leaked into the body:\n%s", body)
	}
}

func TestMergePrBodyRecordsAdminOverride(t *testing.T) {
	h := newHarness(t, protectionJSON(fiveCtx), fiveRuns)
	code, _, _ := h.run(t, map[string]string{"PR": "42", "FAKE_REVIEW": "REVIEW_REQUIRED", "ADMIN": "1"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	body := h.body(t)
	if !strings.Contains(body, "admin override") {
		t.Fatalf("body must record that the admin override was used:\n%s", body)
	}
	if !strings.Contains(body, "reviewDecision=REVIEW_REQUIRED") {
		t.Fatalf("body must record the pre-merge review state:\n%s", body)
	}
}

// Required checks are read from branch protection, not hardcoded: a protection
// rule naming contexts the script has never seen must still gate correctly.
// This is also the regression guard for the word-splitting bug, where
// "Backend Checks" was iterated as two words and every context read MISSING.
func TestMergePrReadsMultiWordContextsFromProtection(t *testing.T) {
	ctx := `"Alpha Job With Spaces","Another Check","Third Required Check"`
	runs := "Alpha Job With Spaces\tcompleted\tsuccess\tgithub-actions\n" +
		"Another Check\tcompleted\tsuccess\tgithub-actions\n" +
		"Third Required Check\tcompleted\tsuccess\tgithub-actions\n"
	h := newHarness(t, protectionJSON(ctx), runs)
	code, out, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 — multi-word contexts must not be split.\nout:\n%s\nstderr:\n%s",
			code, out, stderr)
	}
	if strings.Contains(out, "MISSING") {
		t.Fatalf("multi-word check names were split into fragments:\n%s", out)
	}
	body := h.body(t)
	for _, c := range []string{"Alpha Job With Spaces", "Another Check", "Third Required Check"} {
		if !strings.Contains(body, "- "+c+": success") {
			t.Fatalf("body missing %q:\n%s", c, body)
		}
	}
}

func TestMergePrMissingContextIsReportedPerName(t *testing.T) {
	ctx := `"Alpha Job With Spaces","Absent Check"`
	runs := "Alpha Job With Spaces\tcompleted\tsuccess\tgithub-actions\n"
	h := newHarness(t, protectionJSON(ctx), runs)
	code, out, stderr := h.run(t, map[string]string{"PR": "42"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out, "MISSING") || !strings.Contains(out, "Absent Check") {
		t.Fatalf("expected the absent context reported by name:\n%s", out)
	}
	if strings.Contains(stderr, "Alpha Job With Spaces") {
		t.Fatalf("the present context must not be blamed:\n%s", stderr)
	}
}

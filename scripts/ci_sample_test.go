package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the real scripts/ci-sample.sh with a fake `gh` on PATH,
// following the same convention as merge_pr_test.go: only the network boundary
// is faked, so the script's own settled-poll loop, status/conclusion parsing and
// counting all execute for real.
//
// The contract being pinned: a check-run whose status is not `completed` must
// never be counted, and the script must refuse to report any count at all if a
// run never settles. That is the exact defect that produced two false "empty
// conclusion" reads: `gh run watch` returning does not mean the check-run read
// next is settled, and `conclusion` is the empty string in both the
// still-running and the never-ran case.

const fakeCISampleGH = `#!/usr/bin/env bash
# Fake gh for ci-sample.sh. Env:
#   FAKE_SETTLE_AFTER  number of check-runs reads to answer "in_progress"
#                      before answering "completed" (default 0 = always settled)
#   FAKE_NEVER_SETTLE  if 1, never settle
#   FAKE_CONCLUSION    conclusion to report once settled (default success)
#   FAKE_POLLS_FILE    file to append one line per check-runs read
#   FAKE_ABSENT_JOB    if 1, omit the sampled job from the payload
set -uo pipefail

args=("$@")
sub="${1:-}"; shift || true

case "$sub" in
  auth)   exit 0 ;;
  repo)
    # gh repo view --json nameWithOwner --jq .nameWithOwner
    printf 'KervanServer/kervan\n'; exit 0 ;;
  workflow)
    exit 0 ;;
  api)
    url="${1:-}"; shift || true
    polls=0
    [ -f "${FAKE_POLLS_FILE:-/dev/null}" ] || : > "${FAKE_POLLS_FILE:-/dev/null}"
    polls=$(wc -l < "${FAKE_POLLS_FILE:-/dev/null}" | tr -d ' ')
    printf 'poll\n' >> "${FAKE_POLLS_FILE:-/dev/null}"

    if [ "${FAKE_NEVER_SETTLE:-0}" = "1" ]; then
      status=in_progress
    elif [ "$polls" -lt "${FAKE_SETTLE_AFTER:-0}" ]; then
      status=in_progress
    else
      status=completed
    fi
    concl=""
    [ "$status" = "completed" ] && concl="${FAKE_CONCLUSION:-success}"

    # A sibling job that is always settled: the script requires EVERY check-run
    # at the head to be completed, so an unsettled sibling must block too.
    cat <<JSON
{"total_count":2,"check_runs":[
 {"id":9001,"name":"Backend Checks","status":"$status","conclusion":$([ -n "$concl" ] && printf '"%s"' "$concl" || printf 'null')},
 {"id":9002,"name":"Windows Store Tests","status":"$status","conclusion":$([ -n "$concl" ] && printf '"%s"' "$concl" || printf 'null')}
]}
JSON
    exit 0 ;;
esac
exit 0
`

func writeFakeCISampleGH(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "gh")
	if err := os.WriteFile(p, []byte(fakeCISampleGH), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}

// runSample executes the real script with the fake gh on PATH.
func runSample(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	writeFakeCISampleGH(t, dir)

	base := append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"POLL_INTERVAL=0",
		// REF=HEAD so the test never depends on an ambient branch name. CI
		// checks out a detached HEAD for pull_request events, so a local
		// `master` ref does not exist there -- that made this test fail in CI
		// with a bare exit 128 while passing locally.
		"REF=HEAD",
	)
	// Invoke by path relative to this package dir, not by bare name: the script
	// is not installed on PATH, only the fake gh is prepended to it.
	cmd := exec.Command("./ci-sample.sh", args...)
	cmd.Env = append(base, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run ci-sample.sh: %v (output: %s)", err, out)
	}
	return string(out), code
}

func TestCISampleWaitsForSettledAndCountsOnlyCompleted(t *testing.T) {
	polls := filepath.Join(t.TempDir(), "polls")
	out, code := runSample(t,
		[]string{"FAKE_SETTLE_AFTER=3", "FAKE_POLLS_FILE=" + polls},
		"N=2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out)
	}

	// Every RUN line must be from a settled check-run. A run line carrying a
	// non-completed status is precisely the defect this test pins.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "RUN ") {
			if !strings.Contains(line, "settled=completed") {
				t.Errorf("counted an unsettled check-run: %q", line)
			}
		}
	}
	if !strings.Contains(out, "sampled=2 completed/success=2") {
		t.Errorf("missing settled count; output:\n%s", out)
	}
	// It must have polled more than once per run, proving it waited rather
	// than reading the first response.
	n, _ := os.ReadFile(polls)
	if got := strings.Count(string(n), "poll"); got < 5 {
		t.Errorf("expected the loop to poll past the unsettled reads, got %d polls", got)
	}
}

func TestCISampleRefusesWhenRunNeverSettles(t *testing.T) {
	out, code := runSample(t,
		[]string{"FAKE_NEVER_SETTLE=1", "MAX_POLLS=3", "POLL_INTERVAL=0"},
		"N=1")
	if code == 0 {
		t.Fatalf("expected refusal, got exit 0; output:\n%s", out)
	}
	if !strings.Contains(out, "never settled") {
		t.Errorf("want a 'never settled' refusal; output:\n%s", out)
	}
	// Crucially: no count at all when nothing settled.
	if strings.Contains(out, "sampled=") {
		t.Errorf("reported a count with no settled run; output:\n%s", out)
	}
}

func TestCISampleCountsSettledFailureAsNotGreen(t *testing.T) {
	out, code := runSample(t,
		[]string{"FAKE_CONCLUSION=failure"},
		"N=1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (a settled failure is a valid sample); output:\n%s", code, out)
	}
	// A settled failure must be sampled but NOT counted green.
	if !strings.Contains(out, "sampled=1 completed/success=0") {
		t.Errorf("settled failure must sample without counting green; output:\n%s", out)
	}
}

func TestCISampleRejectsBadArguments(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"missing N", ""},
		{"non-numeric N", "N=many"},
		{"zero N", "N=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var argv []string
			if tc.args != "" {
				argv = []string{tc.args}
			}
			out, code := runSample(t, nil, argv...)
			if code == 0 {
				t.Errorf("want usage error, got exit 0; output:\n%s", out)
			}
		})
	}
}

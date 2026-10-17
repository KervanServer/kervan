package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Live tests for scripts/merge-pr.sh against the REAL gh CLI.
//
// merge_pr_test.go stubs gh on PATH, so it proves the gate's logic but not
// that GitHub's actual JSON satisfies the jq filters the script relies on. If
// gh renames --json state, drops a field, or changes an API shape, every
// stubbed test still passes and the real gate breaks at the next merge.
//
// These tests close that gap for everything the script reads.
//
// SAFETY: nothing here can merge. TestLiveStateGateRefusesMergedPR points the
// real script at an already-merged PR, so merge-pr.sh:36 refuses on
// `state != OPEN` long before gh pr merge is reached. The second test invokes
// read-only gh commands directly rather than running the script at all. The
// destructive path -- gh pr merge -- is deliberately left unexercised; see
// TestLiveCannotMergeIsUntested.
//
// Opt in with MERGE_PR_LIVE=1. They skip by default so `make check` stays
// hermetic and no unattended CI run can reach a live merge path.

// requireLive skips unless the operator opted in and gh is usable.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MERGE_PR_LIVE") != "1" {
		t.Skip("live gh test; set MERGE_PR_LIVE=1 to run against the real CLI")
	}
	if runtime.GOOS == "windows" {
		t.Skip("merge-pr.sh is a bash script; the windows job covers the Go side")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH")
	}
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		t.Skip("gh not authenticated; a live test needs a real session")
	}
}

// mergedPRNumber returns a real, already-merged PR. Its state is MERGED, so the
// script refuses before any write.
func mergedPRNumber(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("gh", "pr", "list", "--state", "merged", "--limit", "1",
		"--json", "number", "--jq", ".[0].number").Output()
	if err != nil {
		t.Skipf("cannot list merged PRs: %v", err)
	}
	n := strings.TrimSpace(string(out))
	if n == "" || n == "null" {
		t.Skip("no merged PR available to target")
	}
	return n
}

// TestLiveStateGateRefusesMergedPR runs the REAL script with the REAL gh and
// asserts it refuses on state. Reaching that refusal at all proves these gh
// calls parsed:
//
//	merge-pr.sh:29  gh repo view  --json nameWithOwner
//	merge-pr.sh:30  gh pr view --json headRefOid
//	merge-pr.sh:31  gh pr view --json baseRefName
//	merge-pr.sh:32  gh pr view --json state
//	merge-pr.sh:33  gh pr view --json mergeable
//	merge-pr.sh:34  gh pr view --json reviewDecision
func TestLiveStateGateRefusesMergedPR(t *testing.T) {
	requireLive(t)
	pr := mergedPRNumber(t)

	cmd := exec.Command("bash", filepath.Join("merge-pr.sh"))
	// Deliberately no ADMIN: this must refuse on state, not on review.
	cmd.Env = append(os.Environ(), "PR="+pr)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	code := runLive(t, cmd)

	if code == 0 {
		t.Fatalf("merge-pr.sh exited 0 on merged PR #%s; the state gate did not hold", pr)
	}
	if !strings.Contains(stderr.String(), "not OPEN") {
		t.Fatalf("refused for the wrong reason, got %q; want the state gate (not OPEN)", stderr.String())
	}
	// The message embeds the real state gh reported, so an empty/renamed
	// state field would surface here rather than silently passing.
	if !strings.Contains(stderr.String(), "MERGED") {
		t.Logf("refusal did not name the real state: %q", stderr.String())
	}
}

// TestLiveProtectionAndCheckRunsParse runs the two gh api calls the script
// makes after the state gate. A merged PR never reaches them, so they are
// checked here directly, using the script's exact jq filters.
func TestLiveProtectionAndCheckRunsParse(t *testing.T) {
	requireLive(t)

	repo, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner").Output()
	if err != nil {
		t.Fatalf("gh repo view failed (matches merge-pr.sh:29): %v", err)
	}
	repoName := strings.TrimSpace(string(repo))
	if repoName == "" {
		t.Fatal("gh repo view returned an empty nameWithOwner")
	}

	base, err := exec.Command("gh", "pr", "list", "--state", "merged", "--limit", "1",
		"--json", "baseRefName", "--jq", ".[0].baseRefName").Output()
	if err != nil || strings.TrimSpace(string(base)) == "" {
		t.Skipf("cannot determine base branch: %v", err)
	}

	// merge-pr.sh:48 then :54
	prot, err := exec.Command("bash", "-c",
		`gh api "repos/`+repoName+`/branches/`+strings.TrimSpace(string(base))+`/protection" | jq -r '.required_status_checks.contexts[]? // empty'`).Output()
	if err != nil {
		t.Fatalf("branch protection query failed (matches merge-pr.sh:48): %v", err)
	}
	contexts := nonEmptyLines(string(prot))
	if len(contexts) == 0 {
		t.Fatal("live protection returned no required contexts; the script would refuse every merge")
	}
	for _, c := range contexts {
		if strings.TrimSpace(c) == "" {
			t.Fatalf("empty context name in %q", contexts)
		}
	}

	// merge-pr.sh:70
	runs, err := exec.Command("bash", "-c",
		`gh api "repos/`+repoName+`/commits/master/check-runs" --jq '.check_runs[] | "\(.name)\t\(.status)\t\(.conclusion // "-")"'`).Output()
	if err != nil {
		t.Fatalf("check-runs query failed (matches merge-pr.sh:70): %v", err)
	}
	if len(nonEmptyLines(string(runs))) == 0 {
		t.Fatal("live check-runs returned nothing; the script would report every context MISSING")
	}
}

func runLive(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		return exitErr.ExitCode()
	}
	t.Fatalf("running %v: %v", cmd.Args, err)
	return -1
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

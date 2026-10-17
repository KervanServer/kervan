package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Destructive live coverage for the `gh pr merge --admin` invocation itself
// (merge-pr.sh:147-156): the ARGS array, the headRefName read inside
// --subject, the --admin append, and the mergeCommit read-back.
//
// merge_pr_live_test.go proves the gate's *reading* of real gh. It stops at
// merge-pr.sh:36, because a merged PR refuses before the merge call. So the
// one thing neither suite reaches is the write path itself: whether
// `gh pr merge --merge --subject --body [--admin]` is accepted, and whether
// mergeCommit.oid reads back after the merge. A drifted or removed flag would
// pass every other test and break the first real merge of the day.
//
// SAFETY. This test performs a REAL merge into master. It is behind two
// independent env gates and it refuses outright in CI:
//
//	MERGE_PR_LIVE=1        the existing read-only gate
//	MERGE_PR_LIVE_MERGE=1  this destructive gate, set separately
//	CI is unset            GitHub Actions sets CI=true, so an unattended
//	                       run skips rather than merges
//
// It then refuses unless local master equals origin/master and the worktree is
// clean, creates its OWN throwaway PR from an empty commit so it can never be
// aimed at someone else's work, and re-checks that the PR still has zero files
// before merging. Cleanup is registered with t.Cleanup so the branch is
// removed even if the merge or an assertion fails mid-way.
//
//	go test ./scripts/ -run TestLiveMergeAdminInvocation -timeout 20m \
//	  -v   (with MERGE_PR_LIVE=1 MERGE_PR_LIVE_MERGE=1)
//
// The 20m budget is not slack: CI on a throwaway PR takes several minutes to
// go green, and the test waits for it with a bounded poll rather than a guess.

const (
	gateReadOnly    = "MERGE_PR_LIVE"
	gateDestructive = "MERGE_PR_LIVE_MERGE"

	// mergeWaitBudget bounds the wait for the throwaway PR to go green.
	mergeWaitBudget = 8 * time.Minute
	mergePollEvery  = 15 * time.Second
)

// requireDestructiveLive applies every gate. It is deliberately stricter than
// requireLive: both env vars, no CI, authenticated gh, and a real gh binary.
func requireDestructiveLive(t *testing.T) {
	t.Helper()
	// Skip before anything else in CI. An unattended run must never merge.
	if ci := os.Getenv("CI"); ci != "" && ci != "false" && ci != "0" {
		t.Skip("CI is set; this test performs a real merge and must never run unattended")
	}
	requireLive(t) // MERGE_PR_LIVE=1, not windows, gh present + authenticated
	if os.Getenv(gateDestructive) != "1" {
		t.Skipf("destructive live test; set %s=1 to run it", gateDestructive)
	}
}

// TestLiveMergeAdminInvocation exercises the real merge path end to end against
// a throwaway PR it creates itself.
func TestLiveMergeAdminInvocation(t *testing.T) {
	requireDestructiveLive(t)

	repo := ghOut(t, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if repo == "" {
		t.Fatal("gh repo view returned an empty nameWithOwner")
	}
	t.Logf("live merge test against %s", repo)

	// Refuse on a diverged or dirty tree: both were real hazards this session.
	git(t, "fetch", "--all", "--prune", "--quiet")
	if strings.TrimSpace(git(t, "status", "--porcelain")) != "" {
		t.Fatal("worktree is dirty; refusing to create a throwaway branch")
	}
	if strings.TrimSpace(git(t, "rev-list", "--left-right", "--count", "master...origin/master")) != "0	0" {
		t.Fatal("local master has diverged from origin/master; fast-forward it first")
	}

	branch := fmt.Sprintf("test/merge-pr-live-merge-%d", os.Getpid())
	t.Cleanup(func() { cleanupThrowaway(t, branch) })

	// Throwaway PR: an empty commit on a fresh branch off origin/master.
	git(t, "checkout", "-b", branch, "origin/master")
	git(t, "commit", "--allow-empty", "-m", "chore: throwaway commit to exercise the live merge path")
	git(t, "push", "-u", "origin", branch)
	pr := createThrowawayPR(t, branch)

	// The whole point is that this PR carries nothing. If it ever does, stop.
	if n := strings.TrimSpace(ghOut(t, "pr", "view", pr, "--json", "files", "--jq", ".files | length")); n != "0" {
		t.Fatalf("throwaway PR #%s has %s changed file(s); refusing to merge", pr, n)
	}

	// waitForRequiredContexts reads check-runs at commits/$ref, so it needs the
	// PR's head SHA. Passing the PR number looks plausible (it is a valid ref to
	// the PR API) but not to the commits API: gh answers 422 "No commit found
	// for SHA: 14" on every poll, and the wait then never converges.
	headSHA := strings.TrimSpace(ghOut(t, "pr", "view", pr, "--json", "headRefOid", "--jq", ".headRefOid"))
	if !isGitSHA(headSHA) {
		t.Fatalf("could not read the head SHA of throwaway PR #%s, got %q", pr, headSHA)
	}
	waitForRequiredContexts(t, repo, headSHA, mergeWaitBudget)

	// The write path under test.
	code, out := runMergePr(t, pr, true)
	if code != 0 {
		t.Fatalf("merge-pr.sh exited %d on throwaway PR #%s\n%s", code, pr, out)
	}
	if !strings.Contains(out, "MERGED #"+pr) {
		t.Fatalf("merge-pr.sh did not report MERGED for #%s\n%s", pr, out)
	}

	// mergeCommit read-back (merge-pr.sh:154) against the real API.
	merged := ghOut(t, "pr", "view", pr, "--json", "state,mergeCommit",
		"--jq", ".state + \" \" + .mergeCommit.oid")
	fields := strings.Fields(merged)
	if len(fields) != 2 || fields[0] != "MERGED" || !isGitSHA(fields[1]) {
		t.Fatalf("mergeCommit read-back failed for #%s, got %q", pr, merged)
	}
	mergeSHA := fields[1]
	t.Logf("merged #%s -> %s", pr, mergeSHA[:7])

	git(t, "fetch", "--all", "--prune", "--quiet")
	// git merge-base, not gh: ghRun prefixes "gh", so this was failing as
	// `gh merge-base` on every run and reporting a healthy merge as lost.
	if code, _ := gitRun(t, "merge-base", "--is-ancestor", mergeSHA, "origin/master"); code != 0 {
		t.Fatalf("merge commit %s is NOT an ancestor of origin/master", mergeSHA[:7])
	}

	// The gate does not watch its own merge commit, so check it here.
	// (01M5492BMP1GQBP2KN83G37G5E, 01M54TR0H67NGPN4JJYCCWX86E.)
	waitForRequiredContexts(t, repo, mergeSHA, mergeWaitBudget)
	t.Logf("all required contexts green on the merge commit %s", mergeSHA[:7])
}

// createThrowawayPR opens the PR and returns its number.
func createThrowawayPR(t *testing.T, branch string) string {
	t.Helper()
	cmd := ghCmd(t, "pr", "create", "--base", "master", "--head", branch,
		"--title", "chore: throwaway PR to exercise the live merge path",
		"--body", "Created by TestLiveMergeAdminInvocation. Empty commit; merged and deleted by the test.")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gh pr create failed: %v\n%s", err, out)
	}
	// gh pr create prints the pull-request URL, e.g.
	// https://github.com/OWNER/REPO/pull/13 -- so take the trailing segment.
	num := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(num, '/'); i >= 0 {
		num = num[i+1:]
	}
	num = strings.TrimSpace(strings.Trim(num, "[]#"))
	if num != "" && strings.IndexFunc(num, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return num
	}
	t.Fatalf("could not parse a PR number from %q", out)
	return ""
}

// waitForRequiredContexts polls until every required context on the ref's head
// is a success, or fails with what it actually saw. Bounded, never a blind sleep.
func waitForRequiredContexts(t *testing.T, repo, ref string, budget time.Duration) {
	t.Helper()
	required := requiredContexts(t, repo)
	deadline := time.Now().Add(budget)
	var last string
	for {
		last = contextReport(t, repo, ref, required)
		if strings.HasSuffix(last, " 0 pending, 0 not-success") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("required contexts on %s not green within %s\n%s", ref, budget, last)
		}
		time.Sleep(mergePollEvery)
	}
}

func requiredContexts(t *testing.T, repo string) []string {
	t.Helper()
	out := ghOut(t, "api", "repos/"+repo+"/branches/master/protection",
		"--jq", ".required_status_checks.contexts[]? // empty")
	ctx := nonEmptyLines(out)
	if len(ctx) == 0 {
		t.Fatal("branch protection returned no required contexts")
	}
	return ctx
}

func contextReport(t *testing.T, repo, ref string, required []string) string {
	t.Helper()
	var sb strings.Builder
	pending, notOK := 0, 0
	for _, c := range required {
		got := strings.TrimSpace(ghOut(t, "api", "repos/"+repo+"/commits/"+ref+"/check-runs",
			"--jq", ".check_runs[] | select(.name==\""+c+"\") | (.status + \" \" + (.conclusion // \"-\"))"))
		sb.WriteString("    [" + c + "] = " + got + "\n")
		switch {
		case got == "":
			pending++
		case strings.HasSuffix(got, "success"):
		default:
			notOK++
		}
	}
	return fmt.Sprintf("%s  %d pending, %d not-success", sb.String(), pending, notOK)
}

// cleanupThrowaway removes the throwaway branch and restores master. Registered
// with t.Cleanup so it runs even when an assertion fails mid-merge.
func cleanupThrowaway(t *testing.T, branch string) {
	t.Helper()
	// Remove the remote branch too: a failed run otherwise leaves a throwaway
	// branch and an open pull request behind on the remote.
	if _, repo := ghRun(t, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"); repo != "" {
		ghRun(t, "api", "-X", "DELETE", "repos/"+strings.TrimSpace(repo)+"/git/refs/heads/"+branch)
	}
	gitSoft(t, "checkout", "master")
	gitSoft(t, "merge", "--ff-only", "origin/master")
	gitSoft(t, "branch", "-D", branch)
	t.Logf("cleanup done for %s", branch)
}

func runMergePr(t *testing.T, pr string, admin bool) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", "merge-pr.sh")
	cmd.Env = append(os.Environ(), "PR="+pr)
	if admin {
		cmd.Env = append(cmd.Env, "ADMIN=1")
	} else {
		cmd.Env = append(cmd.Env, "ADMIN=0")
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	if ok := asExitError(err, &exit); ok {
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("running merge-pr.sh: %v", err)
	return -1, ""
}

func git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func gitSoft(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Logf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

// gitRun runs a git subcommand and reports the exit code instead of failing:
// the caller needs to branch on success. `merge-base --is-ancestor` is the case
// in point -- exit 0 means yes, 1 means no, and both are ordinary answers
// rather than errors. Distinct from git (fatal on any non-zero) and gitSoft
// (logs and discards the result).
func gitRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	if ok := asExitError(err, &exit); ok {
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	return -1, ""
}

// isGitSHA reports whether s looks like a full 40-hex git object name, so a
// truncated or empty mergeCommit.oid cannot pass the read-back assertion.
func isGitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func ghCmd(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("gh"); err != nil {
		t.Fatal("gh not on PATH")
	}
	return exec.Command("gh", args...)
}

func ghOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := ghCmd(t, args...).CombinedOutput()
	if err != nil {
		t.Logf("gh %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func ghRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exit *exec.ExitError
	if ok := asExitError(err, &exit); ok {
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("gh %s: %v", strings.Join(args, " "), err)
	return -1, ""
}

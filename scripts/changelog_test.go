package main

// Changelog guard: keeps the Unreleased section honest.
//
// It cannot infer from prose that every fix is described, so it does the two
// things a machine can check reliably:
//
//  1. Structure — Unreleased exists, is non-empty, uses the Keep a Changelog
//     headings the rest of the file uses, and every entry is a wrapped bullet.
//     A release-notes section that lost its heading, or whose bullets someone
//     hard-wrapped into prose, fails here rather than at release time.
//  2. Coverage — each user-visible fix listed in unreleasedUserVisibleFixes
//     must be named in Unreleased. Adding a fix means adding a row and an
//     entry; forgetting either fails this test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unreleasedUserVisibleFixes maps a short, stable description to a distinctive
// phrase that must appear in the Unreleased section. Keep the phrase copied
// verbatim from the entry so a reworded entry fails loudly instead of
// silently dropping the fix from the release notes.
var unreleasedUserVisibleFixes = map[string]string{
	"websocket change-push delivery":  "WebSocket clients no longer miss a change published while they connect",
	"sqlite race coverage":            "SQLite store backend is now race-tested on every run",
	"json store file corruption":      "JSON store file",
	"backup verify/restore soundness": "backup",
}

const changelogPath = "../CHANGELOG.md"

func readChangelog(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		t.Fatalf("read %s: %v", changelogPath, err)
	}
	return string(raw)
}

// unreleasedSection returns the body of the "## Unreleased" heading, stopping
// at the next "## " heading.
func unreleasedSection(t *testing.T, log string) string {
	t.Helper()
	start := strings.Index(log, "## Unreleased")
	if start < 0 {
		t.Fatal("CHANGELOG.md has no \"## Unreleased\" section")
	}
	rest := log[start+len("## Unreleased"):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestChangelogUnreleasedHasStructure(t *testing.T) {
	section := unreleasedSection(t, readChangelog(t))
	if strings.TrimSpace(section) == "" {
		t.Fatal("the Unreleased section is empty; every user-visible fix must be listed before a release")
	}

	// Headings the rest of the file uses, so the section renders like a
	// changelog and not like an indented blob.
	allowed := map[string]bool{"### Fixed": true, "### Changed": true, "### Added": true,
		"### Security": true, "### Removed": true, "### Deprecated": true}
	foundHeading := false
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		foundHeading = true
		if !allowed[line] {
			t.Errorf("Unreleased uses heading %q; expected one of the Keep a Changelog headings", line)
		}
	}
	if !foundHeading {
		t.Error("Unreleased has no ### subsection; group entries under Fixed/Changed/Added/...")
	}

	// Every entry must be a bullet, and continuation lines must stay indented
	// so the entry renders as one paragraph.
	inBullet := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "- "):
			inBullet = true
		case strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#"):
			inBullet = false
		case inBullet && !strings.HasPrefix(line, "  "):
			t.Errorf("continuation line is not indented under its bullet: %q", line)
		}
	}
}

func TestChangelogUnreleasedNamesEveryUserVisibleFix(t *testing.T) {
	section := unreleasedSection(t, readChangelog(t))
	for fix, phrase := range unreleasedUserVisibleFixes {
		if !strings.Contains(section, phrase) {
			t.Errorf("Unreleased does not mention the %s fix; expected to find %q.\n"+
				"Add a bullet under Unreleased describing it (CONTRIBUTING: docs are part of the change).", fix, phrase)
		}
	}
}

// TestChangelogReleaseProcedureKeepsUnreleasedDistinct guards the inverse
// mistake: cutting a release by moving Unreleased under a version heading
// without adding a fresh Unreleased section would leave the next fix
// silently unreleased.
func TestChangelogReleaseProcedureKeepsUnreleasedDistinct(t *testing.T) {
	log := readChangelog(t)
	unreleasedStart := strings.Index(log, "## Unreleased")
	if unreleasedStart < 0 {
		t.Fatal("no Unreleased section")
	}
	after := log[unreleasedStart+len("## Unreleased"):]
	versionAt := strings.Index(after, "\n## v")
	if versionAt < 0 {
		return // no released section yet; nothing to check
	}
	if strings.TrimSpace(after[:versionAt]) == "" {
		t.Error("Unreleased is empty directly above a released version section; " +
			"start a fresh Unreleased section when cutting a release")
	}
}

func TestChangelogFileIsPresent(t *testing.T) {
	if _, err := os.Stat(filepath.Clean(changelogPath)); err != nil {
		t.Fatalf("CHANGELOG.md must exist at the repository root: %v", err)
	}
}

package build

import "testing"

func TestInfoFormatsBuildMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	defer func() {
		Version, Commit, Date = oldVersion, oldCommit, oldDate
	}()

	Version = "1.2.3"
	Commit = "abc123"
	Date = "2026-04-11T10:00:00Z"

	want := "Kervan 1.2.3 (abc123) built 2026-04-11T10:00:00Z"
	if got := Info(); got != want {
		t.Fatalf("unexpected build info: got=%q want=%q", got, want)
	}
}

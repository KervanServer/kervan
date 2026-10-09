package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeChained(t *testing.T, path string, key []byte, n int, startUser int) {
	t.Helper()
	sink, err := NewChainedFileSink(path, key)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		evt := Event{
			ID:        "e" + string(rune('a'+startUser+i)),
			Timestamp: time.Date(2026, 10, 9, 10, 0, i, 0, time.UTC),
			Type:      EventFileWrite,
			Username:  "user" + string(rune('a'+startUser+i)),
			Path:      `/dir/"quoted" & <html>`,
			Meta:      map[string]string{"b": "2", "a": "1"},
		}
		if err := sink.Write(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func verifyFile(t *testing.T, path string, key []byte) VerifyReport {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	report, err := VerifyChain(f, key)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChainedLogVerifiesAndResumes(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key")); !bytes.Equal(again, key) {
		t.Fatal("key not persisted")
	}
	if info, _ := os.Stat(filepath.Join(dir, "audit.key")); info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", info.Mode().Perm())
	}
	path := filepath.Join(dir, "audit.jsonl")

	// Records written before integrity was enabled stay readable.
	plain, _ := NewFileSink(path)
	_ = plain.Write(context.Background(), Event{ID: "legacy", Type: EventAuthSuccess})
	_ = plain.Close()

	writeChained(t, path, key, 3, 0)
	writeChained(t, path, key, 2, 3) // a restart resumes the chain
	report := verifyFile(t, path, key)
	if !report.OK() || report.Records != 6 || report.Chained != 5 || report.Unchained != 1 || report.Segments != 1 || report.LastSeq != 5 {
		t.Fatalf("report = %+v", report)
	}

	// Existing readers still parse chained lines as plain events.
	var evt Event
	if err := json.Unmarshal([]byte(readLines(t, path)[2]), &evt); err != nil || evt.Username != "userb" || evt.Meta["a"] != "1" {
		t.Fatalf("chained line not readable as an event: %v %+v", err, evt)
	}
}

func TestChainedLogDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	key, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	path := filepath.Join(dir, "audit.jsonl")
	writeChained(t, path, key, 5, 0)
	original := readLines(t, path)

	cases := map[string]func([]string) []string{
		"modified field": func(l []string) []string {
			l[2] = strings.Replace(l[2], `"userc"`, `"mallory"`, 1)
			return l
		},
		"deleted record": func(l []string) []string { return append(l[:2:2], l[3:]...) },
		"reordered":      func(l []string) []string { l[1], l[2] = l[2], l[1]; return l },
		"forged chain fields": func(l []string) []string {
			l[2] = strings.Replace(l[2], `"mac":"`, `"mac":"00`, 1)
			return l
		},
		"unchained record inserted": func(l []string) []string {
			return append(l[:2:2], append([]string{`{"id":"x","type":"file.delete"}`}, l[2:]...)...)
		},
		"head removed": func(l []string) []string { return l[2:] },
	}
	for name, mutate := range cases {
		lines := mutate(append([]string(nil), original...))
		writeLines(t, path, lines)
		if report := verifyFile(t, path, key); report.OK() {
			t.Errorf("%s not detected: %+v", name, report)
		}
	}

	writeLines(t, path, original)
	other, _ := LoadOrCreateChainKey(filepath.Join(dir, "other.key"))
	if report := verifyFile(t, path, other); report.OK() || !strings.Contains(report.Problems[0].Reason, "MAC mismatch") {
		t.Fatalf("wrong key accepted: %+v", report)
	}

	// Documented limitation: truncating the tail is invisible in the file
	// itself, which is why last_seq/last_mac must be anchored elsewhere.
	writeLines(t, path, original[:3])
	if report := verifyFile(t, path, key); !report.OK() || report.LastSeq != 3 {
		t.Fatalf("tail truncation: %+v", report)
	}
}

func TestChainResumeAfterCorruptTailStartsNewSegment(t *testing.T) {
	dir := t.TempDir()
	key, _ := LoadOrCreateChainKey(filepath.Join(dir, "audit.key"))
	path := filepath.Join(dir, "audit.jsonl")
	writeChained(t, path, key, 2, 0)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{\"partial\n")
	_ = f.Close()
	writeChained(t, path, key, 1, 2)
	report := verifyFile(t, path, key)
	if report.OK() || report.LastSeq != 1 {
		t.Fatalf("corrupt tail should surface as a problem and restart the chain: %+v", report)
	}
}

func TestInvalidChainKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.key")
	_ = os.WriteFile(path, []byte("not-hex"), 0o600)
	if _, err := LoadOrCreateChainKey(path); err == nil {
		t.Fatal("invalid key accepted")
	}
}

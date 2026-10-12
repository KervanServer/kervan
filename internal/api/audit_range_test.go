package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditDateRangeFilter(t *testing.T) {
	srv, token := newGroupsTestServer(t)
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	lines := []string{
		`{"id":"1","type":"file.write","timestamp":"2026-10-01T10:00:00Z","username":"root","status":"ok"}`,
		`{"id":"2","type":"file.write","timestamp":"2026-10-02T23:59:59.5Z","username":"root","status":"ok"}`,
		`{"id":"3","type":"file.read","timestamp":"2026-10-03T00:00:00Z","username":"root","status":"ok"}`,
		`{"id":"4","type":"file.read","timestamp":"2026-10-05T08:30:00+03:00","username":"root","status":"ok"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.auditLogPath = logPath
	h := srv.withAuth(srv.handleAudit)

	ids := func(query string) []string {
		code, body := callJSON(t, h, token, http.MethodGet, "/api/v1/audit/events?"+query, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %v", query, code, body)
		}
		var out []string
		for _, e := range body["events"].([]any) {
			out = append(out, e.(map[string]any)["id"].(string))
		}
		return out
	}
	cases := map[string]string{
		"from=2026-10-02":               "4,3,2",
		"to=2026-10-02":                 "2,1", // bare date: the whole day
		"from=2026-10-02&to=2026-10-02": "2",
		"from=2026-10-03T00:00:00Z&to=2026-10-05T05:30:00Z": "3", // to is exclusive; #4 is 05:30Z
		"from=2026-10-02&type=file.read":                    "4,3",
	}
	for query, want := range cases {
		if got := strings.Join(ids(query), ","); got != want {
			t.Errorf("%s → %s, want %s", query, got, want)
		}
	}
	for _, bad := range []string{"from=yesterday", "from=2026-10-05&to=2026-10-01"} {
		if code, _ := callJSON(t, h, token, http.MethodGet, "/api/v1/audit/events?"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s accepted: %d", bad, code)
		}
	}
}

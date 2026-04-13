package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/auth"
)

func TestNormalizeUserDataFormatAndHelpers(t *testing.T) {
	cases := []struct {
		name     string
		format   string
		fileName string
		want     string
		wantErr  string
	}{
		{name: "explicit-json", format: "json", fileName: "", want: "json"},
		{name: "auto-json", format: "auto", fileName: "users.json", want: "json"},
		{name: "auto-csv", format: "", fileName: "users.csv", want: "csv"},
		{name: "missing", format: "", fileName: "", wantErr: "format is required"},
		{name: "undetectable", format: "auto", fileName: "users.txt", wantErr: `unable to detect format for "users.txt"`},
		{name: "unsupported", format: "xml", fileName: "", wantErr: "unsupported format: xml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeUserDataFormat(tc.format, tc.fileName)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}

	trueVal, err := parseOptionalBool("yes")
	if err != nil || trueVal == nil || !*trueVal {
		t.Fatalf("expected yes to parse as true, got value=%v err=%v", trueVal, err)
	}
	falseVal, err := parseOptionalBool("0")
	if err != nil || falseVal == nil || *falseVal {
		t.Fatalf("expected 0 to parse as false, got value=%v err=%v", falseVal, err)
	}
	emptyVal, err := parseOptionalBool("")
	if err != nil || emptyVal != nil {
		t.Fatalf("expected empty value to parse as nil, got value=%v err=%v", emptyVal, err)
	}
	if _, err := parseOptionalBool("maybe"); err == nil {
		t.Fatal("expected invalid optional bool to fail")
	}

	if !parseBoolQuery("on") {
		t.Fatal("expected parseBoolQuery to accept on")
	}
	if parseBoolQuery("off") {
		t.Fatal("expected parseBoolQuery to reject off")
	}

	row := []string{" alice ", "admin"}
	header := map[string]int{"username": 0, "role": 1}
	if got := csvValue(row, header, "username"); got != "alice" {
		t.Fatalf("expected trimmed csv value, got %q", got)
	}
	if got := csvValue(row, header, "missing"); got != "" {
		t.Fatalf("expected missing csv value to be empty, got %q", got)
	}
}

func TestLoadUserImportRecordsForJSONAndCSV(t *testing.T) {
	jsonRecords, err := loadUserImportRecords(strings.NewReader(`[{"username":" alice ","password":" pass ","email":" alice@example.com ","role":" admin ","type":" virtual ","home_dir":" /data "}]`), "json")
	if err != nil {
		t.Fatalf("load json records: %v", err)
	}
	if len(jsonRecords) != 1 {
		t.Fatalf("expected one json record, got %d", len(jsonRecords))
	}
	if jsonRecords[0].Row != 1 || jsonRecords[0].Username != "alice" || jsonRecords[0].HomeDir != "/data" {
		t.Fatalf("unexpected normalized json record: %#v", jsonRecords[0])
	}

	csvPayload := "username,password_hash,email,role,type,home_dir,enabled\nbob,hash,bob@example.com,user,virtual,/srv,true\n"
	csvRecords, err := loadUserImportRecords(strings.NewReader(csvPayload), "csv")
	if err != nil {
		t.Fatalf("load csv records: %v", err)
	}
	if len(csvRecords) != 1 || csvRecords[0].Row != 2 || csvRecords[0].Enabled == nil || !*csvRecords[0].Enabled {
		t.Fatalf("unexpected csv record: %#v", csvRecords)
	}

	if _, err := loadUserImportRecords(strings.NewReader(""), "csv"); err == nil || !strings.Contains(err.Error(), "csv file is empty") {
		t.Fatalf("expected empty csv error, got %v", err)
	}
	if _, err := loadUserImportRecords(strings.NewReader("username,enabled\nbob,maybe\n"), "csv"); err == nil || !strings.Contains(err.Error(), "invalid enabled value") {
		t.Fatalf("expected invalid enabled error, got %v", err)
	}
	if _, err := loadUserImportRecords(strings.NewReader("[]"), "xml"); err == nil || !strings.Contains(err.Error(), "unsupported import format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestParseUserImportRequestVariants(t *testing.T) {
	jsonReq := httptest.NewRequest("POST", "/api/v1/users/import", strings.NewReader(`[{"username":"alice","password":"pw"}]`))
	jsonReq.Header.Set("Content-Type", "application/json")
	format, records, err := parseUserImportRequest(jsonReq, "")
	if err != nil || format != "json" || len(records) != 1 {
		t.Fatalf("unexpected json import parse result: format=%q records=%v err=%v", format, records, err)
	}

	csvReq := httptest.NewRequest("POST", "/api/v1/users/import", strings.NewReader("username,password\nbob,pw\n"))
	csvReq.Header.Set("Content-Type", "text/csv")
	format, records, err = parseUserImportRequest(csvReq, "")
	if err != nil || format != "csv" || len(records) != 1 {
		t.Fatalf("unexpected csv import parse result: format=%q records=%v err=%v", format, records, err)
	}

	octetReq := httptest.NewRequest("POST", "/api/v1/users/import?format=json", strings.NewReader(`[{"username":"carol","password":"pw"}]`))
	octetReq.Header.Set("Content-Type", "application/octet-stream")
	format, records, err = parseUserImportRequest(octetReq, "json")
	if err != nil || format != "json" || len(records) != 1 {
		t.Fatalf("unexpected fallback parse result: format=%q records=%v err=%v", format, records, err)
	}

	badTypeReq := httptest.NewRequest("POST", "/api/v1/users/import", strings.NewReader("x"))
	badTypeReq.Header.Set("Content-Type", "application/octet-stream")
	if _, _, err := parseUserImportRequest(badTypeReq, ""); err == nil || !strings.Contains(err.Error(), "unsupported import content type") {
		t.Fatalf("expected unsupported content type error, got %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("note", "missing-file"); err != nil {
		t.Fatalf("write form field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	multipartReq := httptest.NewRequest("POST", "/api/v1/users/import", body)
	multipartReq.Header.Set("Content-Type", writer.FormDataContentType())
	if _, _, err := parseUserImportRequest(multipartReq, "json"); err == nil || !strings.Contains(err.Error(), "file is required") {
		t.Fatalf("expected multipart missing file error, got %v", err)
	}
}

func TestParseUserImportRequestStreamsCSVRows(t *testing.T) {
	payload := "username,password\nalice,one\nbob,two\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/import", strings.NewReader(payload))
	req.Header.Set("Content-Type", "text/csv")

	format, records, err := parseUserImportRequest(req, "")
	if err != nil {
		t.Fatalf("parse csv request: %v", err)
	}
	if format != "csv" || len(records) != 2 {
		t.Fatalf("unexpected parse result format=%q records=%d", format, len(records))
	}
	if records[0].Row != 2 || records[1].Row != 3 {
		t.Fatalf("unexpected row numbering: %#v", records)
	}
}

func TestUserImportExportHelpers(t *testing.T) {
	if got, err := resolveImportedUserType("", ""); err != nil || got != auth.UserTypeVirtual {
		t.Fatalf("expected default user type virtual, got %q err=%v", got, err)
	}
	if got, err := resolveImportedUserType("admin", ""); err != nil || got != auth.UserTypeAdmin {
		t.Fatalf("expected admin role to map to admin type, got %q err=%v", got, err)
	}
	if _, err := resolveImportedUserType("unknown", ""); err == nil {
		t.Fatal("expected unsupported role/type to fail")
	}

	if got := exportRoleForUser(&auth.User{Type: auth.UserTypeAdmin}); got != "admin" {
		t.Fatalf("expected admin export role, got %q", got)
	}
	if got := exportRoleForUser(nil); got != "user" {
		t.Fatalf("expected nil user export role=user, got %q", got)
	}

	var buf bytes.Buffer
	err := writeUserExportCSV(&buf, []userExportRecord{{
		Username:     "alice",
		Email:        "alice@example.com",
		Role:         "admin",
		Type:         "admin",
		HomeDir:      "/root",
		Enabled:      true,
		PasswordHash: "hash",
	}}, true)
	if err != nil {
		t.Fatalf("write csv: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "password_hash") || !strings.Contains(output, "alice,alice@example.com,admin,admin,/root,true,hash") {
		t.Fatalf("unexpected csv export output: %q", output)
	}

	writerErr := writeUserExportCSV(failingWriter{}, []userExportRecord{{Username: "alice"}}, false)
	if writerErr == nil {
		t.Fatal("expected csv writer to propagate write errors")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

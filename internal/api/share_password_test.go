package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func newShareTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _ := newAuthTestServer(t, false)
	srv.shareLinks = newShareLinkRepository(srv.apiKeys.store)
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{Upload: true, Download: true, ListDir: true}, nil)
	f, err := fsys.Open("/report.pdf", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("secret report"))
	_ = f.Close()
	srv.fsBuilder = func(string) (vfs.FileSystem, error) { return fsys, nil }
	token, err := signToken(srv.secret, "alice", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return srv, token
}

func shareRequest(srv *Server, method, target string, body io.Reader, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.handleShareDownload(rec, req)
	return rec
}

func TestPasswordProtectedShareLink(t *testing.T) {
	srv, token := newShareTestServer(t)
	sink := &memorySink{}
	engine := audit.NewEngine(nil, sink)
	srv.audit = engine
	create := srv.withAuth(srv.handleFilesShare)

	if code, body := callJSON(t, create, token, http.MethodPost, "/api/v1/files/me/share", map[string]any{
		"path": "/report.pdf", "password": "short",
	}); code != http.StatusBadRequest {
		t.Fatalf("short password accepted: %d %v", code, body)
	}
	code, link := callJSON(t, create, token, http.MethodPost, "/api/v1/files/me/share", map[string]any{
		"path": "/report.pdf", "password": "open-sesame", "max_downloads": 2,
	})
	if code != http.StatusCreated || link["password_protected"] != true || !strings.HasPrefix(link["share_url"].(string), "/s/") {
		t.Fatalf("create: %d %v", code, link)
	}
	if _, leaked := link["password_hash"]; leaked {
		t.Fatal("password hash returned to the client")
	}
	tok := link["token"].(string)
	dl := "/api/v1/share/" + tok

	// The landing page metadata does not consume a download.
	rec := shareRequest(srv, http.MethodGet, dl+"/info", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"password_protected":true`) || !strings.Contains(rec.Body.String(), `"downloads_remaining":2`) || !strings.Contains(rec.Body.String(), `"size":13`) {
		t.Fatalf("info: %d %s", rec.Code, rec.Body)
	}

	if rec := shareRequest(srv, http.MethodGet, dl, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("download without password: %d", rec.Code)
	}
	if rec := shareRequest(srv, http.MethodGet, dl, nil, map[string]string{"X-Share-Password": "wrong-one"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("download with wrong header password: %d", rec.Code)
	}
	// A browser form post with a wrong password is sent back to the page.
	form := url.Values{"password": {"nope-nope"}}.Encode()
	rec = shareRequest(srv, http.MethodPost, dl, strings.NewReader(form), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/s/"+tok+"?error=password_required" {
		t.Fatalf("form wrong password: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	form = url.Values{"password": {"open-sesame"}}.Encode()
	rec = shareRequest(srv, http.MethodPost, dl, strings.NewReader(form), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != http.StatusOK || rec.Body.String() != "secret report" {
		t.Fatalf("form download: %d %q", rec.Code, rec.Body)
	}
	rec = shareRequest(srv, http.MethodGet, dl, nil, map[string]string{"X-Share-Password": "open-sesame"})
	if rec.Code != http.StatusOK {
		t.Fatalf("header download: %d", rec.Code)
	}
	// Limit of two reached.
	rec = shareRequest(srv, http.MethodPost, dl, strings.NewReader(form), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "error=exhausted") {
		t.Fatalf("exhausted link: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	engine.Close()
	var ok, failed int
	for _, e := range sink.find(audit.EventShareDownload) {
		if e.Protocol != "share" || e.Username != "alice" {
			t.Fatalf("unexpected share event %+v", e)
		}
		switch e.Status {
		case "ok":
			ok++
		case "failed":
			failed++
		}
	}
	if ok != 2 || failed != 2 || len(sink.find(audit.EventShareCreate)) != 1 {
		t.Fatalf("share audit: ok=%d failed=%d create=%d", ok, failed, len(sink.find(audit.EventShareCreate)))
	}
}

func TestUnprotectedShareLinkStillDirect(t *testing.T) {
	srv, token := newShareTestServer(t)
	code, link := callJSON(t, srv.withAuth(srv.handleFilesShare), token, http.MethodPost, "/api/v1/files/me/share", map[string]any{"path": "/report.pdf"})
	if code != http.StatusCreated || link["password_protected"] != false {
		t.Fatalf("create: %d %v", code, link)
	}
	if rec := shareRequest(srv, http.MethodGet, link["download_url"].(string), nil, nil); rec.Code != http.StatusOK || rec.Body.String() != "secret report" {
		t.Fatalf("direct download: %d %q", rec.Code, rec.Body)
	}
	if rec := shareRequest(srv, http.MethodGet, "/api/v1/share/does-not-exist/info", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown info: %d", rec.Code)
	}
}

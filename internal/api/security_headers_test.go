package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/netguard"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

func TestMiddlewareEnforcesIPFilter(t *testing.T) {
	filter, err := netguard.NewIPFilter([]string{"10.0.0.0/8"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: Config{IPFilter: filter}}
	handler := srv.withMiddleware(okHandler())

	cases := []struct {
		remote, path string
		want         int
	}{
		{"10.1.2.3:4000", "/api/v1/users", http.StatusOK},
		{"192.0.2.1:4000", "/api/v1/users", http.StatusForbidden},
		{"192.0.2.1:4000", "/", http.StatusForbidden},
		{"192.0.2.1:4000", "/health", http.StatusOK}, // probes stay reachable
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = tc.remote
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.remote, tc.path, rec.Code, tc.want)
		}
	}

	// Runtime update of the shared filter applies to the next request.
	_ = filter.Update(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.RemoteAddr = "192.0.2.1:4000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("after clearing filter: status %d", rec.Code)
	}
}

func TestMiddlewareSetsCSPAndHSTS(t *testing.T) {
	srv := &Server{}
	handler := srv.withMiddleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "'sha256-"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	if strings.Contains(strings.SplitN(strings.SplitN(csp, "script-src", 2)[1], ";", 2)[0], "unsafe-inline") {
		t.Errorf("script-src must not allow unsafe-inline: %q", csp)
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be sent over plain HTTP")
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	tlsRec := httptest.NewRecorder()
	handler.ServeHTTP(tlsRec, tlsReq)
	if !strings.HasPrefix(tlsRec.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Error("HSTS missing on TLS response")
	}
}

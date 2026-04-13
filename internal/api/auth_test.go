package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/store"
	ilog "github.com/kervanserver/kervan/internal/util/log"
)

func TestTOTPSetupEnableLoginAndDisable(t *testing.T) {
	srv, repo := newAuthTestServer(t, true)

	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	setupReq.Header.Set("X-Auth-User", "alice")
	setupRec := httptest.NewRecorder()
	srv.handleTOTPSetup(setupRec, setupReq)

	if setupRec.Code != http.StatusOK {
		t.Fatalf("expected setup 200, got %d: %s", setupRec.Code, setupRec.Body.String())
	}

	var setupPayload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupPayload); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	if setupPayload.Secret == "" {
		t.Fatal("expected generated totp secret")
	}

	code, err := auth.GenerateTOTPCode(setupPayload.Secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateTOTPCode: %v", err)
	}

	enableBody, _ := json.Marshal(map[string]string{"code": code})
	enableReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/enable", bytes.NewReader(enableBody))
	enableReq.Header.Set("X-Auth-User", "alice")
	enableRec := httptest.NewRecorder()
	srv.handleTOTPEnable(enableRec, enableReq)

	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected enable 200, got %d: %s", enableRec.Code, enableRec.Body.String())
	}

	loginBody, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "StrongPass123!",
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	srv.handleLogin(loginRec, loginReq)

	if loginRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected login without otp to fail, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	if !bytes.Contains(loginRec.Body.Bytes(), []byte(`"code":"totp_required"`)) {
		t.Fatalf("expected totp_required response, got %s", loginRec.Body.String())
	}
	afterFailedOTP, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername after failed otp: %v", err)
	}
	if afterFailedOTP == nil {
		t.Fatal("expected alice after failed otp")
	}
	if afterFailedOTP.LastLoginAt != nil {
		t.Fatalf("expected failed otp to avoid last_login update, got %#v", afterFailedOTP.LastLoginAt)
	}
	if afterFailedOTP.FailedLogins != 1 {
		t.Fatalf("expected failed otp to increment failed logins, got %d", afterFailedOTP.FailedLogins)
	}

	loginWithOTPBody, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "StrongPass123!",
		"otp":      code,
	})
	loginWithOTPReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginWithOTPBody))
	loginWithOTPRec := httptest.NewRecorder()
	srv.handleLogin(loginWithOTPRec, loginWithOTPReq)

	if loginWithOTPRec.Code != http.StatusOK {
		t.Fatalf("expected login with otp to succeed, got %d: %s", loginWithOTPRec.Code, loginWithOTPRec.Body.String())
	}
	afterSuccess, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername after successful otp: %v", err)
	}
	if afterSuccess == nil || afterSuccess.LastLoginAt == nil {
		t.Fatalf("expected successful otp login to update last_login, got %#v", afterSuccess)
	}
	if afterSuccess.FailedLogins != 0 || afterSuccess.LockedUntil != nil {
		t.Fatalf("expected successful otp login to clear failed counters, got %#v", afterSuccess)
	}

	disableBody, _ := json.Marshal(map[string]string{"code": code})
	disableReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/totp", bytes.NewReader(disableBody))
	disableReq.Header.Set("X-Auth-User", "alice")
	disableRec := httptest.NewRecorder()
	srv.handleTOTP(disableRec, disableReq)

	if disableRec.Code != http.StatusOK {
		t.Fatalf("expected disable 200, got %d: %s", disableRec.Code, disableRec.Body.String())
	}

	updated, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if updated == nil || updated.TOTPEnabled || updated.TOTPSecret != "" {
		t.Fatalf("expected totp to be removed, got %#v", updated)
	}
}

func TestTOTPSetupRequiresCurrentCodeWhenAlreadyEnabled(t *testing.T) {
	srv, repo := newAuthTestServer(t, true)

	initialSetupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	initialSetupReq.Header.Set("X-Auth-User", "alice")
	initialSetupRec := httptest.NewRecorder()
	srv.handleTOTPSetup(initialSetupRec, initialSetupReq)
	if initialSetupRec.Code != http.StatusOK {
		t.Fatalf("initial setup failed: %d %s", initialSetupRec.Code, initialSetupRec.Body.String())
	}

	var initialSetup struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(initialSetupRec.Body.Bytes(), &initialSetup); err != nil {
		t.Fatalf("decode initial setup: %v", err)
	}
	code, err := auth.GenerateTOTPCode(initialSetup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateTOTPCode: %v", err)
	}

	enableBody, _ := json.Marshal(map[string]string{"code": code})
	enableReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/enable", bytes.NewReader(enableBody))
	enableReq.Header.Set("X-Auth-User", "alice")
	enableRec := httptest.NewRecorder()
	srv.handleTOTPEnable(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable failed: %d %s", enableRec.Code, enableRec.Body.String())
	}

	missingCodeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	missingCodeReq.Header.Set("X-Auth-User", "alice")
	missingCodeRec := httptest.NewRecorder()
	srv.handleTOTPSetup(missingCodeRec, missingCodeReq)
	if missingCodeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing current code, got %d: %s", missingCodeRec.Code, missingCodeRec.Body.String())
	}

	invalidCodeBody := bytes.NewBufferString(`{"code":"000000"}`)
	invalidCodeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", invalidCodeBody)
	invalidCodeReq.Header.Set("X-Auth-User", "alice")
	invalidCodeRec := httptest.NewRecorder()
	srv.handleTOTPSetup(invalidCodeRec, invalidCodeReq)
	if invalidCodeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid current code, got %d: %s", invalidCodeRec.Code, invalidCodeRec.Body.String())
	}

	freshCode, err := auth.GenerateTOTPCode(initialSetup.Secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("GenerateTOTPCode (refresh): %v", err)
	}
	validCodeBody, _ := json.Marshal(map[string]string{"code": freshCode})
	validCodeReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", bytes.NewReader(validCodeBody))
	validCodeReq.Header.Set("X-Auth-User", "alice")
	validCodeRec := httptest.NewRecorder()
	srv.handleTOTPSetup(validCodeRec, validCodeReq)
	if validCodeRec.Code != http.StatusOK {
		t.Fatalf("expected setup refresh with valid code to succeed, got %d: %s", validCodeRec.Code, validCodeRec.Body.String())
	}

	user, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if user == nil {
		t.Fatal("expected user to exist")
	}
	if user.TOTPEnabled {
		t.Fatal("expected refreshed setup to move user into pending state")
	}
	if strings.TrimSpace(user.TOTPSecret) == "" {
		t.Fatal("expected refreshed setup to persist new secret")
	}
}

func TestHandleLoginRateLimitsByRemoteIP(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)

	for attempt := 0; attempt < 5; attempt++ {
		body, _ := json.Marshal(map[string]string{
			"username": "alice",
			"password": "wrong-password",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.10:12345"
		rec := httptest.NewRecorder()

		srv.handleLogin(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", attempt+1, rec.Code)
		}
	}

	body, _ := json.Marshal(map[string]string{
		"username": "alice",
		"password": "StrongPass123!",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.10:12345"
	rec := httptest.NewRecorder()

	srv.handleLogin(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header to be set")
	}
}

func TestHandleLoginRejectsOversizedJSONBody(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)

	largeValue := strings.Repeat("a", maxJSONBodyBytes+128)
	body, _ := json.Marshal(map[string]string{
		"username": largeValue,
		"password": "StrongPass123!",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected oversized login body to return 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleServerStatusRequiresAdmin(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	srv.status = func() map[string]any {
		return map[string]any{"name": "test"}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()
	srv.handleServerStatus(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleServerStatusAllowsAdmin(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	if _, err := srv.auth.CreateUser("admin", "StrongPass123!", "/", true); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	srv.status = func() map[string]any {
		return map[string]any{"name": "test"}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
	req.Header.Set("X-Auth-User", "admin")
	rec := httptest.NewRecorder()
	srv.handleServerStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWithMiddlewareAppliesSecurityHeadersAndExactCORS(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	srv.cfg.CORSOrigins = []string{"https://console.example.com"}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("X-Request-ID", "req-123")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()

	srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://console.example.com" {
		t.Fatalf("expected exact CORS origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("expected request id to be echoed, got %q", rec.Header().Get("X-Request-ID"))
	}
	if rec.Header().Get("Traceparent") != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("expected traceparent to be echoed, got %q", rec.Header().Get("Traceparent"))
	}
	if rec.Header().Get("X-Trace-ID") != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected trace id to be exposed, got %q", rec.Header().Get("X-Trace-ID"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff header, got %q", rec.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "X-API-Key") {
		t.Fatalf("expected X-API-Key to be allowed, got %q", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestWithMiddlewareRejectsDisallowedPreflight(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	srv.cfg.CORSOrigins = []string{"https://console.example.com"}

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/users", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "origin is not allowed") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestWithMiddlewareLogsCompletedRequests(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	var logs bytes.Buffer
	srv.logger = ilog.New("debug", "json", &logs)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Request-ID", "req-log-123")
	req.Header.Set("User-Agent", "kervan-test")
	req.RemoteAddr = "203.0.113.10:12345"
	rec := httptest.NewRecorder()

	srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Auth-User", "alice")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(rec, req)

	output := logs.String()
	if !strings.Contains(output, `"msg":"http request completed"`) {
		t.Fatalf("expected request completion log, got %s", output)
	}
	for _, needle := range []string{
		`"request_id":"req-log-123"`,
		`"trace_id":"`,
		`"traceparent":"00-`,
		`"route":"/api/v1/users"`,
		`"status":202`,
		`"auth_user":"alice"`,
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected log to contain %s, got %s", needle, output)
		}
	}
}

func TestWithMiddlewareGeneratesTraceContextWhenMissing(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.withMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	traceparent := rec.Header().Get("Traceparent")
	if !strings.HasPrefix(traceparent, "00-") {
		t.Fatalf("expected generated traceparent, got %q", traceparent)
	}
	if traceID := rec.Header().Get("X-Trace-ID"); len(traceID) != 32 {
		t.Fatalf("expected 32-char trace id, got %q", traceID)
	}
}

func TestStartConfiguresHTTPServerTimeouts(t *testing.T) {
	srv := &Server{
		cfg: Config{
			BindAddress:       "127.0.0.1",
			Port:              0,
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      45 * time.Second,
			IdleTimeout:       90 * time.Second,
		},
		httpMetrics: newHTTPMetrics(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Stop(context.Background())
	})

	if srv.httpServer == nil {
		t.Fatal("expected http server to be initialized")
	}
	if srv.httpServer.ReadTimeout != 15*time.Second {
		t.Fatalf("expected read timeout=15s, got=%s", srv.httpServer.ReadTimeout)
	}
	if srv.httpServer.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("expected read header timeout=10s, got=%s", srv.httpServer.ReadHeaderTimeout)
	}
	if srv.httpServer.WriteTimeout != 45*time.Second {
		t.Fatalf("expected write timeout=45s, got=%s", srv.httpServer.WriteTimeout)
	}
	if srv.httpServer.IdleTimeout != 90*time.Second {
		t.Fatalf("expected idle timeout=90s, got=%s", srv.httpServer.IdleTimeout)
	}
}

func TestApplyRuntimeConfigUpdatesHotSettings(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	srv.cfg.CORSOrigins = []string{"https://old.example.com"}
	srv.loginState["203.0.113.10"] = &loginAttempt{failures: 3}

	srv.ApplyRuntimeConfig(Config{
		SessionTimeout:       2 * time.Hour,
		CORSOrigins:          []string{"https://console.example.com"},
		TOTPEnabled:          true,
		BruteForceEnabled:    false,
		LoginMaxAttempts:     10,
		LoginLockoutDuration: 30 * time.Minute,
	})

	cfg := srv.currentConfig()
	if cfg.SessionTimeout != 2*time.Hour {
		t.Fatalf("expected session timeout=2h, got=%s", cfg.SessionTimeout)
	}
	if !cfg.TOTPEnabled {
		t.Fatal("expected totp to be enabled after runtime config apply")
	}
	if cfg.BruteForceEnabled {
		t.Fatal("expected brute force protection to be disabled after runtime config apply")
	}
	if cfg.LoginMaxAttempts != 10 {
		t.Fatalf("expected login max attempts=10, got=%d", cfg.LoginMaxAttempts)
	}
	if cfg.LoginLockoutDuration != 30*time.Minute {
		t.Fatalf("expected lockout duration=30m, got=%s", cfg.LoginLockoutDuration)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "https://console.example.com" {
		t.Fatalf("unexpected cors origins after runtime config apply: %v", cfg.CORSOrigins)
	}
	if len(srv.loginState) != 0 {
		t.Fatalf("expected login state to be cleared when brute force protection is disabled, got %#v", srv.loginState)
	}
}

func TestWithAuthAcceptsAPIKeyAndTracksUsage(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	token, created, err := srv.apiKeys.Create(alice.ID, "CI key", "read-write")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
	req.Header.Set("Authorization", "ApiKey "+token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, r *http.Request) {
		if got := currentUser(r); got != "alice" {
			t.Fatalf("expected authenticated user alice, got %q", got)
		}
		if got := r.Header.Get("X-Auth-Method"); got != "api-key" {
			t.Fatalf("expected auth method api-key, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	keys, err := srv.apiKeys.ListByUser(alice.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != created.ID || keys[0].LastUsedAt == nil {
		t.Fatalf("expected api key usage to be tracked, got %#v", keys)
	}
}

func TestWithAuthRejectsWriteRequestsForReadOnlyAPIKey(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	token, _, err := srv.apiKeys.Create(alice.ID, "Read only key", "read-only")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/mkdir", strings.NewReader(`{}`))
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Fatalf("expected read-only error, got %s", rec.Body.String())
	}
}

func TestWithAuthAllowsScopedAPIKeyForMatchingEndpoint(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	token, _, err := srv.apiKeys.Create(alice.ID, "Server reader", "server:read")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestWithAuthRejectsScopedAPIKeyForWrongEndpoint(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	token, _, err := srv.apiKeys.Create(alice.ID, "Server reader", "server:read")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/transfers", nil)
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "required scope") {
		t.Fatalf("expected scope error, got %s", rec.Body.String())
	}
}

func TestWithAuthRejectsAPIKeyForAuthEndpoints(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	token, _, err := srv.apiKeys.Create(alice.ID, "Wide key", "read-write")
	if err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not allowed") {
		t.Fatalf("expected endpoint restriction error, got %s", rec.Body.String())
	}
}

func TestWithAuthRejectsBearerTokenForDisabledUser(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	alice.Enabled = false
	if err := repo.Update(alice); err != nil {
		t.Fatalf("disable alice: %v", err)
	}

	token, err := signToken(srv.secret, "alice", time.Hour)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.withAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleAPIKeysIncludesScopeMetadata(t *testing.T) {
	srv, repo := newAuthTestServer(t, false)

	alice, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if _, _, err := srv.apiKeys.Create(alice.ID, "CI key", "read-only"); err != nil {
		t.Fatalf("Create api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apikeys", nil)
	req.Header.Set("X-Auth-User", "alice")
	rec := httptest.NewRecorder()

	srv.handleAPIKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Keys            []map[string]any `json:"keys"`
		SupportedScopes []map[string]any `json:"supported_scopes"`
		Presets         []map[string]any `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Keys) != 1 {
		t.Fatalf("expected one key, got %#v", payload.Keys)
	}
	if len(payload.SupportedScopes) == 0 {
		t.Fatal("expected supported scopes metadata")
	}
	if len(payload.Presets) == 0 {
		t.Fatal("expected presets metadata")
	}
}

func newAuthTestServer(t *testing.T, totpEnabled bool) (*Server, *auth.UserRepository) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "argon2id", 5, 15*time.Minute)
	if _, err := engine.CreateUser("alice", "StrongPass123!", "/", false); err != nil {
		t.Fatalf("create alice: %v", err)
	}

	return &Server{
		cfg: Config{
			TOTPEnabled:          totpEnabled,
			SessionTimeout:       time.Hour,
			BruteForceEnabled:    true,
			LoginMaxAttempts:     5,
			LoginLockoutDuration: 15 * time.Minute,
		},
		auth:       engine,
		users:      repo,
		apiKeys:    NewAPIKeyRepository(st),
		secret:     []byte("0123456789abcdef0123456789abcdef"),
		loginState: make(map[string]*loginAttempt),
	}, repo
}

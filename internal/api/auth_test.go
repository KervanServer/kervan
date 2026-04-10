package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/store"
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
		cfg:    Config{TOTPEnabled: totpEnabled, SessionTimeout: time.Hour},
		auth:   engine,
		users:  repo,
		secret: []byte("0123456789abcdef0123456789abcdef"),
	}, repo
}

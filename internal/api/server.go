package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/events"
	"github.com/kervanserver/kervan/internal/netguard"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"github.com/kervanserver/kervan/internal/webui"
)

const (
	collSystemSecrets       = "system_secrets"
	keySessionSigningSecret = "session_signing_secret"
	maxJSONBodyBytes        = 1 << 20
)

type Config struct {
	BindAddress          string
	Port                 int
	SessionTimeout       time.Duration
	CORSOrigins          []string
	ReadTimeout          time.Duration
	ReadHeaderTimeout    time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	TOTPEnabled          bool
	TLS                  bool
	TLSConfig            *tls.Config
	BruteForceEnabled    bool
	LoginMaxAttempts     int
	LoginLockoutDuration time.Duration
	// IPFilter rejects requests from addresses outside security.allowed_ips or
	// inside security.denied_ips. It is consulted per request, so updating
	// the filter in place takes effect immediately. nil admits everything.
	IPFilter *netguard.IPFilter
	// QuotaEnabled and DefaultMaxStorage mirror quota.enabled and
	// quota.default_max_storage for reporting effective user quotas.
	QuotaEnabled      bool
	DefaultMaxStorage int64
	// DefaultUserRate mirrors bandwidth.default_user_rate (bytes/s).
	DefaultUserRate int64
	// DefaultMaxFiles mirrors quota.default_max_files.
	DefaultMaxFiles int64
	// OIDC enables WebUI sign-in through an OpenID provider; nil disables it.
	OIDC *OIDCSettings
	// Audit records WebUI/API logins and administrative actions.
	Audit *audit.Engine
	// Events drives WebSocket pushes; nil falls back to polling.
	Events *events.Broker
}

type StatusProvider func() map[string]any
type UserFSBuilder func(username string) (vfs.FileSystem, error)
type ServerConfigProvider func() map[string]any
type ReloadProvider func() (map[string]any, error)
type ConfigUpdateProvider func(patch map[string]any) (map[string]any, error)
type ConfigValidateProvider func(patch map[string]any) (map[string]any, error)

type Server struct {
	cfg          Config
	cfgMu        sync.RWMutex
	logger       *slog.Logger
	auth         *auth.Engine
	users        *auth.UserRepository
	sessions     *session.Manager
	status       StatusProvider
	config       ServerConfigProvider
	reload       ReloadProvider
	configUpdate ConfigUpdateProvider
	configCheck  ConfigValidateProvider
	apiKeys      *APIKeyRepository
	groups       *auth.GroupRepository
	oidc         *OIDCSettings
	events       *events.Broker
	audit        *audit.Engine
	oidcGrants   oidcGrants
	shareLinks   *shareLinkRepository
	fsBuilder    UserFSBuilder
	store        *store.Store
	auditLogPath string
	secret       []byte
	transfers    *transfer.Manager
	uiHandler    http.Handler
	loginState   map[string]*loginAttempt
	httpMetrics  *httpMetrics

	httpServer *http.Server
	mu         sync.Mutex
	closed     bool
}

type loginAttempt struct {
	failures     int
	lastFailedAt time.Time
	blockedUntil time.Time
}

type traceContext struct {
	TraceID     string
	ParentID    string
	Traceparent string
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func NewServer(
	cfg Config,
	logger *slog.Logger,
	authEngine *auth.Engine,
	userRepo *auth.UserRepository,
	sessions *session.Manager,
	status StatusProvider,
	configProvider ServerConfigProvider,
	reloadProvider ReloadProvider,
	updateProvider ConfigUpdateProvider,
	validateProvider ConfigValidateProvider,
	keyStore *store.Store,
	fsBuilder UserFSBuilder,
	auditLogPath string,
	transfers *transfer.Manager,
) (*Server, error) {
	if cfg.BindAddress == "" {
		cfg.BindAddress = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	secret, err := loadOrCreateSessionSecret(keyStore)
	if err != nil {
		return nil, err
	}
	uiHandler, err := webui.NewHandler()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:          cfg,
		logger:       logger,
		auth:         authEngine,
		users:        userRepo,
		sessions:     sessions,
		status:       status,
		config:       configProvider,
		reload:       reloadProvider,
		configUpdate: updateProvider,
		configCheck:  validateProvider,
		apiKeys:      NewAPIKeyRepository(keyStore),
		groups:       auth.NewGroupRepository(keyStore, userRepo),
		oidc:         cfg.OIDC,
		events:       cfg.Events,
		audit:        cfg.Audit,
		shareLinks:   newShareLinkRepository(keyStore),
		fsBuilder:    fsBuilder,
		store:        keyStore,
		auditLogPath: auditLogPath,
		secret:       secret,
		transfers:    transfers,
		uiHandler:    uiHandler,
		loginState:   make(map[string]*loginAttempt),
		httpMetrics:  newHTTPMetrics(),
	}, nil
}

func (s *Server) currentConfig() Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return cloneAPIConfig(s.cfg)
}

func (s *Server) ipFilter() *netguard.IPFilter {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.IPFilter
}

func (s *Server) ApplyRuntimeConfig(cfg Config) {
	s.cfgMu.Lock()
	prevBruteForceEnabled := s.cfg.BruteForceEnabled
	s.cfg.SessionTimeout = cfg.SessionTimeout
	s.cfg.CORSOrigins = append([]string(nil), cfg.CORSOrigins...)
	s.cfg.TOTPEnabled = cfg.TOTPEnabled
	s.cfg.BruteForceEnabled = cfg.BruteForceEnabled
	s.cfg.LoginMaxAttempts = cfg.LoginMaxAttempts
	s.cfg.LoginLockoutDuration = cfg.LoginLockoutDuration
	s.cfg.DefaultUserRate = cfg.DefaultUserRate
	s.cfgMu.Unlock()

	if prevBruteForceEnabled && !cfg.BruteForceEnabled {
		s.mu.Lock()
		s.loginState = make(map[string]*loginAttempt)
		s.mu.Unlock()
	}
}

func cloneAPIConfig(cfg Config) Config {
	out := cfg
	if cfg.CORSOrigins != nil {
		out.CORSOrigins = append([]string(nil), cfg.CORSOrigins...)
	}
	return out
}

func loadOrCreateSessionSecret(st *store.Store) ([]byte, error) {
	if st == nil {
		return randomSessionSecret()
	}

	var encoded string
	err := st.Get(collSystemSecrets, keySessionSigningSecret, &encoded)
	switch {
	case err == nil:
		secret, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode session signing secret: %w", decodeErr)
		}
		if len(secret) < 32 {
			return nil, errors.New("stored session signing secret is too short")
		}
		return secret, nil
	case errors.Is(err, store.ErrNotFound):
		secret, genErr := randomSessionSecret()
		if genErr != nil {
			return nil, genErr
		}
		if putErr := st.Put(collSystemSecrets, keySessionSigningSecret, base64.RawURLEncoding.EncodeToString(secret)); putErr != nil {
			return nil, putErr
		}
		return secret, nil
	default:
		return nil, err
	}
}

func randomSessionSecret() ([]byte, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func (s *Server) Start(ctx context.Context) error {
	cfg := s.currentConfig()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/metrics", s.handleMetrics)

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/methods", s.handleAuthMethods)
	mux.HandleFunc("/api/v1/auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("/api/v1/auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("/api/v1/auth/oidc/exchange", s.handleOIDCExchange)
	mux.HandleFunc("/api/v1/auth/totp", s.withAuth(s.handleTOTP))
	mux.HandleFunc("/api/v1/auth/totp/setup", s.withAuth(s.handleTOTPSetup))
	mux.HandleFunc("/api/v1/auth/totp/enable", s.withAuth(s.handleTOTPEnable))
	mux.HandleFunc("/api/ws", s.handleWebSocket)
	mux.HandleFunc("/api/v1/ws", s.handleWebSocket)

	mux.HandleFunc("/api/server/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/v1/server/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/v1/server/config", s.withAuth(s.handleServerConfig))
	mux.HandleFunc("/api/v1/server/config/validate", s.withAuth(s.handleServerConfigValidate))
	mux.HandleFunc("/api/v1/server/reload", s.withAuth(s.handleServerReload))

	mux.HandleFunc("/api/users", s.withAuth(s.handleUsers))
	mux.HandleFunc("/api/v1/users", s.withAuth(s.handleUsers))
	mux.HandleFunc("/api/users/import", s.withAuth(s.handleUsersImport))
	mux.HandleFunc("/api/v1/users/import", s.withAuth(s.handleUsersImport))
	mux.HandleFunc("/api/users/export", s.withAuth(s.handleUsersExport))
	mux.HandleFunc("/api/v1/users/export", s.withAuth(s.handleUsersExport))
	mux.HandleFunc("/api/v1/account", s.withAuth(s.handleAccount))
	mux.HandleFunc("/api/v1/account/password", s.withAuth(s.handleAccountPassword))
	mux.HandleFunc("/api/v1/account/keys", s.withAuth(s.handleAccountKeys))
	mux.HandleFunc("/api/groups", s.withAuth(s.handleGroups))
	mux.HandleFunc("/api/v1/groups", s.withAuth(s.handleGroups))
	mux.HandleFunc("/api/apikeys", s.withAuth(s.handleAPIKeys))
	mux.HandleFunc("/api/v1/apikeys", s.withAuth(s.handleAPIKeys))

	mux.HandleFunc("/api/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/v1/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/sessions/", s.withAuth(s.handleSessionByID))
	mux.HandleFunc("/api/v1/sessions/", s.withAuth(s.handleSessionByID))

	mux.HandleFunc("/api/files/list", s.withAuth(s.handleFilesList))
	mux.HandleFunc("/api/files/mkdir", s.withAuth(s.handleFilesMkdir))
	mux.HandleFunc("/api/files/delete", s.withAuth(s.handleFilesDelete))
	mux.HandleFunc("/api/files/rename", s.withAuth(s.handleFilesRename))
	mux.HandleFunc("/api/files/upload", s.withAuth(s.handleFilesUpload))
	mux.HandleFunc("/api/files/download", s.withAuth(s.handleFilesDownload))
	mux.HandleFunc("/api/v1/files/", s.withAuth(s.handleFilesV1))
	mux.HandleFunc("/api/share", s.withAuth(s.handleShareLinks))
	mux.HandleFunc("/api/v1/share", s.withAuth(s.handleShareLinks))
	mux.HandleFunc("/api/share/", s.handleShareDownload)
	mux.HandleFunc("/api/v1/share/", s.handleShareDownload)

	mux.HandleFunc("/api/audit", s.withAuth(s.handleAudit))
	mux.HandleFunc("/api/v1/audit/events", s.withAuth(s.handleAudit))
	mux.HandleFunc("/api/audit/export", s.withAuth(s.handleAuditExport))
	mux.HandleFunc("/api/v1/audit/export", s.withAuth(s.handleAuditExport))

	mux.HandleFunc("/api/transfers", s.withAuth(s.handleTransfers))
	mux.HandleFunc("/api/v1/transfers", s.withAuth(s.handleTransfers))

	if s.uiHandler != nil {
		mux.Handle("/", s.uiHandler)
	}

	handler := s.withMiddleware(mux)
	s.httpServer = &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddress, itoa(cfg.Port)),
		Handler:           handler,
		TLSConfig:         cfg.TLSConfig,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	if cfg.TLS && s.httpServer.TLSConfig == nil {
		return errors.New("webui tls is enabled but no tls config is configured")
	}
	// Bind synchronously so Start reports a busy port to the caller and the
	// API is reachable as soon as Start returns.
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	// #nosec G118 -- server shutdown intentionally uses a detached context.
	go func() {
		<-ctx.Done()
		_ = s.Stop(context.Background())
	}()

	go func() {
		if s.logger != nil {
			s.logger.Info("API server started", "addr", ln.Addr().String())
		}
		var err error
		if cfg.TLS {
			err = s.httpServer.ServeTLS(ln, "", "")
		} else {
			err = s.httpServer.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) && s.logger != nil {
			s.logger.Error("api server failed", "error", err)
		}
	}()
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.httpServer == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(shutdownCtx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.buildHealthResponse())
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	clientIP := remoteIP(r.RemoteAddr)
	if retryAfter, limited := s.loginRateLimitStatus(clientIP, time.Now().UTC()); limited {
		w.Header().Set("Retry-After", itoa(int(retryAfter.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	throttle := s.auth.IPThrottle()
	if banErr := throttle.Check(r.RemoteAddr, time.Now()); banErr != nil {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts"})
		return
	}
	user, err := s.auth.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		s.recordLoginFailure(clientIP, time.Now().UTC())
		throttle.RecordFailure(r.RemoteAddr, time.Now())
		s.emitAuditFor(r, req.Username, audit.EventAuthFailure, "", "failed", err.Error(), nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if cfg.TOTPEnabled && user.TOTPEnabled {
		if !auth.ValidateTOTP(user.TOTPSecret, req.OTP, time.Now().UTC(), 1) {
			_ = s.auth.RecordFailedLogin(user.ID)
			s.recordLoginFailure(clientIP, time.Now().UTC())
			if strings.TrimSpace(req.OTP) != "" {
				throttle.RecordFailure(r.RemoteAddr, time.Now())
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "two-factor code required",
				"code":  "totp_required",
			})
			return
		}
	}
	s.clearLoginFailures(clientIP)
	throttle.RecordSuccess(r.RemoteAddr)
	if accessErr := user.CheckAccess(r.RemoteAddr, time.Now(), auth.ProtocolWebUI); accessErr != nil {
		s.emitAuditFor(r, user.Username, audit.EventAuthFailure, "", "failed", accessErr.Error(), nil)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": accessErr.Error(), "code": "access_denied"})
		return
	}
	token, err := signToken(s.secret, user.Username, cfg.SessionTimeout)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	_ = s.auth.RecordSuccessfulLogin(user.ID)
	s.emitAuditFor(r, user.Username, audit.EventAuthSuccess, "", "ok", "webui login", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"type":     user.Type,
		},
	})
}

// userEditError marks a validation failure raised inside a
// UserRepository.Mutate callback, so handlers answer 400 rather than 500.
type userEditError struct{ err error }

func (e userEditError) Error() string { return e.err.Error() }
func (e userEditError) Unwrap() error { return e.err }

func userEditStatus(err error) int {
	var editErr userEditError
	if errors.As(err, &editErr) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

var errTOTPChanged = errors.New("two-factor settings changed during the request; retry")

// mutateTOTP applies change to the stored user only while its two-factor
// state still matches seen, the copy the request's code was validated
// against. It writes the error response and reports false on failure.
func (s *Server) mutateTOTP(w http.ResponseWriter, seen *auth.User, change func(*auth.User)) bool {
	saved, err := s.users.Mutate(seen.ID, func(u *auth.User) error {
		if u.TOTPEnabled != seen.TOTPEnabled || u.TOTPSecret != seen.TOTPSecret {
			return errTOTPChanged
		}
		change(u)
		return nil
	})
	switch {
	case errors.Is(err, errTOTPChanged):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	case saved == nil:
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
	default:
		return true
	}
	return false
}

func (s *Server) handleTOTP(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if !cfg.TOTPEnabled {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "pending": false})
		case http.MethodDelete:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "two-factor auth is disabled"})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
		return
	}
	user, err := s.users.GetByUsername(currentUser(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": user.TOTPEnabled,
			"pending": !user.TOTPEnabled && strings.TrimSpace(user.TOTPSecret) != "",
		})
	case http.MethodDelete:
		if user.TOTPEnabled {
			var req struct {
				Code string `json:"code"`
			}
			if err := decodeJSONBody(w, r, &req, true); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
			if !auth.ValidateTOTP(user.TOTPSecret, req.Code, time.Now().UTC(), 1) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid two-factor code"})
				return
			}
		}
		hadEnabled := user.TOTPEnabled
		if !s.mutateTOTP(w, user, func(u *auth.User) {
			u.TOTPEnabled = false
			u.TOTPSecret = ""
		}) {
			return
		}
		if hadEnabled {
			// Disabling an active second factor is a security-posture change
			// and is audited like the other account mutations; clearing a
			// never-enabled pending secret is not.
			s.emitAudit(r, audit.EventTOTPChange, "", "ok", "two-factor disabled", map[string]string{"action": "disabled"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"disabled": true})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !cfg.TOTPEnabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "two-factor auth is disabled"})
		return
	}
	user, err := s.users.GetByUsername(currentUser(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	if user.TOTPEnabled {
		var req struct {
			Code string `json:"code"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current two-factor code is required"})
			return
		}
		if !auth.ValidateTOTP(user.TOTPSecret, req.Code, time.Now().UTC(), 1) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid current two-factor code"})
			return
		}
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate secret"})
		return
	}
	rotated := user.TOTPEnabled
	if !s.mutateTOTP(w, user, func(u *auth.User) {
		u.TOTPSecret = secret
		u.TOTPEnabled = false
	}) {
		return
	}
	if rotated {
		// Rotating the secret of an ENABLED factor is a security-posture
		// change and is audited; a first-time setup only prepares a pending
		// secret and changes nothing until enable.
		s.emitAudit(r, audit.EventTOTPChange, "", "ok", "two-factor secret rotated", map[string]string{"action": "rotated"})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      false,
		"pending":      true,
		"secret":       secret,
		"otpauth_url":  auth.TOTPProvisioningURL("Kervan", user.Username, secret),
		"username":     user.Username,
		"issuer":       "Kervan",
		"generated_at": time.Now().UTC(),
	})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !cfg.TOTPEnabled {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "two-factor auth is disabled"})
		return
	}
	user, err := s.users.GetByUsername(currentUser(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	if strings.TrimSpace(user.TOTPSecret) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "two-factor setup has not been prepared"})
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if !auth.ValidateTOTP(user.TOTPSecret, req.Code, time.Now().UTC(), 1) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid two-factor code"})
		return
	}
	// Enable only the secret the code was just checked against.
	if !s.mutateTOTP(w, user, func(u *auth.User) { u.TOTPEnabled = true }) {
		return
	}
	s.emitAudit(r, audit.EventTOTPChange, "", "ok", "two-factor enabled", map[string]string{"action": "enabled"})
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"pending": false,
	})
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	resp := map[string]any{
		"time": time.Now().UTC(),
	}
	if s.status != nil {
		for k, v := range s.status() {
			resp[k] = v
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleServerConfig(w http.ResponseWriter, r *http.Request) {
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		if s.config == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "server config provider is not available"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config": s.config(),
		})
	case http.MethodPut:
		if s.configUpdate == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "server config update handler is not available"})
			return
		}
		var patch map[string]any
		if err := decodeJSONBody(w, r, &patch, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := s.configUpdate(patch)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if result == nil {
			result = map[string]any{}
		}
		result["updated_at"] = time.Now().UTC()
		s.emitAudit(r, audit.EventAdminConfigUpdate, "", "ok", "configuration updated", nil)
		writeJSON(w, http.StatusOK, result)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleServerReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	if s.reload == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "reload handler is not available"})
		return
	}
	result, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if result == nil {
		result = map[string]any{}
	}
	result["requested_at"] = time.Now().UTC()
	s.emitAudit(r, audit.EventAdminConfigReload, "", "ok", "configuration reloaded", nil)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleServerConfigValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	if s.configCheck == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "server config validate handler is not available"})
		return
	}
	var patch map[string]any
	if err := decodeJSONBody(w, r, &patch, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	result, err := s.configCheck(patch)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if result == nil {
		result = map[string]any{}
	}
	result["validated_at"] = time.Now().UTC()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !s.isAdminUser(currentUser(r)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := s.users.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list users failed"})
			return
		}
		out := make([]userResponse, 0, len(users))
		for _, u := range users {
			if u == nil {
				continue
			}
			out = append(out, s.userResponse(u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			HomeDir  string `json:"home_dir"`
			Admin    bool   `json:"admin"`
			userPolicyPatch
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.HomeDir == "" {
			req.HomeDir = "/"
		}
		// Resolve groups and validate before creating anything; the
		// transaction below must not read the store itself.
		policy, err := s.resolveUserPolicyGroups(req.userPolicyPatch)
		if err == nil {
			err = applyResolvedUserPolicy(&auth.User{}, policy)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		user, err := s.auth.CreateUser(req.Username, req.Password, req.HomeDir, req.Admin)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if policy.set() {
			saved, err := s.users.Mutate(user.ID, func(u *auth.User) error {
				return applyResolvedUserPolicy(u, policy)
			})
			if err == nil && saved == nil {
				err = errors.New("user not found")
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			user = saved
		}
		s.emitAudit(r, audit.EventAdminUserCreate, "", "ok", "user created", map[string]string{"target_user": user.Username, "admin": strconv.FormatBool(req.Admin)})
		writeJSON(w, http.StatusCreated, s.userResponse(user))
	case http.MethodPut:
		var req struct {
			ID             string    `json:"id"`
			HomeDir        string    `json:"home_dir"`
			Enabled        *bool     `json:"enabled"`
			Admin          *bool     `json:"admin"`
			Password       string    `json:"password"`
			AuthorizedKeys *[]string `json:"authorized_keys"`
			userPolicyPatch
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
			return
		}
		// Validate everything that needs no user record (or reads the store)
		// up front; the edits are then applied to the latest stored record in
		// one transaction, so fields this request does not touch keep any
		// concurrent change (a login, a lockout, another admin's edit).
		var (
			homeDir string
			err     error
		)
		if strings.TrimSpace(req.HomeDir) != "" {
			homeDir, err = auth.NormalizeHomeDir(req.HomeDir)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		var keys []string
		if req.AuthorizedKeys != nil {
			keys, err = auth.NormalizeAuthorizedKeys(*req.AuthorizedKeys)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		policy, err := s.resolveUserPolicyGroups(req.userPolicyPatch)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.Password != "" {
			// Reject a weak password before saving any of the other edits.
			if err := s.auth.ValidatePassword(req.Password); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		user, err := s.users.Mutate(strings.TrimSpace(req.ID), func(u *auth.User) error {
			if req.Password != "" && !passwordManaged(u) {
				return userEditError{auth.ErrPasswordNotManaged}
			}
			if homeDir != "" {
				u.HomeDir = homeDir
			}
			if req.Enabled != nil {
				u.Enabled = *req.Enabled
			}
			if err := applyResolvedUserPolicy(u, policy); err != nil {
				return userEditError{err}
			}
			if req.AuthorizedKeys != nil {
				u.AuthorizedKeys = keys
			}
			if req.Admin != nil {
				if *req.Admin {
					u.Type = auth.UserTypeAdmin
				} else {
					u.Type = auth.UserTypeVirtual
				}
			}
			return nil
		})
		if err != nil {
			writeJSON(w, userEditStatus(err), map[string]string{"error": err.Error()})
			return
		}
		if user == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if req.Password != "" {
			// ResetPassword is its own transaction touching only credentials.
			if err := s.auth.ResetPassword(user.Username, req.Password); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			// Old credentials must not keep protocol sessions alive.
			s.terminateUserSessions(user.Username)
			reloaded, err := s.users.GetByID(user.ID)
			if err != nil || reloaded == nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload user failed"})
				return
			}
			user = reloaded
		}
		if req.Enabled != nil && !*req.Enabled {
			s.terminateUserSessions(user.Username)
		}
		changes := map[string]string{"target_user": user.Username}
		if req.Password != "" {
			changes["password_reset"] = "true"
		}
		if req.Enabled != nil {
			changes["enabled"] = strconv.FormatBool(*req.Enabled)
		}
		if req.Admin != nil {
			changes["admin"] = strconv.FormatBool(*req.Admin)
		}
		if req.AuthorizedKeys != nil {
			changes["authorized_keys"] = strconv.Itoa(len(user.AuthorizedKeys))
		}
		if req.PrimaryGroup != nil {
			changes["primary_group"] = user.PrimaryGroup
		}
		if req.ExpiresAt != nil {
			changes["expires_at"] = "never"
			if user.ExpiresAt != nil {
				changes["expires_at"] = user.ExpiresAt.Format(time.RFC3339)
			}
		}
		if req.AllowedIPs != nil {
			changes["allowed_ips"] = strings.Join(user.AllowedIPs, ",")
		}
		if req.AllowedProtocols != nil {
			changes["allowed_protocols"] = strings.Join(user.AllowedProtocols, ",")
		}
		if req.MaxSessions != nil {
			changes["max_sessions"] = strconv.Itoa(user.MaxSessions)
		}
		s.emitAudit(r, audit.EventAdminUserUpdate, "", "ok", "user updated", changes)
		writeJSON(w, http.StatusOK, s.userResponse(user))
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
			return
		}
		user, err := s.users.GetByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if user == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		if err := s.users.Delete(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.terminateUserSessions(user.Username)
		s.emitAudit(r, audit.EventAdminUserDelete, "", "ok", "user deleted", map[string]string{"target_user": user.Username})
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.sessions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
		return
	}
	viewer := currentUser(r)
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	remoteIP := strings.TrimSpace(r.URL.Query().Get("ip"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": filterSessions(
			filterSessionsByUsername(s.sessions.List(), s.viewerScopedUsername(viewer, r.URL.Query().Get("username"))),
			protocol,
			remoteIP,
			query,
		),
	})
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/"), "/api/sessions/"), "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	if s.sessions == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	sessionItem := s.sessions.Get(id)
	if sessionItem == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	viewer := currentUser(r)
	if !s.isAdminUser(viewer) && !strings.EqualFold(viewer, sessionItem.Username) {
		// Same answer as a missing session, so IDs cannot be probed.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, sessionItem)
	case http.MethodDelete:
		if !s.sessions.Kill(id) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		s.emitAudit(r, audit.EventAdminSessionKill, "", "ok", "session terminated", map[string]string{"session_id": id})
		writeJSON(w, http.StatusOK, map[string]any{"killed": true, "id": id})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	if s.apiKeys == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "api keys are not configured"})
		return
	}
	authenticated := currentUser(r)
	user, err := s.users.GetByUsername(authenticated)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys, err := s.apiKeys.ListByUser(user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		response := make([]map[string]any, 0, len(keys))
		for _, item := range keys {
			if item == nil {
				continue
			}
			response = append(response, map[string]any{
				"id":          item.ID,
				"name":        item.Name,
				"permissions": item.Permissions,
				"prefix":      item.Prefix,
				"created_at":  item.CreatedAt,
				"last_used":   item.LastUsedAt,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"keys":             response,
			"supported_scopes": apiKeySupportedScopeInfo(),
			"presets":          apiKeyPresets(),
		})
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Permissions string `json:"permissions"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		raw, created, err := s.apiKeys.Create(user.ID, req.Name, req.Permissions)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.emitAudit(r, audit.EventAdminAPIKeyCreate, "", "ok", "api key created", map[string]string{"name": req.Name, "permissions": created.Permissions})
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          created.ID,
			"name":        created.Name,
			"permissions": created.Permissions,
			"prefix":      created.Prefix,
			"created_at":  created.CreatedAt,
			"key":         raw,
		})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
			return
		}
		if err := s.apiKeys.Delete(user.ID, id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrAPIKeyNotFound) {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		s.emitAudit(r, audit.EventAdminAPIKeyDelete, "", "ok", "api key revoked", map[string]string{"key_id": id})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p := normalizeAPIPath(r.URL.Query().Get("path"))
	entries, err := fsys.ReadDir(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	type fileEntry struct {
		Name    string      `json:"name"`
		Path    string      `json:"path"`
		IsDir   bool        `json:"is_dir"`
		Size    int64       `json:"size"`
		Mode    fs.FileMode `json:"mode"`
		ModTime time.Time   `json:"mod_time"`
	}
	out := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		out = append(out, fileEntry{
			Name:    e.Name(),
			Path:    normalizeAPIPath(path.Join(p, e.Name())),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    p,
		"entries": out,
	})
}

func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSONBody(w, r, &req, false); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	p := normalizeAPIPath(req.Path)
	if err := fsys.MkdirAll(p, 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.emitAudit(r, audit.EventDirCreate, p, "ok", "mkdir", nil)
	writeJSON(w, http.StatusCreated, map[string]any{"path": p})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	p := normalizeAPIPath(rawPath)
	recursive := strings.EqualFold(r.URL.Query().Get("recursive"), "true")
	if recursive {
		err = fsys.RemoveAll(p)
	} else {
		err = fsys.Remove(p)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.emitAudit(r, audit.EventFileDelete, p, "ok", "delete", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rawFrom := strings.TrimSpace(r.URL.Query().Get("from"))
	rawTo := strings.TrimSpace(r.URL.Query().Get("to"))
	if rawFrom == "" || rawTo == "" {
		var req struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and to are required"})
			return
		}
		rawFrom = strings.TrimSpace(req.From)
		rawTo = strings.TrimSpace(req.To)
	}
	if rawFrom == "" || rawTo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and to are required"})
		return
	}
	from := normalizeAPIPath(rawFrom)
	to := normalizeAPIPath(rawTo)
	if err := fsys.Rename(from, to); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.emitAudit(r, audit.EventFileRename, from, "ok", "rename", map[string]string{"to": to})
	writeJSON(w, http.StatusOK, map[string]any{
		"from":       from,
		"to":         to,
		"renamed_at": time.Now().UTC(),
	})
}

func (s *Server) handleFilesShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.shareLinks == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "share links are not configured"})
		return
	}

	var req struct {
		Path         string `json:"path"`
		TTL          string `json:"ttl"`
		MaxDownloads int    `json:"max_downloads"`
		// Password is accepted only in the body, never the query string,
		// so it stays out of access logs.
		Password string `json:"password"`
	}
	if !isBodyEmpty(r) {
		if err := decodeJSONBody(w, r, &req, false); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
	}

	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		rawPath = strings.TrimSpace(req.Path)
	}
	if rawPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	p := normalizeAPIPath(rawPath)

	rawTTL := strings.TrimSpace(r.URL.Query().Get("ttl"))
	if rawTTL == "" {
		rawTTL = strings.TrimSpace(req.TTL)
	}
	ttl, err := parseTTL(rawTTL, 24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	maxDownloads := req.MaxDownloads
	if q := strings.TrimSpace(r.URL.Query().Get("max_downloads")); q != "" {
		maxDownloads = parseInt(q, req.MaxDownloads)
	}

	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	info, err := fsys.Stat(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sharing directories is not supported"})
		return
	}

	link, err := s.shareLinks.Create(user, p, ttl, maxDownloads, req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.emitAudit(r, audit.EventShareCreate, p, "ok", "share link created", map[string]string{
		"share":              shortToken(link.Token),
		"expires_at":         link.ExpiresAt.Format(time.RFC3339),
		"max_downloads":      strconv.Itoa(link.MaxDownloads),
		"password_protected": strconv.FormatBool(link.Protected()),
	})
	writeJSON(w, http.StatusCreated, shareLinkJSON(link))
}

// uploadTempPrefix names the sibling file handleFilesUpload streams into
// before renaming it over the target. It is a PREFIX of the target's base
// name, so the staging file's extension equals the target's: UserVFS.Open
// applies the Allowed/DeniedExts policy to the name being opened, and a
// ".tmp"-suffixed staging name would bypass denied extensions and reject
// allowed ones.
const uploadTempPrefix = "kervan-upload-"

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	p := normalizeAPIPath(rawPath)
	// Stream into a sibling temp file and rename it into place only after the
	// whole body lands. O_TRUNC is applied by the backend at Open time, so
	// writing straight into p destroyed the previous contents the moment an
	// upload was rejected partway — a client disconnect, a quota rejection or a
	// disk error left the target truncated to a partial prefix even though the
	// handler answered 500 and accepted nothing.
	// Each request owns its staging name: a fixed sibling name could truncate
	// and then remove an unrelated user file. Keep the target extension so
	// extension policies still apply, and refuse existing files where supported.
	tmpPath := path.Join(path.Dir(p), uploadTempPrefix+rand.Text()+"-"+path.Base(p))
	tmp, err := fsys.Open(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	n, copyErr := io.Copy(tmp, r.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = fsys.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": copyErr.Error()})
		return
	}
	if closeErr != nil {
		_ = fsys.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": closeErr.Error()})
		return
	}
	if err := fsys.Rename(tmpPath, p); err != nil {
		_ = fsys.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.emitAudit(r, audit.EventFileWrite, p, "ok", "upload", map[string]string{"bytes": strconv.FormatInt(n, 10)})
	writeJSON(w, http.StatusOK, map[string]any{
		"path":  p,
		"bytes": n,
	})
}

func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	p := normalizeAPIPath(rawPath)
	f, err := fsys.Open(p, os.O_RDONLY, 0)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	// A directory is not downloadable. Declaring its size as Content-Length
	// and then failing to read it (EISDIR) would answer 200 with a body
	// shorter than promised, truncating the response for every client.
	// handleShareDownload and handleFilesShare refuse the same target.
	if info != nil && info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "download target is a directory"})
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	setAttachmentDisposition(w.Header(), path.Base(p))
	if info != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	n, copyErr := io.Copy(w, f)
	status := "ok"
	if copyErr != nil {
		status = "incomplete"
	}
	s.emitAudit(r, audit.EventFileRead, p, status, "download", map[string]string{"bytes": strconv.FormatInt(n, 10)})
}

func (s *Server) handleFilesV1(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/files/"), "/"), "/")
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	targetUser := normalizeTargetUser(parts[0], currentUser(r))
	if targetUser == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target user is required"})
		return
	}

	allowed, err := s.canAccessTargetUser(currentUser(r), targetUser)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if !strings.EqualFold(targetUser, currentUser(r)) {
		// Only admins get here; name a missing user plainly instead of
		// failing later inside the filesystem builder.
		if target, err := s.users.GetByUsername(targetUser); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if target == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
	}

	requestWithTarget := withTargetUser(r, targetUser)
	action := parts[1]
	switch action {
	case "ls":
		s.handleFilesList(w, requestWithTarget)
	case "mkdir":
		if requestWithTarget.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if isBodyEmpty(requestWithTarget) {
			rewriteBodyWithPath(requestWithTarget, requestWithTarget.URL.Query().Get("path"))
		}
		s.handleFilesMkdir(w, requestWithTarget)
	case "rm":
		s.handleFilesDelete(w, requestWithTarget)
	case "rename":
		s.handleFilesRename(w, requestWithTarget)
	case "share":
		s.handleFilesShare(w, requestWithTarget)
	case "upload":
		s.handleFilesUpload(w, requestWithTarget)
	case "download":
		s.handleFilesDownload(w, requestWithTarget)
	case "stat":
		s.handleFilesStat(w, requestWithTarget)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) handleFilesStat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is required"})
		return
	}
	p := normalizeAPIPath(rawPath)
	info, err := fsys.Stat(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":      p,
		"name":      info.Name(),
		"size":      info.Size(),
		"mode":      info.Mode(),
		"is_dir":    info.IsDir(),
		"mod_time":  info.ModTime().UTC(),
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleShareLinks(w http.ResponseWriter, r *http.Request) {
	if s.shareLinks == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "share links are not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		username := currentUser(r)
		if s.isAdminUser(username) {
			if requested := strings.TrimSpace(r.URL.Query().Get("user")); requested != "" {
				username = requested
			}
		}
		links, err := s.shareLinks.ListByUsername(username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out := make([]map[string]any, 0, len(links))
		for _, item := range links {
			if item == nil {
				continue
			}
			out = append(out, shareLinkJSON(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"links": out})
	case http.MethodDelete:
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
			return
		}
		link, err := s.shareLinks.Get(token)
		if err != nil || link == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
			return
		}
		requestUser := currentUser(r)
		if !s.isAdminUser(requestUser) && !strings.EqualFold(requestUser, link.Username) {
			// Same answer as a missing link, so tokens cannot be probed.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
			return
		}
		if err := s.shareLinks.Delete(token); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.emitAudit(r, audit.EventShareRevoke, link.Path, "ok", "share link revoked", map[string]string{"share": shortToken(token), "owner": link.Username})
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "token": token})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// shareLinkJSON is the owner-facing view of a link. share_url opens the
// public landing page; download_url fetches the file directly (for
// unprotected links, or with an X-Share-Password header).
func shareLinkJSON(l *ShareLink) map[string]any {
	return map[string]any{
		"token":              l.Token,
		"username":           l.Username,
		"path":               l.Path,
		"created_at":         l.CreatedAt,
		"expires_at":         l.ExpiresAt,
		"download_count":     l.DownloadCount,
		"max_downloads":      l.MaxDownloads,
		"password_protected": l.Protected(),
		"share_url":          "/s/" + l.Token,
		"download_url":       "/api/v1/share/" + l.Token,
	}
}

// shortToken identifies a share link in audit records without recording
// the full bearer token.
func shortToken(token string) string {
	token = strings.TrimPrefix(token, "share_")
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}

// handleShareDownload serves the public share endpoints:
//
//	GET  /api/v1/share/<token>/info  metadata for the landing page
//	GET  /api/v1/share/<token>       download (X-Share-Password if protected)
//	POST /api/v1/share/<token>       download with a form-encoded password
func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	if s.shareLinks == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
		return
	}
	token := ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/v1/share/"):
		token = strings.TrimPrefix(r.URL.Path, "/api/v1/share/")
	case strings.HasPrefix(r.URL.Path, "/api/share/"):
		token = strings.TrimPrefix(r.URL.Path, "/api/share/")
	}
	token = strings.Trim(strings.TrimSpace(token), "/")
	infoRequest := false
	if t, ok := strings.CutSuffix(token, "/info"); ok {
		token, infoRequest = t, true
	}
	if token == "" || strings.Contains(token, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	switch {
	case infoRequest && r.Method == http.MethodGet:
		s.handleShareInfo(w, token)
		return
	case !infoRequest && (r.Method == http.MethodGet || r.Method == http.MethodPost):
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// A browser form post gets redirects back to the landing page instead
	// of JSON errors.
	formPost := r.Method == http.MethodPost
	fail := func(status int, code, msg string) {
		if formPost {
			http.Redirect(w, r, "/s/"+url.PathEscape(token)+"?error="+code, http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]string{"error": msg, "code": code})
	}

	link, err := s.shareLinks.Get(token)
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && link == nil):
		fail(http.StatusNotFound, "not_found", "share link not found")
		return
	case err != nil:
		fail(http.StatusInternalServerError, "server_error", "share link lookup failed")
		return
	}

	if link.Protected() {
		password := r.Header.Get("X-Share-Password")
		if formPost {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := r.ParseForm(); err == nil {
				password = r.PostForm.Get("password")
			}
		}
		throttle := s.auth.IPThrottle()
		now := time.Now()
		if banErr := throttle.Check(r.RemoteAddr, now); banErr != nil {
			fail(http.StatusTooManyRequests, "throttled", "too many failed attempts, try again later")
			return
		}
		if password == "" || !auth.VerifyPassword(password, link.PasswordHash) {
			if password != "" {
				throttle.RecordFailure(r.RemoteAddr, now)
				s.emitAuditFor(r, link.Username, audit.EventShareDownload, link.Path, "failed", "wrong share password", map[string]string{"share": shortToken(token)})
			}
			fail(http.StatusUnauthorized, "password_required", "this link needs a password")
			return
		}
	}

	link, err = s.shareLinks.ReserveDownload(token, time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrNotFound) || (err == nil && link == nil):
		fail(http.StatusNotFound, "not_found", "share link not found")
		return
	case errors.Is(err, ErrShareLinkExpired):
		fail(http.StatusGone, "expired", err.Error())
		return
	case errors.Is(err, ErrShareLinkDownloadLimitExceeded):
		fail(http.StatusGone, "exhausted", err.Error())
		return
	case err != nil:
		fail(http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	releaseReservation := true
	defer func() {
		if releaseReservation {
			_ = s.shareLinks.ReleaseDownload(token)
		}
	}()

	if s.fsBuilder == nil {
		fail(http.StatusInternalServerError, "server_error", "filesystem builder is not available")
		return
	}
	fsys, err := s.fsBuilder(link.Username)
	defer vfs.ReleaseFS(fsys)
	if err != nil {
		fail(http.StatusInternalServerError, "server_error", "unable to open shared file")
		return
	}
	f, err := fsys.Open(link.Path, os.O_RDONLY, 0)
	if err != nil {
		fail(http.StatusNotFound, "unavailable", "shared file is not available")
		return
	}
	defer f.Close()

	info, _ := f.Stat()
	if info != nil && info.IsDir() {
		fail(http.StatusBadRequest, "unavailable", "shared target is a directory")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	setAttachmentDisposition(w.Header(), path.Base(link.Path))
	if info != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	n, copyErr := io.Copy(w, f)
	status := "ok"
	if copyErr == nil {
		releaseReservation = false
	} else {
		status = "incomplete"
	}
	s.emitAuditFor(r, link.Username, audit.EventShareDownload, link.Path, status, "share download", map[string]string{
		"share": shortToken(token),
		"bytes": strconv.FormatInt(n, 10),
	})
}

// handleShareInfo describes a link for the public landing page without
// consuming a download. It reveals only the file's name and size.
func (s *Server) handleShareInfo(w http.ResponseWriter, token string) {
	link, err := s.shareLinks.Get(token)
	if err != nil || link == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found", "code": "not_found"})
		return
	}
	now := time.Now().UTC()
	out := map[string]any{
		"name":               path.Base(link.Path),
		"expires_at":         link.ExpiresAt,
		"password_protected": link.Protected(),
		"expired":            !link.ExpiresAt.IsZero() && now.After(link.ExpiresAt),
		"exhausted":          link.MaxDownloads > 0 && link.DownloadCount >= link.MaxDownloads,
	}
	if link.MaxDownloads > 0 {
		out["downloads_remaining"] = max(link.MaxDownloads-link.DownloadCount, 0)
	}
	if s.fsBuilder != nil {
		fsys, err := s.fsBuilder(link.Username)
		defer vfs.ReleaseFS(fsys)
		if err == nil {
			if info, err := fsys.Stat(link.Path); err == nil && !info.IsDir() {
				out["size"] = info.Size()
			} else {
				out["unavailable"] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.auditLogPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"events":     []any{},
			"pagination": map[string]any{"page": 1, "page_size": 0, "total": 0},
		})
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	page := parseInt(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	q, err := s.parseAuditQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Stream the audit log instead of replaying it: matches are counted and
	// only the requested page window is retained, so per-request memory does
	// not grow with the total audit log size. Events are stored oldest first,
	// so page 1 must return the newest matches — the page window is collected
	// in file order and reversed below.
	total := 0
	if err := s.streamAuditEvents(func(fields auditEventFilterFields, _ []byte) error {
		if fields.matches(q) {
			total++
		}
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	start := (page - 1) * limit
	windowLo := total - start - limit
	if windowLo < 0 {
		windowLo = 0
	}
	windowHi := total - start
	if windowHi < 0 {
		windowHi = 0
	}

	eventsPage := make([]map[string]any, 0, limit)
	matchIdx := 0
	if err := s.streamAuditEvents(func(fields auditEventFilterFields, line []byte) error {
		if !fields.matches(q) {
			return nil
		}
		if matchIdx >= windowLo && matchIdx < windowHi {
			var evt map[string]any
			if err := json.Unmarshal(line, &evt); err != nil {
				return nil
			}
			eventsPage = append(eventsPage, evt)
		}
		matchIdx++
		if matchIdx >= windowHi {
			return errStopAuditScan
		}
		return nil
	}); err != nil && !errors.Is(err, errStopAuditScan) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for i, j := 0, len(eventsPage)-1; i < j; i, j = i+1, j-1 {
		eventsPage[i], eventsPage[j] = eventsPage[j], eventsPage[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": eventsPage,
		"pagination": map[string]any{
			"page":      page,
			"page_size": limit,
			"total":     total,
		},
	})
}

func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.auditLogPath == "" {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "csv" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported format"})
		return
	}

	q, err := s.parseAuditQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Stream the log and keep only matches, so a narrow export of a large
	// log does not load the whole log into memory.
	filtered := []map[string]any{}
	if err := s.streamAuditEvents(func(fields auditEventFilterFields, line []byte) error {
		if !fields.matches(q) {
			return nil
		}
		var evt map[string]any
		if json.Unmarshal(line, &evt) == nil {
			filtered = append(filtered, evt)
		}
		return nil
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	slices.Reverse(filtered) // newest first

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-export.%s"`, format))
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(filtered)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		// Headers are already sent; a failure here is the client going
		// away, so it is only logged.
		if err := writeAuditCSV(w, filtered); err != nil && s.logger != nil {
			s.logger.Warn("audit csv export failed", "error", err)
		}
	}
}

func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.transfers == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"active":     []any{},
			"recent":     []any{},
			"stats":      map[string]any{},
			"pagination": map[string]any{"page": 1, "page_size": 0, "total": 0},
		})
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	page := parseInt(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	viewer := currentUser(r)
	username := s.viewerScopedUsername(viewer, r.URL.Query().Get("username"))
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	active := filterTransfers(s.transfers.Active(), username, protocol, status, direction, query)
	recentAll := filterTransfers(s.transfers.Recent(5000), username, protocol, status, direction, query)
	recentPage, total := paginateTransfers(recentAll, page, limit)
	stats := any(s.transfers.Stats())
	if username != "" {
		stats = scopedTransferStats(active, recentAll)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"active": active,
		"recent": recentPage,
		"stats":  stats,
		"pagination": map[string]any{
			"page":      page,
			"page_size": limit,
			"total":     total,
		},
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.writeMetrics(w)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token != "" {
			user, err := s.activeUserFromBearerToken(token)
			if err != nil {
				if errors.Is(err, errAuthenticatedUserUnavailable) {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve authenticated user"})
				return
			}
			if !writeAccessDenied(w, user, r) {
				return
			}
			r.Header.Set("X-Auth-User", user.Username)
			r.Header.Set("X-Auth-Method", "bearer")
			next(w, r)
			return
		}

		rawAPIKey := apiKeyToken(r)
		if rawAPIKey == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token or api key"})
			return
		}
		if s.apiKeys == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "api key auth is not configured"})
			return
		}

		record, err := s.apiKeys.GetByToken(rawAPIKey)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return
		}
		if record == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return
		}

		user, err := s.users.GetByID(record.UserID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve api key owner"})
			return
		}
		if user == nil || !user.Enabled {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "api key owner is not available"})
			return
		}
		if !writeAccessDenied(w, user, r) {
			return
		}
		if allowed, reason := apiKeyPermissionAllowsRequest(record.Permissions, r.Method, r.URL.Path); !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
			return
		}
		if err := s.apiKeys.UpdateLastUsed(record.ID, time.Now().UTC()); err != nil && s.logger != nil {
			s.logger.Warn("failed to update api key last used timestamp", "key_id", record.ID, "error", err)
		}

		r.Header.Set("X-Auth-User", user.Username)
		r.Header.Set("X-Auth-Method", "api-key")
		r.Header.Set("X-Auth-Permissions", record.Permissions)
		next(w, r)
	}
}

// writeAccessDenied applies the user's access policy to an authenticated
// API request, so expiring an account or narrowing its addresses takes
// effect on live sessions. It reports whether the request may proceed.
func writeAccessDenied(w http.ResponseWriter, user *auth.User, r *http.Request) bool {
	err := user.CheckAccess(r.RemoteAddr, time.Now(), auth.ProtocolWebUI)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrAccountExpired):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "code": "access_denied"})
	}
	return false
}

// contentSecurityPolicy is computed once from the embedded index.html.
var contentSecurityPolicy = webui.ContentSecurityPolicy()

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range internalAuthHeaders {
			r.Header.Del(h)
		}
		// /health stays reachable so local container/LB probes keep working
		// under a restrictive allow list; it exposes no sensitive data.
		if r.URL.Path != "/health" && !s.ipFilter().AllowedRemote(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		requestID := requestIDFromHeader(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		trace := requestTraceContext(r.Header.Get("Traceparent"))
		r.Header.Set("Traceparent", trace.Traceparent)
		r.Header.Set("X-Trace-ID", trace.TraceID)
		recorder := &statusRecorder{ResponseWriter: w}
		recorder.Header().Set("X-Request-ID", requestID)
		recorder.Header().Set("Traceparent", trace.Traceparent)
		recorder.Header().Set("X-Trace-ID", trace.TraceID)
		recorder.Header().Set("X-Content-Type-Options", "nosniff")
		recorder.Header().Set("X-Frame-Options", "DENY")
		recorder.Header().Set("Referrer-Policy", "no-referrer")
		recorder.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		recorder.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		recorder.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if r.TLS != nil {
			recorder.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		recorder.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Request-ID, Traceparent")
		recorder.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		recorder.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Traceparent, X-Trace-ID")

		startedAt := time.Now()
		routeLabel := metricPathLabel(r.URL.Path)
		httpMetrics := s.httpMetricsState()
		httpMetrics.begin(r.Method, routeLabel)
		defer func() {
			statusCode := recorder.status
			if statusCode == 0 {
				statusCode = http.StatusOK
			}
			duration := time.Since(startedAt)
			httpMetrics.complete(r.Method, routeLabel, statusCode, duration)
			s.logHTTPRequest(r, requestID, trace, routeLabel, statusCode, recorder.bytes, duration)
		}()

		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
			allowedOrigin, allowed := s.allowedOrigin(origin)
			if !allowed {
				if r.Method == http.MethodOptions {
					writeJSON(recorder, http.StatusForbidden, map[string]string{"error": "origin is not allowed"})
					return
				}
			} else {
				recorder.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
				if allowedOrigin != "*" {
					recorder.Header().Add("Vary", "Origin")
				}
			}
		}
		if r.Method == http.MethodOptions {
			recorder.WriteHeader(http.StatusNoContent)
			return
		}

		defer func() {
			if recovered := recover(); recovered != nil {
				if s.logger != nil {
					s.logger.Error(
						"http request panicked",
						"request_id",
						requestID,
						"trace_id",
						trace.TraceID,
						"traceparent",
						trace.Traceparent,
						"method",
						r.Method,
						"path",
						r.URL.Path,
						"remote_addr",
						r.RemoteAddr,
						"panic",
						recovered,
					)
				}
				if !recorder.wroteHeader {
					writeJSON(recorder, http.StatusInternalServerError, map[string]string{
						"error":      "internal server error",
						"request_id": requestID,
						"trace_id":   trace.TraceID,
					})
				}
			}
		}()

		next.ServeHTTP(recorder, r)
	})
}

func (s *Server) httpMetricsState() *httpMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpMetrics == nil {
		s.httpMetrics = newHTTPMetrics()
	}
	return s.httpMetrics
}

func (s *Server) logHTTPRequest(
	r *http.Request,
	requestID string,
	trace traceContext,
	route string,
	statusCode int,
	bytesWritten int,
	duration time.Duration,
) {
	if s.logger == nil {
		return
	}

	level := slog.LevelInfo
	switch {
	case statusCode >= 500:
		level = slog.LevelError
	case statusCode >= 400:
		level = slog.LevelWarn
	case route == "/api/v1/health" || route == "/api/v1/metrics":
		level = slog.LevelDebug
	}

	s.logger.Log(
		r.Context(),
		level,
		"http request completed",
		"request_id",
		requestID,
		"trace_id",
		trace.TraceID,
		"traceparent",
		trace.Traceparent,
		"method",
		r.Method,
		"path",
		r.URL.Path,
		"route",
		route,
		"status",
		statusCode,
		"bytes",
		bytesWritten,
		"duration_ms",
		duration.Milliseconds(),
		"remote_addr",
		remoteIP(r.RemoteAddr),
		"user_agent",
		r.UserAgent(),
		"auth_user",
		currentUser(r),
	)
}

func (s *Server) userFS(username string) (vfs.FileSystem, error) {
	if s.fsBuilder == nil {
		return nil, errors.New("filesystem access is not configured")
	}
	return s.fsBuilder(username)
}

func currentUser(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Auth-User"))
}

func normalizeAPIPath(p string) string {
	if p == "" {
		return "/"
	}
	clean := path.Clean("/" + strings.TrimSpace(p))
	if clean == "." {
		return "/"
	}
	return clean
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	if r == nil || r.Body == nil {
		if allowEmpty {
			return nil
		}
		return io.EOF
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("invalid json payload")
	}
	if err := r.Body.Close(); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func apiKeyToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.Header.Get("X-API-Key")); token != "" {
		return token
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "ApiKey") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func (s *Server) loginRateLimitStatus(ip string, now time.Time) (time.Duration, bool) {
	cfg := s.currentConfig()
	if !cfg.BruteForceEnabled || cfg.LoginMaxAttempts <= 0 || cfg.LoginLockoutDuration <= 0 || ip == "" {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.loginState == nil {
		s.loginState = make(map[string]*loginAttempt)
	}
	entry := s.loginState[ip]
	if entry == nil {
		return 0, false
	}
	if !entry.blockedUntil.IsZero() && now.Before(entry.blockedUntil) {
		return time.Until(entry.blockedUntil).Round(time.Second), true
	}
	if !entry.blockedUntil.IsZero() || now.Sub(entry.lastFailedAt) > cfg.LoginLockoutDuration {
		delete(s.loginState, ip)
	}
	return 0, false
}

func (s *Server) recordLoginFailure(ip string, now time.Time) {
	cfg := s.currentConfig()
	if !cfg.BruteForceEnabled || cfg.LoginMaxAttempts <= 0 || cfg.LoginLockoutDuration <= 0 || ip == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.loginState[ip]
	if entry == nil {
		entry = &loginAttempt{}
		s.loginState[ip] = entry
	}
	if now.Sub(entry.lastFailedAt) > cfg.LoginLockoutDuration {
		entry.failures = 0
		entry.blockedUntil = time.Time{}
	}
	entry.lastFailedAt = now
	entry.failures++
	if entry.failures >= cfg.LoginMaxAttempts {
		entry.failures = 0
		entry.blockedUntil = now.Add(cfg.LoginLockoutDuration)
	}
}

func (s *Server) clearLoginFailures(ip string) {
	if ip == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loginState, ip)
}

func (s *Server) allowedOrigin(origin string) (string, bool) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return "", false
	}
	cfg := s.currentConfig()
	for _, candidate := range cfg.CORSOrigins {
		candidate = strings.TrimSpace(candidate)
		switch {
		case candidate == "":
			continue
		case candidate == "*":
			return "*", true
		case strings.EqualFold(candidate, origin):
			return origin, true
		}
	}
	return "", false
}

func requestIDFromHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	return value
}

func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return itoa(int(time.Now().UnixNano()))
	}
	return hex.EncodeToString(buf)
}

func requestTraceContext(header string) traceContext {
	if trace, ok := parseTraceparent(header); ok {
		return trace
	}
	return newTraceContext()
}

func parseTraceparent(header string) (traceContext, bool) {
	header = strings.TrimSpace(header)
	parts := strings.Split(header, "-")
	if len(parts) != 4 {
		return traceContext{}, false
	}
	version := normalizeHexPart(parts[0], 2)
	traceID := normalizeHexPart(parts[1], 32)
	parentID := normalizeHexPart(parts[2], 16)
	flags := normalizeHexPart(parts[3], 2)
	if version == "" || traceID == "" || parentID == "" || flags == "" {
		return traceContext{}, false
	}
	if isAllZeroHex(traceID) || isAllZeroHex(parentID) {
		return traceContext{}, false
	}
	return traceContext{
		TraceID:     traceID,
		ParentID:    parentID,
		Traceparent: version + "-" + traceID + "-" + parentID + "-" + flags,
	}, true
}

func newTraceContext() traceContext {
	traceID := newRandomHex(16)
	if isAllZeroHex(traceID) {
		traceID = "00000000000000000000000000000001"
	}
	parentID := newRandomHex(8)
	if isAllZeroHex(parentID) {
		parentID = "0000000000000001"
	}
	return traceContext{
		TraceID:     traceID,
		ParentID:    parentID,
		Traceparent: "00-" + traceID + "-" + parentID + "-01",
	}
}

func newRandomHex(byteLen int) string {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func normalizeHexPart(value string, expectedLen int) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if len(value) != expectedLen {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func isAllZeroHex(value string) bool {
	if value == "" {
		return true
	}
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(addr)
}

func itoa(v int) string {
	buf := make([]byte, 0, 16)
	if v == 0 {
		return "0"
	}
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}

func parseInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func setAttachmentDisposition(header http.Header, filename string) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		header.Set("Content-Disposition", "attachment")
		return
	}
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); value != "" {
		header.Set("Content-Disposition", value)
		return
	}
	header.Set("Content-Disposition", "attachment")
}

func parseTTL(raw string, fallback time.Duration) (time.Duration, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return fallback, nil
	}
	if strings.HasSuffix(trimmed, "d") {
		daysRaw := strings.TrimSpace(strings.TrimSuffix(trimmed, "d"))
		days, err := strconv.Atoi(daysRaw)
		if err != nil || days <= 0 {
			return 0, errors.New("invalid ttl format")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	out, err := time.ParseDuration(trimmed)
	if err != nil || out <= 0 {
		return 0, errors.New("invalid ttl format")
	}
	return out, nil
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func filterTransfers(
	in []*transfer.Transfer,
	username, protocol, status, direction, query string,
) []*transfer.Transfer {
	if len(in) == 0 {
		return in
	}
	out := make([]*transfer.Transfer, 0, len(in))
	for _, tr := range in {
		if tr == nil {
			continue
		}
		if username != "" && !strings.EqualFold(tr.Username, username) {
			continue
		}
		if protocol != "" && !strings.EqualFold(tr.Protocol, protocol) {
			continue
		}
		if status != "" && !strings.EqualFold(string(tr.Status), status) {
			continue
		}
		if direction != "" && !strings.EqualFold(string(tr.Direction), direction) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(tr.Path + " " + tr.Username + " " + tr.Protocol)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, tr)
	}
	return out
}

func paginateTransfers(in []*transfer.Transfer, page, size int) ([]*transfer.Transfer, int) {
	total := len(in)
	if total == 0 {
		return []*transfer.Transfer{}, 0
	}
	start := (page - 1) * size
	if start >= total {
		return []*transfer.Transfer{}, total
	}
	end := start + size
	if end > total {
		end = total
	}
	return in[start:end], total
}

func filterAuditEvents(events []map[string]any, q auditQuery) []map[string]any {
	if len(events) == 0 {
		return events
	}
	out := make([]map[string]any, 0, len(events))
	for _, evt := range events {
		fields := auditEventFilterFields{
			Username:  strField(evt, "username"),
			Protocol:  strField(evt, "protocol"),
			Type:      strField(evt, "type"),
			Status:    strField(evt, "status"),
			Message:   strField(evt, "message"),
			Path:      strField(evt, "path"),
			Timestamp: strField(evt, "timestamp"),
		}
		if fields.matches(q) {
			out = append(out, evt)
		}
	}
	return out
}

// matches reports whether this audit event passes the viewer scope and
// filter arguments shared by the whole-file and streaming audit readers.
// The receiver carries the event's own field values; the arguments carry the
// request filters (username is the viewer-scoped username).
// auditQuery is a parsed audit filter. Empty fields match everything; From
// is inclusive and To exclusive.
type auditQuery struct {
	Username, Protocol, Type, Status string
	// Text is a lower-cased substring searched across the text fields.
	Text     string
	From, To time.Time
}

// parseAuditQuery reads the audit filter parameters. from/to accept RFC 3339
// times or YYYY-MM-DD dates (UTC); a bare "to" date includes that whole day.
func (s *Server) parseAuditQuery(r *http.Request) (auditQuery, error) {
	v := r.URL.Query()
	q := auditQuery{
		Username: s.viewerScopedUsername(currentUser(r), v.Get("username")),
		Protocol: strings.TrimSpace(v.Get("protocol")),
		Type:     strings.TrimSpace(v.Get("type")),
		Status:   strings.TrimSpace(v.Get("status")),
		Text:     strings.ToLower(strings.TrimSpace(v.Get("q"))),
	}
	var err error
	if q.From, err = parseAuditTime(v.Get("from"), false); err != nil {
		return q, fmt.Errorf("from: %w", err)
	}
	if q.To, err = parseAuditTime(v.Get("to"), true); err != nil {
		return q, fmt.Errorf("to: %w", err)
	}
	if !q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To) {
		return q, errors.New("from must be before to")
	}
	return q, nil
}

func parseAuditTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	d, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, errors.New("use RFC 3339 or YYYY-MM-DD")
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}

func (f auditEventFilterFields) matches(q auditQuery) bool {
	if q.Username != "" && !strings.EqualFold(f.Username, q.Username) {
		return false
	}
	if q.Protocol != "" && !strings.EqualFold(f.Protocol, q.Protocol) {
		return false
	}
	if q.Type != "" && !strings.EqualFold(f.Type, q.Type) {
		return false
	}
	if q.Status != "" && !strings.EqualFold(f.Status, q.Status) {
		return false
	}
	if !q.From.IsZero() || !q.To.IsZero() {
		ts, err := time.Parse(time.RFC3339Nano, f.Timestamp)
		if err != nil {
			return false
		}
		if (!q.From.IsZero() && ts.Before(q.From)) || (!q.To.IsZero() && !ts.Before(q.To)) {
			return false
		}
	}
	if q.Text != "" {
		hay := strings.ToLower(strings.Join([]string{f.Username, f.Protocol, f.Type, f.Status, f.Message, f.Path}, " "))
		if !strings.Contains(hay, q.Text) {
			return false
		}
	}
	return true
}

func strField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(itoaAny(t)), "\n", " "), "\r", " "))
	}
}

func itoaAny(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) readRecentAuditEvents(limit int) []map[string]any {
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}
	if s.auditLogPath == "" {
		return []map[string]any{}
	}
	files, err := audit.LogFiles(s.auditLogPath)
	if err != nil {
		return []map[string]any{}
	}
	events := make([]map[string]any, 0, limit)
	// Newest file first; each file is bounded by the rotation size.
	for f := len(files) - 1; f >= 0 && len(events) < limit; f-- {
		raw, err := os.ReadFile(files[f])
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i := len(lines) - 1; i >= 0 && len(events) < limit; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			var evt map[string]any
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				continue
			}
			events = append(events, evt)
		}
	}
	return events
}

// maxAuditScanLineBytes bounds the scanner buffer for replaying audit lines;
// audit events are small JSON objects, with generous headroom for odd lines.
const maxAuditScanLineBytes = 1 << 20

// errStopAuditScan stops streamAuditEvents early once the caller has what it
// needs (e.g. the requested page window has been collected).
var errStopAuditScan = errors.New("stop audit scan")

// auditEventFilterFields carries the filter-relevant audit fields with a
// cheap decode for streaming scans. Lines whose filter fields are not JSON
// strings fall back to the whole-event decode in streamAuditEvents.
type auditEventFilterFields struct {
	Username string `json:"username"`
	Protocol string `json:"protocol"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Path     string `json:"path"`
	// Timestamp stays a string; it is parsed only when a time range is set.
	Timestamp string `json:"timestamp"`
}

// streamAuditEvents replays the audit log in file order (oldest first),
// decoding the filter fields of one line at a time so per-request allocation
// does not grow with the total log size. Malformed lines are skipped,
// matching the whole-file reader; lines with non-string filter fields are
// decoded through the whole-event path to preserve their strField text.
// Return errStopAuditScan from visit to stop early. line is only valid
// during the visit call.
func (s *Server) streamAuditEvents(visit func(fields auditEventFilterFields, line []byte) error) error {
	if s.auditLogPath == "" {
		return nil
	}
	// Rotated files first, then the live file, oldest record first.
	file, err := audit.OpenLog(s.auditLogPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxAuditScanLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var fields auditEventFilterFields
		if err := json.Unmarshal(line, &fields); err != nil {
			var evt map[string]any
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}
			fields = auditEventFilterFields{
				Username:  strField(evt, "username"),
				Protocol:  strField(evt, "protocol"),
				Type:      strField(evt, "type"),
				Status:    strField(evt, "status"),
				Message:   strField(evt, "message"),
				Path:      strField(evt, "path"),
				Timestamp: strField(evt, "timestamp"),
			}
		}
		if err := visit(fields, line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func writeAuditCSV(w io.Writer, events []map[string]any) error {
	writer := csv.NewWriter(w)
	header := []string{"timestamp", "type", "username", "protocol", "status", "path", "ip", "message"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, event := range events {
		row := []string{
			strField(event, "timestamp"),
			strField(event, "type"),
			strField(event, "username"),
			strField(event, "protocol"),
			strField(event, "status"),
			strField(event, "path"),
			strField(event, "ip"),
			strField(event, "message"),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func normalizeTargetUser(raw, current string) string {
	name := strings.TrimSpace(raw)
	if name == "" || strings.EqualFold(name, "me") || strings.EqualFold(name, "self") {
		return current
	}
	return name
}

// withTargetUser runs a file handler as username (whose filesystem it acts
// on) while X-Auth-Actor keeps the authenticated caller for the audit log.
func withTargetUser(r *http.Request, username string) *http.Request {
	clone := r.Clone(r.Context())
	if actor := currentUser(r); actor != "" && !strings.EqualFold(actor, username) {
		clone.Header.Set("X-Auth-Actor", actor)
	}
	clone.Header.Set("X-Auth-User", username)
	return clone
}

// internalAuthHeaders are set by the auth middleware only; any client-sent
// copies are dropped before routing so they cannot be spoofed.
var internalAuthHeaders = []string{"X-Auth-User", "X-Auth-Method", "X-Auth-Permissions", "X-Auth-Actor"}

func isBodyEmpty(r *http.Request) bool {
	return r.Body == nil || r.ContentLength == 0
}

func rewriteBodyWithPath(r *http.Request, p string) {
	payload, _ := json.Marshal(map[string]string{"path": p})
	r.Body = io.NopCloser(strings.NewReader(string(payload)))
	r.ContentLength = int64(len(payload))
	r.Header.Set("Content-Type", "application/json")
}

func (s *Server) canAccessTargetUser(actorUsername, targetUsername string) (bool, error) {
	if actorUsername == "" || targetUsername == "" {
		return false, nil
	}
	if strings.EqualFold(actorUsername, targetUsername) {
		return true, nil
	}
	actor, err := s.users.GetByUsername(actorUsername)
	if err != nil {
		return false, err
	}
	if actor == nil {
		return false, nil
	}
	return actor.Type == auth.UserTypeAdmin, nil
}

func (s *Server) isAdminUser(username string) bool {
	if strings.TrimSpace(username) == "" {
		return false
	}
	user, err := s.users.GetByUsername(username)
	if err != nil || user == nil {
		return false
	}
	return user.Type == auth.UserTypeAdmin
}

var errAuthenticatedUserUnavailable = errors.New("authenticated user is unavailable")

func (s *Server) activeUserFromBearerToken(token string) (*auth.User, error) {
	claims, err := verifyToken(s.secret, token)
	if err != nil {
		return nil, errAuthenticatedUserUnavailable
	}
	user, err := s.activeUserByUsername(claims.Sub)
	if err != nil {
		return nil, err
	}
	// Sessions issued before the last password change are revoked.
	if user.SessionsValidAfter != nil && claims.Iat < user.SessionsValidAfter.Unix() {
		return nil, errAuthenticatedUserUnavailable
	}
	return user, nil
}

func (s *Server) activeUserByUsername(username string) (*auth.User, error) {
	if s.users == nil {
		return nil, errors.New("user repository is not configured")
	}
	user, err := s.users.GetByUsername(strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if user == nil || !user.Enabled {
		return nil, errAuthenticatedUserUnavailable
	}
	return user, nil
}

func (s *Server) terminateUserSessions(username string) {
	if s == nil || s.sessions == nil || strings.TrimSpace(username) == "" {
		return
	}
	for _, item := range s.sessions.List() {
		if item == nil || !strings.EqualFold(item.Username, username) {
			continue
		}
		s.sessions.Kill(item.ID)
	}
}

func (s *Server) viewerScopedUsername(viewerUsername, requestedUsername string) string {
	viewerUsername = strings.TrimSpace(viewerUsername)
	requestedUsername = strings.TrimSpace(requestedUsername)
	if viewerUsername == "" {
		return requestedUsername
	}
	if s.isAdminUser(viewerUsername) {
		return requestedUsername
	}
	return viewerUsername
}

func filterSessionsByUsername(in []*session.Session, username string) []*session.Session {
	if strings.TrimSpace(username) == "" {
		return in
	}
	out := make([]*session.Session, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		if !strings.EqualFold(item.Username, username) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterSessions(in []*session.Session, protocol, remoteIP, query string) []*session.Session {
	if len(in) == 0 {
		return in
	}
	out := make([]*session.Session, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		if protocol != "" && !strings.EqualFold(item.Protocol, protocol) {
			continue
		}
		if remoteIP != "" && !strings.Contains(strings.ToLower(item.RemoteAddr), strings.ToLower(remoteIP)) {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				item.ID,
				item.Username,
				item.Protocol,
				item.RemoteAddr,
			}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func scopedTransferStats(active []*transfer.Transfer, recent []*transfer.Transfer) transfer.Stats {
	stats := transfer.Stats{
		ActiveTransfers: int64(len(active)),
	}
	for _, item := range recent {
		if item == nil {
			continue
		}
		stats.TotalTransfers++
		switch item.Status {
		case transfer.StatusCompleted:
			stats.Completed++
		case transfer.StatusFailed:
			stats.Failed++
		}
		if item.Direction == transfer.DirectionUpload {
			stats.UploadBytes += item.BytesDone
		} else {
			stats.DownloadBytes += item.BytesDone
		}
	}
	stats.TotalTransfers += int64(len(active))
	for _, item := range active {
		if item == nil {
			continue
		}
		if item.Direction == transfer.DirectionUpload {
			stats.UploadBytes += item.BytesDone
		} else {
			stats.DownloadBytes += item.BytesDone
		}
	}
	return stats
}

package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
)

type Config struct {
	BindAddress    string
	Port           int
	SessionTimeout time.Duration
	CORSOrigins    []string
}

type StatusProvider func() map[string]any
type UserFSBuilder func(username string) (vfs.FileSystem, error)

type Server struct {
	cfg          Config
	logger       *slog.Logger
	auth         *auth.Engine
	users        *auth.UserRepository
	sessions     *session.Manager
	status       StatusProvider
	fsBuilder    UserFSBuilder
	auditLogPath string
	secret       []byte
	transfers    *transfer.Manager

	httpServer *http.Server
	mu         sync.Mutex
	closed     bool
}

func NewServer(
	cfg Config,
	logger *slog.Logger,
	authEngine *auth.Engine,
	userRepo *auth.UserRepository,
	sessions *session.Manager,
	status StatusProvider,
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
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &Server{
		cfg:          cfg,
		logger:       logger,
		auth:         authEngine,
		users:        userRepo,
		sessions:     sessions,
		status:       status,
		fsBuilder:    fsBuilder,
		auditLogPath: auditLogPath,
		secret:       secret,
		transfers:    transfers,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/server/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/users", s.withAuth(s.handleUsers))
	mux.HandleFunc("/api/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/files/list", s.withAuth(s.handleFilesList))
	mux.HandleFunc("/api/files/mkdir", s.withAuth(s.handleFilesMkdir))
	mux.HandleFunc("/api/files/delete", s.withAuth(s.handleFilesDelete))
	mux.HandleFunc("/api/files/upload", s.withAuth(s.handleFilesUpload))
	mux.HandleFunc("/api/files/download", s.withAuth(s.handleFilesDownload))
	mux.HandleFunc("/api/audit", s.withAuth(s.handleAudit))
	mux.HandleFunc("/api/transfers", s.withAuth(s.handleTransfers))

	handler := s.withMiddleware(mux)
	s.httpServer = &http.Server{
		Addr:    net.JoinHostPort(s.cfg.BindAddress, itoa(s.cfg.Port)),
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		_ = s.Stop(context.Background())
	}()

	go func() {
		if s.logger != nil {
			s.logger.Info("API server started", "addr", s.httpServer.Addr)
		}
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && s.logger != nil {
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
	checks := map[string]any{
		"auth_engine":     s.auth != nil,
		"user_repository": s.users != nil,
		"session_manager": s.sessions != nil,
		"fs_builder":      s.fsBuilder != nil,
		"audit_log_path":  s.auditLogPath != "",
	}
	ok := true
	for _, v := range checks {
		if b, isBool := v.(bool); isBool && !b {
			ok = false
		}
	}
	status := "ok"
	if !ok {
		status = "degraded"
	}
	resp := map[string]any{
		"status": status,
		"time":   time.Now().UTC(),
		"checks": checks,
	}
	if s.status != nil {
		resp["server"] = s.status()
	}
	if s.transfers != nil {
		resp["transfers"] = s.transfers.Stats()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	user, err := s.auth.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := signToken(s.secret, user.Username, s.cfg.SessionTimeout)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"type":     user.Type,
		},
	})
}

func (s *Server) handleServerStatus(w http.ResponseWriter, _ *http.Request) {
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

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.users.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list users failed"})
			return
		}
		type userResp struct {
			ID       string    `json:"id"`
			Username string    `json:"username"`
			Type     string    `json:"type"`
			Enabled  bool      `json:"enabled"`
			HomeDir  string    `json:"home_dir"`
			LastSeen time.Time `json:"updated_at"`
		}
		out := make([]userResp, 0, len(users))
		for _, u := range users {
			if u == nil {
				continue
			}
			out = append(out, userResp{
				ID:       u.ID,
				Username: u.Username,
				Type:     string(u.Type),
				Enabled:  u.Enabled,
				HomeDir:  u.HomeDir,
				LastSeen: u.UpdatedAt,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": out})
	case http.MethodPost:
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			HomeDir  string `json:"home_dir"`
			Admin    bool   `json:"admin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.HomeDir == "" {
			req.HomeDir = "/"
		}
		user, err := s.auth.CreateUser(req.Username, req.Password, req.HomeDir, req.Admin)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"type":     user.Type,
		})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
			return
		}
		if err := s.users.Delete(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
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
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.sessions.List()})
}

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
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
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	p := normalizeAPIPath(req.Path)
	if err := fsys.MkdirAll(p, 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": p})
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p := normalizeAPIPath(r.URL.Query().Get("path"))
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	user := currentUser(r)
	fsys, err := s.userFS(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p := normalizeAPIPath(r.URL.Query().Get("path"))
	f, err := fsys.Open(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	n, err := io.Copy(f, r.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p := normalizeAPIPath(r.URL.Query().Get("path"))
	f, err := fsys.Open(p, os.O_RDONLY, 0)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	info, _ := f.Stat()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+path.Base(p)+`"`)
	if info != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.auditLogPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{"events": []any{}})
		return
	}
	raw, err := os.ReadFile(s.auditLogPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{"events": []any{}})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}

	lines := strings.Split(string(raw), "\n")
	events := make([]map[string]any, 0, limit)
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
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.transfers == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"active": []any{},
			"recent": []any{},
			"stats":  map[string]any{},
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
	writeJSON(w, http.StatusOK, map[string]any{
		"active": s.transfers.Active(),
		"recent": s.transfers.Recent(limit),
		"stats":  s.transfers.Stats(),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	stats := map[string]float64{}
	if s.transfers != nil {
		t := s.transfers.Stats()
		stats["kervan_transfers_active"] = float64(t.ActiveTransfers)
		stats["kervan_transfers_total"] = float64(t.TotalTransfers)
		stats["kervan_transfers_completed_total"] = float64(t.Completed)
		stats["kervan_transfers_failed_total"] = float64(t.Failed)
		stats["kervan_transfer_upload_bytes_total"] = float64(t.UploadBytes)
		stats["kervan_transfer_download_bytes_total"] = float64(t.DownloadBytes)
	}
	if s.sessions != nil {
		stats["kervan_sessions_active"] = float64(len(s.sessions.List()))
	}
	if s.users != nil {
		users, err := s.users.List()
		if err == nil {
			stats["kervan_users_total"] = float64(len(users))
		}
	}

	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = io.WriteString(w, k+" "+formatFloat(stats[k])+"\n")
	}
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
			return
		}
		claims, err := verifyToken(s.secret, token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		r.Header.Set("X-Auth-User", claims.Sub)
		next(w, r)
	}
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.CORSOrigins) > 0 {
			w.Header().Set("Access-Control-Allow-Origin", strings.Join(s.cfg.CORSOrigins, ","))
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

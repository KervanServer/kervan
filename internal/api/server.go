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
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"github.com/kervanserver/kervan/internal/webui"
)

type Config struct {
	BindAddress    string
	Port           int
	SessionTimeout time.Duration
	CORSOrigins    []string
}

type StatusProvider func() map[string]any
type UserFSBuilder func(username string) (vfs.FileSystem, error)
type ServerConfigProvider func() map[string]any
type ReloadProvider func() (map[string]any, error)
type ConfigUpdateProvider func(patch map[string]any) (map[string]any, error)
type ConfigValidateProvider func(patch map[string]any) (map[string]any, error)

type Server struct {
	cfg          Config
	logger       *slog.Logger
	auth         *auth.Engine
	users        *auth.UserRepository
	sessions     *session.Manager
	status       StatusProvider
	config       ServerConfigProvider
	reload       ReloadProvider
	configUpdate ConfigUpdateProvider
	configCheck  ConfigValidateProvider
	apiKeys      *apiKeyRepository
	shareLinks   *shareLinkRepository
	fsBuilder    UserFSBuilder
	store        *store.Store
	auditLogPath string
	secret       []byte
	transfers    *transfer.Manager
	uiHandler    http.Handler

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
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
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
		apiKeys:      newAPIKeyRepository(keyStore),
		shareLinks:   newShareLinkRepository(keyStore),
		fsBuilder:    fsBuilder,
		store:        keyStore,
		auditLogPath: auditLogPath,
		secret:       secret,
		transfers:    transfers,
		uiHandler:    uiHandler,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/metrics", s.handleMetrics)

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/api/ws", s.handleWebSocket)
	mux.HandleFunc("/api/v1/ws", s.handleWebSocket)

	mux.HandleFunc("/api/server/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/v1/server/status", s.withAuth(s.handleServerStatus))
	mux.HandleFunc("/api/v1/server/config", s.withAuth(s.handleServerConfig))
	mux.HandleFunc("/api/v1/server/config/validate", s.withAuth(s.handleServerConfigValidate))
	mux.HandleFunc("/api/v1/server/reload", s.withAuth(s.handleServerReload))

	mux.HandleFunc("/api/users", s.withAuth(s.handleUsers))
	mux.HandleFunc("/api/v1/users", s.withAuth(s.handleUsers))
	mux.HandleFunc("/api/apikeys", s.withAuth(s.handleAPIKeys))
	mux.HandleFunc("/api/v1/apikeys", s.withAuth(s.handleAPIKeys))

	mux.HandleFunc("/api/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/v1/sessions", s.withAuth(s.handleSessions))

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

	mux.HandleFunc("/api/transfers", s.withAuth(s.handleTransfers))
	mux.HandleFunc("/api/v1/transfers", s.withAuth(s.handleTransfers))

	if s.uiHandler != nil {
		mux.Handle("/", s.uiHandler)
	}

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
	writeJSON(w, http.StatusOK, s.buildHealthResponse())
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
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
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
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
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
		writeJSON(w, http.StatusOK, map[string]any{"keys": response})
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Permissions string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		raw, created, err := s.apiKeys.Create(user.ID, req.Name, req.Permissions)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
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
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
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

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
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

	from := normalizeAPIPath(r.URL.Query().Get("from"))
	to := normalizeAPIPath(r.URL.Query().Get("to"))
	if from == "" || to == "" {
		var req struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and to are required"})
			return
		}
		from = normalizeAPIPath(req.From)
		to = normalizeAPIPath(req.To)
	}
	if from == "" || to == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and to are required"})
		return
	}
	if err := fsys.Rename(from, to); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
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
	}
	if !isBodyEmpty(r) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	link, err := s.shareLinks.Create(user, p, ttl, maxDownloads)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":         link.Token,
		"path":          link.Path,
		"username":      link.Username,
		"expires_at":    link.ExpiresAt,
		"max_downloads": link.MaxDownloads,
		"share_url":     "/api/v1/share/" + link.Token,
	})
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
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p := normalizeAPIPath(r.URL.Query().Get("path"))
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
			out = append(out, map[string]any{
				"token":          item.Token,
				"username":       item.Username,
				"path":           item.Path,
				"created_at":     item.CreatedAt,
				"expires_at":     item.ExpiresAt,
				"download_count": item.DownloadCount,
				"max_downloads":  item.MaxDownloads,
				"share_url":      "/api/v1/share/" + item.Token,
			})
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
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if err := s.shareLinks.Delete(token); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "token": token})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleShareDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
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
	if token == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
		return
	}

	link, err := s.shareLinks.Get(token)
	if err != nil || link == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
		return
	}
	now := time.Now().UTC()
	if !link.ExpiresAt.IsZero() && now.After(link.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "share link expired"})
		return
	}
	if link.MaxDownloads > 0 && link.DownloadCount >= link.MaxDownloads {
		writeJSON(w, http.StatusGone, map[string]string{"error": "share link download limit exceeded"})
		return
	}

	if s.fsBuilder == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "filesystem builder is not available"})
		return
	}
	fsys, err := s.fsBuilder(link.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to open shared file"})
		return
	}
	f, err := fsys.Open(link.Path, os.O_RDONLY, 0)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "shared file is not available"})
		return
	}
	defer f.Close()

	info, _ := f.Stat()
	if info != nil && info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shared target is a directory"})
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+path.Base(link.Path)+`"`)
	if info != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	if _, err := io.Copy(w, f); err == nil {
		_ = s.shareLinks.Increment(token)
	}
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
	page := parseInt(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	eventType := strings.TrimSpace(r.URL.Query().Get("type"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	lines := strings.Split(string(raw), "\n")
	allEvents := make([]map[string]any, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		allEvents = append(allEvents, evt)
	}
	filtered := filterAuditEvents(allEvents, username, protocol, eventType, status, query)
	eventsPage, total := paginateAudit(filtered, page, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": eventsPage,
		"pagination": map[string]any{
			"page":      page,
			"page_size": limit,
			"total":     total,
		},
	})
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
	page := parseInt(r.URL.Query().Get("page"), 1)
	if page < 1 {
		page = 1
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	active := filterTransfers(s.transfers.Active(), username, protocol, status, direction, query)
	recentAll := filterTransfers(s.transfers.Recent(5000), username, protocol, status, direction, query)
	recentPage, total := paginateTransfers(recentAll, page, limit)

	writeJSON(w, http.StatusOK, map[string]any{
		"active": active,
		"recent": recentPage,
		"stats":  s.transfers.Stats(),
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
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.CORSOrigins[0])
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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

func filterAuditEvents(
	events []map[string]any,
	username, protocol, eventType, status, query string,
) []map[string]any {
	if len(events) == 0 {
		return events
	}
	out := make([]map[string]any, 0, len(events))
	for _, evt := range events {
		u := strField(evt, "username")
		p := strField(evt, "protocol")
		t := strField(evt, "type")
		s := strField(evt, "status")
		msg := strField(evt, "message")
		pathVal := strField(evt, "path")

		if username != "" && !strings.EqualFold(u, username) {
			continue
		}
		if protocol != "" && !strings.EqualFold(p, protocol) {
			continue
		}
		if eventType != "" && !strings.EqualFold(t, eventType) {
			continue
		}
		if status != "" && !strings.EqualFold(s, status) {
			continue
		}
		if query != "" {
			hay := strings.ToLower(strings.Join([]string{u, p, t, s, msg, pathVal}, " "))
			if !strings.Contains(hay, query) {
				continue
			}
		}
		out = append(out, evt)
	}
	return out
}

func paginateAudit(in []map[string]any, page, size int) ([]map[string]any, int) {
	total := len(in)
	if total == 0 {
		return []map[string]any{}, 0
	}
	start := (page - 1) * size
	if start >= total {
		return []map[string]any{}, total
	}
	end := start + size
	if end > total {
		end = total
	}
	return in[start:end], total
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
	raw, err := os.ReadFile(s.auditLogPath)
	if err != nil {
		return []map[string]any{}
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
	return events
}

func normalizeTargetUser(raw, current string) string {
	name := strings.TrimSpace(raw)
	if name == "" || strings.EqualFold(name, "me") || strings.EqualFold(name, "self") {
		return current
	}
	return name
}

func withTargetUser(r *http.Request, username string) *http.Request {
	clone := r.Clone(r.Context())
	clone.Header.Set("X-Auth-User", username)
	return clone
}

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

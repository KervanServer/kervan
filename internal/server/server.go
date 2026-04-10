package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/acme"
	"github.com/kervanserver/kervan/internal/api"
	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/build"
	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/protocol/ftp"
	"github.com/kervanserver/kervan/internal/protocol/sftp"
	"github.com/kervanserver/kervan/internal/quota"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/storage/s3"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"gopkg.in/yaml.v3"
)

type App struct {
	cfg       *config.Config
	logger    *slog.Logger
	store     *store.Store
	authRepo  *auth.UserRepository
	auth      *auth.Engine
	audit     *audit.Engine
	sessions  *session.Manager
	transfers *transfer.Manager

	ftpServer  *ftp.Server
	sftpServer *sftp.Server
	apiServer  *api.Server
	acmeMgr    *acme.Manager
	acmeHTTP   *http.Server

	cancel context.CancelFunc
	wg     sync.WaitGroup
	start  time.Time
}

func New(cfg *config.Config, configPath string, logger *slog.Logger) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		return nil, err
	}

	st, err := store.Open(cfg.Server.DataDir)
	if err != nil {
		return nil, err
	}

	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(
		repo,
		cfg.Auth.PasswordHash,
		cfg.Security.BruteForce.MaxAttempts,
		cfg.Security.BruteForce.LockoutDuration,
	)
	if cfg.Auth.LDAP.Enabled {
		engine.SetLDAPProvider(auth.NewLDAPProvider(cfg.Auth.LDAP))
	}

	sinkPath := filepath.Join(cfg.Server.DataDir, "audit.jsonl")
	if len(cfg.Audit.Outputs) > 0 && cfg.Audit.Outputs[0].Path != "" {
		sinkPath = cfg.Audit.Outputs[0].Path
	}
	fileSink, err := audit.NewFileSink(sinkPath)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	auditEngine := audit.NewEngine(logger, fileSink)

	app := &App{
		cfg:       cfg,
		logger:    logger,
		store:     st,
		authRepo:  repo,
		auth:      engine,
		audit:     auditEngine,
		sessions:  session.NewManager(),
		transfers: transfer.NewManager(2000),
		acmeMgr:   nil,
		start:     time.Now().UTC(),
	}
	if err := app.ensureAdmin(); err != nil {
		_ = app.Close()
		return nil, err
	}

	var sharedTLSConfig *tls.Config
	var acmeManager *acme.Manager
	if cfg.FTPS.AutoCert.Enabled {
		manager, mgrErr := acme.New(acme.Config{
			CacheDir: cfg.FTPS.AutoCert.ACMEDir,
			Email:    cfg.FTPS.AutoCert.ACMEEmail,
			Domains:  cfg.FTPS.AutoCert.Domains,
		})
		if mgrErr != nil {
			_ = app.Close()
			return nil, fmt.Errorf("build acme manager: %w", mgrErr)
		}
		acmeManager = manager
		app.acmeMgr = acmeManager
		minVersion, parseErr := icrypto.ParseTLSVersion(cfg.FTPS.MinTLSVersion)
		if parseErr != nil {
			_ = app.Close()
			return nil, fmt.Errorf("parse min tls version: %w", parseErr)
		}
		maxVersion, parseErr := icrypto.ParseTLSVersion(cfg.FTPS.MaxTLSVersion)
		if parseErr != nil {
			_ = app.Close()
			return nil, fmt.Errorf("parse max tls version: %w", parseErr)
		}
		if minVersion > maxVersion {
			_ = app.Close()
			return nil, fmt.Errorf("min tls version cannot be higher than max tls version")
		}
		sharedTLSConfig = acmeManager.TLSConfig(minVersion, maxVersion)
	} else if strings.TrimSpace(cfg.FTPS.CertFile) != "" || strings.TrimSpace(cfg.FTPS.KeyFile) != "" {
		tlsCfg, tlsErr := icrypto.BuildServerTLSConfig(
			cfg.FTPS.MinTLSVersion,
			cfg.FTPS.MaxTLSVersion,
			cfg.FTPS.CertFile,
			cfg.FTPS.KeyFile,
		)
		if tlsErr != nil {
			_ = app.Close()
			return nil, fmt.Errorf("build ftps tls config: %w", tlsErr)
		}
		sharedTLSConfig = tlsCfg
	}

	app.ftpServer = ftp.NewServer(
		ftp.Config{
			ListenAddr:       cfg.Server.ListenAddress,
			Port:             cfg.FTP.Port,
			Banner:           cfg.FTP.Banner,
			PassivePortRange: cfg.FTP.PassivePortRange,
			PassiveIP:        cfg.FTP.PassiveIP,
			IdleTimeout:      cfg.FTP.IdleTimeout,
			TransferTimeout:  cfg.FTP.TransferTimeout,
			FTPSMode:         cfg.FTPS.Mode,
			FTPSImplicitPort: cfg.FTPS.ImplicitPort,
			TLSConfig:        sharedTLSConfig,
		},
		logger,
		engine,
		app.sessions,
		auditEngine,
		app.buildUserFS,
		app.transfers,
	)

	app.sftpServer = sftp.NewServer(
		sftp.Config{
			ListenAddr:  cfg.Server.ListenAddress,
			Port:        cfg.SFTP.Port,
			HostKeyDir:  cfg.SFTP.HostKeyDir,
			IdleTimeout: cfg.SFTP.IdleTimeout,
		},
		logger,
		engine,
		app.sessions,
		auditEngine,
		func(username string) (vfs.FileSystem, error) {
			user, err := app.authRepo.GetByUsername(username)
			if err != nil {
				return nil, err
			}
			if user == nil {
				return nil, errors.New("user not found")
			}
			return app.buildUserFS(user)
		},
		app.transfers,
	)

	app.apiServer, err = api.NewServer(
		api.Config{
			BindAddress:    cfg.WebUI.BindAddress,
			Port:           cfg.WebUI.Port,
			SessionTimeout: cfg.WebUI.SessionTimeout,
			CORSOrigins:    cfg.WebUI.CORSOrigins,
			TOTPEnabled:    cfg.WebUI.TOTPEnabled,
			TLS:            cfg.WebUI.TLS,
			TLSConfig:      sharedTLSConfig,
		},
		logger,
		engine,
		repo,
		app.sessions,
		func() map[string]any {
			storageBackend, storageRoot := resolveStorageStatus(cfg)
			xstats := app.transfers.Stats()
			tlsCertificate := icrypto.ResolveCertificateInfo(
				cfg.FTPS.CertFile,
				cfg.FTPS.AutoCert.Enabled,
				cfg.FTPS.AutoCert.ACMEDir,
				cfg.FTPS.AutoCert.Domains,
				time.Now().UTC(),
			)
			return map[string]any{
				"name":                  cfg.Server.Name,
				"version":               build.Version,
				"started_at":            app.start,
				"uptime_seconds":        int64(time.Since(app.start).Seconds()),
				"active_sessions":       len(app.sessions.List()),
				"active_transfers":      xstats.ActiveTransfers,
				"upload_bytes":          xstats.UploadBytes,
				"download_bytes":        xstats.DownloadBytes,
				"ftp_enabled":           cfg.FTP.Enabled,
				"ftp_port":              cfg.FTP.Port,
				"ftps_enabled":          cfg.FTPS.Enabled,
				"ftps_mode":             cfg.FTPS.Mode,
				"ftps_explicit_enabled": cfg.FTPS.Enabled && (cfg.FTPS.Mode == "explicit" || cfg.FTPS.Mode == "both"),
				"ftps_implicit_enabled": cfg.FTPS.Enabled && (cfg.FTPS.Mode == "implicit" || cfg.FTPS.Mode == "both"),
				"ftps_implicit_port":    cfg.FTPS.ImplicitPort,
				"sftp_enabled":          cfg.SFTP.Enabled,
				"sftp_port":             cfg.SFTP.Port,
				"scp_enabled":           cfg.SCP.Enabled,
				"webui_enabled":         cfg.WebUI.Enabled,
				"webui_port":            cfg.WebUI.Port,
				"data_dir":              cfg.Server.DataDir,
				"store_path":            filepath.Join(cfg.Server.DataDir, "kervan-store.json"),
				"storage_backend":       storageBackend,
				"storage_root":          storageRoot,
				"audit_log_path":        sinkPath,
				"tls_certificate":       tlsCertificate,
			}
		},
		func() map[string]any {
			source := cfg
			if configPath != "" {
				if loaded, err := config.Load(configPath); err == nil {
					source = loaded
				}
			}
			return redactConfig(source)
		},
		func() (map[string]any, error) {
			if configPath == "" {
				return map[string]any{
					"validated":        false,
					"requires_restart": true,
					"message":          "Config path is not available.",
				}, nil
			}
			nextCfg, err := config.Load(configPath)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"validated":        true,
				"requires_restart": true,
				"message":          "Configuration file is valid. Restart is required to apply runtime changes.",
				"config":           redactConfig(nextCfg),
			}, nil
		},
		func(patch map[string]any) (map[string]any, error) {
			if configPath == "" {
				return nil, errors.New("config path is not available")
			}
			currentCfg, err := config.Load(configPath)
			if err != nil {
				return nil, err
			}
			mergedCfg, changedPaths, err := mergeConfigPatch(currentCfg, patch)
			if err != nil {
				return nil, err
			}
			if err := writeConfigFile(configPath, mergedCfg); err != nil {
				return nil, err
			}
			return map[string]any{
				"updated":          true,
				"requires_restart": true,
				"message":          "Configuration updated on disk. Restart is required to apply changes.",
				"changed_paths":    changedPaths,
				"config":           redactConfig(mergedCfg),
			}, nil
		},
		func(patch map[string]any) (map[string]any, error) {
			if configPath == "" {
				return nil, errors.New("config path is not available")
			}
			currentCfg, err := config.Load(configPath)
			if err != nil {
				return nil, err
			}
			mergedCfg, changedPaths, err := mergeConfigPatch(currentCfg, patch)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"validated":        true,
				"requires_restart": true,
				"changed_paths":    changedPaths,
				"config":           redactConfig(mergedCfg),
			}, nil
		},
		st,
		func(username string) (vfs.FileSystem, error) {
			user, err := app.authRepo.GetByUsername(username)
			if err != nil {
				return nil, err
			}
			if user == nil {
				return nil, errors.New("user not found")
			}
			return app.buildUserFS(user)
		},
		sinkPath,
		app.transfers,
	)
	if err != nil {
		_ = app.Close()
		return nil, err
	}

	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	if a.cfg.FTP.Enabled {
		if err := a.ftpServer.Start(runCtx); err != nil {
			return fmt.Errorf("start ftp: %w", err)
		}
	}
	if a.cfg.FTPS.AutoCert.Enabled {
		acmeMux := http.NewServeMux()
		acmeMux.Handle("/", http.NotFoundHandler())
		if a.acmeMgr == nil {
			_ = a.ftpServer.Stop()
			return fmt.Errorf("start acme manager: not initialized")
		}
		a.acmeHTTP = &http.Server{
			Addr:    net.JoinHostPort(a.cfg.Server.ListenAddress, "80"),
			Handler: a.acmeMgr.HTTPHandler(acmeMux),
		}
		go func() {
			if err := a.acmeHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) && a.logger != nil {
				a.logger.Error("acme http challenge server failed", "error", err)
			}
		}()
		if a.logger != nil {
			a.logger.Info("ACME HTTP challenge server started", "addr", a.acmeHTTP.Addr)
		}
	}
	if a.cfg.SFTP.Enabled {
		if err := a.sftpServer.Start(runCtx); err != nil {
			if a.acmeHTTP != nil {
				_ = a.acmeHTTP.Shutdown(context.Background())
			}
			_ = a.ftpServer.Stop()
			return fmt.Errorf("start sftp: %w", err)
		}
	}
	if a.cfg.WebUI.Enabled {
		if err := a.apiServer.Start(runCtx); err != nil {
			if a.acmeHTTP != nil {
				_ = a.acmeHTTP.Shutdown(context.Background())
			}
			_ = a.sftpServer.Stop()
			_ = a.ftpServer.Stop()
			return fmt.Errorf("start api: %w", err)
		}
	}
	if a.logger != nil {
		a.logger.Info("Kervan server started")
	}
	return nil
}

func (a *App) WaitForContext(ctx context.Context) {
	<-ctx.Done()
}

func (a *App) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if a.ftpServer != nil {
			_ = a.ftpServer.Stop()
		}
		if a.sftpServer != nil {
			_ = a.sftpServer.Stop()
		}
		if a.apiServer != nil {
			_ = a.apiServer.Stop(context.Background())
		}
		if a.acmeHTTP != nil {
			_ = a.acmeHTTP.Shutdown(context.Background())
		}
		if a.audit != nil {
			a.audit.Close()
		}
		if a.store != nil {
			_ = a.store.Close()
		}
	}()

	timeout := a.cfg.Server.GracefulShutdownTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	select {
	case <-done:
	case <-time.After(timeout):
		return errors.New("shutdown timeout exceeded")
	}
	return nil
}

func (a *App) ensureAdmin() error {
	users, err := a.authRepo.List()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u != nil && u.Type == auth.UserTypeAdmin {
			return nil
		}
	}
	password := a.cfg.WebUI.AdminPassword
	if password == "" {
		password = "admin123!"
	}
	_, err = a.auth.CreateUser(
		a.cfg.WebUI.AdminUsername,
		password,
		"/",
		true,
	)
	return err
}

func (a *App) buildUserFS(user *auth.User) (vfs.FileSystem, error) {
	mounts := vfs.NewMountTable()
	backendName := a.cfg.Storage.DefaultBackend
	if backendName == "" {
		backendName = "local"
	}
	backendCfg := a.cfg.Storage.Backends[backendName]
	backendType := backendCfg.Type
	if backendType == "" {
		backendType = "local"
	}
	var rootFS vfs.FileSystem

	switch backendType {
	case "memory":
		rootFS = memory.New()
	case "s3":
		prefix := strings.TrimSpace(backendCfg.Options["prefix"])
		if user.HomeDir != "" && user.HomeDir != "/" {
			homeDir := strings.Trim(user.HomeDir, "/")
			if homeDir != "" {
				prefix = joinStoragePath(prefix, homeDir)
			}
		}
		s3Backend, err := s3.New(s3.Options{
			Endpoint:     backendCfg.Options["endpoint"],
			Region:       backendCfg.Options["region"],
			Bucket:       backendCfg.Options["bucket"],
			Prefix:       prefix,
			AccessKey:    backendCfg.Options["access_key"],
			SecretKey:    backendCfg.Options["secret_key"],
			UsePathStyle: parseBoolOption(backendCfg.Options["force_path_style"]),
			DisableSSL:   parseBoolOption(backendCfg.Options["disable_ssl"]),
		})
		if err != nil {
			return nil, err
		}
		rootFS = s3Backend
	default:
		root := backendCfg.Options["root"]
		if root == "" {
			root = filepath.Join(a.cfg.Server.DataDir, "files")
		}
		if user.HomeDir != "" && user.HomeDir != "/" {
			trimmed := user.HomeDir
			if filepath.IsAbs(trimmed) {
				trimmed = trimmed[1:]
			}
			root = filepath.Join(root, trimmed)
		}
		localBackend, err := local.New(local.Options{
			Root:            root,
			CreateRoot:      true,
			FilePermissions: 0o644,
			DirPermissions:  0o755,
		})
		if err != nil {
			return nil, err
		}
		rootFS = localBackend
	}
	mounts.Mount("/", rootFS, false)

	var quotaTracker vfs.QuotaTracker
	if a.cfg.Quota.Enabled && user != nil && user.Type != auth.UserTypeAdmin && rootFS != nil {
		tracker, err := quota.NewTracker(rootFS, a.cfg.Quota.DefaultMaxStorage)
		if err != nil {
			return nil, err
		}
		quotaTracker = tracker
	}

	perms := &vfs.UserPermissions{
		Upload:      user.Permissions.Upload,
		Download:    user.Permissions.Download,
		Delete:      user.Permissions.Delete,
		Rename:      user.Permissions.Rename,
		CreateDir:   user.Permissions.CreateDir,
		ListDir:     user.Permissions.ListDir,
		Chmod:       user.Permissions.Chmod,
		MaxFileSize: user.Permissions.MaxFileSize,
		AllowedExts: user.Permissions.AllowedExt,
		DeniedExts:  user.Permissions.DeniedExt,
	}
	return vfs.NewUserVFS(mounts, perms, quotaTracker), nil
}

func redactConfig(cfg *config.Config) map[string]any {
	if cfg == nil {
		return map[string]any{}
	}
	out, err := configToMap(cfg)
	if err != nil {
		return map[string]any{}
	}
	redactMap(out)
	return out
}

func redactMap(m map[string]any) {
	for k, v := range m {
		if shouldRedact(k) {
			m[k] = "***REDACTED***"
			continue
		}
		switch typed := v.(type) {
		case map[string]any:
			redactMap(typed)
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					redactMap(nested)
				}
			}
		}
	}
}

func shouldRedact(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "password") {
		return true
	}
	if strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
		return true
	}
	if strings.Contains(lower, "private_key") || strings.Contains(lower, "access_key") {
		return true
	}
	return false
}

func mergeConfigPatch(base *config.Config, patch map[string]any) (*config.Config, []string, error) {
	if base == nil {
		return nil, nil, errors.New("base config is nil")
	}
	if patch == nil {
		return nil, nil, errors.New("patch is nil")
	}

	baseMap, err := configToMap(base)
	if err != nil {
		return nil, nil, err
	}
	if err := validatePatchMap(baseMap, patch, ""); err != nil {
		return nil, nil, err
	}
	changed := make(map[string]struct{})
	deepMergeMap(baseMap, patch, "", changed)

	merged, err := mapToConfig(baseMap)
	if err != nil {
		return nil, nil, err
	}
	if err := merged.Validate(); err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(changed))
	for p := range changed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return merged, paths, nil
}

func deepMergeMap(dst, src map[string]any, prefix string, changed map[string]struct{}) {
	for k, v := range src {
		path := joinPath(prefix, k)
		existing, ok := dst[k]
		if !ok {
			dst[k] = v
			changed[path] = struct{}{}
			continue
		}

		srcMap, srcIsMap := v.(map[string]any)
		dstMap, dstIsMap := existing.(map[string]any)
		if srcIsMap && dstIsMap {
			deepMergeMap(dstMap, srcMap, path, changed)
			dst[k] = dstMap
			continue
		}
		if !isSameJSONValue(existing, v) {
			changed[path] = struct{}{}
		}
		dst[k] = v
	}
}

func validatePatchMap(baseMap, patch map[string]any, prefix string) error {
	for k, v := range patch {
		path := joinPath(prefix, k)
		baseVal, ok := baseMap[k]
		if !ok {
			return fmt.Errorf("unknown config field: %s", path)
		}
		if isSensitivePath(path) {
			return fmt.Errorf("updating sensitive field is not allowed: %s", path)
		}
		if str, ok := v.(string); ok && str == "***REDACTED***" {
			return fmt.Errorf("redacted values are not allowed in patches: %s", path)
		}

		patchMap, patchIsMap := v.(map[string]any)
		baseNested, baseIsMap := baseVal.(map[string]any)
		if baseIsMap && !patchIsMap {
			return fmt.Errorf("field must be an object: %s", path)
		}
		if patchIsMap {
			if !baseIsMap {
				return fmt.Errorf("field is not an object: %s", path)
			}
			if err := validatePatchMap(baseNested, patchMap, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinPath(prefix, key string) string {
	if strings.TrimSpace(prefix) == "" {
		return key
	}
	return prefix + "." + key
}

func isSensitivePath(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
		return true
	}
	return strings.Contains(lower, "private_key")
}

func isSameJSONValue(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(left) == string(right)
}

func configToMap(cfg *config.Config) (map[string]any, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func mapToConfig(input map[string]any) (*config.Config, error) {
	raw, err := yaml.Marshal(input)
	if err != nil {
		return nil, err
	}
	out := &config.Config{}
	if err := yaml.Unmarshal(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeConfigFile(path string, cfg *config.Config) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config path is empty")
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func resolveStorageStatus(cfg *config.Config) (string, string) {
	if cfg == nil {
		return "", ""
	}
	backendName := cfg.Storage.DefaultBackend
	if backendName == "" {
		backendName = "local"
	}
	backendCfg := cfg.Storage.Backends[backendName]
	backendType := backendCfg.Type
	if backendType == "" {
		backendType = "local"
	}
	if backendType != "local" {
		if backendType == "s3" {
			bucket := strings.TrimSpace(backendCfg.Options["bucket"])
			prefix := strings.Trim(strings.TrimSpace(backendCfg.Options["prefix"]), "/")
			if bucket == "" {
				return backendType, ""
			}
			if prefix == "" {
				return backendType, "s3://" + bucket
			}
			return backendType, "s3://" + bucket + "/" + prefix
		}
		return backendType, ""
	}
	root := backendCfg.Options["root"]
	if strings.TrimSpace(root) == "" {
		root = filepath.Join(cfg.Server.DataDir, "files")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return backendType, root
	}
	return backendType, absRoot
}

func parseBoolOption(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func joinStoragePath(parts ...string) string {
	var clean []string
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return path.Join(clean...)
}

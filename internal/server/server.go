package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/api"
	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/build"
	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/protocol/ftp"
	"github.com/kervanserver/kervan/internal/protocol/sftp"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
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

	cancel context.CancelFunc
	wg     sync.WaitGroup
	start  time.Time
}

func New(cfg *config.Config, logger *slog.Logger) (*App, error) {
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
		start:     time.Now().UTC(),
	}
	if err := app.ensureAdmin(); err != nil {
		_ = app.Close()
		return nil, err
	}

	var ftpTLSConfig *tls.Config
	if cfg.FTPS.Enabled {
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
		ftpTLSConfig = tlsCfg
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
			TLSConfig:        ftpTLSConfig,
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
		},
		logger,
		engine,
		repo,
		app.sessions,
		func() map[string]any {
			xstats := app.transfers.Stats()
			return map[string]any{
				"name":             cfg.Server.Name,
				"version":          build.Version,
				"started_at":       app.start,
				"uptime_seconds":   int64(time.Since(app.start).Seconds()),
				"active_sessions":  len(app.sessions.List()),
				"active_transfers": xstats.ActiveTransfers,
				"upload_bytes":     xstats.UploadBytes,
				"download_bytes":   xstats.DownloadBytes,
				"ftp_enabled":      cfg.FTP.Enabled,
				"sftp_enabled":     cfg.SFTP.Enabled,
			}
		},
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
	if a.cfg.SFTP.Enabled {
		if err := a.sftpServer.Start(runCtx); err != nil {
			_ = a.ftpServer.Stop()
			return fmt.Errorf("start sftp: %w", err)
		}
	}
	if a.cfg.WebUI.Enabled {
		if err := a.apiServer.Start(runCtx); err != nil {
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

	switch backendType {
	case "memory":
		mounts.Mount("/", memory.New(), false)
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
		mounts.Mount("/", localBackend, false)
	}

	perms := &vfs.UserPermissions{
		Upload:      user.Permissions.Upload,
		Download:    user.Permissions.Download,
		Delete:      user.Permissions.Delete,
		Rename:      user.Permissions.Rename,
		CreateDir:   user.Permissions.CreateDir,
		ListDir:     user.Permissions.ListDir,
		Chmod:       user.Permissions.Chmod,
		AllowedExts: user.Permissions.AllowedExt,
		DeniedExts:  user.Permissions.DeniedExt,
	}
	return vfs.NewUserVFS(mounts, perms, nil), nil
}

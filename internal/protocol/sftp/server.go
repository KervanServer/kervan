package sftp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/crypto"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	ListenAddr  string
	Port        int
	HostKeyDir  string
	IdleTimeout time.Duration
}

type UserFSBuilder func(username string) (vfs.FileSystem, error)

type Server struct {
	cfg      Config
	logger   *slog.Logger
	auth     *auth.Engine
	sessions *session.Manager
	audit    *audit.Engine
	buildFS  UserFSBuilder
	xfer     *transfer.Manager

	listener net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
}

func NewServer(cfg Config, logger *slog.Logger, authEngine *auth.Engine, sessions *session.Manager, auditEngine *audit.Engine, buildFS UserFSBuilder, xfer *transfer.Manager) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 2222
	}
	if cfg.HostKeyDir == "" {
		cfg.HostKeyDir = "./data/host_keys"
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	return &Server{
		cfg:      cfg,
		logger:   logger,
		auth:     authEngine,
		sessions: sessions,
		audit:    auditEngine,
		buildFS:  buildFS,
		xfer:     xfer,
	}
}

func (s *Server) Start(ctx context.Context) error {
	keyPath, err := crypto.EnsureHostKeys(s.cfg.HostKeyDir)
	if err != nil {
		return err
	}
	signer, err := crypto.LoadSigner(keyPath)
	if err != nil {
		return err
	}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			user, authErr := s.auth.Authenticate(ctx, meta.User(), string(pass))
			if authErr != nil {
				s.emitAudit(audit.EventAuthFailure, meta.User(), "sftp", "", meta.RemoteAddr().String(), "failed", authErr.Error())
				return nil, errors.New("invalid credentials")
			}
			s.emitAudit(audit.EventAuthSuccess, user.Username, "sftp", "", meta.RemoteAddr().String(), "ok", "login success")
			return &ssh.Permissions{
				Extensions: map[string]string{
					"username": user.Username,
					"user_id":  user.ID,
				},
			}, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			user, authErr := s.auth.AuthenticatePublicKey(ctx, meta.User(), key)
			if authErr != nil {
				s.emitAudit(audit.EventAuthFailure, meta.User(), "sftp", "", meta.RemoteAddr().String(), "failed", authErr.Error())
				return nil, errors.New("invalid public key")
			}
			s.emitAudit(audit.EventAuthSuccess, user.Username, "sftp", "", meta.RemoteAddr().String(), "ok", "public key login success")
			return &ssh.Permissions{
				Extensions: map[string]string{
					"username": user.Username,
					"user_id":  user.ID,
				},
			}, nil
		},
	}
	sshCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.ListenAddr, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return err
	}
	s.listener = ln
	if s.logger != nil {
		s.logger.Info("SFTP server started", "addr", ln.Addr().String())
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				if s.isClosed() {
					return
				}
				if s.logger != nil {
					s.logger.Error("sftp accept failed", "error", acceptErr)
				}
				continue
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				s.handleConn(sshCfg, c)
			}(conn)
		}
	}()
	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) handleConn(cfg *ssh.ServerConfig, c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(s.cfg.IdleTimeout))

	sshConn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()

	username := sshConn.Permissions.Extensions["username"]
	userFS, err := s.buildFS(username)
	if err != nil {
		_ = sshConn.Close()
		return
	}
	sess := s.sessions.Start(username, "sftp", c.RemoteAddr().String())
	_ = s.sessions.AttachTerminator(sess.ID, func() {
		_ = c.Close()
		_ = sshConn.Close()
	})
	defer s.sessions.End(sess.ID)
	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, requests, acceptErr := ch.Accept()
		if acceptErr != nil {
			continue
		}
		go s.handleSessionChannel(channel, requests, userFS, username, c.RemoteAddr().String())
	}
}

func (s *Server) handleSessionChannel(ch ssh.Channel, requests <-chan *ssh.Request, fsys vfs.FileSystem, username, remoteAddr string) {
	defer ch.Close()
	for req := range requests {
		switch req.Type {
		case "subsystem":
			if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
				_ = req.Reply(true, nil)
				s.runSFTP(ch, fsys, username, remoteAddr)
				return
			}
			_ = req.Reply(false, nil)
		case "exec":
			command, err := parseExecPayload(req.Payload)
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}
			mode, target, err := parseSCPExec(command)
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}
			_ = req.Reply(true, nil)
			if runErr := s.runSCP(ch, fsys, mode, target, username, remoteAddr); runErr != nil && s.logger != nil {
				s.logger.Debug("scp request failed", "error", runErr, "user", username, "mode", mode, "target", target)
			}
			return
		case "shell":
			_ = req.Reply(false, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *Server) emitAudit(t audit.EventType, username, protocol, p, ip, status, msg string) {
	if s.audit == nil {
		return
	}
	s.audit.Emit(audit.Event{
		Type:     t,
		Username: username,
		Protocol: protocol,
		Path:     p,
		IP:       ip,
		Status:   status,
		Message:  msg,
	})
}

func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

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
	"github.com/kervanserver/kervan/internal/netguard"
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
	// IPFilter rejects connections from denied addresses; nil admits all.
	IPFilter *netguard.IPFilter
	// MaxConnections caps concurrent SSH connections; <= 0 is unlimited.
	MaxConnections int
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
	limiter  *netguard.Limiter

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
		limiter:  netguard.NewLimiter(cfg.MaxConnections),
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
			remote := meta.RemoteAddr().String()
			throttle := s.auth.IPThrottle()
			if banErr := throttle.Check(remote, time.Now()); banErr != nil {
				s.emitAudit(audit.EventAuthFailure, meta.User(), "sftp", "", remote, "failed", banErr.Error())
				return nil, banErr
			}
			user, authErr := s.auth.Authenticate(ctx, meta.User(), string(pass))
			if authErr != nil {
				throttle.RecordFailure(remote, time.Now())
				s.emitAudit(audit.EventAuthFailure, meta.User(), "sftp", "", remote, "failed", authErr.Error())
				return nil, errors.New("invalid credentials")
			}
			throttle.RecordSuccess(remote)
			_ = s.auth.RecordSuccessfulLogin(user.ID)
			s.emitAudit(audit.EventAuthSuccess, user.Username, "sftp", "", meta.RemoteAddr().String(), "ok", "login success")
			return &ssh.Permissions{
				Extensions: map[string]string{
					"username": user.Username,
					"user_id":  user.ID,
				},
			}, nil
		},
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			// A rejected public key is a normal step of SSH auth negotiation
			// (clients offer every key they hold), so it is not counted as a
			// failure; only an active ban is enforced here.
			if banErr := s.auth.IPThrottle().Check(meta.RemoteAddr().String(), time.Now()); banErr != nil {
				return nil, banErr
			}
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
		acceptFailures := 0
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				if s.isClosed() {
					return
				}
				acceptFailures++
				if s.logger != nil {
					s.logger.Error("sftp accept failed", "error", acceptErr)
				}
				time.Sleep(netguard.AcceptBackoff(acceptFailures))
				continue
			}
			acceptFailures = 0
			if !s.admit(conn) {
				continue
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				defer s.limiter.Release()
				defer s.recoverConnPanic(c)
				s.handleConn(sshCfg, c)
			}(conn)
		}
	}()
	return nil
}

// admit applies the IP filter and connection cap to a freshly accepted
// connection. A rejected connection is closed; on success the caller owns one
// limiter slot and must Release it.
func (s *Server) admit(conn net.Conn) bool {
	remote := conn.RemoteAddr().String()
	if !s.cfg.IPFilter.AllowedRemote(remote) {
		// Not audited: a scanner on a denied range would flood the audit log.
		if s.logger != nil {
			s.logger.Debug("sftp connection rejected", "remote_addr", remote, "reason", "ip not allowed")
		}
		_ = conn.Close()
		return false
	}
	if !s.limiter.TryAcquire() {
		if s.logger != nil {
			s.logger.Warn("sftp connection rejected", "remote_addr", remote, "reason", "max connections reached")
		}
		s.emitAudit(audit.EventConnectionRejected, "", "sftp", "", remote, "rejected", "max connections reached")
		_ = conn.Close()
		return false
	}
	return true
}

func (s *Server) recoverConnPanic(conn net.Conn) {
	if recovered := recover(); recovered != nil {
		if s.logger != nil {
			s.logger.Error("sftp connection panicked", "panic", recovered, "remote_addr", conn.RemoteAddr().String())
		}
		_ = conn.Close()
	}
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
	touch := func() { _ = c.SetDeadline(time.Now().Add(s.cfg.IdleTimeout)) }
	touch()

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
	renewDeadline := touch
	touch = func() {
		renewDeadline()
		s.sessions.Touch(sess.ID)
	}
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
		go s.handleSessionChannel(channel, requests, userFS, username, c.RemoteAddr().String(), touch)
	}
}

func (s *Server) handleSessionChannel(ch ssh.Channel, requests <-chan *ssh.Request, fsys vfs.FileSystem, username, remoteAddr string, touch func()) {
	defer ch.Close()
	for req := range requests {
		switch req.Type {
		case "subsystem":
			if len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
				_ = req.Reply(true, nil)
				s.runSFTP(ch, fsys, username, remoteAddr, touch)
				// Without an exit-status the OpenSSH client reports failure
				// (scp exits 1) even though every operation succeeded.
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
			_ = req.Reply(false, nil)
		case "exec":
			command, err := parseExecPayload(req.Payload)
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}
			scpReq, err := parseSCPExec(command)
			if err != nil {
				_ = req.Reply(false, nil)
				return
			}
			_ = req.Reply(true, nil)
			exitStatus := uint32(0)
			if runErr := s.runSCP(ch, fsys, scpReq, username, remoteAddr, touch); runErr != nil {
				exitStatus = 1
				if s.logger != nil {
					s.logger.Debug("scp request failed", "error", runErr, "user", username, "mode", scpReq.mode, "target", scpReq.target)
				}
			}
			// Report the outcome like a real remote scp process would, so
			// clients surface failures instead of assuming success.
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exitStatus}))
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

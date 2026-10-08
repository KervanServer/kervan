package ftp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/netguard"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
)

type Config struct {
	ListenAddr       string
	Port             int
	Banner           string
	PassivePortRange string
	PassiveIP        string
	IdleTimeout      time.Duration
	TransferTimeout  time.Duration
	FTPSMode         string
	FTPSImplicitPort int
	TLSConfig        *tls.Config
	// ActiveMode enables PORT/EPRT. Active data connections are only ever
	// dialed back to the control connection's own address (no FXP/bounce).
	ActiveMode bool
	// IPFilter rejects control connections from denied addresses; nil admits all.
	IPFilter *netguard.IPFilter
	// MaxConnections caps concurrent control connections across all FTP
	// listeners; <= 0 is unlimited.
	MaxConnections int
	// VirtualHosts (RFC 7151 HOST) keyed by lower-case host name. When
	// empty, any HOST is accepted and nothing changes.
	VirtualHosts map[string]VirtualHost
}

// VirtualHost customizes the server for clients that name it with HOST (or
// TLS SNI).
type VirtualHost struct {
	Banner string
	// AllowedGroups, when non-empty, limits logins to members (primary or
	// secondary) of at least one of these groups.
	AllowedGroups []string
}

type UserFSBuilder func(*auth.User) (vfs.FileSystem, error)

type Server struct {
	cfg      Config
	logger   *slog.Logger
	auth     *auth.Engine
	sessions *session.Manager
	audit    *audit.Engine
	buildFS  UserFSBuilder
	xfer     *transfer.Manager
	limiter  *netguard.Limiter

	listeners []net.Listener
	wg        sync.WaitGroup
	mu        sync.Mutex
	closed    bool
}

func NewServer(cfg Config, logger *slog.Logger, authEngine *auth.Engine, sessions *session.Manager, auditEngine *audit.Engine, buildFS UserFSBuilder, xfer *transfer.Manager) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "0.0.0.0"
	}
	if cfg.Port == 0 {
		cfg.Port = 2121
	}
	if cfg.Banner == "" {
		cfg.Banner = "Welcome to Kervan File Server"
	}
	if cfg.PassivePortRange == "" {
		cfg.PassivePortRange = "50000-50100"
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.TransferTimeout <= 0 {
		cfg.TransferTimeout = 1 * time.Hour
	}
	if cfg.FTPSMode == "" {
		cfg.FTPSMode = "explicit"
	}
	if cfg.FTPSImplicitPort == 0 {
		cfg.FTPSImplicitPort = 990
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
	if err := s.startListener(ctx, s.cfg.Port, "FTP", false); err != nil {
		return err
	}
	if s.ftpsImplicitEnabled() {
		if err := s.startListener(ctx, s.cfg.FTPSImplicitPort, "FTPS-implicit", true); err != nil {
			_ = s.Stop()
			return err
		}
	}
	if s.ftpsExplicitEnabled() && s.logger != nil {
		s.logger.Info("FTPS explicit enabled on FTP listener", "port", s.cfg.Port)
	}
	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	for _, ln := range s.listeners {
		_ = ln.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) startListener(ctx context.Context, port int, label string, implicitTLS bool) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(s.cfg.ListenAddr, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	s.listeners = append(s.listeners, ln)
	if s.logger != nil {
		s.logger.Info(label+" server started", "addr", ln.Addr().String())
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
					s.logger.Error("ftp accept failed", "error", acceptErr, "listener", label)
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
				s.handleConn(ctx, c, implicitTLS)
			}(conn)
		}
	}()
	return nil
}

// admit applies the IP filter and connection cap to a freshly accepted
// control connection. A rejected connection is closed; on success the caller
// owns one limiter slot and must Release it.
func (s *Server) admit(conn net.Conn) bool {
	remote := conn.RemoteAddr().String()
	if !s.cfg.IPFilter.AllowedRemote(remote) {
		// Not audited: a scanner on a denied range would flood the audit log.
		if s.logger != nil {
			s.logger.Debug("ftp connection rejected", "remote_addr", remote, "reason", "ip not allowed")
		}
		_ = conn.Close()
		return false
	}
	if !s.limiter.TryAcquire() {
		s.emitRejection(remote, "max connections reached")
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write([]byte("421 Too many connections, try again later.\r\n"))
		_ = conn.Close()
		return false
	}
	return true
}

func (s *Server) emitRejection(remote, reason string) {
	if s.logger != nil {
		s.logger.Warn("ftp connection rejected", "remote_addr", remote, "reason", reason)
	}
	if s.audit != nil {
		s.audit.Emit(audit.Event{
			Type:     audit.EventConnectionRejected,
			Protocol: "ftp",
			IP:       remote,
			Status:   "rejected",
			Message:  reason,
		})
	}
}

func (s *Server) recoverConnPanic(conn net.Conn) {
	if recovered := recover(); recovered != nil {
		if s.logger != nil {
			s.logger.Error("ftp connection panicked", "panic", recovered, "remote_addr", conn.RemoteAddr().String())
		}
		_ = conn.Close()
	}
}

func (s *Server) ftpsExplicitEnabled() bool {
	mode := strings.ToLower(s.cfg.FTPSMode)
	return s.cfg.TLSConfig != nil && (mode == "explicit" || mode == "both")
}

func (s *Server) ftpsImplicitEnabled() bool {
	mode := strings.ToLower(s.cfg.FTPSMode)
	return s.cfg.TLSConfig != nil && (mode == "implicit" || mode == "both")
}

type connState struct {
	username        string
	user            *auth.User
	session         *session.Session
	cwd             string
	fs              vfs.FileSystem
	rnfr            string
	passiveLn       net.Listener
	activeAddr      string
	restOffset      int64 // REST marker for the next RETR/STOR
	vhost           string
	passiveIP       string
	remoteAddr      string
	secureControl   bool
	pbszSet         bool
	dataProtPrivate bool
}

// maxControlLineBytes bounds a single FTP control-channel line: legitimate
// command lines are a few kilobytes at most, and an uncapped ReadString would
// let any pre-authentication client grow the connection buffer without bound
// toward process OOM.
const maxControlLineBytes = 64 << 10

// readControlLine reads one '\n'-terminated line, capping total accumulation
// at maxControlLineBytes. An over-cap line returns an error, which the command
// loop treats like any read error: the connection is closed.
func readControlLine(reader *bufio.Reader) (string, error) {
	var buf []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if err == nil {
			buf = append(buf, chunk...)
			return string(buf), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			buf = append(buf, chunk...)
			if len(buf) > maxControlLineBytes {
				return "", fmt.Errorf("control line exceeds %d bytes", maxControlLineBytes)
			}
			continue
		}
		if len(buf) > 0 {
			buf = append(buf, chunk...)
			return string(buf), err
		}
		return "", err
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn, implicitTLS bool) {
	defer conn.Close()
	state := &connState{
		cwd:             "/",
		passiveIP:       s.cfg.PassiveIP,
		remoteAddr:      conn.RemoteAddr().String(),
		secureControl:   false,
		pbszSet:         false,
		dataProtPrivate: false,
	}
	if implicitTLS {
		tlsConn := tls.Server(conn, s.cfg.TLSConfig)
		if err := tlsConn.Handshake(); err != nil {
			if s.logger != nil {
				s.logger.Debug("implicit tls handshake failed", "error", err)
			}
			return
		}
		conn = tlsConn
		state.secureControl = true
		s.adoptSNIHost(state, tlsConn)
	}
	reader := bufio.NewReader(conn)
	banner := s.cfg.Banner
	if vh, ok := s.cfg.VirtualHosts[state.vhost]; ok && vh.Banner != "" {
		banner = vh.Banner
	}
	writeReply(conn, 220, banner)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		line, err := readControlLine(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && s.logger != nil {
				s.logger.Debug("ftp read error", "error", err)
			}
			s.cleanupConnState(state)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if state.session != nil {
			s.sessions.Touch(state.session.ID)
		}

		cmd, arg := splitCommand(line)
		switch cmd {
		case "HOST":
			// RFC 7151: HOST must precede USER/login.
			if state.user != nil || state.username != "" {
				writeReply(conn, 503, "HOST must be sent before USER.")
				continue
			}
			name := normalizeHostName(arg)
			if name == "" {
				writeReply(conn, 501, "Syntax error in host name.")
				continue
			}
			vh, known := s.cfg.VirtualHosts[name]
			if len(s.cfg.VirtualHosts) > 0 && !known {
				writeReply(conn, 504, "Unknown virtual host.")
				continue
			}
			state.vhost = name
			if vh.Banner != "" {
				writeReply(conn, 220, vh.Banner)
			} else {
				writeReply(conn, 220, "Host accepted.")
			}
		case "USER":
			state.username = arg
			writeReply(conn, 331, "User name okay, need password.")
		case "PASS":
			if state.username == "" {
				writeReply(conn, 503, "Login with USER first.")
				continue
			}
			throttle := s.auth.IPThrottle()
			if banErr := throttle.Check(state.remoteAddr, time.Now()); banErr != nil {
				writeReply(conn, 421, "Too many failed logins, try again later.")
				s.emitAudit(audit.EventAuthFailure, state.username, "ftp", "", state.remoteAddr, "failed", banErr.Error())
				s.cleanupConnState(state)
				return
			}
			user, authErr := s.auth.Authenticate(ctx, state.username, arg)
			if authErr != nil {
				throttle.RecordFailure(state.remoteAddr, time.Now())
				writeReply(conn, 530, "Login incorrect.")
				s.emitAudit(audit.EventAuthFailure, state.username, "ftp", "", state.remoteAddr, "failed", authErr.Error())
				continue
			}
			throttle.RecordSuccess(state.remoteAddr)
			if !s.vhostAllows(state.vhost, user) {
				writeReply(conn, 530, "Login incorrect.")
				s.emitAudit(audit.EventAuthFailure, user.Username, "ftp", "", state.remoteAddr, "failed", "not permitted on virtual host "+state.vhost)
				continue
			}
			userFS, fsErr := s.buildFS(user)
			if fsErr != nil {
				writeReply(conn, 550, "Unable to mount filesystem.")
				continue
			}
			_ = s.auth.RecordSuccessfulLogin(user.ID)
			state.user = user
			state.fs = userFS
			state.cwd = "/"
			// A successful re-login on the same connection replaces the
			// previous session; end it first so it does not linger in the
			// manager (its stale terminator would otherwise close this
			// connection if the orphan were ever killed).
			if state.session != nil {
				s.sessions.End(state.session.ID)
			}
			state.session = s.sessions.Start(user.Username, "ftp", state.remoteAddr)
			_ = s.sessions.AttachTerminator(state.session.ID, func() {
				_ = conn.Close()
			})
			writeReply(conn, 230, "User logged in, proceed.")
			loginMsg := "login success"
			if state.vhost != "" {
				loginMsg += " (host " + state.vhost + ")"
			}
			s.emitAudit(audit.EventAuthSuccess, user.Username, "ftp", "", state.remoteAddr, "ok", loginMsg)
		case "QUIT":
			writeReply(conn, 221, "Goodbye.")
			s.cleanupConnState(state)
			return
		case "NOOP":
			writeReply(conn, 200, "OK")
		case "SYST":
			writeReply(conn, 215, "UNIX Type: L8")
		case "FEAT":
			features := []string{
				" UTF8",
				" PASV",
				" EPSV",
				" REST STREAM",
				" HOST",
				" SIZE",
				" MDTM",
				" MLST type*;size*;modify*;",
				" MLSD",
			}
			if s.cfg.ActiveMode {
				features = append(features, " EPRT")
			}
			if s.ftpsExplicitEnabled() || implicitTLS {
				features = append(features, " AUTH TLS", " PBSZ", " PROT")
			}
			// writeMultiline renders lines[0] on the opening reply and
			// lines[last] on the terminating reply, so the feature list needs
			// explicit framing elements — otherwise the first and last features
			// would be consumed by the framing and stay invisible to clients
			// (RFC 2389 §3.2).
			framed := make([]string, 0, len(features)+2)
			framed = append(framed, "Features:")
			framed = append(framed, features...)
			framed = append(framed, "End")
			writeMultiline(conn, 211, framed)
		case "OPTS":
			writeReply(conn, 200, "OK")
		case "PWD":
			if !isAuthed(conn, state) {
				continue
			}
			writeReply(conn, 257, fmt.Sprintf("\"%s\" is current directory.", state.cwd))
		case "TYPE":
			if arg != "I" && arg != "A" {
				writeReply(conn, 504, "Type not supported.")
				continue
			}
			writeReply(conn, 200, "Type set.")
		case "CWD", "CDUP":
			if !isAuthed(conn, state) {
				continue
			}
			if cmd == "CDUP" {
				arg = ".."
			}
			target := resolvePath(state.cwd, arg)
			info, statErr := state.fs.Stat(target)
			if statErr != nil || !info.IsDir() {
				writeReply(conn, 550, "Failed to change directory.")
				continue
			}
			state.cwd = target
			writeReply(conn, 250, "Directory changed.")
		case "MKD":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			if err := state.fs.Mkdir(p, 0o755); err != nil {
				writeReply(conn, 550, "Create directory failed.")
				continue
			}
			writeReply(conn, 257, fmt.Sprintf("\"%s\" created.", p))
		case "RMD":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			if err := state.fs.Remove(p); err != nil {
				writeReply(conn, 550, "Remove directory failed.")
				continue
			}
			writeReply(conn, 250, "Directory removed.")
		case "DELE":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			if err := state.fs.Remove(p); err != nil {
				writeReply(conn, 550, "Delete failed.")
				continue
			}
			writeReply(conn, 250, "Delete successful.")
			s.emitAudit(audit.EventFileDelete, state.user.Username, "ftp", p, state.remoteAddr, "ok", "file deleted")
		case "RNFR":
			if !isAuthed(conn, state) {
				continue
			}
			state.rnfr = resolvePath(state.cwd, arg)
			if _, err := state.fs.Stat(state.rnfr); err != nil {
				state.rnfr = ""
				writeReply(conn, 550, "File unavailable.")
				continue
			}
			writeReply(conn, 350, "Requested file action pending further information.")
		case "RNTO":
			if !isAuthed(conn, state) {
				continue
			}
			if state.rnfr == "" {
				writeReply(conn, 503, "Need RNFR first.")
				continue
			}
			to := resolvePath(state.cwd, arg)
			if err := state.fs.Rename(state.rnfr, to); err != nil {
				writeReply(conn, 550, "Rename failed.")
				continue
			}
			state.rnfr = ""
			writeReply(conn, 250, "Rename successful.")
		case "SIZE":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			info, err := state.fs.Stat(p)
			if err != nil || info.IsDir() {
				writeReply(conn, 550, "File unavailable.")
				continue
			}
			writeReply(conn, 213, strconv.FormatInt(info.Size(), 10))
		case "MDTM":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			info, err := state.fs.Stat(p)
			if err != nil {
				writeReply(conn, 550, "File unavailable.")
				continue
			}
			writeReply(conn, 213, info.ModTime().UTC().Format("20060102150405"))
		case "PASV":
			if !isAuthed(conn, state) {
				continue
			}
			if err := s.enterPassiveMode(state); err != nil {
				writeReply(conn, 425, "Can't open passive connection.")
				continue
			}
			host, port, _ := net.SplitHostPort(state.passiveLn.Addr().String())
			if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
				host = ip.String()
			}
			if state.passiveIP != "" {
				host = state.passiveIP
			}
			h := strings.Split(host, ".")
			if len(h) != 4 {
				writeReply(conn, 425, "Passive address is not IPv4.")
				continue
			}
			p, _ := strconv.Atoi(port)
			writeReply(conn, 227, fmt.Sprintf("Entering Passive Mode (%s,%s,%s,%s,%d,%d).", h[0], h[1], h[2], h[3], p/256, p%256))
		case "EPSV":
			if !isAuthed(conn, state) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(arg), "ALL") {
				writeReply(conn, 200, "EPSV ALL ok.")
				continue
			}
			if proto := strings.TrimSpace(arg); proto != "" && proto != "1" && proto != "2" {
				writeReply(conn, 522, "Network protocol not supported, use (1,2)")
				continue
			}
			if err := s.enterPassiveMode(state); err != nil {
				writeReply(conn, 425, "Can't open passive connection.")
				continue
			}
			_, port, _ := net.SplitHostPort(state.passiveLn.Addr().String())
			writeReply(conn, 229, fmt.Sprintf("Entering Extended Passive Mode (|||%s|)", port))
		case "PORT", "EPRT":
			if !isAuthed(conn, state) {
				continue
			}
			if !s.cfg.ActiveMode {
				writeReply(conn, 502, "Active mode is disabled; use PASV or EPSV.")
				continue
			}
			var target string
			var parseErr error
			if cmd == "PORT" {
				target, parseErr = parsePORTArg(arg)
			} else {
				target, parseErr = parseEPRTArg(arg)
			}
			if parseErr != nil {
				writeReply(conn, 501, "Syntax error in parameters.")
				continue
			}
			if reason := validateActiveTarget(target, state.remoteAddr); reason != "" {
				if s.logger != nil {
					s.logger.Warn("rejecting ftp active data target", "target", target, "control", state.remoteAddr, "reason", reason)
				}
				writeReply(conn, 504, "Illegal PORT/EPRT target.")
				continue
			}
			if state.passiveLn != nil {
				_ = state.passiveLn.Close()
				state.passiveLn = nil
			}
			state.activeAddr = target
			writeReply(conn, 200, cmd+" command successful.")
		case "LIST", "NLST", "MLSD":
			if !isAuthed(conn, state) {
				continue
			}
			target := state.cwd
			if strings.TrimSpace(arg) != "" {
				target = resolvePath(state.cwd, arg)
			}
			dc, err := s.acceptDataConn(state)
			if err != nil {
				writeReply(conn, 425, "Use PASV, EPSV or PORT first.")
				continue
			}
			writeReply(conn, 150, "Opening data connection.")
			err = writeListing(dc, state.fs, target, cmd)
			// A zero-entry listing performs no I/O, so the lazy tls.Server
			// handshake must be completed explicitly before the close —
			// otherwise FTPS clients see EOF mid-handshake (RFC 4217).
			if tc, ok := dc.(*tls.Conn); ok {
				if hsErr := tc.Handshake(); hsErr != nil && err == nil {
					err = hsErr
				}
			}
			_ = dc.Close()
			if err != nil {
				writeReply(conn, 550, "Listing failed.")
				continue
			}
			writeReply(conn, 226, "Transfer complete.")
		case "MLST":
			// RFC 3659 §7: MLST returns the machine-facts entry for a single
			// file or directory on the CONTROL channel (MLSD lists a
			// directory over the data connection). FEAT advertises MLST, so
			// the command must be honored, not rejected as unimplemented.
			if !isAuthed(conn, state) {
				continue
			}
			mlstTarget := state.cwd
			if strings.TrimSpace(arg) != "" {
				mlstTarget = resolvePath(state.cwd, arg)
			}
			var buf bytes.Buffer
			if err := writeListing(&buf, state.fs, mlstTarget, "MLST"); err != nil {
				writeReply(conn, 550, "Listing failed.")
				continue
			}
			if _, err := fmt.Fprintf(conn, "250-Listing %s\r\n", mlstTarget); err != nil {
				continue
			}
			_, _ = conn.Write(buf.Bytes())
			_, _ = fmt.Fprintf(conn, "250 End\r\n")
		case "REST":
			if !isAuthed(conn, state) {
				continue
			}
			offset, parseErr := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
			if parseErr != nil || offset < 0 {
				writeReply(conn, 501, "Invalid restart marker.")
				continue
			}
			state.restOffset = offset
			writeReply(conn, 350, fmt.Sprintf("Restarting at %d. Send STOR or RETR.", offset))
		case "RETR":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			offset := state.restOffset
			state.restOffset = 0
			dc, err := s.acceptDataConn(state)
			if err != nil {
				writeReply(conn, 425, "Use PASV, EPSV or PORT first.")
				continue
			}
			f, err := state.fs.Open(p, os.O_RDONLY, 0)
			if err != nil {
				_ = dc.Close()
				writeReply(conn, 550, "File unavailable.")
				continue
			}
			var total int64
			if info, statErr := f.Stat(); statErr == nil {
				total = info.Size()
			}
			if offset > 0 {
				if offset > total {
					_ = f.Close()
					_ = dc.Close()
					writeReply(conn, 554, "Restart marker beyond end of file.")
					continue
				}
				if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
					_ = f.Close()
					_ = dc.Close()
					writeReply(conn, 554, "Cannot restart at the requested offset.")
					continue
				}
				total -= offset
			}
			transferID := ""
			if s.xfer != nil {
				transferID = s.xfer.Start(state.user.Username, "ftp", p, transfer.DirectionDownload, total)
			}
			writeReply(conn, 150, "Opening binary mode data connection.")
			n, err := io.Copy(dc, f)
			// A zero-byte file means io.Copy never touches dc, so the lazy
			// tls.Server handshake must be completed explicitly (RFC 4217).
			if tc, ok := dc.(*tls.Conn); ok {
				if hsErr := tc.Handshake(); hsErr != nil && err == nil {
					err = hsErr
				}
			}
			closeFileErr := f.Close()
			closeDataErr := dc.Close()
			if err == nil {
				if closeFileErr != nil {
					err = closeFileErr
				} else if closeDataErr != nil {
					err = closeDataErr
				}
			}
			if err != nil {
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				writeReply(conn, 426, "Transfer aborted.")
				continue
			}
			if s.xfer != nil && transferID != "" {
				s.xfer.AddBytes(transferID, n)
				s.xfer.End(transferID, transfer.StatusCompleted, "")
			}
			writeReply(conn, 226, "Transfer complete.")
			s.emitAudit(audit.EventFileRead, state.user.Username, "ftp", p, state.remoteAddr, "ok", "download")
		case "STOR", "APPE":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			offset := state.restOffset
			state.restOffset = 0
			dc, err := s.acceptDataConn(state)
			if err != nil {
				writeReply(conn, 425, "Use PASV, EPSV or PORT first.")
				continue
			}
			flags := os.O_CREATE | os.O_WRONLY
			switch {
			case cmd == "APPE":
				flags |= os.O_APPEND
			case offset == 0:
				flags |= os.O_TRUNC
			}
			f, err := state.fs.Open(p, flags, 0o644)
			if err != nil {
				_ = dc.Close()
				writeReply(conn, 550, "Cannot open target file.")
				continue
			}
			if cmd == "STOR" && offset > 0 {
				// Resumed upload: keep the bytes the client already sent and
				// continue writing at the restart marker.
				if _, seekErr := f.Seek(offset, io.SeekStart); seekErr != nil {
					_ = f.Close()
					_ = dc.Close()
					writeReply(conn, 554, "Cannot restart at the requested offset.")
					continue
				}
			}
			transferID := ""
			if s.xfer != nil {
				transferID = s.xfer.Start(state.user.Username, "ftp", p, transfer.DirectionUpload, -1)
			}
			writeReply(conn, 150, "Ok to send data.")
			n, err := io.Copy(f, dc)
			closeDataErr := dc.Close()
			closeFileErr := f.Close()
			if err == nil {
				if closeFileErr != nil {
					err = closeFileErr
				} else if closeDataErr != nil {
					err = closeDataErr
				}
			}
			if err != nil {
				if s.xfer != nil && transferID != "" {
					s.xfer.AddBytes(transferID, n)
					s.xfer.End(transferID, transfer.StatusFailed, err.Error())
				}
				writeReply(conn, 426, "Transfer aborted.")
				continue
			}
			if s.xfer != nil && transferID != "" {
				s.xfer.AddBytes(transferID, n)
				s.xfer.End(transferID, transfer.StatusCompleted, "")
			}
			writeReply(conn, 226, "Transfer complete.")
			s.emitAudit(audit.EventFileWrite, state.user.Username, "ftp", p, state.remoteAddr, "ok", "upload")
		case "AUTH":
			if !s.ftpsExplicitEnabled() {
				writeReply(conn, 502, "TLS not configured.")
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(arg), "TLS") {
				writeReply(conn, 504, "Only AUTH TLS is supported.")
				continue
			}
			if state.secureControl {
				writeReply(conn, 503, "Already using TLS.")
				continue
			}
			writeReply(conn, 234, "AUTH TLS successful.")
			// Commands the client pipelined with AUTH TLS may already sit in
			// the old reader's buffer; requeue them so they are processed
			// over the new TLS control channel instead of being silently
			// dropped by the reader swap.
			var pending []byte
			if n := reader.Buffered(); n > 0 {
				if pendingBytes, peekErr := reader.Peek(n); peekErr == nil {
					pending = append([]byte(nil), pendingBytes...)
				}
			}
			tlsConn := tls.Server(conn, s.cfg.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				if s.logger != nil {
					s.logger.Debug("explicit tls handshake failed", "error", err)
				}
				s.cleanupConnState(state)
				return
			}
			conn = tlsConn
			if len(pending) > 0 {
				reader = bufio.NewReader(io.MultiReader(bytes.NewReader(pending), tlsConn))
			} else {
				reader = bufio.NewReader(conn)
			}
			state.secureControl = true
			if state.username == "" {
				s.adoptSNIHost(state, tlsConn)
			}
			state.pbszSet = false
			state.dataProtPrivate = false
		case "PBSZ":
			if !state.secureControl {
				writeReply(conn, 503, "Secure control connection required.")
				continue
			}
			if strings.TrimSpace(arg) != "0" {
				writeReply(conn, 501, "PBSZ must be 0.")
				continue
			}
			state.pbszSet = true
			writeReply(conn, 200, "PBSZ=0")
		case "PROT":
			if !state.secureControl {
				writeReply(conn, 503, "Secure control connection required.")
				continue
			}
			if !state.pbszSet {
				writeReply(conn, 503, "Send PBSZ 0 first.")
				continue
			}
			switch strings.ToUpper(strings.TrimSpace(arg)) {
			case "P":
				state.dataProtPrivate = true
				writeReply(conn, 200, "Data channel protection set to Private.")
			case "C":
				state.dataProtPrivate = false
				writeReply(conn, 200, "Data channel protection set to Clear.")
			default:
				writeReply(conn, 504, "PROT accepts only C or P.")
			}
		default:
			writeReply(conn, 502, "Command not implemented.")
		}
	}
}

// normalizeHostName canonicalizes a HOST argument: lower case, no trailing
// dot, IPv6 literal brackets removed. Empty when malformed.
func normalizeHostName(raw string) string {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	if name == "" || len(name) > 253 {
		return ""
	}
	for _, r := range name {
		if !(r == '-' || r == '.' || r == ':' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return name
}

// adoptSNIHost uses the TLS server name as the virtual host when the client
// has not sent HOST and the name is configured.
func (s *Server) adoptSNIHost(state *connState, tlsConn *tls.Conn) {
	if state.vhost != "" || len(s.cfg.VirtualHosts) == 0 {
		return
	}
	name := normalizeHostName(tlsConn.ConnectionState().ServerName)
	if _, ok := s.cfg.VirtualHosts[name]; ok {
		state.vhost = name
	}
}

// vhostAllows applies the virtual host's group restriction to user.
func (s *Server) vhostAllows(vhost string, user *auth.User) bool {
	vh, ok := s.cfg.VirtualHosts[vhost]
	if !ok || len(vh.AllowedGroups) == 0 {
		return true
	}
	for _, allowed := range vh.AllowedGroups {
		if strings.EqualFold(user.PrimaryGroup, allowed) {
			return true
		}
		for _, g := range user.SecondaryGrps {
			if strings.EqualFold(g, allowed) {
				return true
			}
		}
	}
	return false
}

func (s *Server) cleanupConnState(state *connState) {
	state.activeAddr = ""
	if state.passiveLn != nil {
		_ = state.passiveLn.Close()
		state.passiveLn = nil
	}
	if state.session != nil {
		s.sessions.End(state.session.ID)
		state.session = nil
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

func (s *Server) enterPassiveMode(state *connState) error {
	state.activeAddr = ""
	if state.passiveLn != nil {
		_ = state.passiveLn.Close()
		state.passiveLn = nil
	}
	start, end, err := parsePortRange(s.cfg.PassivePortRange)
	if err != nil {
		return err
	}
	var ln net.Listener
	for p := start; p <= end; p++ {
		candidate, listenErr := net.Listen("tcp", net.JoinHostPort(s.cfg.ListenAddr, strconv.Itoa(p)))
		if listenErr == nil {
			ln = candidate
			break
		}
	}
	if ln == nil {
		return errors.New("no passive port available")
	}
	state.passiveLn = ln
	return nil
}

func (s *Server) acceptDataConn(state *connState) (net.Conn, error) {
	if state.activeAddr != "" {
		target := state.activeAddr
		state.activeAddr = ""
		conn, err := net.DialTimeout("tcp", target, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return s.wrapDataConn(state, conn), nil
	}
	if state.passiveLn == nil {
		return nil, errors.New("passive listener not ready")
	}
	ln := state.passiveLn
	state.passiveLn = nil
	defer func() {
		_ = ln.Close()
	}()
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	controlHost := hostFromAddr(state.remoteAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil, err
		}
		dataHost := hostFromAddr(conn.RemoteAddr().String())
		if controlHost != "" && dataHost != "" && !hostsEqual(controlHost, dataHost) {
			if s.logger != nil {
				s.logger.Warn(
					"rejecting ftp passive data connection from unexpected peer",
					"control_host", controlHost,
					"data_host", dataHost,
					"remote_addr", conn.RemoteAddr().String(),
				)
			}
			_ = conn.Close()
			continue
		}
		return s.wrapDataConn(state, conn), nil
	}
}

// wrapDataConn applies PROT P TLS and the transfer deadline to a data
// connection. Per RFC 4217 the FTP server is always the TLS server, whichever
// side opened the TCP connection.
func (s *Server) wrapDataConn(state *connState, conn net.Conn) net.Conn {
	if state.secureControl && state.dataProtPrivate && s.cfg.TLSConfig != nil {
		// The TLS handshake must NOT be awaited here: RFC 4217 clients
		// start the data-TLS handshake when they connect the data
		// channel, before (or concurrently with) the transfer command.
		// Wrapping without an eager handshake lets it complete lazily on
		// first I/O on either side; the transfer deadline below bounds it.
		conn = tls.Server(conn, s.cfg.TLSConfig)
	}
	_ = conn.SetDeadline(time.Now().Add(s.cfg.TransferTimeout))
	return conn
}

// parsePORTArg parses "h1,h2,h3,h4,p1,p2" into host:port.
func parsePORTArg(arg string) (string, error) {
	parts := strings.Split(strings.TrimSpace(arg), ",")
	if len(parts) != 6 {
		return "", errors.New("PORT needs 6 fields")
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return "", errors.New("PORT field out of range")
		}
		nums[i] = n
	}
	ip := net.IPv4(byte(nums[0]), byte(nums[1]), byte(nums[2]), byte(nums[3]))
	return net.JoinHostPort(ip.String(), strconv.Itoa(nums[4]*256+nums[5])), nil
}

// parseEPRTArg parses RFC 2428 "<d>proto<d>addr<d>port<d>" into host:port.
func parseEPRTArg(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if len(arg) < 7 {
		return "", errors.New("EPRT too short")
	}
	fields := strings.Split(arg, arg[:1])
	if len(fields) != 5 || fields[0] != "" || fields[4] != "" {
		return "", errors.New("EPRT malformed")
	}
	ip := net.ParseIP(fields[2])
	if ip == nil {
		return "", errors.New("EPRT bad address")
	}
	switch fields[1] {
	case "1":
		if ip.To4() == nil {
			return "", errors.New("EPRT protocol/address mismatch")
		}
	case "2":
		if ip.To4() != nil && !strings.Contains(fields[2], ":") {
			return "", errors.New("EPRT protocol/address mismatch")
		}
	default:
		return "", errors.New("EPRT unsupported protocol")
	}
	port, err := strconv.Atoi(fields[3])
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("EPRT bad port")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

// validateActiveTarget blocks FTP bounce attacks: the data connection may
// only go back to the client's own address and to an unprivileged port.
func validateActiveTarget(target, controlRemote string) string {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return "malformed target"
	}
	if port, _ := strconv.Atoi(portStr); port < 1024 {
		return "privileged port"
	}
	controlHost := hostFromAddr(controlRemote)
	if controlHost == "" || !hostsEqual(controlHost, host) {
		return "address differs from control connection"
	}
	return ""
}

func hostFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]")
	}
	return strings.Trim(host, "[]")
}

func hostsEqual(a, b string) bool {
	ipA := net.ParseIP(a)
	ipB := net.ParseIP(b)
	if ipA != nil && ipB != nil {
		return ipA.Equal(ipB)
	}
	return strings.EqualFold(a, b)
}

func isAuthed(conn net.Conn, state *connState) bool {
	if state.user == nil || state.fs == nil {
		writeReply(conn, 530, "Please login with USER and PASS.")
		return false
	}
	return true
}

func splitCommand(line string) (string, string) {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(strings.TrimSpace(parts[0]))
	if len(parts) == 1 {
		return cmd, ""
	}
	return cmd, strings.TrimSpace(parts[1])
}

func resolvePath(cwd, arg string) string {
	if arg == "" {
		return cwd
	}
	if strings.HasPrefix(arg, "/") {
		return path.Clean(arg)
	}
	return path.Clean(path.Join(cwd, arg))
}

func writeReply(conn net.Conn, code int, msg string) {
	_, _ = fmt.Fprintf(conn, "%d %s\r\n", code, msg)
}

func writeMultiline(conn net.Conn, code int, lines []string) {
	if len(lines) == 0 {
		writeReply(conn, code, "")
		return
	}
	_, _ = fmt.Fprintf(conn, "%d-%s\r\n", code, strings.TrimSpace(lines[0]))
	// Intermediate lines start with a single space: RFC 2389 requires it for
	// FEAT entries, and it can never be mistaken for a "ddd " terminator.
	for i := 1; i < len(lines)-1; i++ {
		_, _ = fmt.Fprintf(conn, " %s\r\n", strings.TrimSpace(lines[i]))
	}
	_, _ = fmt.Fprintf(conn, "%d %s\r\n", code, strings.TrimSpace(lines[len(lines)-1]))
}

func parsePortRange(raw string) (int, int, error) {
	parts := strings.SplitN(raw, "-", 2)
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid range")
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	if start < 1024 || end > 65535 || start > end {
		return 0, 0, errors.New("invalid range bounds")
	}
	return start, end, nil
}

func writeListing(w io.Writer, fsys vfs.FileSystem, target string, mode string) error {
	info, err := fsys.Stat(target)
	if mode == "MLST" {
		// RFC 3659 §7: MLST lists exactly the named object — one facts line,
		// for directories too (the entries loop below lists children only).
		if err != nil {
			return err
		}
		kind := "file"
		if info.IsDir() {
			kind = "dir"
		}
		_, err = fmt.Fprintf(w, " type=%s;size=%d;modify=%s; %s\r\n", kind, info.Size(), info.ModTime().UTC().Format("20060102150405"), target)
		return err
	}
	if err == nil && !info.IsDir() {
		switch mode {
		case "NLST":
			_, err = fmt.Fprintf(w, "%s\r\n", info.Name())
		case "MLSD":
			_, err = fmt.Fprintf(w, "type=file;size=%d;modify=%s; %s\r\n", info.Size(), info.ModTime().UTC().Format("20060102150405"), info.Name())
		default:
			_, err = fmt.Fprintf(w, "%s\r\n", formatLIST(info))
		}
		return err
	}

	entries, err := fsys.ReadDir(target)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fi, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		switch mode {
		case "NLST":
			if _, err := fmt.Fprintf(w, "%s\r\n", entry.Name()); err != nil {
				return err
			}
		case "MLSD":
			t := "file"
			if entry.IsDir() {
				t = "dir"
			}
			if _, err := fmt.Fprintf(w, "type=%s;size=%d;modify=%s; %s\r\n", t, fi.Size(), fi.ModTime().UTC().Format("20060102150405"), entry.Name()); err != nil {
				return err
			}
		default:
			if _, err := fmt.Fprintf(w, "%s\r\n", formatLIST(fi)); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatLIST(fi fs.FileInfo) string {
	perms := "-rw-r--r--"
	if fi.IsDir() {
		perms = "drwxr-xr-x"
	}
	return fmt.Sprintf("%s 1 owner group %12d %s %s", perms, fi.Size(), fi.ModTime().Format("Jan _2 15:04"), fi.Name())
}

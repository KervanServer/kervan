package ftp

import (
	"bufio"
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
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				if s.isClosed() {
					return
				}
				if s.logger != nil {
					s.logger.Error("ftp accept failed", "error", acceptErr, "listener", label)
				}
				continue
			}
			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				s.handleConn(ctx, c, implicitTLS)
			}(conn)
		}
	}()
	return nil
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
	passiveIP       string
	remoteAddr      string
	secureControl   bool
	pbszSet         bool
	dataProtPrivate bool
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
	}
	reader := bufio.NewReader(conn)
	writeReply(conn, 220, s.cfg.Banner)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		line, err := reader.ReadString('\n')
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

		cmd, arg := splitCommand(line)
		switch cmd {
		case "USER":
			state.username = arg
			writeReply(conn, 331, "User name okay, need password.")
		case "PASS":
			if state.username == "" {
				writeReply(conn, 503, "Login with USER first.")
				continue
			}
			user, authErr := s.auth.Authenticate(ctx, state.username, arg)
			if authErr != nil {
				writeReply(conn, 530, "Login incorrect.")
				s.emitAudit(audit.EventAuthFailure, state.username, "ftp", "", state.remoteAddr, "failed", authErr.Error())
				continue
			}
			userFS, fsErr := s.buildFS(user)
			if fsErr != nil {
				writeReply(conn, 550, "Unable to mount filesystem.")
				continue
			}
			state.user = user
			state.fs = userFS
			state.cwd = "/"
			state.session = s.sessions.Start(user.Username, "ftp", state.remoteAddr)
			writeReply(conn, 230, "User logged in, proceed.")
			s.emitAudit(audit.EventAuthSuccess, user.Username, "ftp", "", state.remoteAddr, "ok", "login success")
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
				" SIZE",
				" MDTM",
				" MLST type*;size*;modify*;",
				" MLSD",
			}
			if s.ftpsExplicitEnabled() || implicitTLS {
				features = append(features, " AUTH TLS", " PBSZ", " PROT")
			}
			writeMultiline(conn, 211, features)
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
		case "CWD":
			if !isAuthed(conn, state) {
				continue
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
				writeReply(conn, 425, "Use PASV first.")
				continue
			}
			writeReply(conn, 150, "Opening data connection.")
			err = writeListing(dc, state.fs, target, cmd)
			_ = dc.Close()
			if err != nil {
				writeReply(conn, 550, "Listing failed.")
				continue
			}
			writeReply(conn, 226, "Transfer complete.")
		case "RETR":
			if !isAuthed(conn, state) {
				continue
			}
			p := resolvePath(state.cwd, arg)
			dc, err := s.acceptDataConn(state)
			if err != nil {
				writeReply(conn, 425, "Use PASV first.")
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
			transferID := ""
			if s.xfer != nil {
				transferID = s.xfer.Start(state.user.Username, "ftp", p, transfer.DirectionDownload, total)
			}
			writeReply(conn, 150, "Opening binary mode data connection.")
			n, err := io.Copy(dc, f)
			_ = f.Close()
			_ = dc.Close()
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
			dc, err := s.acceptDataConn(state)
			if err != nil {
				writeReply(conn, 425, "Use PASV first.")
				continue
			}
			flags := os.O_CREATE | os.O_WRONLY
			if cmd == "APPE" {
				flags |= os.O_APPEND
			} else {
				flags |= os.O_TRUNC
			}
			f, err := state.fs.Open(p, flags, 0o644)
			if err != nil {
				_ = dc.Close()
				writeReply(conn, 550, "Cannot open target file.")
				continue
			}
			transferID := ""
			if s.xfer != nil {
				transferID = s.xfer.Start(state.user.Username, "ftp", p, transfer.DirectionUpload, -1)
			}
			writeReply(conn, 150, "Ok to send data.")
			n, err := io.Copy(f, dc)
			_ = dc.Close()
			_ = f.Close()
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
			tlsConn := tls.Server(conn, s.cfg.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				if s.logger != nil {
					s.logger.Debug("explicit tls handshake failed", "error", err)
				}
				s.cleanupConnState(state)
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			state.secureControl = true
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

func (s *Server) cleanupConnState(state *connState) {
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
	if state.passiveLn == nil {
		return nil, errors.New("passive listener not ready")
	}
	ln := state.passiveLn
	state.passiveLn = nil
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	conn, err := ln.Accept()
	_ = ln.Close()
	if err != nil {
		return nil, err
	}
	if state.secureControl && state.dataProtPrivate && s.cfg.TLSConfig != nil {
		tlsConn := tls.Server(conn, s.cfg.TLSConfig)
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tlsConn
	}
	_ = conn.SetDeadline(time.Now().Add(s.cfg.TransferTimeout))
	return conn, nil
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
	for i := 1; i < len(lines)-1; i++ {
		_, _ = fmt.Fprintf(conn, "%s\r\n", strings.TrimSpace(lines[i]))
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

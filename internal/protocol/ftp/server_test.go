package ftp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestNewServerAppliesDefaultsAndTLSModes(t *testing.T) {
	srv := NewServer(Config{}, nil, nil, nil, nil, nil, nil)
	if srv.cfg.ListenAddr != "0.0.0.0" || srv.cfg.Port != 2121 || srv.cfg.FTPSImplicitPort != 990 {
		t.Fatalf("unexpected default config: %#v", srv.cfg)
	}
	if srv.ftpsExplicitEnabled() || srv.ftpsImplicitEnabled() {
		t.Fatal("expected TLS modes to stay disabled without TLS config")
	}

	srv = NewServer(Config{TLSConfig: dummyTLSConfig(), FTPSMode: "both"}, nil, nil, nil, nil, nil, nil)
	if !srv.ftpsExplicitEnabled() || !srv.ftpsImplicitEnabled() {
		t.Fatal("expected both FTPS modes to be enabled")
	}
}

func TestFTPPathAndCommandHelpers(t *testing.T) {
	cmd, arg := splitCommand("stor  /tmp/file.txt ")
	if cmd != "STOR" || arg != "/tmp/file.txt" {
		t.Fatalf("unexpected split command result cmd=%q arg=%q", cmd, arg)
	}
	cmd, arg = splitCommand("NOOP")
	if cmd != "NOOP" || arg != "" {
		t.Fatalf("unexpected single-word command split cmd=%q arg=%q", cmd, arg)
	}

	if got := resolvePath("/home/alice", "docs/file.txt"); got != "/home/alice/docs/file.txt" {
		t.Fatalf("unexpected resolved path: %q", got)
	}
	if got := resolvePath("/home/alice", "/etc/passwd"); got != "/etc/passwd" {
		t.Fatalf("unexpected absolute resolved path: %q", got)
	}
	if got := resolvePath("/home/alice", ""); got != "/home/alice" {
		t.Fatalf("expected empty arg to keep cwd, got %q", got)
	}
}

func TestParsePortRange(t *testing.T) {
	start, end, err := parsePortRange("50000-50010")
	if err != nil || start != 50000 || end != 50010 {
		t.Fatalf("unexpected parsed range start=%d end=%d err=%v", start, end, err)
	}

	for _, raw := range []string{"broken", "x-10", "50000-y", "20-10", "1-70000"} {
		if _, _, err := parsePortRange(raw); err == nil {
			t.Fatalf("expected invalid port range %q to fail", raw)
		}
	}
}

func TestFTPHostHelpers(t *testing.T) {
	if got := hostFromAddr("127.0.0.1:2121"); got != "127.0.0.1" {
		t.Fatalf("unexpected host from addr: %q", got)
	}
	if got := hostFromAddr("[::1]:2121"); got != "::1" {
		t.Fatalf("unexpected ipv6 host from addr: %q", got)
	}
	if !hostsEqual("127.0.0.1", "::ffff:127.0.0.1") {
		t.Fatal("expected mapped IPv4 addresses to match")
	}
	if hostsEqual("127.0.0.1", "192.0.2.10") {
		t.Fatal("expected different hosts not to match")
	}
}

func TestWriteReplyAndMultiline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(client)
		done <- string(raw)
	}()

	writeReply(server, 220, "Welcome")
	writeMultiline(server, 211, []string{"Features", " UTF8 ", " PASV "})
	writeMultiline(server, 200, nil)
	_ = server.Close()

	output := <-done
	if !strings.Contains(output, "220 Welcome\r\n") {
		t.Fatalf("expected single-line reply in output, got %q", output)
	}
	if !strings.Contains(output, "211-Features\r\nUTF8\r\n211 PASV\r\n") {
		t.Fatalf("expected multiline feature reply, got %q", output)
	}
	if !strings.Contains(output, "200 \r\n") {
		t.Fatalf("expected empty multiline fallback reply, got %q", output)
	}
}

func TestWriteListingModesAndFormatLIST(t *testing.T) {
	backend := memory.New()
	if err := backend.MkdirAll("/docs", 0); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	file, err := backend.Open("/docs/readme.txt", os.O_CREATE|os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	if _, err := file.Write([]byte("hello")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	for _, mode := range []string{"LIST", "NLST", "MLSD"} {
		var buf bytes.Buffer
		if err := writeListing(&buf, backend, "/docs", mode); err != nil {
			t.Fatalf("write listing for %s: %v", mode, err)
		}
		if !strings.Contains(buf.String(), "readme.txt") {
			t.Fatalf("expected listing %s to mention file, got %q", mode, buf.String())
		}
	}

	var fileBuf bytes.Buffer
	if err := writeListing(&fileBuf, backend, "/docs/readme.txt", "MLSD"); err != nil {
		t.Fatalf("write file listing: %v", err)
	}
	if !strings.Contains(fileBuf.String(), "type=file;size=5;") {
		t.Fatalf("expected file MLSD output, got %q", fileBuf.String())
	}

	fileInfo, err := backend.Stat("/docs/readme.txt")
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	dirInfo, err := backend.Stat("/docs")
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !strings.HasPrefix(formatLIST(fileInfo), "-rw-r--r--") {
		t.Fatalf("expected file LIST formatting, got %q", formatLIST(fileInfo))
	}
	if !strings.HasPrefix(formatLIST(dirInfo), "drwxr-xr-x") {
		t.Fatalf("expected dir LIST formatting, got %q", formatLIST(dirInfo))
	}
}

func TestCleanupConnStateAndIsAuthed(t *testing.T) {
	sessions := dummySessions()
	srv := &Server{sessions: sessions}
	sess := sessions.Start("alice", "ftp", "127.0.0.1:1")

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	readDone := make(chan string, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := client.Read(buf)
		readDone <- string(buf[:n])
	}()

	state := &connState{session: sess}
	if isAuthed(server, state) {
		t.Fatal("expected unauthenticated state to fail")
	}
	if !strings.Contains(<-readDone, "530 Please login with USER and PASS.") {
		t.Fatal("expected auth error reply")
	}

	state.passiveLn, _ = net.Listen("tcp", "127.0.0.1:0")
	srv.cleanupConnState(state)
	if sessions.Get(sess.ID) != nil {
		t.Fatal("expected cleanup to end session")
	}
	if state.session != nil || state.passiveLn != nil {
		t.Fatalf("expected cleanup to clear state, got %#v", state)
	}
}

func dummyTLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

func dummySessions() *session.Manager {
	return session.NewManager()
}

// featReplyFeatures reads FTP replies off conn until the multiline
// terminator (a line starting with "<code> ") and returns the feature lines
// between the opening ("<code>-") and terminating line, per RFC 2389 §3.2.
func featReplyFeatures(t *testing.T, conn net.Conn) []string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	var features []string
	seenOpening := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading ftp reply: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if !seenOpening {
			if strings.HasPrefix(line, "211-") {
				seenOpening = true
			}
			continue
		}
		if strings.HasPrefix(line, "211 ") {
			return features
		}
		features = append(features, strings.TrimSpace(line))
	}
}

// Regression: the live FEAT reply must advertise every supported feature.
// writeMultiline renders lines[0] on the opening reply and lines[last] on
// the terminating reply, so the feature list is framed with explicit
// "Features:"/"End" elements — without them the first and last features
// were consumed by the framing and stayed invisible to clients
// (RFC 2389 §3.2).
func TestFEATReplyAdvertisesAllFeatures(t *testing.T) {
	srv := NewServer(Config{Banner: "test-banner"}, nil, nil, nil, nil, nil, nil)
	client, serverConn := net.Pipe()
	defer client.Close()

	handleDone := make(chan struct{})
	go func() {
		defer close(handleDone)
		srv.handleConn(context.Background(), serverConn, false)
	}()

	client.SetReadDeadline(time.Now().Add(5 * time.Second))
	banner := make([]byte, 64)
	if _, err := client.Read(banner); err != nil {
		t.Fatalf("reading banner: %v", err)
	}

	if _, err := client.Write([]byte("FEAT\r\n")); err != nil {
		t.Fatalf("sending FEAT: %v", err)
	}
	features := featReplyFeatures(t, client)

	_ = client.Close()
	<-handleDone

	want := []string{"UTF8", "PASV", "SIZE", "MDTM", "MLST type*;size*;modify*;", "MLSD"}
	have := make(map[string]bool, len(features))
	for _, f := range features {
		have[f] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) != 0 {
		t.Fatalf("FEAT reply omits advertised features %v (got %v)", missing, features)
	}
}

// Regression: a successful re-login on one connection must replace the
// previous session. The old code started a new session without ending the
// previous one, orphaning it in the manager: it showed as active forever and
// a Kill on the orphan fired its stale terminator, closing the current
// user's connection.
func TestFTPReLoginReplacesSessionWithoutOrphan(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := auth.NewEngine(auth.NewUserRepository(st), "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "secret123", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessions := session.NewManager()
	srv := NewServer(Config{Banner: "test-banner"}, nil, engine, sessions, nil,
		func(*auth.User) (vfs.FileSystem, error) { return memory.New(), nil }, nil)

	client, serverConn := net.Pipe()
	defer client.Close()
	handleDone := make(chan struct{})
	go func() {
		defer close(handleDone)
		srv.handleConn(context.Background(), serverConn, false)
	}()

	reader := bufio.NewReader(client)
	readReply := func() string {
		t.Helper()
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading reply: %v", err)
		}
		return strings.TrimSpace(line)
	}
	send := func(cmd string) {
		t.Helper()
		if _, err := client.Write([]byte(cmd + "\r\n")); err != nil {
			t.Fatalf("sending %q: %v", cmd, err)
		}
	}
	login := func() {
		send("USER alice")
		if reply := readReply(); !strings.HasPrefix(reply, "331 ") {
			t.Fatalf("expected 331 after USER, got %q", reply)
		}
		send("PASS secret123")
		if reply := readReply(); !strings.HasPrefix(reply, "230 ") {
			t.Fatalf("expected 230 after PASS, got %q", reply)
		}
	}

	if reply := readReply(); !strings.HasPrefix(reply, "220 ") {
		t.Fatalf("expected 220 banner, got %q", reply)
	}

	login()
	if got := len(sessions.List()); got != 1 {
		t.Fatalf("expected 1 active session after first login, got %d", got)
	}

	send("USER alice")
	if reply := readReply(); !strings.HasPrefix(reply, "331 ") {
		t.Fatalf("expected 331 after re-login USER, got %q", reply)
	}
	send("PASS secret123")
	if reply := readReply(); !strings.HasPrefix(reply, "230 ") {
		t.Fatalf("expected 230 after re-login PASS, got %q", reply)
	}

	if got := len(sessions.List()); got != 1 {
		t.Fatalf("%d sessions active for one connection after re-login (want 1): previous session is orphaned in the manager", got)
	}

	send("QUIT")
	if reply := readReply(); !strings.HasPrefix(reply, "221 ") {
		t.Fatalf("expected 221 after QUIT, got %q", reply)
	}
	select {
	case <-handleDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("handleConn did not exit after QUIT")
	}
	if got := len(sessions.List()); got != 0 {
		t.Fatalf("%d sessions remain after QUIT (want 0): orphaned session leaks in the manager", got)
	}
}

func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kervan-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}

func startExplicitFTP(t *testing.T) net.Conn {
	t.Helper()
	srv := NewServer(Config{Banner: "test-banner", FTPSMode: "explicit", TLSConfig: selfSignedTLSConfig(t)}, nil, nil, nil, nil, nil, nil)
	client, serverConn := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go srv.handleConn(context.Background(), serverConn, false)
	return client
}

func readFTPReply(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("reading ftp reply: %v", err)
	}
	return strings.TrimRight(line, "\r\n")
}

// Commands the client pipelines with AUTH TLS (sent in the same segment and
// consumed into the control bufio.Reader before the upgrade) must be honored
// over the new TLS control channel instead of being silently dropped by the
// reader swap.
func TestExplicitUpgradeHonorsPipelinedCommand(t *testing.T) {
	client := startExplicitFTP(t)
	r := bufio.NewReader(client)
	client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("expected banner, got %q", reply)
	}
	if _, err := client.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("expected 234, got %q", reply)
	}
	tlsClient := tls.Client(client, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	tlsClient.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 256)
	n, readErr := tlsClient.Read(buf)
	if readErr != nil {
		t.Fatalf("pipelined PBSZ 0 was dropped by the AUTH TLS upgrade (no reply): %v", readErr)
	}
	if !strings.HasPrefix(string(buf[:n]), "200") {
		t.Fatalf("expected 200 PBSZ=0 for the pipelined command, got %q", string(buf[:n]))
	}
}

func TestExplicitUpgradeWithoutPipelining(t *testing.T) {
	client := startExplicitFTP(t)
	r := bufio.NewReader(client)
	client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("expected banner, got %q", reply)
	}
	if _, err := client.Write([]byte("AUTH TLS\r\n")); err != nil {
		t.Fatalf("write AUTH TLS: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("expected 234, got %q", reply)
	}
	tlsClient := tls.Client(client, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	if _, err := tlsClient.Write([]byte("PBSZ 0\r\n")); err != nil {
		t.Fatalf("write PBSZ over tls: %v", err)
	}
	tlsClient.SetReadDeadline(time.Now().Add(3 * time.Second))
	if reply := readFTPReply(t, bufio.NewReader(tlsClient)); !strings.HasPrefix(reply, "200") {
		t.Fatalf("expected 200 PBSZ=0, got %q", reply)
	}
}

// startPlainFTP starts a no-TLS control connection over net.Pipe and returns
// the client side positioned just after the 220 banner.
func startPlainFTP(t *testing.T) net.Conn {
	t.Helper()
	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		IdleTimeout: 30 * time.Second,
	}, nil, nil, nil, nil, nil, nil)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	go srv.handleConn(context.Background(), serverConn, false)
	r := bufio.NewReader(clientConn)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("expected banner, got %q", reply)
	}
	return clientConn
}

func TestControlLineUnderCapProcessed(t *testing.T) {
	client := startPlainFTP(t)
	r := bufio.NewReader(client)
	client.SetWriteDeadline(time.Now().Add(30 * time.Second))
	line := "NOOP " + strings.Repeat("a", 60*1024) + "\r\n"
	if _, err := client.Write([]byte(line)); err != nil {
		t.Fatalf("write 60KiB control line: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if reply := readFTPReply(t, r); strings.TrimSpace(reply) == "" {
		t.Fatalf("empty reply for a 60KiB control line under the cap")
	}
}

func TestControlLineOverCapClosesConnection(t *testing.T) {
	client := startPlainFTP(t)
	client.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, werr := client.Write([]byte(strings.Repeat("A", 1024*1024) + "\r\n"))
	if werr != nil {
		return
	}
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	r := bufio.NewReader(client)
	if _, rerr := r.ReadString('\n'); rerr == nil {
		t.Fatalf("connection stayed open after a 1MiB control line — the control-line length cap is not enforced, so any pre-authentication client can grow the buffer without bound toward process OOM")
	}
}

// Contract: the FTP data plane transfers file content over the PASV data
// connection for authenticated users — STOR persists the exact bytes through
// the user's VFS and RETR returns them — with the control replies tracking
// the transfer lifecycle (150/226).

func startFTPWithDataPlane(t *testing.T) (net.Conn, *bufio.Reader, *vfs.UserVFS, *memory.Backend) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("expected banner, got %q", reply)
	}
	return cc, r, fsys, backend
}

func ftpCmd(t *testing.T, cc net.Conn, r *bufio.Reader, cmd string) string {
	t.Helper()
	if _, err := cc.Write([]byte(cmd + "\r\n")); err != nil {
		t.Fatalf("write %q: %v", cmd, err)
	}
	return readFTPReply(t, r)
}

func enterFTPPassive(t *testing.T, cc net.Conn, r *bufio.Reader) net.Conn {
	t.Helper()
	reply := ftpCmd(t, cc, r, "PASV")
	if !strings.HasPrefix(reply, "227") {
		t.Fatalf("PASV reply: %q", reply)
	}
	open := reply[strings.Index(reply, "(")+1 : strings.Index(reply, ")")]
	parts := strings.Split(open, ",")
	if len(parts) != 6 {
		t.Fatalf("unparsable PASV reply: %q", reply)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			t.Fatalf("unparsable PASV reply %q: %v", reply, err)
		}
		nums[i] = v
	}
	addr := net.JoinHostPort(fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3]), strconv.Itoa(nums[4]*256+nums[5]))
	dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial data %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = dc.Close() })
	_ = dc.SetDeadline(time.Now().Add(30 * time.Second))
	return dc
}

func TestFTPDataPlaneSTORAndRETRWithAuth(t *testing.T) {
	cc, r, fsys, backend := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply %q, want 331", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply %q, want 230", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply %q, want 200", reply)
	}

	payload := []byte("kervan ftp data plane integration payload\r\nwith a second line\n")

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR /upload.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR reply %q, want 150", reply)
	}
	if _, err := dc.Write(payload); err != nil {
		t.Fatalf("write payload over data connection: %v", err)
	}
	if tc, ok := dc.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	} else {
		_ = dc.Close()
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("post-transfer reply %q, want 226; the data-plane transfer did not complete", reply)
	}

	f, err := fsys.Open("/upload.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("uploaded file missing from the user VFS: %v", err)
	}
	got, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("uploaded content mismatch: got %q, want %q", got, payload)
	}

	dc2 := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /upload.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR reply %q, want 150", reply)
	}
	downloaded, err := io.ReadAll(dc2)
	if err != nil {
		t.Fatalf("read RETR data connection: %v", err)
	}
	if string(downloaded) != string(payload) {
		t.Fatalf("RETR content mismatch: got %q, want %q", downloaded, payload)
	}

	if _, err := backend.Open("/upload.txt", os.O_RDONLY, 0); err != nil {
		t.Fatalf("backend instance mismatch: %v", err)
	}
}

// Contract: the FTP rename/delete path operates on the authenticated user's
// VFS — RNFR+RNTO renames (the new path serves the old content, the old path
// disappears, siblings stay intact), DELE removes, and RNTO without RNFR is
// a 503 sequence error. Every transfer requires its own PASV.

func TestFTPRenameDeleteOverUserVFS(t *testing.T) {
	cc, r, fsys, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply %q, want 331", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply %q, want 230", reply)
	}

	seed := func(path, payload string) {
		t.Helper()
		dc := enterFTPPassive(t, cc, r)
		if reply := ftpCmd(t, cc, r, "STOR "+path); !strings.HasPrefix(reply, "150") {
			t.Fatalf("STOR %s reply %q, want 150", path, reply)
		}
		if _, err := dc.Write([]byte(payload)); err != nil {
			t.Fatalf("STOR %s data write: %v", path, err)
		}
		if tc, ok := dc.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		} else {
			_ = dc.Close()
		}
		if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
			t.Fatalf("STOR %s post-transfer reply %q, want 226", path, reply)
		}
	}
	seed("/a.txt", "payload A")
	seed("/b.txt", "payload B")

	if reply := ftpCmd(t, cc, r, "RNFR /a.txt"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("RNFR reply %q, want 350", reply)
	}
	if reply := ftpCmd(t, cc, r, "RNTO /renamed.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RNTO reply %q, want 250", reply)
	}

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /renamed.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR renamed reply %q, want 150", reply)
	}
	got, err := io.ReadAll(dc)
	if err != nil {
		t.Fatalf("read renamed content: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("post-transfer reply %q, want 226", reply)
	}
	if string(got) != "payload A" {
		t.Fatalf("renamed content mismatch: got %q, want %q", got, "payload A")
	}

	dc = enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /a.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("RETR old-path reply %q, want 550 after rename", reply)
	}
	_ = dc

	dc2 := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /b.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR sibling reply %q, want 150", reply)
	}
	sibling, err := io.ReadAll(dc2)
	if err != nil {
		t.Fatalf("read sibling content: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("post-transfer reply %q, want 226", reply)
	}
	if string(sibling) != "payload B" {
		t.Fatalf("sibling content mismatch: got %q, want %q", sibling, "payload B")
	}

	if reply := ftpCmd(t, cc, r, "DELE /b.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("DELE reply %q, want 250", reply)
	}
	if _, err := fsys.Open("/b.txt", os.O_RDONLY, 0); err == nil {
		t.Fatalf("deleted file still present in the user VFS")
	}

	if reply := ftpCmd(t, cc, r, "RNTO /nope.txt"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("RNTO-without-RNFR reply %q, want 503", reply)
	}
}

// TestFTPAppeAndRestOverDataPlane drives the append path and pins the
// REST-not-implemented contract over the PASV data connection.
func TestFTPAppeAndRestOverDataPlane(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}

	store := func(path, content string) {
		t.Helper()
		data := enterFTPPassive(t, cc, r)
		if reply := ftpCmd(t, cc, r, "STOR "+path); !strings.HasPrefix(reply, "150") {
			t.Fatalf("STOR %s start reply = %q", path, reply)
		}
		if _, err := io.WriteString(data, content); err != nil {
			t.Fatalf("STOR %s write: %v", path, err)
		}
		if err := data.Close(); err != nil {
			t.Fatalf("STOR %s data close: %v", path, err)
		}
		if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
			t.Fatalf("STOR %s completion = %q", path, line)
		}
	}
	appe := func(path, content string) {
		t.Helper()
		data := enterFTPPassive(t, cc, r)
		if reply := ftpCmd(t, cc, r, "APPE "+path); !strings.HasPrefix(reply, "150") {
			t.Fatalf("APPE %s start reply = %q", path, reply)
		}
		if _, err := io.WriteString(data, content); err != nil {
			t.Fatalf("APPE %s write: %v", path, err)
		}
		if err := data.Close(); err != nil {
			t.Fatalf("APPE %s data close: %v", path, err)
		}
		if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
			t.Fatalf("APPE %s completion = %q", path, line)
		}
	}
	retr := func(path string) string {
		t.Helper()
		data := enterFTPPassive(t, cc, r)
		if reply := ftpCmd(t, cc, r, "RETR "+path); !strings.HasPrefix(reply, "150") {
			t.Fatalf("RETR %s start reply = %q", path, reply)
		}
		got, err := io.ReadAll(data)
		if err != nil {
			t.Fatalf("RETR %s read: %v", path, err)
		}
		if err := data.Close(); err != nil {
			t.Fatalf("RETR %s data close: %v", path, err)
		}
		if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
			t.Fatalf("RETR %s completion = %q", path, line)
		}
		return string(got)
	}

	store("/f.txt", "A")
	appe("/f.txt", "B")
	if got := retr("/f.txt"); got != "AB" {
		t.Fatalf("after APPE, RETR /f.txt = %q, want %q", got, "AB")
	}
	appe("/new.txt", "C")
	if got := retr("/new.txt"); got != "C" {
		t.Fatalf("APPE on a nonexistent file must create it: RETR = %q, want %q", got, "C")
	}
	if reply := ftpCmd(t, cc, r, "REST 1"); !strings.HasPrefix(reply, "502") {
		t.Fatalf("REST reply = %q, want 502 (restart is not implemented)", reply)
	}
	if got := retr("/f.txt"); got != "AB" {
		t.Fatalf("RETR after REST = %q, want %q — REST must not change transfer state", got, "AB")
	}
}

// TestFTPMkdirRmdirCwdOverControl drives the directory flows over the
// authenticated control connection, including the non-empty RMD refusal and
// the CDUP-not-implemented contract.
func TestFTPMkdirRmdirCwdOverControl(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}

	if reply := ftpCmd(t, cc, r, "MKD /dir"); !strings.HasPrefix(reply, "257") {
		t.Fatalf("MKD reply = %q, want 257", reply)
	}
	if reply := ftpCmd(t, cc, r, "MKD /dir"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("duplicate MKD reply = %q, want 550", reply)
	}

	if reply := ftpCmd(t, cc, r, "CWD /dir"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("CWD reply = %q, want 250", reply)
	}
	// Seed a file inside the directory so RMD hits the non-empty refusal.
	data := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR file.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR start reply = %q", reply)
	}
	if _, err := io.WriteString(data, "inner"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	if reply := ftpCmd(t, cc, r, "RMD /dir"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("RMD on non-empty dir reply = %q, want 550", reply)
	}

	if reply := ftpCmd(t, cc, r, "CWD /"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("CWD / reply = %q, want 250", reply)
	}
	if reply := ftpCmd(t, cc, r, "DELE /dir/file.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("DELE reply = %q, want 250", reply)
	}
	if reply := ftpCmd(t, cc, r, "RMD /dir"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RMD reply = %q, want 250", reply)
	}
	if reply := ftpCmd(t, cc, r, "CWD /dir"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("CWD into removed dir reply = %q, want 550", reply)
	}

	if reply := ftpCmd(t, cc, r, "CDUP"); !strings.HasPrefix(reply, "502") {
		t.Fatalf("CDUP reply = %q, want 502 (not implemented; document the actual behavior)", reply)
	}
}

// TestFTPPwdSizeMdtmMetadataReplies pins the metadata replies over the
// authenticated control connection: PWD quoted-path form (before and after
// CWD), SIZE by relative and absolute path, MDTM 14-digit UTC stamp, the
// dir/missing 550 refusals, and the TYPE I/X contract.
func TestFTPPwdSizeMdtmMetadataReplies(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}

	if reply := ftpCmd(t, cc, r, "PWD"); !strings.HasPrefix(reply, `257 "/"`) {
		t.Fatalf("PWD at login reply = %q, want 257 \"/\"", reply)
	}

	if reply := ftpCmd(t, cc, r, "MKD /dir"); !strings.HasPrefix(reply, "257") {
		t.Fatalf("MKD reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "CWD /dir"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("CWD reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PWD"); !strings.HasPrefix(reply, `257 "/dir"`) {
		t.Fatalf("PWD after CWD reply = %q, want 257 \"/dir\"", reply)
	}

	data := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR file.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR start reply = %q", reply)
	}
	if _, err := io.WriteString(data, "metadata"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	if reply := ftpCmd(t, cc, r, "SIZE file.txt"); !strings.HasPrefix(reply, "213 8") {
		t.Fatalf("SIZE reply = %q, want 213 8", reply)
	}
	if reply := ftpCmd(t, cc, r, "SIZE /dir/file.txt"); !strings.HasPrefix(reply, "213 8") {
		t.Fatalf("SIZE via absolute path reply = %q, want 213 8", reply)
	}
	mdtm := ftpCmd(t, cc, r, "MDTM file.txt")
	if !strings.HasPrefix(mdtm, "213 ") {
		t.Fatalf("MDTM reply = %q, want 213 <timestamp>", mdtm)
	}
	stamp := strings.TrimSpace(strings.TrimPrefix(mdtm, "213"))
	if len(stamp) != 14 {
		t.Fatalf(" MDTM timestamp = %q, want 14-digit YYYYMMDDHHMMSS", stamp)
	}
	modTime, err := time.Parse("20060102150405", stamp)
	if err != nil {
		t.Fatalf(" MDTM timestamp %q unparseable: %v", stamp, err)
	}
	if delta := time.Since(modTime); delta < 0 || delta > 10*time.Minute {
		t.Fatalf(" MDTM timestamp %q is %v away from now — not the file's mtime", stamp, delta)
	}

	if reply := ftpCmd(t, cc, r, "SIZE /dir"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("SIZE on a directory reply = %q, want 550", reply)
	}
	if reply := ftpCmd(t, cc, r, "SIZE /missing.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("SIZE on a missing file reply = %q, want 550", reply)
	}
	if reply := ftpCmd(t, cc, r, "MDTM /missing.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("MDTM on a missing file reply = %q, want 550", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q, want 200", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE X"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("TYPE X reply = %q, want 504", reply)
	}
}

// parsePASVDataAddr extracts the host:port advertised in a 227 PASV reply.
func parsePASVDataAddr(t *testing.T, reply string) string {
	t.Helper()
	open := strings.Index(reply, "(")
	closeIdx := strings.Index(reply, ")")
	if open < 0 || closeIdx < open {
		t.Fatalf("PASV reply has no (h1,h2,h3,h4,p1,p2): %q", reply)
	}
	var h1, h2, h3, h4, p1, p2 int
	if _, err := fmt.Sscanf(reply[open+1:closeIdx], "%d,%d,%d,%d,%d,%d", &h1, &h2, &h3, &h4, &p1, &p2); err != nil {
		t.Fatalf("parse PASV %q: %v", reply, err)
	}
	return fmt.Sprintf("%d.%d.%d.%d:%d", h1, h2, h3, h4, p1*256+p2)
}

// TestExplicitTLSDataPlaneAppeAfterUpgrade drives the explicit-FTPS path end
// to end: pipelined AUTH TLS+PBSZ (requeue), TLS login, PROT P, then
// STOR/APPE/RETR over per-transfer TLS data connections. The server must not
// await the data-TLS handshake inside acceptDataConn — RFC 4217 clients
// handshake when they connect the data channel, before or concurrently with
// the transfer command — so PROT P transfers complete without deadlocking.
func TestExplicitTLSDataPlaneAppeAfterUpgrade(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}

	// Round-22 requeue: PBSZ 0 pipelined with AUTH TLS must be honored over
	// the new TLS control channel.
	if _, err := cc.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("AUTH TLS reply = %q, want 234", reply)
	}
	tlsCtl := tls.Client(cc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsCtl.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	rTLS := bufio.NewReader(tlsCtl)
	if reply := readFTPReply(t, rTLS); !strings.HasPrefix(reply, "200") {
		t.Fatalf("requeued PBSZ reply = %q, want 200", reply)
	}

	if reply := ftpCmd(t, tlsCtl, rTLS, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P reply = %q, want 200", reply)
	}

	// dialData: PASV → plain dial. The server accepts the data connection
	// lazily when the transfer command arrives, so the TLS wrap must happen
	// after the 150 reply.
	dialData := func() net.Conn {
		t.Helper()
		reply := ftpCmd(t, tlsCtl, rTLS, "PASV")
		if !strings.HasPrefix(reply, "227") {
			t.Fatalf("PASV reply = %q", reply)
		}
		addr := parsePASVDataAddr(t, reply)
		dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("data dial %s: %v", addr, err)
		}
		return dc
	}
	wrapTLS := func(dc net.Conn) *tls.Conn {
		t.Helper()
		td := tls.Client(dc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
		if err := td.Handshake(); err != nil {
			dc.Close()
			t.Fatalf("data tls handshake: %v", err)
		}
		return td
	}
	transfer := func(cmd, path, content string) {
		t.Helper()
		dc := dialData()
		if reply := ftpCmd(t, tlsCtl, rTLS, cmd+" "+path); !strings.HasPrefix(reply, "150") {
			dc.Close()
			t.Fatalf("%s start reply = %q", cmd, reply)
		}
		td := wrapTLS(dc)
		if _, err := io.WriteString(td, content); err != nil {
			t.Fatalf("%s write: %v", cmd, err)
		}
		if err := td.Close(); err != nil {
			t.Fatalf("%s data close: %v", cmd, err)
		}
		if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
			t.Fatalf("%s completion = %q", cmd, line)
		}
	}

	transfer("STOR", "/f.txt", "A")
	transfer("APPE", "/f.txt", "B")

	retr := func(path string) string {
		t.Helper()
		dc := dialData()
		if reply := ftpCmd(t, tlsCtl, rTLS, "RETR "+path); !strings.HasPrefix(reply, "150") {
			dc.Close()
			t.Fatalf("RETR %s start reply = %q", path, reply)
		}
		td := wrapTLS(dc)
		got, err := io.ReadAll(td)
		if err != nil {
			t.Fatalf("RETR %s read: %v", path, err)
		}
		if err := td.Close(); err != nil {
			t.Fatalf("RETR %s data close: %v", path, err)
		}
		if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
			t.Fatalf("RETR %s completion = %q", path, line)
		}
		return string(got)
	}
	if got := retr("/f.txt"); got != "AB" {
		t.Fatalf(" after AUTH TLS + APPE, RETR /f.txt = %q, want %q", got, "AB")
	}

	if info, serr := backend.Stat("/f.txt"); serr != nil || info == nil || info.Size() != 2 {
		got := int64(-1)
		if info != nil {
			got = info.Size()
		}
		t.Fatalf(" backend /f.txt size = %d (err %v), want 2", got, serr)
	}
}

// TestFTPProtStateEdgesOverExplicitTLS pins the PROT state machine over the
// explicit-FTPS control channel: protected transfer, downgrade to clear
// (subsequent transfers must be plaintext), re-upgrade, unknown protection
// levels, and the PBSZ-first guard on a fresh connection.
func TestFTPProtStateEdgesOverExplicitTLS(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}

	// Pipelined AUTH TLS + PBSZ 0 (round-22 requeue), then TLS handshake.
	if _, err := cc.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("AUTH TLS reply = %q, want 234", reply)
	}
	tlsCtl := tls.Client(cc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsCtl.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	rTLS := bufio.NewReader(tlsCtl)
	if reply := readFTPReply(t, rTLS); !strings.HasPrefix(reply, "200") {
		t.Fatalf("requeued PBSZ reply = %q, want 200", reply)
	}

	if reply := ftpCmd(t, tlsCtl, rTLS, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q", reply)
	}

	dialData := func() net.Conn {
		t.Helper()
		reply := ftpCmd(t, tlsCtl, rTLS, "PASV")
		if !strings.HasPrefix(reply, "227") {
			t.Fatalf("PASV reply = %q", reply)
		}
		addr := parsePASVDataAddr(t, reply)
		dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("data dial %s: %v", addr, err)
		}
		return dc
	}
	wrapTLS := func(dc net.Conn) *tls.Conn {
		t.Helper()
		td := tls.Client(dc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
		if err := td.Handshake(); err != nil {
			dc.Close()
			t.Fatalf("data tls handshake: %v", err)
		}
		return td
	}

	// A. Protected transfer (PROT P).
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P reply = %q, want 200", reply)
	}
	dc := dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "STOR /p.txt"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("STOR start reply = %q", reply)
	}
	td := wrapTLS(dc)
	if _, err := io.WriteString(td, "prot"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := td.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	// B. Downgrade to clear: PROT C, then a PLAINTEXT data transfer.
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT C"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT C reply = %q, want 200", reply)
	}
	dc = dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "STOR /c.txt"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("clear STOR start reply = %q", reply)
	}
	if _, err := io.WriteString(dc, "clear"); err != nil {
		t.Fatalf("clear STOR write: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Fatalf("clear STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("clear STOR completion = %q", line)
	}
	if info, serr := backend.Stat("/c.txt"); serr != nil || info == nil || info.Size() != 5 {
		got := int64(-1)
		if info != nil {
			got = info.Size()
		}
		t.Fatalf(" PROT C downgrade — clear transfer stored %d bytes (err %v), want 5", got, serr)
	}

	// C. Re-upgrade to protected.
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P re-upgrade reply = %q, want 200", reply)
	}
	dc = dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "STOR /p2.txt"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("re-protected STOR start reply = %q", reply)
	}
	td = wrapTLS(dc)
	if _, err := io.WriteString(td, "p2"); err != nil {
		t.Fatalf("re-protected STOR write: %v", err)
	}
	if err := td.Close(); err != nil {
		t.Fatalf("re-protected STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("re-protected STOR completion = %q", line)
	}

	// D. Unknown protection levels.
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT S"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("PROT S reply = %q, want 504", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT E"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("PROT E reply = %q, want 504", reply)
	}

	// E. PBSZ-first guard on a fresh control connection.
	cc2, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("fresh control dial: %v", err)
	}
	t.Cleanup(func() { _ = cc2.Close() })
	_ = cc2.SetDeadline(time.Now().Add(15 * time.Second))
	r2 := bufio.NewReader(cc2)
	if reply := readFTPReply(t, r2); !strings.HasPrefix(reply, "220") {
		t.Fatalf("fresh banner = %q", reply)
	}
	if _, err := cc2.Write([]byte("AUTH TLS\r\n")); err != nil {
		t.Fatalf("fresh AUTH TLS write: %v", err)
	}
	if reply := readFTPReply(t, r2); !strings.HasPrefix(reply, "234") {
		t.Fatalf("fresh AUTH TLS reply = %q, want 234", reply)
	}
	td2 := tls.Client(cc2, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := td2.Handshake(); err != nil {
		t.Fatalf("fresh client tls handshake: %v", err)
	}
	r2TLS := bufio.NewReader(td2)
	if reply := ftpCmd(t, td2, r2TLS, "PROT P"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("PROT P without PBSZ reply = %q, want 503", reply)
	}
	if reply := ftpCmd(t, td2, r2TLS, "PBSZ 0"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PBSZ reply = %q, want 200", reply)
	}
	if reply := ftpCmd(t, td2, r2TLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P after PBSZ reply = %q, want 200", reply)
	}
}

// readFeatFeatures consumes a full multiline FEAT reply through the given
// buffered reader and returns the advertised feature names (trimmed).
func readFeatFeatures(t *testing.T, r *bufio.Reader) []string {
	t.Helper()
	var features []string
	seen := false
	for {
		line := readFTPReply(t, r)
		if strings.HasPrefix(line, "211-") {
			seen = true
			continue
		}
		if strings.HasPrefix(line, "211 ") {
			return features
		}
		if seen {
			features = append(features, strings.TrimSpace(line))
		}
	}
}

// TestFTPFeatOverAuthAndMLSD pins FEAT after login (base features only while
// TLS is off; FTPS features advertised on a TLS-enabled server — the round-4
// framing fix), the MLSD machine-facts listing over the data plane, NOOP, and
// DELE-on-missing.
func TestFTPFeatOverAuthAndMLSD(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}

	// FEAT after login: base features only (TLS off in this harness). The
	// reply is multiline — read the whole 211- block through the SAME
	// buffered reader the login used (no second reader over the socket).
	fmt.Fprintf(cc, "FEAT\r\n")
	features := readFeatFeatures(t, r)
	joined := strings.Join(features, " ")
	for _, want := range []string{"UTF8", "PASV", "SIZE", "MDTM", "MLST", "MLSD"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("FAIL: FEAT after login is missing %q: %v", want, features)
		}
	}
	for _, absent := range []string{"AUTH TLS", "PBSZ", "PROT"} {
		if strings.Contains(joined, absent) {
			t.Fatalf("FAIL: FEAT advertises %q while TLS is disabled: %v", absent, features)
		}
	}

	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("NOOP reply = %q, want 200", reply)
	}

	// Seed a file, then MLSD the root over the data plane.
	data := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR /f.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR start reply = %q", reply)
	}
	if _, err := io.WriteString(data, "mlsd"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	listData := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "MLSD /"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("MLSD start reply = %q", reply)
	}
	listing, err := io.ReadAll(listData)
	if err != nil {
		t.Fatalf("MLSD read: %v", err)
	}
	if err := listData.Close(); err != nil {
		t.Fatalf("MLSD data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("MLSD completion = %q", line)
	}
	listText := string(listing)
	if !strings.Contains(listText, "f.txt") || !strings.Contains(listText, "type=file;") {
		t.Fatalf(" MLSD listing %q lacks a type=file fact for f.txt", listText)
	}
	if !strings.Contains(listText, "size=4;") {
		t.Fatalf(" MLSD listing %q lacks size=4 for f.txt", listText)
	}
	if !strings.Contains(listText, "modify=") {
		t.Fatalf(" MLSD listing %q lacks modify= facts", listText)
	}

	if reply := ftpCmd(t, cc, r, "DELE /missing.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("DELE on missing file reply = %q, want 550", reply)
	}

	// TLS-enabled server: FEAT must advertise the FTPS features (round-4 fix).
	cc2 := startExplicitFTP(t)
	r2 := bufio.NewReader(cc2)
	if reply := readFTPReply(t, r2); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}
	fmt.Fprintf(cc2, "FEAT\r\n")
	feats2 := readFeatFeatures(t, r2)
	joined2 := strings.Join(feats2, " ")
	for _, want := range []string{"AUTH TLS", "PBSZ", "PROT"} {
		if !strings.Contains(joined2, want) {
			t.Fatalf("FAIL: FEAT on the TLS-enabled server is missing %q: %v", want, feats2)
		}
	}
}

// TestFTPMlsdOverTLSAfterAuthUpgrade drives the round-22 AUTH-TLS upgrade
// followed by PROT P, STOR seeding, and MLSD over the TLS-protected data
// plane, then a PROT C downgrade with MLSD over a plain data connection —
// the full protection-flip composition over one control connection.
func TestFTPMlsdOverTLSAfterAuthUpgrade(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}

	// Round-22 requeue: PBSZ 0 pipelined with AUTH TLS.
	if _, err := cc.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("AUTH TLS reply = %q, want 234", reply)
	}
	tlsCtl := tls.Client(cc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsCtl.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	rTLS := bufio.NewReader(tlsCtl)
	if reply := readFTPReply(t, rTLS); !strings.HasPrefix(reply, "200") {
		t.Fatalf("requeued PBSZ reply = %q, want 200", reply)
	}

	if reply := ftpCmd(t, tlsCtl, rTLS, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q", reply)
	}

	// FEAT in the authenticated + TLS state: the full nine-feature matrix.
	fmt.Fprintf(tlsCtl, "FEAT\r\n")
	feats := readFeatFeatures(t, rTLS)
	joined := strings.Join(feats, " ")
	for _, want := range []string{"UTF8", "PASV", "SIZE", "MDTM", "MLST", "MLSD", "AUTH TLS", "PBSZ", "PROT"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("FAIL: FEAT (authed+TLS) is missing %q: %v", want, feats)
		}
	}

	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P reply = %q, want 200", reply)
	}

	dialData := func() net.Conn {
		t.Helper()
		reply := ftpCmd(t, tlsCtl, rTLS, "PASV")
		if !strings.HasPrefix(reply, "227") {
			t.Fatalf("PASV reply = %q", reply)
		}
		addr := parsePASVDataAddr(t, reply)
		dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("data dial %s: %v", addr, err)
		}
		return dc
	}
	wrapTLS := func(dc net.Conn) *tls.Conn {
		t.Helper()
		td := tls.Client(dc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
		if err := td.Handshake(); err != nil {
			dc.Close()
			t.Fatalf("data tls handshake: %v", err)
		}
		return td
	}

	// Seed /f.txt (4 bytes) over the TLS data plane.
	dc := dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "STOR /f.txt"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("STOR start reply = %q", reply)
	}
	td := wrapTLS(dc)
	if _, err := io.WriteString(td, "mlsd"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := td.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	// MLSD / over the TLS-protected data plane.
	dc = dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "MLSD /"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("MLSD over TLS start reply = %q", reply)
	}
	td = wrapTLS(dc)
	listing, err := io.ReadAll(td)
	if err != nil {
		t.Fatalf("MLSD over TLS read: %v", err)
	}
	if err := td.Close(); err != nil {
		t.Fatalf("MLSD over TLS data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("MLSD over TLS completion = %q", line)
	}
	listText := string(listing)
	if !strings.Contains(listText, "f.txt") || !strings.Contains(listText, "type=file;size=4;") {
		t.Fatalf(" MLSD over TLS listing %q lacks the type=file;size=4; fact for f.txt", listText)
	}

	// PROT C downgrade, then MLSD over a PLAIN data connection.
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT C"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT C reply = %q, want 200", reply)
	}
	dc = dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "MLSD /"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("MLSD over clear start reply = %q", reply)
	}
	listing2, err := io.ReadAll(dc)
	if err != nil {
		t.Fatalf("MLSD over clear read: %v", err)
	}
	if err := dc.Close(); err != nil {
		t.Fatalf("MLSD over clear data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("MLSD over clear completion = %q", line)
	}
	list2 := string(listing2)
	if !strings.Contains(list2, "f.txt") || !strings.Contains(list2, "type=file;size=4;") {
		t.Fatalf(" MLSD over clear listing %q lacks the type=file;size=4; fact for f.txt", list2)
	}
}

// TestFTPMLSTAdvertisedAndWorking pins the MLST capability contract: FEAT
// advertises MLST, so MLST must return the RFC 3659 machine-facts entry for a
// single file (and for a directory object) on the CONTROL channel — not 502 —
// while MLSD keeps listing the directory over the data connection.
func TestFTPMLSTAdvertisedAndWorking(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)

	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}

	// FEAT must advertise MLST (the capability contract under test).
	fmt.Fprintf(cc, "FEAT\r\n")
	features := readFeatFeatures(t, r)
	joined := strings.Join(features, " ")
	if !strings.Contains(joined, "MLST") {
		t.Fatalf(" FEAT does not advertise MLST: %v", features)
	}

	// Seed /f.txt (4 bytes) over the data plane.
	data := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR /f.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR start reply = %q", reply)
	}
	if _, err := io.WriteString(data, "mlsd"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	// THE DEFECT: MLST is advertised, so MLST /f.txt must return the
	// RFC 3659 machine-facts entry on the CONTROL channel — not 502.
	if reply := ftpCmd(t, cc, r, "MLST /f.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf(" FEAT advertises MLST but MLST /f.txt reply = %q — capability-advertisement violation (RFC 3659 §7)", reply)
	}
	// Multiline MLST form: "250-Listing <path>" / " <facts> <path>" / "250 End".
	factsLine := readFTPReply(t, r)
	if !strings.HasPrefix(factsLine, " type=file;size=4;") || !strings.Contains(factsLine, "/f.txt") {
		t.Fatalf(" MLST entry line = %q, want \" type=file;size=4;… /f.txt\"", factsLine)
	}
	if end := readFTPReply(t, r); !strings.HasPrefix(end, "250 End") {
		t.Fatalf(" MLST terminator = %q, want \"250 End\"", end)
	}

	// MLST on a directory lists the directory object itself (type=dir).
	if reply := ftpCmd(t, cc, r, "MKD /dir"); !strings.HasPrefix(reply, "257") {
		t.Fatalf("MKD reply = %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "MLST /dir"); !strings.HasPrefix(reply, "250") {
		t.Fatalf(" MLST /dir reply = %q, want 250", reply)
	}
	dirLine := readFTPReply(t, r)
	if !strings.HasPrefix(dirLine, " type=dir;") || !strings.Contains(dirLine, "/dir") {
		t.Fatalf(" MLST dir entry line = %q, want \" type=dir;… /dir\"", dirLine)
	}
	if end := readFTPReply(t, r); !strings.HasPrefix(end, "250 End") {
		t.Fatalf(" MLST dir terminator = %q, want \"250 End\"", end)
	}

	// Regression control: MLSD over the data plane still lists facts.
	data = enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "MLSD /"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("MLSD start reply = %q", reply)
	}
	listing, err := io.ReadAll(data)
	if err != nil {
		t.Fatalf("MLSD read: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("MLSD data close: %v", err)
	}
	if line := readFTPReply(t, r); !strings.HasPrefix(line, "226") {
		t.Fatalf("MLSD completion = %q", line)
	}
	if !strings.Contains(string(listing), "type=file;size=4;") || !strings.Contains(string(listing), "f.txt") {
		t.Fatalf(" MLSD listing %q lacks the f.txt facts", string(listing))
	}
}

// TestExplicitTLSEmptyListingAndZeroByteRetr pins the round-48 contract: a
// PROT-protected data transfer that performs zero I/O (an empty-directory
// MLSD, a zero-byte STOR/RETR) must still complete the server-side TLS
// handshake on the data connection before the close — otherwise RFC 4217
// clients see EOF mid-handshake instead of the listing/transfer.
func TestExplicitTLSEmptyListingAndZeroByteRetr(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw123456", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}
	if _, err := cc.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("AUTH TLS reply = %q, want 234", reply)
	}
	tlsCtl := tls.Client(cc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsCtl.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	rTLS := bufio.NewReader(tlsCtl)
	if reply := readFTPReply(t, rTLS); !strings.HasPrefix(reply, "200") {
		t.Fatalf("requeued PBSZ reply = %q, want 200", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PASS pw123456"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P reply = %q, want 200", reply)
	}

	// dialExplicitData follows the server's verified ordering: PASV, dial,
	// transfer command, 150, THEN the client-side TLS handshake.
	dialExplicitData := func(name, cmd string) *tls.Conn {
		t.Helper()
		reply := ftpCmd(t, tlsCtl, rTLS, "PASV")
		if !strings.HasPrefix(reply, "227") {
			t.Fatalf("PASV reply = %q", reply)
		}
		addr := parsePASVDataAddr(t, reply)
		dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("data dial %s: %v", addr, err)
		}
		_ = dc.SetDeadline(time.Now().Add(8 * time.Second))
		if reply := ftpCmd(t, tlsCtl, rTLS, cmd); !strings.HasPrefix(reply, "150") {
			t.Fatalf("%s start reply = %q, want 150", cmd, reply)
		}
		td := tls.Client(dc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
		if err := td.Handshake(); err != nil {
			_ = dc.Close()
			t.Fatalf("data tls handshake (%s): %v", name, err)
		}
		return td
	}

	// 1. MLSD of the empty root over the protected data connection: the
	// listing performs zero I/O, so the handshake must come from the
	// explicit completion, and the empty result is the correct answer.
	data := dialExplicitData("mlsd", "MLSD /")
	listing, err := io.ReadAll(data)
	if err != nil {
		t.Fatalf("MLSD read: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("MLSD data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("MLSD completion = %q, want 226", line)
	}
	if len(listing) != 0 {
		t.Fatalf("MLSD listing = %q, want empty for an empty root", string(listing))
	}

	// 2. Zero-byte STOR over the protected data connection.
	data = dialExplicitData("stor", "STOR /zero.bin")
	if err := data.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q, want 226", line)
	}
	if info, statErr := backend.Stat("/zero.bin"); statErr != nil || info.Size() != 0 {
		t.Fatalf("zero.bin stat = (%v, %v), want a 0-byte file", info, statErr)
	}

	// 3. Zero-byte RETR over the protected data connection: io.Copy touches
	// the data conn zero times, so the handshake again needs the explicit
	// completion before the close.
	data = dialExplicitData("retr", "RETR /zero.bin")
	content, err := io.ReadAll(data)
	if err != nil {
		t.Fatalf("RETR read: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("RETR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("RETR completion = %q, want 226", line)
	}
	if len(content) != 0 {
		t.Fatalf("RETR content = %q, want empty", string(content))
	}
}
func TestFTPRntoAfterTLSUpgradeAndMlstOnDir(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 5, time.Minute)
	if _, err := engine.CreateUser("alice", "pw12345", "/", false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	fsys := vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(context.Background(), conn, false)
		}
	}()

	cc, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(cc)
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "220") {
		t.Fatalf("banner = %q", reply)
	}

	if _, err := cc.Write([]byte("AUTH TLS\r\nPBSZ 0\r\n")); err != nil {
		t.Fatalf("write pipelined commands: %v", err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "234") {
		t.Fatalf("AUTH TLS reply = %q, want 234", reply)
	}
	tlsCtl := tls.Client(cc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
	if err := tlsCtl.Handshake(); err != nil {
		t.Fatalf("client tls handshake: %v", err)
	}
	rTLS := bufio.NewReader(tlsCtl)
	if reply := readFTPReply(t, rTLS); !strings.HasPrefix(reply, "200") {
		t.Fatalf("requeued PBSZ reply = %q, want 200", reply)
	}

	if reply := ftpCmd(t, tlsCtl, rTLS, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "PROT P"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PROT P reply = %q, want 200", reply)
	}

	dialData := func() net.Conn {
		t.Helper()
		reply := ftpCmd(t, tlsCtl, rTLS, "PASV")
		if !strings.HasPrefix(reply, "227") {
			t.Fatalf("PASV reply = %q", reply)
		}
		addr := parsePASVDataAddr(t, reply)
		dc, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("data dial %s: %v", addr, err)
		}
		return dc
	}
	wrapTLS := func(dc net.Conn) *tls.Conn {
		t.Helper()
		td := tls.Client(dc, &tls.Config{InsecureSkipVerify: true, ServerName: "kervan-test"})
		if err := td.Handshake(); err != nil {
			dc.Close()
			t.Fatalf("data tls handshake: %v", err)
		}
		return td
	}

	// Seed /a.txt = "payload A" (9 bytes) over the protected data plane.
	dc := dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "STOR /a.txt"); !strings.HasPrefix(reply, "150") {
		dc.Close()
		t.Fatalf("STOR start reply = %q", reply)
	}
	td := wrapTLS(dc)
	if _, err := io.WriteString(td, "payload A"); err != nil {
		t.Fatalf("STOR write: %v", err)
	}
	if err := td.Close(); err != nil {
		t.Fatalf("STOR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("STOR completion = %q", line)
	}

	// RNFR + RNTO over the TLS-upgraded control channel.
	if reply := ftpCmd(t, tlsCtl, rTLS, "RNFR /a.txt"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("RNFR reply = %q, want 350", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "RNTO /renamed.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RNTO reply = %q, want 250", reply)
	}

	// MLST on the renamed file: file facts carry size=9.
	if reply := ftpCmd(t, tlsCtl, rTLS, "MLST /renamed.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("MLST /renamed.txt reply = %q, want 250", reply)
	}
	fileLine := readFTPReply(t, rTLS)
	if !strings.HasPrefix(fileLine, " type=file;size=9;") || !strings.Contains(fileLine, "/renamed.txt") {
		t.Fatalf("MLST file entry = %q, want \" type=file;size=9;… /renamed.txt\"", fileLine)
	}
	if end := readFTPReply(t, rTLS); !strings.HasPrefix(end, "250 End") {
		t.Fatalf("MLST file terminator = %q, want \"250 End\"", end)
	}

	// MLST on a directory: dir facts (type=dir), no size.
	if reply := ftpCmd(t, tlsCtl, rTLS, "MKD /sub"); !strings.HasPrefix(reply, "257") {
		t.Fatalf("MKD reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "MLST /sub"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("MLST /sub reply = %q, want 250", reply)
	}
	dirLine := readFTPReply(t, rTLS)
	if !strings.HasPrefix(dirLine, " type=dir;") || !strings.Contains(dirLine, "/sub") || !strings.Contains(dirLine, "size=0;") {
		t.Fatalf("MLST dir entry = %q, want \" type=dir;size=0;… /sub\" (the server pins size=0 for dirs in both MLST and MLSD)", dirLine)
	}
	if end := readFTPReply(t, rTLS); !strings.HasPrefix(end, "250 End") {
		t.Fatalf("MLST dir terminator = %q, want \"250 End\"", end)
	}

	// The rename persisted: RETR over the renamed path returns the content.
	dc2 := dialData()
	if reply := ftpCmd(t, tlsCtl, rTLS, "RETR /renamed.txt"); !strings.HasPrefix(reply, "150") {
		dc2.Close()
		t.Fatalf("RETR start reply = %q", reply)
	}
	td2 := wrapTLS(dc2)
	got, err := io.ReadAll(td2)
	if err != nil {
		t.Fatalf("RETR read: %v", err)
	}
	if err := td2.Close(); err != nil {
		t.Fatalf("RETR data close: %v", err)
	}
	if line := readFTPReply(t, rTLS); !strings.HasPrefix(line, "226") {
		t.Fatalf("RETR completion = %q", line)
	}
	if string(got) != "payload A" {
		t.Fatalf("RETR /renamed.txt = %q, want %q", string(got), "payload A")
	}

	// Cleanup over the same TLS control channel.
	if reply := ftpCmd(t, tlsCtl, rTLS, "DELE /renamed.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("DELE reply = %q", reply)
	}
	if reply := ftpCmd(t, tlsCtl, rTLS, "RMD /sub"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RMD reply = %q", reply)
	}
}

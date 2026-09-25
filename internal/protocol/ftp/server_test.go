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
	"io"
	"math/big"
	"net"
	"os"
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

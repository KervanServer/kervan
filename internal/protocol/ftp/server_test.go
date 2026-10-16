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
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
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
	// Intermediate lines carry one leading space (RFC 2389 §3.2).
	if !strings.Contains(output, "211-Features\r\n UTF8\r\n211 PASV\r\n") {
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
	perms := defaultDataPlanePerms()
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	// One shared UserVFS for every connection: returning memory.New() from the
	// builder hands each connection its own isolated storage, which is the
	// production bug pattern App.buildUserFS had.
	fsys := vfs.NewUserVFS(mounts, &perms, nil)

	sessions := session.NewManager()
	srv := NewServer(Config{Banner: "test-banner"}, nil, engine, sessions, nil,
		func(*auth.User) (vfs.FileSystem, error) { return fsys, nil }, nil)

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

func selfSignedTLSConfig(t testing.TB) *tls.Config {
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

// defaultDataPlanePerms is the permission set startFTPWithDataPlane has always
// granted. Callers that need a narrower or wider set pass their own value; see
// dataPlanePermsWithChmod.
func defaultDataPlanePerms() vfs.UserPermissions {
	return vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true,
	}
}

// dataPlanePermsWithChmod is defaultDataPlanePerms plus Chmod, so commands
// guarded by the VFS permission gate (SITE CHMOD) are reachable.
func dataPlanePermsWithChmod() vfs.UserPermissions {
	perms := defaultDataPlanePerms()
	perms.Chmod = true
	return perms
}

// ftpHarnessOptions varies the shared FTP test server. Per-permission and
// active-mode harnesses are thin wrappers over startFTPWithOptions rather than
// copies of its store/engine/backend/listener/dial setup.
type ftpHarnessOptions struct {
	perms      vfs.UserPermissions
	activeMode bool
}

// startFTPWithDataPlane builds a real FTP server over a real auth engine and a
// shared in-memory backend, and returns a control connection that has already
// read the 220 banner. It does NOT log in: callers that need an authenticated
// session should use loginOnDataPlane, which wraps this.
func startFTPWithDataPlane(t *testing.T, perms vfs.UserPermissions) (net.Conn, *bufio.Reader, *vfs.UserVFS, *memory.Backend) {
	t.Helper()
	return startFTPWithOptions(t, ftpHarnessOptions{perms: perms, activeMode: true})
}

// startFTPWithOptions is the single owner of the FTP test-server plumbing:
// store, auth engine, shared memory backend, listener, accept loop, control
// dial and 220 banner. Per-permission and active-mode harnesses delegate here.
//
// The listener, accept loop and control dial come from startEphemeralFTP,
// which also carries the two harness invariants from the round-43 notes:
// handleConn gets a non-nil context, and fresh control connections dial the
// listener's own Addr() — a TLS control conn's LocalAddr() is the client end
// of that connection, so it is useless as a dial target.
func startFTPWithOptions(t *testing.T, opts ftpHarnessOptions) (net.Conn, *bufio.Reader, *vfs.UserVFS, *memory.Backend) {
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
	fsys := vfs.NewUserVFS(mounts, &opts.perms, nil)

	srv := NewServer(Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		ActiveMode:  opts.activeMode,
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		return fsys, nil
	}, nil)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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

// testUser is one account to seed into a test server's store.
type testUser struct {
	name         string
	password     string
	primaryGroup string
}

// newTestServer builds the store/auth-engine/backend scaffolding shared by the
// harnesses that drive a real listener through Start(): one store, one engine,
// the requested users (with optional primary groups), and one shared in-memory
// backend behind a UserVFS.
//
// It is deliberately NOT used by startFTPWithOptions, whose harness must bypass
// admit(): admission control is wired inside startListener, so a server driven
// through Start() and one driven by a hand-rolled accept loop are different
// code paths and only the latter is what the data-plane tests want.
//
// The fsBuilder returns the SAME UserVFS on every call. Returning a fresh
// memory.New() per call — as these harnesses used to — silently gives each
// connection its own isolated storage, which is a production bug pattern
// (App.buildUserFS had it) and would make any cross-connection assertion
// misleading.
//
// The auth.Engine is returned so a caller can attach collaborators (an
// IPThrottle, say) before starting the listeners.
func newTestServer(t *testing.T, cfg Config, users []testUser, bcryptCost int) (*Server, *auth.Engine, *memory.Backend) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", bcryptCost, time.Minute)
	for _, u := range users {
		created, err := engine.CreateUser(u.name, u.password, "/", false)
		if err != nil {
			t.Fatalf("create user %s: %v", u.name, err)
		}
		if u.primaryGroup != "" {
			created.PrimaryGroup = u.primaryGroup
			if err := repo.Update(created); err != nil {
				t.Fatalf("set primary group for %s: %v", u.name, err)
			}
		}
	}
	backend := memory.New()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", backend, false)
	perms := defaultDataPlanePerms()
	fsys := vfs.NewUserVFS(mounts, &perms, nil)
	srv := NewServer(cfg, nil, engine, session.NewManager(), nil,
		func(*auth.User) (vfs.FileSystem, error) { return fsys, nil }, nil)
	return srv, engine, backend
}

// testServerBuilder builds a fresh Server bound to the given ports. A retry
// needs a fresh Server, not merely a fresh port — see startRealListeners.
type testServerBuilder func(port, implicitPort int) *Server

// startRealListeners starts a real listener (so admit() runs) and returns the
// explicit and implicit-FTPS dial targets. It is the Start()-based counterpart
// to the hand-rolled accept loop in startFTPWithOptions.
//
// freeTCPPort releases its probe listener before Start rebinds the port, so
// another process can take it in between (a TOCTOU window no test harness can
// close without a production change that lets Start accept a listener). On an
// "address already in use" failure this re-picks the ports and retries, up to
// startListenerAttempts times. Any other Start error fails immediately: it is
// not a bind race, and retrying would only bury a real fault.
//
// Each attempt builds a NEW Server because Server.Stop sets s.closed = true
// with no reset. Start() calls Stop() itself when the FTP listener binds but
// the FTPS-implicit one does not, so reusing that server would bind and then
// immediately treat every accept as closed — a silent failure worse than the
// race this retry exists to remove.
func startRealListeners(t *testing.T, build testServerBuilder, wantImplicit bool) (explicit, implicit string) {
	t.Helper()
	const startListenerAttempts = 5
	var lastErr error
	for attempt := 1; attempt <= startListenerAttempts; attempt++ {
		port := freeTCPPort(t)
		implicitPort := 0
		if wantImplicit {
			implicitPort = freeTCPPort(t)
		}
		srv := build(port, implicitPort)
		err := srv.Start(context.Background())
		if err != nil {
			_ = srv.Stop()
			if !isAddrInUse(err) {
				t.Fatalf("start ftp server: %v", err)
			}
			lastErr = err
			continue
		}
		t.Cleanup(func() { _ = srv.Stop() })
		explicit = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if wantImplicit {
			implicit = net.JoinHostPort("127.0.0.1", strconv.Itoa(implicitPort))
		}
		return explicit, implicit
	}
	t.Fatalf("could not bind an ftp listener after %d attempts (last error: %v)",
		startListenerAttempts, lastErr)
	return "", ""
}

// isAddrInUse reports whether err is a bind conflict. errors.Is covers the
// wrapped *net.OpError on every platform Go supports; the string check is a
// fallback because the errno differs per platform.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) ||
		strings.Contains(err.Error(), "address already in use")
}

// startEphemeralFTP serves srv on a kernel-assigned loopback port and returns
// that address. Binding :0 is what makes this race-free: the port is chosen and
// held by a live listener, so unlike freeTCPPort there is no probe-then-rebind
// window and no need for startRealListeners' bind retry.
//
// It calls srv.handleConn directly, which bypasses admit() — admission is
// wired inside startListener, so only Server.Start runs it. That is deliberate:
// these are data-plane tests, and admission has its own harnesses
// (startAdmissionFTP). It is also why this helper and startRealListeners are
// kept separate rather than merged.
//
// The listener is closed by t.Cleanup, and handleConn gets a non-nil context
// (nil panics inside its ctx operations).
func startEphemeralFTP(t *testing.T, srv *Server) string {
	t.Helper()
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
	// Dial the listener's own address. For a TLS control connection
	// tlsCtl.LocalAddr() is the client's ephemeral port, useless as a target.
	return ln.Addr().String()
}

// TestStartRealListenersRetriesABindConflict proves the retry rather than
// asserting it: the builder occupies the port it is handed on the first attempt,
// so the real Server.Start genuinely fails with a bind conflict. The harness
// must notice, re-pick a port, and end up serving.
func TestStartRealListenersRetriesABindConflict(t *testing.T) {
	attempts := 0
	build := func(port, _ int) *Server {
		attempts++
		if attempts == 1 {
			// Steal this port so Start() cannot bind it.
			thief, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				t.Fatalf("occupying port %d: %v", port, err)
			}
			t.Cleanup(func() { _ = thief.Close() })
		}
		cfg := Config{ListenAddr: "127.0.0.1", Port: port, Banner: "retry test", IdleTimeout: 30 * time.Second}
		srv, _, _ := newTestServer(t, cfg, nil, 5)
		return srv
	}

	addr, implicit := startRealListeners(t, build, false)
	if attempts < 2 {
		t.Fatalf("expected the harness to retry a bind conflict, but it made %d attempt(s)", attempts)
	}
	if implicit != "" {
		t.Fatalf("implicit = %q, want empty when no implicit listener was requested", implicit)
	}

	// The port it settled on must actually be serving, not just bound-then-dropped.
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s after retry: %v", addr, err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("reading banner from %s: %v", addr, err)
	}
	if !strings.Contains(line, "220") {
		t.Fatalf("banner after retry = %q, want a 220 greeting", strings.TrimSpace(line))
	}
}

// TestIsAddrInUse pins the filter: only a genuine bind conflict may be retried.
// Retrying an unrelated Start failure would hide a real fault behind five
// identical attempts.
func TestIsAddrInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	_, bindErr := net.Listen("tcp", addr)
	if bindErr == nil {
		t.Skip("platform allowed a second bind on the same address")
	}
	if !isAddrInUse(bindErr) {
		t.Fatalf("isAddrInUse(%v) = false, want true for a real bind conflict", bindErr)
	}
	for _, other := range []error{
		errors.New("plain failure"),
		fmt.Errorf("wrapped: %w", os.ErrNotExist),
		errors.New("listen tcp 127.0.0.1:99999: invalid port"),
	} {
		if isAddrInUse(other) {
			t.Fatalf("isAddrInUse(%v) = true, want false for an unrelated error", other)
		}
	}
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
	cc, r, fsys, backend := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	cc, r, fsys, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	// REST resumes a download at the marker, and applies to one transfer only.
	if reply := ftpCmd(t, cc, r, "REST 1"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("REST reply = %q, want 350", reply)
	}
	if got := retr("/f.txt"); got != "B" {
		t.Fatalf("RETR after REST 1 = %q, want %q", got, "B")
	}
	if got := retr("/f.txt"); got != "AB" {
		t.Fatalf("second RETR = %q, want %q — the marker must be consumed", got, "AB")
	}
	// REST + STOR resumes an interrupted upload without truncating it.
	if reply := ftpCmd(t, cc, r, "REST 2"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("REST reply = %q, want 350", reply)
	}
	store("/f.txt", "CD")
	if got := retr("/f.txt"); got != "ABCD" {
		t.Fatalf("after resumed STOR, RETR = %q, want %q", got, "ABCD")
	}
	if reply := ftpCmd(t, cc, r, "REST -1"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("negative REST reply = %q, want 501", reply)
	}
}

// TestFTPMkdirRmdirCwdOverControl drives the directory flows over the
// authenticated control connection, including the non-empty RMD refusal and
// CDUP back to the parent directory.
func TestFTPMkdirRmdirCwdOverControl(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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

	if reply := ftpCmd(t, cc, r, "MKD /a"); !strings.HasPrefix(reply, "257") {
		t.Fatalf("MKD /a reply = %q", reply)
	}
	ftpCmd(t, cc, r, "CWD /a")
	if reply := ftpCmd(t, cc, r, "CDUP"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("CDUP reply = %q, want 250", reply)
	}
	if reply := ftpCmd(t, cc, r, "PWD"); !strings.Contains(reply, `"/"`) {
		t.Fatalf("PWD after CDUP = %q, want /", reply)
	}
	if reply := ftpCmd(t, cc, r, "CDUP"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("CDUP at root reply = %q, want 250 (stays at /)", reply)
	}
}

// TestFTPPwdSizeMdtmMetadataReplies pins the metadata replies over the
// authenticated control connection: PWD quoted-path form (before and after
// CWD), SIZE by relative and absolute path, MDTM 14-digit UTC stamp, the
// dir/missing 550 refusals, and the TYPE I/X contract.
func TestFTPPwdSizeMdtmMetadataReplies(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	cfg := Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}
	srv, _, backend := newTestServer(t, cfg, []testUser{{name: "alice", password: "pw12345"}}, 5)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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
	cfg := Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}
	srv, _, backend := newTestServer(t, cfg, []testUser{{name: "alice", password: "pw12345"}}, 5)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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
	cc2, err := net.DialTimeout("tcp", addr, 5*time.Second)
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
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	cfg := Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}
	srv, _, _ := newTestServer(t, cfg, []testUser{{name: "alice", password: "pw12345"}}, 5)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())

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
	cfg := Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}
	srv, _, backend := newTestServer(t, cfg, []testUser{{name: "alice", password: "pw123456"}}, 5)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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
	cfg := Config{
		Port:        2121,
		Banner:      "kervan test",
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 30 * time.Second,
		FTPSMode:    "explicit",
		TLSConfig:   selfSignedTLSConfig(t),
	}
	srv, _, _ := newTestServer(t, cfg, []testUser{{name: "alice", password: "pw12345"}}, 5)

	addr := startEphemeralFTP(t, srv)

	cc, err := net.DialTimeout("tcp", addr, 5*time.Second)
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

// listOverDataPlane drives a real handleConn through login, PASV and LIST <arg>
// using this file's existing harness, returning the control reply and the
// data-plane body.
func listOverDataPlane(t *testing.T, arg string) (string, string) {
	t.Helper()
	cc, r, _, backend := startFTPWithDataPlane(t, defaultDataPlanePerms())
	if err := backend.Mkdir("/sub", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER: %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS: %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I: %q", reply)
	}
	dc := enterFTPPassive(t, cc, r)
	ctrl := ftpCmd(t, cc, r, "LIST "+arg)
	_ = dc.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, _ := io.ReadAll(dc)
	return ctrl, string(body)
}

// TestFTPListAcceptsRFC959FlagArgument pins that LIST/NLST accept the client
// flags RFC 959 §4.1.2 defines ("<FTP-flag>... [<pathname>]"). Mainstream
// clients send "LIST -la"; the flags used to be joined into the path, so the
// server stat'd "/-la", listed nothing, and still answered 226 Transfer
// complete — a plausible-looking but empty directory.
func TestFTPListAcceptsRFC959FlagArgument(t *testing.T) {
	ctrl, body := listOverDataPlane(t, "-la")
	if strings.HasPrefix(ctrl, "450") || strings.HasPrefix(ctrl, "550") {
		t.Fatalf("`LIST -la` answered %q; the RFC 959 flag must not be folded into the path (body %q)",
			ctrl, body)
	}
	if !strings.Contains(body, "sub") {
		t.Fatalf("`LIST -la` returned %q but listed no entries (no \"sub\"): %q", ctrl, body)
	}
}

// TestFTPListPlainArgumentUnaffected is the control: a bare LIST and an explicit
// path must behave exactly as before.
func TestFTPListPlainArgumentUnaffected(t *testing.T) {
	ctrl, body := listOverDataPlane(t, "")
	if strings.HasPrefix(ctrl, "450") || strings.HasPrefix(ctrl, "550") {
		t.Fatalf("control: bare LIST answered %q: %q", ctrl, body)
	}
	if !strings.Contains(body, "sub") {
		t.Fatalf("control: bare LIST listed no entries: %q", body)
	}
	ctrl, body = listOverDataPlane(t, "/sub")
	if strings.HasPrefix(ctrl, "450") || strings.HasPrefix(ctrl, "550") {
		t.Fatalf("control: LIST /sub answered %q: %q", ctrl, body)
	}
}

// TestStripListFlags covers the stripping rule and its boundaries: flags only,
// flags then a path, an absolute path, and a trailing token that is not a flag.
func TestStripListFlags(t *testing.T) {
	cases := map[string]string{
		"-la":          "",
		"-l -a":        "",
		"-l":           "",
		"   -la  ":     "",
		"-la /sub":     "/sub",
		"-l /sub":      "/sub",
		"/sub":         "/sub",
		"sub":          "sub",
		"-la -la /sub": "/sub",
		"sub -la":      "sub -la", // not leading: kept verbatim
		"":             "",
	}
	for in, want := range cases {
		if got := stripListFlags(in); got != want {
			t.Errorf("stripListFlags(%q) = %q, want %q", in, got, want)
		}
	}
}

// loginOnDataPlaneWith completes the USER/PASS/TYPE I handshake on a control
// connection already returned by startFTPWithDataPlane (which reads the 220
// banner but does not authenticate), and returns the shared backend.
func loginOnDataPlaneWith(t *testing.T, cc net.Conn, r *bufio.Reader, backend *memory.Backend) (net.Conn, *bufio.Reader, *memory.Backend) {
	t.Helper()
	if reply := ftpCmd(t, cc, r, "USER alice"); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER: %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS: %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I: %q", reply)
	}
	return cc, r, backend
}

// loginOnDataPlane is an authenticated session with defaultDataPlanePerms.
func loginOnDataPlane(t *testing.T) (net.Conn, *bufio.Reader, *memory.Backend) {
	t.Helper()
	cc, r, _, backend := startFTPWithDataPlane(t, defaultDataPlanePerms())
	return loginOnDataPlaneWith(t, cc, r, backend)
}

// TestFTPAborWithNoTransferInProgress covers RFC 959 §4.1.3: ABOR with nothing
// running answers 225 and leaves the control channel usable. ABOR reaches the
// command dispatch only when watchAbort did not consume it, so this exercises
// the idle half of the abort path.
func TestFTPAborWithNoTransferInProgress(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "ABOR"); !strings.HasPrefix(reply, "225") {
		t.Fatalf("ABOR with no transfer = %q, want 225", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after idle ABOR: NOOP = %q", reply)
	}
}

// TestFTPAborDuringTransfer covers RFC 959 §4.1.3: an ABOR arriving while a
// download is running aborts it — 426 Transfer aborted. followed by 226 ABOR
// command successful. — and the control channel still works afterwards.
// watchAbort's stop() pins the control read deadline into the past to wake its
// watcher, so the command loop must re-arm it; this pins that it does.
func TestFTPAborDuringTransfer(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)

	// Large enough that the server blocks writing to the unread data socket,
	// so the transfer is genuinely still in flight when ABOR arrives.
	w, err := backend.Open("/big.bin", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := w.Write([]byte(strings.Repeat("x", 2<<20))); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /big.bin"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR = %q, want 150", reply)
	}
	// Deliberately do NOT read dc: the server blocks mid-copy.

	if _, err := cc.Write([]byte("ABOR\r\n")); err != nil {
		t.Fatalf("write ABOR: %v", err)
	}
	_ = cc.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read abort notice: %v", err)
	}
	if !strings.Contains(first, "426") {
		t.Fatalf("in-flight ABOR first reply = %q, want a 426 transfer-aborted notice",
			strings.TrimSpace(first))
	}
	second, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read ABOR confirmation: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(second), "226") {
		t.Fatalf("in-flight ABOR confirmation = %q, want 226", strings.TrimSpace(second))
	}

	_ = dc.Close()
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after in-flight ABOR: NOOP = %q", reply)
	}
}

// activeModeListener opens the client-side data listener that an active-mode
// PORT/EPRT must point the server at.
func activeModeListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// portArg renders "h1,h2,h3,h4,p1,p2" (RFC 959 §4.1.2) for a local listener.
func portArg(ln net.Listener) string {
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return ""
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("127,0,0,1,%d,%d", p/256, p%256)
}

func seedBackendFile(t *testing.T, backend *memory.Backend, name, content string) {
	t.Helper()
	w, err := backend.Open(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("seed write %s: %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("seed close %s: %v", name, err)
	}
}

// TestFTPActiveModePortTransfer drives a full active-mode round trip: PORT to a
// listener the client opened, then RETR, with the server dialing back to us.
func TestFTPActiveModePortTransfer(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/f.txt", "active-payload")

	ln := activeModeListener(t)
	if reply := ftpCmd(t, cc, r, "PORT "+portArg(ln)); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PORT = %q, want 200", reply)
	}
	if reply := ftpCmd(t, cc, r, "RETR /f.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR after PORT = %q, want 150", reply)
	}

	dc, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept server data connection: %v", err)
	}
	defer dc.Close()
	_ = dc.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.ReadAll(dc)
	if string(got) != "active-payload" {
		t.Fatalf("active-mode PORT RETR delivered %q, want %q", string(got), "active-payload")
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("RETR completion = %q, want 226", reply)
	}
}

// TestFTPActiveModeEprtTransfer is the RFC 2428 equivalent of the PORT case.
func TestFTPActiveModeEprtTransfer(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/f.txt", "eprt-payload")

	ln := activeModeListener(t)
	arg := fmt.Sprintf("|1|127.0.0.1|%s|", strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:"))
	if reply := ftpCmd(t, cc, r, "EPRT "+arg); !strings.HasPrefix(reply, "200") {
		t.Fatalf("EPRT = %q, want 200", reply)
	}
	if reply := ftpCmd(t, cc, r, "RETR /f.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR after EPRT = %q, want 150", reply)
	}
	dc, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept server data connection: %v", err)
	}
	defer dc.Close()
	_ = dc.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.ReadAll(dc)
	if string(got) != "eprt-payload" {
		t.Fatalf("active-mode EPRT RETR delivered %q, want %q", string(got), "eprt-payload")
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("RETR completion = %q, want 226", reply)
	}
}

// TestFTPPortRejectsBounceTarget pins the no-FXP rule: a PORT/EPRT naming an
// address other than the control connection's own is refused with 504, so the
// server cannot be used to reach a third party. The target is well-formed and
// clears the privileged floor (7*256+144 = 1936), so it must reach the bounce
// check rather than fail the syntax check.
func TestFTPPortRejectsBounceTarget(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "PORT 10,0,0,5,7,144"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("PORT to a foreign address = %q, want 504 (no FXP/bounce)", reply)
	}
	if reply := ftpCmd(t, cc, r, "EPRT |1|10.0.0.5|1936|"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("EPRT to a foreign address = %q, want 504", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after rejected PORT: NOOP = %q", reply)
	}
}

// TestFTPPortRejectsMalformedAndPrivileged pins argument validation: a
// malformed PORT is 501, a well-formed one below the privileged floor is 504,
// and the control channel survives both.
func TestFTPPortRejectsMalformedAndPrivileged(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "PORT not,a,port"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("malformed PORT = %q, want 501", reply)
	}
	// PORT splits the port into two bytes of at most 255 each.
	if reply := ftpCmd(t, cc, r, "PORT 127,0,0,1,2000,50"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("PORT with an out-of-range port field = %q, want 501", reply)
	}
	if reply := ftpCmd(t, cc, r, "PORT 127,0,0,1,0,80"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("PORT to a privileged port = %q, want 504", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after rejected PORT: NOOP = %q", reply)
	}
}

// TestFTPFeatAdvertisesEPRTWhenActiveMode pins the RFC 2389 §2 contract that
// matters here: the active-mode extension is advertised exactly when active
// mode is enabled. PORT is a base RFC 959 command, not a FEAT extension, so it
// is deliberately absent from the list.
func TestFTPFeatAdvertisesEPRTWhenActiveMode(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	feat := readFeat(t, cc, r)
	if !strings.Contains(feat, "EPRT") {
		t.Fatalf("FEAT omitted EPRT while ActiveMode is enabled.\nFEAT: %q", feat)
	}
	if !strings.Contains(feat, "PASV") {
		t.Fatalf("FEAT omitted PASV: %q", feat)
	}
}

// readFeat issues FEAT and joins its reply. It reads the 211-…/211 framing
// directly because writeMultiline's middle-line TrimSpace strips the leading
// space that RFC 2389 §3.2 gives each feature line.
func readFeat(t *testing.T, cc net.Conn, r *bufio.Reader) string {
	t.Helper()
	if _, err := cc.Write([]byte("FEAT\r\n")); err != nil {
		t.Fatalf("write FEAT: %v", err)
	}
	var feat strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read FEAT: %v", err)
		}
		feat.WriteString(line)
		if strings.HasPrefix(line, "211 ") { // terminator (RFC 2389 §3.2)
			return feat.String()
		}
	}
}

// startPassiveOnlyFTP is an authenticated session with ActiveMode disabled, so
// the negative half of the active-mode contract (PORT/EPRT answer 502 and FEAT
// omits EPRT) can be driven over a real handleConn. It delegates all server
// plumbing to startFTPWithOptions; only ActiveMode and the login handshake are
// specific to it.
func startPassiveOnlyFTP(t *testing.T) (net.Conn, *bufio.Reader, *memory.Backend) {
	t.Helper()
	cc, r, _, backend := startFTPWithOptions(t, ftpHarnessOptions{
		perms:      defaultDataPlanePerms(),
		activeMode: false, // the point of this harness
	})
	return loginOnDataPlaneWith(t, cc, r, backend)
}

// TestFTPFeatOmitsEPRTWhenActiveModeDisabled pins the other half of the RFC 2389
// §2 contract: with active mode off, the active-mode extension must not be
// advertised. Advertising EPRT and then refusing it with 502 is exactly the
// advertisement/implementation mismatch round 46 fixed for MLST.
func TestFTPFeatOmitsEPRTWhenActiveModeDisabled(t *testing.T) {
	cc, r, _ := startPassiveOnlyFTP(t)

	feat := readFeat(t, cc, r)
	if strings.Contains(feat, "EPRT") {
		t.Fatalf("FEAT advertised EPRT while ActiveMode is disabled.\nFEAT: %q", feat)
	}
	if !strings.Contains(feat, "PASV") {
		t.Fatalf("FEAT omitted PASV (passive mode is still available): %q", feat)
	}
}

// TestFTPPortAndEprtRefusedWhenActiveModeDisabled pins that both active-mode
// commands answer 502 when the feature is off, and that the control channel
// survives the refusal.
func TestFTPPortAndEprtRefusedWhenActiveModeDisabled(t *testing.T) {
	cc, r, _ := startPassiveOnlyFTP(t)

	if reply := ftpCmd(t, cc, r, "PORT 127,0,0,1,7,144"); !strings.HasPrefix(reply, "502") {
		t.Fatalf("PORT with ActiveMode disabled = %q, want 502", reply)
	}
	if reply := ftpCmd(t, cc, r, "EPRT |1|127.0.0.1|1936|"); !strings.HasPrefix(reply, "502") {
		t.Fatalf("EPRT with ActiveMode disabled = %q, want 502", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after refused PORT/EPRT: NOOP = %q", reply)
	}
}

// TestFTPPassiveModeUnaffectedByDisabledActiveMode is the control: turning
// active mode off must not disturb passive transfers.
func TestFTPPassiveModeUnaffectedByDisabledActiveMode(t *testing.T) {
	cc, r, backend := startPassiveOnlyFTP(t)
	seedBackendFile(t, backend, "/f.txt", "passive-payload")

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /f.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR = %q, want 150", reply)
	}
	_ = dc.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, _ := io.ReadAll(dc)
	if string(body) != "passive-payload" {
		t.Fatalf("passive RETR delivered %q, want %q", string(body), "passive-payload")
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("RETR completion = %q, want 226", reply)
	}
}

// TestFTPTypeWithoutArgumentReportsCurrentType pins RFC 959 §4.1.3: a client may
// use TYPE with no argument to obtain the current type. The dispatch used to
// compare the empty argument against "I"/"A" and answer 504 "Type not
// supported.", so a valid query was rejected — and the chosen type was never
// stored, so it could not be reported back at all.
func TestFTPTypeWithoutArgumentReportsCurrentType(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	// The connection starts in binary (IMAGE), which is what the server
	// actually performs: transfers are byte-exact.
	reply := ftpCmd(t, cc, r, "TYPE")
	if !strings.HasPrefix(reply, "200") {
		t.Fatalf("bare TYPE = %q, want 200 reporting the current type (RFC 959 §4.1.3)", reply)
	}

	if got := ftpCmd(t, cc, r, "TYPE A"); !strings.HasPrefix(got, "200") {
		t.Fatalf("TYPE A = %q, want 200", got)
	}
	reply = ftpCmd(t, cc, r, "TYPE")
	if !strings.HasPrefix(reply, "200") {
		t.Fatalf("bare TYPE after TYPE A = %q, want 200", reply)
	}
	if !strings.Contains(reply, "A") {
		t.Fatalf("bare TYPE after TYPE A = %q, want it to report the current type A", reply)
	}
}

// TestFTPTypeSupportedAndUnsupportedCodes pins the accept/reject codes: I and A
// are accepted, EBCDIC (E) and local byte size (L) are refused with 504, and the
// control channel survives every rejection.
func TestFTPTypeSupportedAndUnsupportedCodes(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "TYPE I"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE I = %q, want 200", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE A"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("TYPE A = %q, want 200", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE E"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("TYPE E = %q, want 504 (EBCDIC unsupported)", reply)
	}
	if reply := ftpCmd(t, cc, r, "TYPE L"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("TYPE L = %q, want 504 (local byte size unsupported)", reply)
	}
	// RFC 959 type codes are uppercase; a lowercase code is not a valid request.
	if reply := ftpCmd(t, cc, r, "TYPE i"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("lowercase TYPE i = %q, want 504", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after TYPE: NOOP = %q", reply)
	}
}

// TestFTPModeAndStruReplyCodes pins the RFC 959 §4.2 reply codes for the two
// transfer-mode parameters.
//
// The distinction matters and is deliberate: a *recognised* code that the server
// does not implement gets 504 ("Command parameter not implemented" — MODE
// B/C/Z, STRU R/P), while a code that is not a mode at all gets 501 ("Syntax
// error in parameters or arguments"). Collapsing both onto 504 would tell a
// client its request was well-formed but unimplemented when it was in fact
// nonsense.
func TestFTPModeAndStruReplyCodes(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "MODE S"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("MODE S = %q, want 200 (Stream is supported)", reply)
	}
	// Recognised RFC 959 modes the server does not implement.
	for _, mode := range []string{"MODE B", "MODE C", "MODE Z"} {
		if reply := ftpCmd(t, cc, r, mode); !strings.HasPrefix(reply, "504") {
			t.Fatalf("%s = %q, want 504 (recognised but unimplemented)", mode, reply)
		}
	}
	// Not a mode at all: a parameter error, not an unimplemented parameter.
	if reply := ftpCmd(t, cc, r, "MODE X"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("MODE X = %q, want 501 (invalid parameter)", reply)
	}

	if reply := ftpCmd(t, cc, r, "STRU F"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("STRU F = %q, want 200 (File structure is supported)", reply)
	}
	for _, stru := range []string{"STRU R", "STRU P"} {
		if reply := ftpCmd(t, cc, r, stru); !strings.HasPrefix(reply, "504") {
			t.Fatalf("%s = %q, want 504 (recognised but unimplemented)", stru, reply)
		}
	}
	if reply := ftpCmd(t, cc, r, "STRU X"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("STRU X = %q, want 501 (invalid parameter)", reply)
	}

	// Case-insensitive, per the command's argument parsing.
	if reply := ftpCmd(t, cc, r, "MODE s"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("MODE s = %q, want 200 (arguments are case-insensitive)", reply)
	}

	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after MODE/STRU: NOOP = %q", reply)
	}
}

// TestFTPCombIsNotImplemented pins that COMB — which RFC 959 §4.1.3 defines but
// this server does not implement — falls through to 502 "Command not
// implemented.", and is likewise absent from the HELP command list, so the
// server never advertises a command it refuses.
func TestFTPCombIsNotImplemented(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	if reply := ftpCmd(t, cc, r, "COMB I"); !strings.HasPrefix(reply, "502") {
		t.Fatalf("COMB I = %q, want 502 (RFC 959 §4.2: command not implemented)", reply)
	}

	// HELP is a multi-line 214 reply, so it is read to its terminator rather
	// than through ftpCmd (which stops after the first line) — otherwise a
	// continuation line would be mistaken for the next command's reply.
	if _, err := cc.Write([]byte("HELP\r\n")); err != nil {
		t.Fatalf("write HELP: %v", err)
	}
	var help strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read HELP: %v", err)
		}
		help.WriteString(line)
		if strings.HasPrefix(line, "214 ") { // terminator
			break
		}
	}
	if strings.Contains(help.String(), "COMB") {
		t.Fatalf("HELP lists COMB although the server refuses it with 502:\n%q", help.String())
	}

	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after COMB: NOOP = %q", reply)
	}
}

// TestFTPRntoOverwritesExistingTarget drives the RFC 959 §3.1.2 rename overload:
// RNTO naming a path that already exists must replace that file. Prior coverage
// only ever renamed onto a NEW name, so the overload never reached the control
// connection.
func TestFTPRntoOverwritesExistingTarget(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/src.txt", "SOURCE")
	seedBackendFile(t, backend, "/target.txt", "TARGET-OLD")

	if reply := ftpCmd(t, cc, r, "RNFR /src.txt"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("RNFR = %q, want 350 (RFC 959 §3.1.2: action pending)", reply)
	}
	if reply := ftpCmd(t, cc, r, "RNTO /target.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RNTO onto an existing target = %q, want 250", reply)
	}

	// The target must now serve the source's bytes, not its old ones.
	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /target.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR /target.txt = %q, want 150", reply)
	}
	_ = dc.SetReadDeadline(time.Now().Add(5 * time.Second))
	body, _ := io.ReadAll(dc)
	if string(body) != "SOURCE" {
		t.Fatalf("after the overload /target.txt serves %q, want %q — the existing target "+
			"was not overwritten", string(body), "SOURCE")
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("RETR completion = %q, want 226", reply)
	}

	// The source path must be gone after the rename.
	if reply := ftpCmd(t, cc, r, "SIZE /src.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("SIZE /src.txt after rename = %q, want 550 (the source path should be gone)", reply)
	}
}

// TestFTPRnfrRejectsMissingSourceAndRntoNeedsRnfr pins the two guard replies
// around the pair: RNFR on a missing object is 550, and RNTO without a pending
// RNFR is 503 (RFC 959 §3.1.2 sequence).
func TestFTPRnfrRejectsMissingSourceAndRntoNeedsRnfr(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/a.txt", "A")

	if reply := ftpCmd(t, cc, r, "RNTO /b.txt"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("RNTO with no pending RNFR = %q, want 503", reply)
	}
	if reply := ftpCmd(t, cc, r, "RNFR /nope.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("RNFR on a missing file = %q, want 550", reply)
	}
	// The failed RNFR must not leave a pending rename behind.
	if reply := ftpCmd(t, cc, r, "RNTO /b.txt"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("RNTO after a failed RNFR = %q, want 503 (no pending rename)", reply)
	}
	// A good pair still works afterwards.
	if reply := ftpCmd(t, cc, r, "RNFR /a.txt"); !strings.HasPrefix(reply, "350") {
		t.Fatalf("RNFR /a.txt = %q, want 350", reply)
	}
	if reply := ftpCmd(t, cc, r, "RNTO /b.txt"); !strings.HasPrefix(reply, "250") {
		t.Fatalf("RNTO /b.txt = %q, want 250", reply)
	}
	// The rename is consumed: a second RNTO needs a fresh RNFR.
	if reply := ftpCmd(t, cc, r, "RNTO /c.txt"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("RNTO after a completed pair = %q, want 503 (the RNFR was consumed)", reply)
	}
}

// ftpReplyCode reports whether line begins with a three-digit reply code
// followed by sep, i.e. is a real RFC 959 status line rather than the free-form
// continuation lines writeMultiline emits between the first and last line.
func ftpReplyCode(line string, sep byte) bool {
	if len(line) < 4 || line[3] != sep {
		return false
	}
	for i := 0; i < 3; i++ {
		if line[i] < '0' || line[i] > '9' {
			return false
		}
	}
	return true
}

// readMultilineFTPReply reads a reply frame to its terminating "NNN " line and
// returns the body. writeMultiline emits the first line as "NNN-...", the last
// as "NNN ...", and every intermediate line as free text prefixed with a single
// space — so continuation lines must NOT be parsed as if they carried a code.
// Frames whose format depends on exact whitespace are hand-framed, never routed
// through writeMultiline's TrimSpace.
func readMultilineFTPReply(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var lines []string
	for i := 0; i < 32; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading multiline reply: %v (read so far: %q)", err, lines)
		}
		line = strings.TrimRight(line, "\r\n")
		if ftpReplyCode(line, ' ') {
			return strings.Join(lines, "\n")
		}
		if ftpReplyCode(line, '-') {
			lines = append(lines, line[4:])
			continue
		}
		lines = append(lines, strings.TrimPrefix(line, " "))
	}
	t.Fatalf("multiline reply exceeded 32 lines")
	return ""
}

// TestFTPAlloReplyCode pins ALLO, the one pre-authentication command in this
// group: RFC 959 §4.1.1 has it answer 202 (command okay, no action taken) and
// it must be usable before login.
func TestFTPAlloReplyCode(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)
	if reply := ftpCmd(t, cc, r, "ALLO"); !strings.HasPrefix(reply, "202") {
		t.Fatalf("ALLO = %q, want 202 (RFC 959 §4.1.1)", reply)
	}
	if reply := ftpCmd(t, cc, r, "ALLO 1000"); !strings.HasPrefix(reply, "202") {
		t.Fatalf("ALLO 1000 = %q, want 202 (the server takes no allocation)", reply)
	}
}

// TestFTPSiteCommandGroup drives SITE HELP / CHMOD and the unknown-subcommand
// fallback over the control connection.
func TestFTPSiteCommandGroup(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/a.txt", "A")

	// Bare SITE and SITE HELP both list the recognized subcommands.
	// SITE replies are multi-line frames, so they must be drained with the
	// multiline reader — ftpCmd reads a single line and would strand the
	// continuation lines in the buffer for the next command.
	if _, err := fmt.Fprintf(cc, "SITE\r\n"); err != nil {
		t.Fatalf("write SITE: %v", err)
	}
	if body := readMultilineFTPReply(t, r); !strings.Contains(body, "CHMOD HELP") {
		t.Fatalf("bare SITE body = %q, want it to list the recognized subcommands", body)
	}
	if _, err := fmt.Fprintf(cc, "SITE HELP\r\n"); err != nil {
		t.Fatalf("write SITE HELP: %v", err)
	}
	if body := readMultilineFTPReply(t, r); !strings.Contains(body, "CHMOD HELP") {
		t.Fatalf("SITE HELP body = %q, want it to list the recognized subcommands", body)
	}

	// Malformed usage -> 501, distinct from a refused request.
	if reply := ftpCmd(t, cc, r, "SITE CHMOD notanumber /a.txt"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("SITE CHMOD with a bad mode = %q, want 501 (usage error)", reply)
	}
	if reply := ftpCmd(t, cc, r, "SITE CHMOD 644"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("SITE CHMOD with no target = %q, want 501 (usage error)", reply)
	}
	// loginOnDataPlane grants Upload/Download/Delete/Rename/CreateDir/ListDir but
	// NOT Chmod, so UserVFS.Chmod returns os.ErrPermission and the command is
	// correctly refused with 550 — a refusal, not a usage error. Pinning it here
	// keeps the permission gate covered; TestFTPSiteChmodAppliesWhenPermitted
	// covers the 200 path on a harness that grants it.
	if reply := ftpCmd(t, cc, r, "SITE CHMOD 600 /a.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("SITE CHMOD without the Chmod permission = %q, want 550", reply)
	}
	// Well-formed request against a missing object is also 550, but that is a
	// different cause; assert the session is still usable either way.
	if reply := ftpCmd(t, cc, r, "SITE CHMOD 600 /missing.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("SITE CHMOD on a missing file = %q, want 550", reply)
	}
	// An unrecognized SITE subcommand -> 504, and the session survives it.
	if reply := ftpCmd(t, cc, r, "SITE BOGUS"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("SITE BOGUS = %q, want 504", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after SITE BOGUS: NOOP = %q", reply)
	}
}

// loginOnDataPlaneChmod is loginOnDataPlane with Chmod granted, so commands
// behind the VFS permission gate (SITE CHMOD) are reachable. UserVFS.Chmod
// refuses with os.ErrPermission when it is unset, which is why the default
// harness correctly reports 550 for SITE CHMOD.
func loginOnDataPlaneChmod(t *testing.T) (net.Conn, *bufio.Reader, *memory.Backend) {
	t.Helper()
	cc, r, _, backend := startFTPWithDataPlane(t, dataPlanePermsWithChmod())
	return loginOnDataPlaneWith(t, cc, r, backend)
}

// TestFTPSiteChmodAppliesWhenPermitted covers the SITE CHMOD success reply,
// which the shared harness cannot reach because it does not grant Chmod.
func TestFTPSiteChmodAppliesWhenPermitted(t *testing.T) {
	cc, r, backend := loginOnDataPlaneChmod(t)
	seedBackendFile(t, backend, "/a.txt", "A")

	if reply := ftpCmd(t, cc, r, "SITE CHMOD 600 /a.txt"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("SITE CHMOD 600 = %q, want 200", reply)
	}
	// The mode must actually have been applied, not merely acknowledged.
	info, err := backend.Stat("/a.txt")
	if err != nil {
		t.Fatalf("stat after SITE CHMOD: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode after SITE CHMOD 600 = %o, want 600", info.Mode().Perm())
	}
	// An out-of-range mode is refused by handleSite's own bounds check.
	if reply := ftpCmd(t, cc, r, "SITE CHMOD 77777 /a.txt"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("SITE CHMOD 77777 = %q, want 501 (mode above 07777)", reply)
	}
}

// TestFTPMfmtSetsModificationTime drives MFMT end to end: a valid stamp must be
// applied and reported, and the two malformed-input branches must be distinct.
func TestFTPMfmtSetsModificationTime(t *testing.T) {
	cc, r, backend := loginOnDataPlane(t)
	seedBackendFile(t, backend, "/a.txt", "A")

	if reply := ftpCmd(t, cc, r, "MFMT 20200102150405 /a.txt"); !strings.HasPrefix(reply, "213") {
		t.Fatalf("MFMT = %q, want 213 (draft-somers-ftp-mfxx)", reply)
	}
	// The timestamp must actually have been applied, not merely acknowledged.
	if reply := ftpCmd(t, cc, r, "MDTM /a.txt"); !strings.HasPrefix(reply, "213") ||
		!strings.Contains(reply, "20200102150405") {
		t.Fatalf("MDTM after MFMT = %q, want it to report 20200102150405", reply)
	}

	// A bad stamp and a missing target are both usage errors -> 501.
	if reply := ftpCmd(t, cc, r, "MFMT not-a-stamp /a.txt"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("MFMT with a bad stamp = %q, want 501", reply)
	}
	if reply := ftpCmd(t, cc, r, "MFMT 20200102150405"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("MFMT with no target = %q, want 501", reply)
	}
	// A well-formed request against a missing object -> 550.
	if reply := ftpCmd(t, cc, r, "MFMT 20200102150405 /missing.txt"); !strings.HasPrefix(reply, "550") {
		t.Fatalf("MFMT on a missing file = %q, want 550", reply)
	}
}

// TestFTPOptsAndSystReplyCodes drives OPTS and SYST over a real handleConn.
// Both are pre-auth commands (RFC 959 §4.1.1 lists SYST among the commands
// accepted before login), so they are exercised both before and after login.
func TestFTPOptsAndSystReplyCodes(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	for _, tc := range []struct{ cmd, want string }{
		{"OPTS", "200"},
		{"OPTS UTF8 ON", "200"},
		{"OPTS MLST type*;size*;", "200"},
		{"SYST", "215"},
	} {
		if got := ftpCmd(t, cc, r, tc.cmd); !strings.HasPrefix(got, tc.want) {
			t.Fatalf("%q = %q, want a %s reply", tc.cmd, got, tc.want)
		}
	}
	// SYST must name a system type a client can act on, not just return 215.
	if body := ftpCmd(t, cc, r, "SYST"); !strings.Contains(body, "UNIX Type") {
		t.Fatalf("SYST body = %q, want it to name the system type", body)
	}
	// Control channel survives, so the codes above were real replies.
	if got := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(got, "200") {
		t.Fatalf("NOOP after OPTS/SYST = %q, want 200", got)
	}
}

// TestFTPLegacyRFC775AliasCommands proves the RFC 775 X-commands are not just
// accepted but actually re-aimed at their modern equivalents: each one must
// change observable server state, not merely return a plausible reply code.
func TestFTPLegacyRFC775AliasCommands(t *testing.T) {
	cc, r, _ := loginOnDataPlane(t)

	// XMKD -> MKD creates the directory.
	if got := ftpCmd(t, cc, r, "XMKD /newdir"); !strings.HasPrefix(got, "257") {
		t.Fatalf("XMKD = %q, want 257 created", got)
	}
	// XCWD -> CWD actually moves: PWD must report the new directory.
	if got := ftpCmd(t, cc, r, "XCWD /newdir"); !strings.HasPrefix(got, "250") {
		t.Fatalf("XCWD = %q, want 250 directory changed", got)
	}
	if got := ftpCmd(t, cc, r, "PWD"); !strings.Contains(got, "/newdir") {
		t.Fatalf("PWD after XCWD = %q, want it to report /newdir", got)
	}
	// XCUP -> CDUP actually returns to the root.
	if got := ftpCmd(t, cc, r, "XCUP"); !strings.HasPrefix(got, "250") {
		t.Fatalf("XCUP = %q, want 250 directory changed", got)
	}
	if got := ftpCmd(t, cc, r, "PWD"); strings.Contains(got, "/newdir") {
		t.Fatalf("PWD after XCUP = %q, want back at the root", got)
	}
	// XRMD -> RMD removes it; the alias must not merely acknowledge.
	if got := ftpCmd(t, cc, r, "XRMD /newdir"); !strings.HasPrefix(got, "250") {
		t.Fatalf("XRMD = %q, want 250 directory removed", got)
	}
	if got := ftpCmd(t, cc, r, "XCWD /newdir"); !strings.HasPrefix(got, "550") {
		t.Fatalf("XCWD to the removed directory = %q, want 550", got)
	}
	if got := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(got, "200") {
		t.Fatalf("NOOP after the alias sequence = %q, want 200", got)
	}
}

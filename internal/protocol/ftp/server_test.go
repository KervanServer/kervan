package ftp

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
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

package ftp

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// readFullReply reads one reply, including every line of a multi-line one.
func readFullReply(t *testing.T, r *bufio.Reader) []string {
	t.Helper()
	first := readFTPReply(t, r)
	lines := []string{first}
	if len(first) < 4 || first[3] != '-' {
		return lines
	}
	end := first[:3] + " "
	for {
		line := readFTPReply(t, r)
		lines = append(lines, line)
		if strings.HasPrefix(line, end) {
			return lines
		}
	}
}

func loginFTP(t *testing.T, cc net.Conn, r *bufio.Reader) {
	t.Helper()
	ftpCmd(t, cc, r, "USER alice")
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply %q", reply)
	}
}

func TestFTPAbortDuringUpload(t *testing.T) {
	cc, r, fsys, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())
	loginFTP(t, cc, r)

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR /partial.bin"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR reply %q", reply)
	}
	if _, err := dc.Write(bytes.Repeat([]byte("x"), 1000)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	// Telnet IP + Synch ahead of ABOR, as RFC 959 clients send it.
	if _, err := cc.Write([]byte("\xff\xf4\xff\xf2ABOR\r\n")); err != nil {
		t.Fatal(err)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "426") {
		t.Fatalf("aborted transfer reply %q, want 426", reply)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("ABOR reply %q, want 226", reply)
	}
	if reply := ftpCmd(t, cc, r, "NOOP"); !strings.HasPrefix(reply, "200") {
		t.Fatalf("control channel unusable after ABOR: %q", reply)
	}
	if info, err := fsys.Stat("/partial.bin"); err != nil || info.Size() != 1000 {
		t.Fatalf("partial upload: %v %v", info, err)
	}
}

func TestFTPAbortDuringDownload(t *testing.T) {
	cc, r, fsys, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())
	loginFTP(t, cc, r)
	f, err := fsys.Open("/big.bin", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 32<<20)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "RETR /big.bin"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR reply %q", reply)
	}
	// Read a little, then stall so the server blocks writing the data.
	if _, err := dc.Read(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	if reply := ftpCmd(t, cc, r, "ABOR"); !strings.HasPrefix(reply, "426") {
		t.Fatalf("aborted transfer reply %q, want 426", reply)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("ABOR reply %q, want 226", reply)
	}
	if reply := ftpCmd(t, cc, r, "ABOR"); !strings.HasPrefix(reply, "225") {
		t.Fatalf("idle ABOR reply %q, want 225", reply)
	}
}

// A command sent during a transfer is not swallowed by the ABOR watcher.
func TestFTPCommandPipelinedDuringTransfer(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())
	loginFTP(t, cc, r)
	dc := enterFTPPassive(t, cc, r)
	if reply := ftpCmd(t, cc, r, "STOR /p.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR reply %q", reply)
	}
	if _, err := cc.Write([]byte("NOOP\r\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	_, _ = dc.Write([]byte("hello"))
	_ = dc.(*net.TCPConn).CloseWrite()
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("transfer reply %q, want 226", reply)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "200") {
		t.Fatalf("pipelined NOOP reply %q, want 200", reply)
	}
}

func TestFTPStoreUnique(t *testing.T) {
	cc, r, fsys, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())
	loginFTP(t, cc, r)
	for i, want := range []string{"x.txt", "x.txt.1"} {
		dc := enterFTPPassive(t, cc, r)
		reply := ftpCmd(t, cc, r, "STOU x.txt")
		if reply != "150 FILE: "+want {
			t.Fatalf("STOU #%d reply %q, want FILE: %s", i, reply, want)
		}
		_, _ = dc.Write([]byte("data"))
		_ = dc.(*net.TCPConn).CloseWrite()
		if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
			t.Fatalf("STOU #%d completion %q", i, reply)
		}
		if _, err := fsys.Stat("/" + want); err != nil {
			t.Fatalf("STOU #%d file missing: %v", i, err)
		}
	}
}

func TestFTPMiscCommands(t *testing.T) {
	cc, r, fsys, _ := startFTPWithDataPlane(t, defaultDataPlanePerms())
	if lines := func() []string { _, _ = cc.Write([]byte("STAT\r\n")); return readFullReply(t, r) }(); lines[0] != "211-Kervan FTP server status:" || !strings.Contains(strings.Join(lines, "\n"), "Not logged in") {
		t.Fatalf("STAT before login: %q", lines)
	}
	loginFTP(t, cc, r)
	f, _ := fsys.Open("/doc.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	_ = f.Close()

	simple := map[string]string{
		"MODE S":                  "200",
		"MODE B":                  "504",
		"STRU F":                  "200",
		"STRU R":                  "504",
		"ALLO 1000":               "202",
		"XPWD":                    "257",
		"SITE FROB":               "504",
		"SITE CHMOD 644 /doc.txt": "550", // test user has no chmod permission
		"SITE CHMOD 9z9 /doc.txt": "501",
		// Timestamps are chmod-gated metadata, like SITE CHMOD.
		"MFMT 20200102030405 /doc.txt": "550",
		"MFMT yesterday /doc.txt":      "501",
		"MFMT 20200102030405 /nope":    "550",
	}
	for cmd, want := range simple {
		if reply := ftpCmd(t, cc, r, cmd); !strings.HasPrefix(reply, want) {
			t.Errorf("%s → %q, want %s", cmd, reply, want)
		}
	}
	// The refused MFMT must not have touched the file either (the test user
	// has no chmod permission).
	if info, err := fsys.Stat("/doc.txt"); err != nil {
		t.Fatalf("stat after refused MFMT: %v", err)
	} else if info.ModTime().Equal(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("refused MFMT still set mtime: %v", info.ModTime())
	}

	_, _ = cc.Write([]byte("HELP\r\n"))
	if lines := readFullReply(t, r); !strings.HasPrefix(lines[0], "214-") || !strings.Contains(strings.Join(lines, " "), "STOU") {
		t.Fatalf("HELP: %q", lines)
	}
	_, _ = cc.Write([]byte("STAT /\r\n"))
	if lines := readFullReply(t, r); lines[0] != "213-Status of /:" || !strings.Contains(strings.Join(lines, "\n"), "doc.txt") {
		t.Fatalf("STAT /: %q", lines)
	}
	_, _ = cc.Write([]byte("FEAT\r\n"))
	if lines := readFullReply(t, r); !strings.Contains(strings.Join(lines, "\n"), " MFMT") {
		t.Fatalf("FEAT lacks MFMT: %q", lines)
	}
}

// Data connections must not send TLS 1.3 session tickets (lftp uploads fail
// otherwise); the control connection's config is left untouched.
func TestDataTLSConfigDisablesSessionTickets(t *testing.T) {
	base := selfSignedTLSConfig(t)
	data := dataTLSConfig(base)
	if !data.SessionTicketsDisabled || base.SessionTicketsDisabled {
		t.Fatalf("data=%v base=%v", data.SessionTicketsDisabled, base.SessionTicketsDisabled)
	}
	if len(data.Certificates) != len(base.Certificates) {
		t.Fatal("certificates not carried over")
	}
	if dataTLSConfig(nil) != nil {
		t.Fatal("nil base must stay nil")
	}
}

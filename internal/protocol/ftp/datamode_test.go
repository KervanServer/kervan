package ftp

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParsePORTAndEPRT(t *testing.T) {
	if got, err := parsePORTArg("127,0,0,1,4,1"); err != nil || got != "127.0.0.1:1025" {
		t.Fatalf("PORT: %q %v", got, err)
	}
	for _, bad := range []string{"", "1,2,3", "256,0,0,1,4,1", "a,b,c,d,e,f"} {
		if _, err := parsePORTArg(bad); err == nil {
			t.Errorf("PORT %q accepted", bad)
		}
	}
	if got, err := parseEPRTArg("|1|127.0.0.1|2000|"); err != nil || got != "127.0.0.1:2000" {
		t.Fatalf("EPRT v4: %q %v", got, err)
	}
	if got, err := parseEPRTArg("|2|::1|2000|"); err != nil || got != "[::1]:2000" {
		t.Fatalf("EPRT v6: %q %v", got, err)
	}
	for _, bad := range []string{"|3|1.2.3.4|20|", "|1|::1|2000|", "|1|1.2.3.4|0|", "|1|1.2.3.4|", "x"} {
		if _, err := parseEPRTArg(bad); err == nil {
			t.Errorf("EPRT %q accepted", bad)
		}
	}
}

func TestValidateActiveTargetBlocksBounce(t *testing.T) {
	if r := validateActiveTarget("127.0.0.1:2000", "127.0.0.1:5555"); r != "" {
		t.Fatalf("own address rejected: %s", r)
	}
	if r := validateActiveTarget("10.9.9.9:2000", "127.0.0.1:5555"); r == "" {
		t.Fatal("third-party target accepted (FTP bounce)")
	}
	if r := validateActiveTarget("127.0.0.1:25", "127.0.0.1:5555"); r == "" {
		t.Fatal("privileged port accepted")
	}
}

func TestFTPEPSVAndActiveModeTransfers(t *testing.T) {
	cc, r, _, _ := startFTPWithDataPlane(t)
	ftpCmd(t, cc, r, "USER alice")
	if reply := ftpCmd(t, cc, r, "PASS pw12345"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("login: %q", reply)
	}
	ftpCmd(t, cc, r, "TYPE I")

	// Upload over EPSV.
	reply := ftpCmd(t, cc, r, "EPSV")
	if !strings.HasPrefix(reply, "229") {
		t.Fatalf("EPSV reply %q", reply)
	}
	portStr := reply[strings.Index(reply, "|||")+3 : strings.LastIndex(reply, "|")]
	dc, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", portStr), 5*time.Second)
	if err != nil {
		t.Fatalf("dial EPSV port: %v", err)
	}
	if reply := ftpCmd(t, cc, r, "STOR /epsv.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("STOR: %q", reply)
	}
	payload := "uploaded over EPSV\n"
	_, _ = dc.Write([]byte(payload))
	_ = dc.Close()
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("STOR completion: %q", reply)
	}

	// Download over active mode (PORT): the client listens, the server dials.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- "accept error: " + err.Error()
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		b, _ := io.ReadAll(c)
		got <- string(b)
	}()
	portArg := "127,0,0,1," + strconv.Itoa(port/256) + "," + strconv.Itoa(port%256)
	if reply := ftpCmd(t, cc, r, "PORT "+portArg); !strings.HasPrefix(reply, "200") {
		t.Fatalf("PORT: %q", reply)
	}
	if reply := ftpCmd(t, cc, r, "RETR /epsv.txt"); !strings.HasPrefix(reply, "150") {
		t.Fatalf("RETR: %q", reply)
	}
	if body := <-got; body != payload {
		t.Fatalf("active RETR body %q, want %q", body, payload)
	}
	if reply := readFTPReply(t, r); !strings.HasPrefix(reply, "226") {
		t.Fatalf("RETR completion: %q", reply)
	}

	// EPRT to a third-party address is refused.
	if reply := ftpCmd(t, cc, r, "EPRT |1|192.0.2.10|2000|"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("bounce EPRT reply %q, want 504", reply)
	}
}

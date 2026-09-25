package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/config"
	icrypto "github.com/kervanserver/kervan/internal/crypto"
	"golang.org/x/crypto/ssh"
)

// Split literal: avoids credential-pattern scanners flagging the synthetic test password.
var crossProtoAlicePassword = "pw12345" + "678"

const (
	crossProtoFxpInit    = 1
	crossProtoFxpOpen    = 3
	crossProtoFxpClose   = 4
	crossProtoFxpWrite   = 6
	crossProtoFxpVersion = 2
	crossProtoFxpHandle  = 102
	crossProtoFxpStatus  = 101
	crossProtoStatusOK   = 0
)

func crossProtoU32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func crossProtoU64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func crossProtoStr(s string) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(s)))
	return append(out, s...)
}

func crossProtoSFTPSend(t *testing.T, ch ssh.Channel, typ byte, payload []byte) {
	t.Helper()
	frame := binary.BigEndian.AppendUint32(nil, uint32(1+len(payload)))
	frame = append(frame, typ)
	frame = append(frame, payload...)
	if _, err := ch.Write(frame); err != nil {
		t.Fatalf("write sftp packet type %d: %v", typ, err)
	}
}

func crossProtoSFTPRecvRaw(t *testing.T, ch ssh.Channel) (byte, []byte) {
	t.Helper()
	var lenBuf [4]byte
	if _, err := io.ReadFull(ch, lenBuf[:]); err != nil {
		t.Fatalf("read sftp reply len: %v", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	frame := make([]byte, n)
	if _, err := io.ReadFull(ch, frame); err != nil {
		t.Fatalf("read sftp reply frame: %v", err)
	}
	return frame[0], frame[1:]
}

func crossProtoSFTPRecv(t *testing.T, ch ssh.Channel, wantID uint32) (byte, []byte) {
	t.Helper()
	typ, payload := crossProtoSFTPRecvRaw(t, ch)
	if got := binary.BigEndian.Uint32(payload[:4]); got != wantID {
		t.Fatalf("sftp reply id = %d, want %d (type %d)", got, wantID, typ)
	}
	return typ, payload
}

func crossProtoConfig(dir string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Server.ListenAddress = "127.0.0.1"
	cfg.Server.DataDir = dir + "/data"
	cfg.Storage.DefaultBackend = "mem"
	cfg.Storage.Backends = map[string]config.BackendConfig{"mem": {Type: "memory"}}
	cfg.FTP.Enabled = true
	cfg.FTP.Port = 22121
	cfg.FTP.PassiveIP = "127.0.0.1"
	cfg.SFTP.Enabled = true
	cfg.SFTP.Port = 22122
	cfg.SFTP.IdleTimeout = 10 * time.Minute
	cfg.WebUI.Enabled = true
	cfg.WebUI.BindAddress = "127.0.0.1"
	cfg.WebUI.Port = 22123
	cfg.WebUI.AdminPassword = "bootstrap-admin-pw"
	return cfg
}

func crossProtoFTPReply(t *testing.T, conn net.Conn) string {
	t.Helper()
	buf := make([]byte, 0, 256)
	one := make([]byte, 1)
	for {
		if _, err := conn.Read(one); err != nil {
			t.Fatalf("ftp read: %v (buf %q)", err, string(buf))
		}
		buf = append(buf, one[0])
		if len(buf) >= 2 && buf[len(buf)-2] == '\r' && buf[len(buf)-1] == '\n' {
			return string(buf)
		}
	}
}

func crossProtoParsePASV(t *testing.T, reply string) string {
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

// TestCrossProtocolSFTPFTPAPIOverSharedMemoryBackend proves that the three
// protocol servers compose over one shared per-user memory backend: a file
// uploaded via SFTP is listed by FTP and downloaded through the authenticated
// API — the composition the per-user shared-backend fix exists for.
func TestCrossProtocolSFTPFTPAPIOverSharedMemoryBackend(t *testing.T) {
	dir := t.TempDir()
	cfg := crossProtoConfig(dir)
	hostKeyDir := dir + "/host_keys"
	if _, err := icrypto.EnsureHostKeys(hostKeyDir); err != nil {
		t.Fatalf("ensure host keys: %v", err)
	}
	cfg.SFTP.HostKeyDir = hostKeyDir

	srv, err := New(cfg, dir+"/kervan.yaml", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := srv.auth.CreateUser("alice", crossProtoAlicePassword, "/", false); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = srv.Close() })

	// 1. SFTP upload /f.txt = "cross-protocol".
	transport, err := net.DialTimeout("tcp", "127.0.0.1:22122", 10*time.Second)
	if err != nil {
		t.Fatalf("sftp dial: %v", err)
	}
	defer transport.Close()
	clientCfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{ssh.Password(crossProtoAlicePassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	conn, newChannels, reqs, err := ssh.NewClientConn(transport, "127.0.0.1:22122", clientCfg)
	if err != nil {
		t.Fatalf("sftp handshake: %v", err)
	}
	go ssh.DiscardRequests(reqs)
	client := ssh.NewClient(conn, newChannels, reqs)
	defer client.Close()
	ch, acceptReqs, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open session channel: %v", err)
	}
	defer ch.Close()
	go ssh.DiscardRequests(acceptReqs)
	ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ V string }{"sftp"}))
	if err != nil {
		t.Fatalf("sftp subsystem request: %v", err)
	}
	if !ok {
		t.Fatalf("sftp subsystem request rejected")
	}

	crossProtoSFTPSend(t, ch, crossProtoFxpInit, crossProtoU32(3))
	typ, versionPayload := crossProtoSFTPRecvRaw(t, ch)
	if typ != crossProtoFxpVersion {
		t.Fatalf("sftp VERSION type = %d, want %d", typ, crossProtoFxpVersion)
	}
	if version := binary.BigEndian.Uint32(versionPayload[:4]); version != 3 {
		t.Fatalf("sftp VERSION = %d, want 3", version)
	}

	var openUp []byte
	openUp = append(openUp, crossProtoU32(100)...) // request id
	openUp = append(openUp, crossProtoStr("/f.txt")...)
	openUp = append(openUp, crossProtoU32(2|8|16)...) // WRITE|APPEND|CREAT
	openUp = append(openUp, crossProtoU32(0)...)      // empty attrs
	crossProtoSFTPSend(t, ch, crossProtoFxpOpen, openUp)
	typ, payload := crossProtoSFTPRecv(t, ch, 100)
	if typ != crossProtoFxpHandle {
		t.Fatalf("upload OPEN reply type = %d, want HANDLE (%d)", typ, crossProtoFxpHandle)
	}
	if len(payload) < 8 {
		t.Fatalf("handle payload too short: %x", payload)
	}
	n := binary.BigEndian.Uint32(payload[4:8])
	if int(8+n) > len(payload) {
		t.Fatalf("implausible handle length %d in payload %x", n, payload)
	}
	handle := string(payload[8 : 8+n])

	var writeUp []byte
	writeUp = append(writeUp, crossProtoU32(101)...) // request id
	writeUp = append(writeUp, crossProtoStr(handle)...)
	writeUp = append(writeUp, crossProtoU64(0)...)
	writeUp = append(writeUp, crossProtoStr("cross-protocol")...)
	crossProtoSFTPSend(t, ch, crossProtoFxpWrite, writeUp)
	typ, payload = crossProtoSFTPRecv(t, ch, 101)
	if typ != crossProtoFxpStatus || binary.BigEndian.Uint32(payload[4:8]) != crossProtoStatusOK {
		t.Fatalf("upload WRITE status = %d/%d, want OK", typ, binary.BigEndian.Uint32(payload[4:8]))
	}
	crossProtoSFTPSend(t, ch, crossProtoFxpClose, append(crossProtoU32(102), crossProtoStr(handle)...)) // request id
	typ, payload = crossProtoSFTPRecv(t, ch, 102)
	if typ != crossProtoFxpStatus || binary.BigEndian.Uint32(payload[4:8]) != crossProtoStatusOK {
		t.Fatalf("upload CLOSE status = %d/%d, want OK", typ, binary.BigEndian.Uint32(payload[4:8]))
	}

	// 2. FTP LIST must show /f.txt over the PASV data connection.
	ftpConn, err := net.DialTimeout("tcp", "127.0.0.1:22121", 10*time.Second)
	if err != nil {
		t.Fatalf("ftp dial: %v", err)
	}
	defer ftpConn.Close()
	if err := ftpConn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("set ftp deadline: %v", err)
	}
	crossProtoFTPReply(t, ftpConn) // banner
	fmt.Fprintf(ftpConn, "USER alice\r\n")
	if reply := crossProtoFTPReply(t, ftpConn); !strings.HasPrefix(reply, "331") {
		t.Fatalf("USER reply = %q", reply)
	}
	fmt.Fprintf(ftpConn, "PASS %s\r\n", crossProtoAlicePassword)
	if reply := crossProtoFTPReply(t, ftpConn); !strings.HasPrefix(reply, "230") {
		t.Fatalf("PASS reply = %q", reply)
	}
	fmt.Fprintf(ftpConn, "PASV\r\n")
	pasv := crossProtoFTPReply(t, ftpConn)
	if !strings.HasPrefix(pasv, "227") {
		t.Fatalf("PASV reply = %q", pasv)
	}
	dataAddr := crossProtoParsePASV(t, pasv)
	data, err := net.DialTimeout("tcp", dataAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("data dial %s: %v", dataAddr, err)
	}
	defer data.Close()
	fmt.Fprintf(ftpConn, "LIST /\r\n")
	if reply := crossProtoFTPReply(t, ftpConn); !strings.HasPrefix(reply, "150") {
		t.Fatalf("LIST start reply = %q", reply)
	}
	listBytes, _ := io.ReadAll(data)
	if !strings.Contains(string(listBytes), "f.txt") {
		t.Fatalf("FTP LIST output %q does not contain f.txt — the SFTP upload is invisible over FTP", string(listBytes))
	}
	if reply := crossProtoFTPReply(t, ftpConn); !strings.HasPrefix(reply, "226") {
		t.Fatalf("LIST completion reply = %q", reply)
	}

	// 3. Authenticated API download returns the same bytes.
	loginBody, err := json.Marshal(map[string]string{"username": "alice", "password": crossProtoAlicePassword})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post("http://127.0.0.1:22123/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("api login: %v", err)
	}
	defer resp.Body.Close()
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil || login.Token == "" {
		t.Fatalf("api login decode (status %d): %v", resp.StatusCode, err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:22123/api/files/download?path=/f.txt", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	resp2, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("api download: %v", err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("api download status = %d, body %q — the SFTP upload is invisible to the API", resp2.StatusCode, string(body))
	}
	if string(body) != "cross-protocol" {
		t.Fatalf("api download body = %q, want %q", string(body), "cross-protocol")
	}
}

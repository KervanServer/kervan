package api

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/events"
	"github.com/kervanserver/kervan/internal/session"
)

// readServerFrame reads one unmasked server-to-client frame.
func readServerFrame(t *testing.T, r *bufio.Reader) []byte {
	t.Helper()
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	n := int64(head[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		_, _ = io.ReadFull(r, ext[:])
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		_, _ = io.ReadFull(r, ext[:])
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return payload
}

func TestWebSocketPushesOnSessionChange(t *testing.T) {
	srv, _ := newAuthTestServer(t, false)
	srv.sessions = session.NewManager()
	srv.events = events.NewBroker()
	srv.sessions.SetOnChange(srv.events.Publisher(events.TopicSessions))
	token, _ := signToken(srv.secret, "alice", time.Hour)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleWebSocket))
	defer ts.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("GET /api/v1/ws?types=sessions HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nAuthorization: Bearer " + token + "\r\n\r\n"))
	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var first map[string]any
	_ = json.Unmarshal(readServerFrame(t, r), &first)
	if got := first["sessions"].([]any); len(got) != 0 {
		t.Fatalf("initial sessions = %v", got)
	}

	// A new session must be pushed well before the 15s heartbeat.
	started := time.Now()
	srv.sessions.Start("alice", "sftp", "10.0.0.1:2222")
	srv.sessions.Start("alice", "ftp", "10.0.0.1:2121") // burst: one frame
	var next map[string]any
	_ = json.Unmarshal(readServerFrame(t, r), &next)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("push took %v; expected an event-driven update", elapsed)
	}
	if got := next["sessions"].([]any); len(got) != 2 {
		t.Fatalf("pushed sessions = %d, want 2 (burst coalesced into one frame)", len(got))
	}

	// Topics the client did not ask for do not wake it.
	srv.events.Publish(events.TopicAudit)
	_ = conn.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
	if _, err := r.ReadByte(); err == nil {
		t.Fatal("unrequested topic produced a frame")
	}
}

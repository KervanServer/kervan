package sftp

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/storage/memory"
)

func TestParseExecPayload(t *testing.T) {
	raw := append([]byte{0, 0, 0, 11}, []byte("scp -t /tmp")...)
	cmd, err := parseExecPayload(raw)
	if err != nil {
		t.Fatalf("parseExecPayload error: %v", err)
	}
	if cmd != "scp -t /tmp" {
		t.Fatalf("unexpected command: %q", cmd)
	}
}

func TestParseSCPExec(t *testing.T) {
	mode, target, err := parseSCPExec("scp -t /upload")
	if err != nil {
		t.Fatalf("parseSCPExec error: %v", err)
	}
	if mode != scpModeSink || target != "/upload" {
		t.Fatalf("unexpected parse result: mode=%q target=%q", mode, target)
	}

	mode, target, err = parseSCPExec("scp -f /download/file.txt")
	if err != nil {
		t.Fatalf("parseSCPExec error: %v", err)
	}
	if mode != scpModeSource || target != "/download/file.txt" {
		t.Fatalf("unexpected parse result: mode=%q target=%q", mode, target)
	}
}

func TestParseSCPFileHeader(t *testing.T) {
	mode, size, name, err := parseSCPFileHeader("C0644 12 file.txt")
	if err != nil {
		t.Fatalf("parseSCPFileHeader error: %v", err)
	}
	if mode != 0o644 || size != 12 || name != "file.txt" {
		t.Fatalf("unexpected header parse: mode=%o size=%d name=%q", mode, size, name)
	}
}

func TestParseSCPFileHeaderRejectsPathTraversalNames(t *testing.T) {
	tests := []string{
		"C0644 12 ../file.txt",
		"C0644 12 nested/file.txt",
		`C0644 12 ..\\file.txt`,
		"C0644 12 .",
	}
	for _, raw := range tests {
		if _, _, _, err := parseSCPFileHeader(raw); err == nil {
			t.Fatalf("expected invalid file name for %q", raw)
		}
	}
}

func TestReadSCPAck(t *testing.T) {
	if err := readSCPAck(bufio.NewReader(bytes.NewReader([]byte{0}))); err != nil {
		t.Fatalf("expected ack success: %v", err)
	}
	if err := readSCPAck(bufio.NewReader(bytes.NewReader([]byte{1, 'o', 'k', '\n'}))); err == nil {
		t.Fatal("expected ack error")
	}
}

// pipeSCPChannel adapts a net.Pipe conn to ssh.Channel for the sink harness.
type pipeSCPChannel struct {
	net.Conn
}

func (c pipeSCPChannel) CloseWrite() error { return nil }

func (c pipeSCPChannel) SendRequest(name string, wantReply bool, payload []byte) (bool, error) {
	return false, nil
}

func (c pipeSCPChannel) Stderr() io.ReadWriter { return nil }

func TestSCPSinkUploadCompletes(t *testing.T) {
	fsys := memory.New()
	srv := NewServer(Config{}, nil, nil, nil, nil, nil, nil)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	go func() {
		_ = srv.runSCPSink(pipeSCPChannel{Conn: serverConn}, fsys, "/", "alice", "remote")
		_ = serverConn.Close()
	}()

	readAck := func() byte {
		t.Helper()
		clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var b [1]byte
		if _, err := io.ReadFull(clientConn, b[:]); err != nil {
			t.Fatalf("read scp ack: %v", err)
		}
		return b[0]
	}
	if readAck() != 0 {
		t.Fatalf("init ack != 0")
	}
	clientConn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := clientConn.Write([]byte("C0644 5 hello.txt\n")); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if readAck() != 0 {
		t.Fatalf("header ack != 0")
	}
	if _, err := clientConn.Write([]byte("world\x00")); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if readAck() != 0 {
		t.Fatalf("file ack != 0")
	}
	if _, err := clientConn.Write([]byte("E\n")); err != nil {
		t.Fatalf("write E: %v", err)
	}
	if readAck() != 0 {
		t.Fatalf("end ack != 0")
	}
	f, err := fsys.Open("/hello.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	defer f.Close()
	raw := make([]byte, 5)
	if _, err := io.ReadFull(f, raw); err != nil {
		t.Fatalf("read uploaded content: %v", err)
	}
	if string(raw) != "world" {
		t.Fatalf("uploaded content = %q, want %q", string(raw), "world")
	}
}

func TestSCPSinkOversizedHeaderClosesConnection(t *testing.T) {
	fsys := memory.New()
	srv := NewServer(Config{}, nil, nil, nil, nil, nil, nil)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	go func() {
		_ = srv.runSCPSink(pipeSCPChannel{Conn: serverConn}, fsys, "/", "alice", "remote")
		_ = serverConn.Close()
	}()

	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	if _, err := io.ReadFull(clientConn, b[:]); err != nil {
		t.Fatalf("read init ack: %v", err)
	}
	clientConn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, werr := clientConn.Write([]byte(strings.Repeat("A", 1024*1024) + "\n"))
	if werr != nil {
		return
	}
	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame [1]byte
	if _, rerr := io.ReadFull(clientConn, frame[:]); rerr == nil {
		t.Fatalf("sink stayed alive after a 1MiB header line — the SCP line length cap is not enforced, so an authenticated scp client can grow the buffer without bound toward process OOM")
	}
}

func TestReadSCPAckErrorMessageBounded(t *testing.T) {
	payload := append([]byte{1}, bytes.Repeat([]byte("A"), 1024*1024)...)
	err := readSCPAck(bufio.NewReader(bytes.NewReader(payload)))
	if err == nil {
		t.Fatal("expected an ack error")
	}
	if len(err.Error()) > 70000 {
		t.Fatalf("ack error message is %d bytes — the SCP line length cap is not enforced, so an authenticated scp client can make the server materialize unbounded error strings", len(err.Error()))
	}
}

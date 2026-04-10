package sftp

import (
	"bufio"
	"bytes"
	"testing"
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

func TestReadSCPAck(t *testing.T) {
	if err := readSCPAck(bufio.NewReader(bytes.NewReader([]byte{0}))); err != nil {
		t.Fatalf("expected ack success: %v", err)
	}
	if err := readSCPAck(bufio.NewReader(bytes.NewReader([]byte{1, 'o', 'k', '\n'}))); err == nil {
		t.Fatal("expected ack error")
	}
}

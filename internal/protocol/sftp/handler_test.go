package sftp

import (
	"bytes"
	"io"
	"math"
	"os"
	"testing"
)

func TestMapOpenFlags(t *testing.T) {
	if got := mapOpenFlags(sshFxRead); got != os.O_RDONLY {
		t.Fatalf("read flag mismatch: %d", got)
	}
	if got := mapOpenFlags(sshFxWrite | sshFxCreat | sshFxTrunc); got != (os.O_WRONLY | os.O_CREATE | os.O_TRUNC) {
		t.Fatalf("write flag mismatch: %d", got)
	}
	if got := mapOpenFlags(sshFxRead | sshFxWrite); got != os.O_RDWR {
		t.Fatalf("rdwr flag mismatch: %d", got)
	}
}

func TestPacketRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello")
	if err := writePacket(&buf, fxpData, payload); err != nil {
		t.Fatalf("write packet: %v", err)
	}
	packetType, got, err := readPacket(&buf)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	if packetType != fxpData {
		t.Fatalf("packet type mismatch: %d", packetType)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q", got)
	}
}

func TestReadPacketEOF(t *testing.T) {
	_, _, err := readPacket(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestSFTPFileOffsetBounds(t *testing.T) {
	offset, err := sftpFileOffset(math.MaxInt64)
	if err != nil || offset != math.MaxInt64 {
		t.Fatalf("expected max int64 offset to pass, got offset=%d err=%v", offset, err)
	}

	if _, err := sftpFileOffset(math.MaxInt64 + 1); err == nil || err.Error() != "offset too large" {
		t.Fatalf("expected oversized offset error, got %v", err)
	}
}

func TestValidateReadLengthBounds(t *testing.T) {
	if err := validateReadLength(maxPacketSize); err != nil {
		t.Fatalf("expected max packet size to pass, got %v", err)
	}
	if err := validateReadLength(maxPacketSize + 1); err == nil || err.Error() != "read length too large" {
		t.Fatalf("expected oversized read length error, got %v", err)
	}
}

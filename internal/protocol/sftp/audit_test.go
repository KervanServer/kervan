package sftp

import (
	"context"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/storage/memory"
)

type discardChannel struct{ ssh.Channel }

func (discardChannel) Write(p []byte) (int, error) { return len(p), nil }

type recordSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *recordSink) Write(_ context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *recordSink) Close() error { return nil }

func openPacket(id uint32, path string, flags uint32) []byte {
	var w packetWriter
	w.uint32(id)
	w.string(path)
	w.uint32(flags)
	w.uint32(0) // no attrs
	return w.buf
}

func handlePacket(id uint32, handle string) []byte {
	var w packetWriter
	w.uint32(id)
	w.string(handle)
	return w.buf
}

func writePacketPayload(id uint32, handle string, offset uint64, data []byte) []byte {
	var w packetWriter
	w.uint32(id)
	w.string(handle)
	w.uint64(offset)
	w.bytes(data)
	return w.buf
}

func onlyHandle(t *testing.T, h *sftpHandler) string {
	t.Helper()
	if len(h.handles) != 1 {
		t.Fatalf("expected one open handle, got %d", len(h.handles))
	}
	for k := range h.handles {
		return k
	}
	return ""
}

// An upload is audited once, on close, with its total size — not once per
// WRITE packet.
func TestSFTPUploadAuditedOncePerFile(t *testing.T) {
	sink := &recordSink{}
	engine := audit.NewEngine(nil, sink)
	h := &sftpHandler{
		ch:       discardChannel{},
		fsys:     memory.New(),
		audit:    engine,
		username: "alice",
		remoteIP: "192.0.2.7",
		handles:  map[string]any{},
	}

	if err := h.handleOpen(openPacket(1, "/a.bin", sshFxWrite|sshFxCreat|sshFxTrunc)); err != nil {
		t.Fatal(err)
	}
	handle := onlyHandle(t, h)
	chunk := make([]byte, 1000)
	for i := 0; i < 5; i++ {
		if err := h.handleWrite(writePacketPayload(uint32(2+i), handle, uint64(i*len(chunk)), chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.handleClose(handlePacket(10, handle)); err != nil {
		t.Fatal(err)
	}

	// A second upload is left open when the connection drops.
	if err := h.handleOpen(openPacket(11, "/b.bin", sshFxWrite|sshFxCreat)); err != nil {
		t.Fatal(err)
	}
	if err := h.handleWrite(writePacketPayload(12, onlyHandle(t, h), 0, chunk[:300])); err != nil {
		t.Fatal(err)
	}
	h.closeAllHandles()
	engine.Close()

	if len(sink.events) != 2 {
		t.Fatalf("want 2 audit events, got %d: %+v", len(sink.events), sink.events)
	}
	first, second := sink.events[0], sink.events[1]
	if first.Type != audit.EventFileWrite || first.Path != "/a.bin" || first.Status != "ok" || first.Meta["bytes"] != "5000" || first.Username != "alice" || first.IP != "192.0.2.7" {
		t.Fatalf("unexpected upload event %+v", first)
	}
	if second.Type != audit.EventFileWrite || second.Path != "/b.bin" || second.Status != "incomplete" || second.Meta["bytes"] != "300" {
		t.Fatalf("unexpected interrupted upload event %+v", second)
	}
}

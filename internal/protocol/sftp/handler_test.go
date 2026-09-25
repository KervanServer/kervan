package sftp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/vfs"
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

// --- helpers for wire-format regression tests (SFTP ATTRS structures) ---

type attrsTestFsys struct {
	entries    []fs.DirEntry
	statResult os.FileInfo
}

func (f *attrsTestFsys) Open(string, int, os.FileMode) (vfs.File, error) {
	return nil, errors.New("attrsTestFsys: open not supported")
}

func (f *attrsTestFsys) Stat(string) (os.FileInfo, error) {
	if f.statResult != nil {
		return f.statResult, nil
	}
	return nil, os.ErrNotExist
}

func (f *attrsTestFsys) Lstat(string) (os.FileInfo, error)  { return nil, os.ErrNotExist }
func (f *attrsTestFsys) Rename(string, string) error        { return errors.New("attrsTestFsys: rename") }
func (f *attrsTestFsys) Remove(string) error                { return errors.New("attrsTestFsys: remove") }
func (f *attrsTestFsys) RemoveAll(string) error             { return errors.New("attrsTestFsys: removeall") }
func (f *attrsTestFsys) Mkdir(string, os.FileMode) error    { return errors.New("attrsTestFsys: mkdir") }
func (f *attrsTestFsys) MkdirAll(string, os.FileMode) error { return errors.New("attrsTestFsys: mkdirall") }

func (f *attrsTestFsys) ReadDir(string) ([]fs.DirEntry, error) { return f.entries, nil }

func (f *attrsTestFsys) Symlink(string, string) error    { return errors.New("attrsTestFsys: symlink") }
func (f *attrsTestFsys) Readlink(string) (string, error) { return "", errors.New("attrsTestFsys: readlink") }
func (f *attrsTestFsys) Chmod(string, os.FileMode) error { return errors.New("attrsTestFsys: chmod") }
func (f *attrsTestFsys) Chown(string, int, int) error    { return errors.New("attrsTestFsys: chown") }

func (f *attrsTestFsys) Chtimes(string, time.Time, time.Time) error {
	return errors.New("attrsTestFsys: chtimes")
}

func (f *attrsTestFsys) Statvfs(string) (*vfs.StatVFS, error) { return &vfs.StatVFS{}, nil }

type attrsStubInfo struct{ name string }

func (i attrsStubInfo) Name() string       { return i.name }
func (i attrsStubInfo) Size() int64        { return 3 }
func (i attrsStubInfo) Mode() os.FileMode  { return 0o644 }
func (i attrsStubInfo) ModTime() time.Time { return time.Unix(1700000000, 0).UTC() }
func (i attrsStubInfo) IsDir() bool        { return false }
func (i attrsStubInfo) Sys() any           { return nil }

type attrsGoodEntry struct{ name string }

func (e attrsGoodEntry) Name() string               { return e.name }
func (e attrsGoodEntry) IsDir() bool                { return false }
func (e attrsGoodEntry) Type() fs.FileMode          { return 0 }
func (e attrsGoodEntry) Info() (os.FileInfo, error) { return attrsStubInfo{name: e.name}, nil }

// attrsBadEntry models an entry whose stat fails after the directory scan,
// e.g. a file removed between ReadDir and the per-entry Info() call.
type attrsBadEntry struct{ name string }

func (e attrsBadEntry) Name() string               { return e.name }
func (e attrsBadEntry) IsDir() bool                { return false }
func (e attrsBadEntry) Type() fs.FileMode          { return 0 }
func (e attrsBadEntry) Info() (os.FileInfo, error) { return nil, errors.New("lstat failed") }

type attrsTestChannel struct {
	buf    bytes.Buffer
	stderr bytes.Buffer
}

func (c *attrsTestChannel) Read(p []byte) (int, error)  { return 0, io.EOF }
func (c *attrsTestChannel) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *attrsTestChannel) Close() error                { return nil }
func (c *attrsTestChannel) CloseWrite() error           { return nil }

func (c *attrsTestChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}

func (c *attrsTestChannel) ExtendedData(uint32, []byte) error { return nil }
func (c *attrsTestChannel) Stderr() io.ReadWriter             { return &c.stderr }

type attrsPacket []byte

func appendAttrsPacketU32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendAttrsPacketString(b []byte, s string) []byte {
	b = appendAttrsPacketU32(b, uint32(len(s)))
	return append(b, s...)
}

type attrsPacketParser struct {
	buf []byte
	pos int
}

func (p *attrsPacketParser) u32() (uint32, error) {
	if p.pos+4 > len(p.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint32(p.buf[p.pos : p.pos+4])
	p.pos += 4
	return v, nil
}

func (p *attrsPacketParser) str() (string, error) {
	n, err := p.u32()
	if err != nil {
		return "", err
	}
	if p.pos+int(n) > len(p.buf) {
		return "", io.ErrUnexpectedEOF
	}
	out := string(p.buf[p.pos : p.pos+int(n)])
	p.pos += int(n)
	return out, nil
}

// attrsFlags reads the leading uint32 of an ATTRS structure, which per
// draft-ietf-secsh-filexfer-02 §5 begins directly with the flags field.
func (p *attrsPacketParser) attrsFlags() (uint32, error) { return p.u32() }

func (p *attrsPacketParser) skipAttrBody() error {
	// ATTRS body after the flags word for flags 0xD: size(8) + perms(4) + mtime(4) + atime(4).
	const postFlagsAttrsLen = 8 + 4 + 4 + 4
	if p.pos+postFlagsAttrsLen > len(p.buf) {
		return io.ErrUnexpectedEOF
	}
	p.pos += postFlagsAttrsLen
	return nil
}

// Regression: READDIR NAME packets must encode count triplets of
// (name, longname, raw ATTRS). ATTRS is a wire structure without a length
// prefix; encoding it through the length-prefixed bytes() helper made every
// compliant SFTP client misparse directory listings.
func TestHandleReadDirEmitsRawAttrsStructures(t *testing.T) {
	ch := &attrsTestChannel{}
	h := &sftpHandler{
		ch:      ch,
		fsys:    &attrsTestFsys{entries: []fs.DirEntry{attrsGoodEntry{"a.txt"}, attrsGoodEntry{"b.txt"}, attrsGoodEntry{"c.txt"}}},
		handles: map[string]any{},
	}
	var opendir attrsPacket
	opendir = appendAttrsPacketU32(opendir, 0x2A)
	opendir = appendAttrsPacketString(opendir, "/dir")
	if err := h.handleOpenDir(opendir); err != nil {
		t.Fatalf("handleOpenDir: %v", err)
	}
	packetType, payload, err := readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read HANDLE reply: %v", err)
	}
	if packetType != fxpHandle {
		t.Fatalf("expected fxpHandle reply, got type %d", packetType)
	}
	handleReader := &attrsPacketParser{buf: payload}
	if _, err := handleReader.u32(); err != nil {
		t.Fatalf("HANDLE reply id: %v", err)
	}
	handle, err := handleReader.str()
	if err != nil {
		t.Fatalf("HANDLE reply handle: %v", err)
	}

	ch.buf.Reset()
	var readdir attrsPacket
	readdir = appendAttrsPacketU32(readdir, 0x2B)
	readdir = appendAttrsPacketString(readdir, handle)
	if err := h.handleReadDir(readdir); err != nil {
		t.Fatalf("handleReadDir: %v", err)
	}
	packetType, payload, err = readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read NAME reply: %v", err)
	}
	if packetType != fxpName {
		t.Fatalf("expected fxpName reply, got type %d", packetType)
	}
	reader := &attrsPacketParser{buf: payload}
	if _, err := reader.u32(); err != nil {
		t.Fatalf("NAME reply id: %v", err)
	}
	count, err := reader.u32()
	if err != nil {
		t.Fatalf("NAME reply count: %v", err)
	}
	if count != 3 {
		t.Fatalf("NAME count = %d, want 3", count)
	}
	for i := 0; i < 3; i++ {
		name, err := reader.str()
		if err != nil {
			t.Fatalf("entry %d name truncated: %v", i+1, err)
		}
		if _, err := reader.str(); err != nil { // longname
			t.Fatalf("entry %d (%q) longname truncated: %v", i+1, name, err)
		}
		flags, err := reader.attrsFlags()
		if err != nil {
			t.Fatalf("entry %d (%q) attrs truncated: %v", i+1, name, err)
		}
		if flags != 0x0D {
			t.Fatalf("entry %d (%q) attrs start with %d (0x%x), want flags 0xD: ATTRS must be a raw structure without a length prefix", i+1, name, flags, flags)
		}
		if err := reader.skipAttrBody(); err != nil {
			t.Fatalf("entry %d (%q) attrs body truncated: %v", i+1, name, err)
		}
	}
	if reader.pos != len(reader.buf) {
		t.Fatalf("%d trailing bytes after the last entry: NAME packet is not spec-shaped", len(reader.buf)-reader.pos)
	}
}

// Regression: STAT replies must be fxpAttrs = id + raw ATTRS structure whose
// first field is the flags word, with no length prefix.
func TestHandleStatEmitsRawAttrsStructure(t *testing.T) {
	ch := &attrsTestChannel{}
	h := &sftpHandler{
		ch:      ch,
		fsys:    &attrsTestFsys{statResult: attrsStubInfo{name: "target.txt"}},
		handles: map[string]any{},
	}
	var stat attrsPacket
	stat = appendAttrsPacketU32(stat, 0x2C)
	stat = appendAttrsPacketString(stat, "/target.txt")
	if err := h.handleStat(stat, false); err != nil {
		t.Fatalf("handleStat: %v", err)
	}
	packetType, payload, err := readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read ATTRS reply: %v", err)
	}
	if packetType != fxpAttrs {
		t.Fatalf("expected fxpAttrs reply, got type %d", packetType)
	}
	reader := &attrsPacketParser{buf: payload}
	if _, err := reader.u32(); err != nil {
		t.Fatalf("ATTRS reply id: %v", err)
	}
	flags, err := reader.attrsFlags()
	if err != nil {
		t.Fatalf("ATTRS structure truncated: %v", err)
	}
	if flags != 0x0D {
		t.Fatalf("fxpAttrs starts with %d (0x%x), want flags 0xD: ATTRS must be a raw structure without a length prefix", flags, flags)
	}
	if err := reader.skipAttrBody(); err != nil {
		t.Fatalf("ATTRS body truncated: %v", err)
	}
	if reader.pos != len(reader.buf) {
		t.Fatalf("%d trailing bytes after the ATTRS structure: packet is not spec-shaped", len(reader.buf)-reader.pos)
	}
}

// Regression: READDIR NAME packets must stay self-consistent when an entry's
// stat fails after the directory scan — the declared count must equal the
// encoded entries (unstatable entries are skipped and never re-offered), or
// compliant clients hit a truncated packet and the listing breaks.
func TestHandleReadDirSkipsUnstatableEntries(t *testing.T) {
	ch := &attrsTestChannel{}
	h := &sftpHandler{
		ch:      ch,
		fsys:    &attrsTestFsys{entries: []fs.DirEntry{attrsGoodEntry{"a.txt"}, attrsBadEntry{"cursed.txt"}, attrsGoodEntry{"b.txt"}}},
		handles: map[string]any{},
	}
	var opendir attrsPacket
	opendir = appendAttrsPacketU32(opendir, 0x3A)
	opendir = appendAttrsPacketString(opendir, "/dir")
	if err := h.handleOpenDir(opendir); err != nil {
		t.Fatalf("handleOpenDir: %v", err)
	}
	packetType, payload, err := readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read HANDLE reply: %v", err)
	}
	if packetType != fxpHandle {
		t.Fatalf("expected fxpHandle reply, got type %d", packetType)
	}
	handleReader := &attrsPacketParser{buf: payload}
	if _, err := handleReader.u32(); err != nil {
		t.Fatalf("HANDLE reply id: %v", err)
	}
	handle, err := handleReader.str()
	if err != nil {
		t.Fatalf("HANDLE reply handle: %v", err)
	}

	ch.buf.Reset()
	var readdir attrsPacket
	readdir = appendAttrsPacketU32(readdir, 0x3B)
	readdir = appendAttrsPacketString(readdir, handle)
	if err := h.handleReadDir(readdir); err != nil {
		t.Fatalf("handleReadDir: %v", err)
	}
	packetType, payload, err = readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read NAME reply: %v", err)
	}
	if packetType != fxpName {
		t.Fatalf("expected fxpName reply, got type %d", packetType)
	}
	reader := &attrsPacketParser{buf: payload}
	if _, err := reader.u32(); err != nil {
		t.Fatalf("NAME reply id: %v", err)
	}
	count, err := reader.u32()
	if err != nil {
		t.Fatalf("NAME reply count: %v", err)
	}
	var names []string
	for i := uint32(0); i < count; i++ {
		name, err := reader.str()
		if err != nil {
			t.Fatalf("NAME packet declares %d entries but entry %d cannot be parsed (only %d encoded): %v", count, i+1, len(names), err)
		}
		if _, err := reader.str(); err != nil { // longname
			t.Fatalf("entry %d longname truncated: %v", i+1, err)
		}
		if _, err := reader.attrsFlags(); err != nil {
			t.Fatalf("entry %d attrs truncated: %v", i+1, err)
		}
		if err := reader.skipAttrBody(); err != nil {
			t.Fatalf("entry %d attrs body truncated: %v", i+1, err)
		}
		names = append(names, name)
	}
	if len(names) != 2 || names[0] != "a.txt" || names[1] != "b.txt" {
		t.Fatalf("expected the two statable entries a.txt and b.txt, got %v", names)
	}

	ch.buf.Reset()
	if err := h.handleReadDir(readdir); err != nil {
		t.Fatalf("second handleReadDir: %v", err)
	}
	packetType, payload, err = readPacket(bytes.NewReader(ch.buf.Bytes()))
	if err != nil {
		t.Fatalf("read second READDIR reply: %v", err)
	}
	if packetType != fxpStatus {
		t.Fatalf("expected fxpStatus EOF reply, got type %d", packetType)
	}
	statusReader := &attrsPacketParser{buf: payload}
	if _, err := statusReader.u32(); err != nil {
		t.Fatalf("EOF status id: %v", err)
	}
	code, err := statusReader.u32()
	if err != nil {
		t.Fatalf("EOF status code: %v", err)
	}
	if code != fxEOF {
		t.Fatalf("second READDIR returned status code %d, want fxEOF (%d)", code, fxEOF)
	}
}

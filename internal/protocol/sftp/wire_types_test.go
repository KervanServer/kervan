package sftp

import (
	"encoding/binary"
	"io"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Contract: SFTP reply packet types follow draft-ietf-secsh-filexfer-02 on the
// wire — SSH_FXP_STATUS=101, SSH_FXP_HANDLE=102, SSH_FXP_NAME=103,
// SSH_FXP_DATA=104. These tests assert SPEC LITERALS, not the package's
// fxp* constants: the constants were once swapped (fxpData=103, fxpName=104),
// which typed every READ reply as NAME and every READDIR/REALPATH reply as
// DATA, and the package-constant tests could not detect it.

const (
	wireFxpRead           = 5
	wireFxpOpen           = 3
	wireFxpClose          = 4
	wireFxpWrite          = 6
	wireFxpRemove         = 13
	wireFxpRename         = 18
	wireFxpStatus         = 101
	wireFxpHandle         = 102
	wireFxpName           = 103
	wireFxpData           = 104
	wireNoSuchFile        = 2
	wireFxpStat           = 17
	wireFxpFStat          = 8
	wireFxpAttrs          = 105
	wireStatusFail        = 4
	wireStatusOK          = 0
	wireStatusUnsupported = 8
	wireBadMessage        = 5
	wireFxpSetstat        = 9
	wireFxpFsetstat       = 10
	wireFxpReadlink       = 19
	wireFxpSymlink        = 20
	wireFxpExtended       = 200
)

func wireStr(s string) []byte {
	out := make([]byte, 4+len(s))
	binary.BigEndian.PutUint32(out, uint32(len(s)))
	copy(out[4:], s)
	return out
}

func wireU32(v uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, v)
	return out
}

func wireU64(v uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, v)
	return out
}

func wireSend(t *testing.T, ch ssh.Channel, pktType byte, id uint32, fields ...[]byte) {
	t.Helper()
	body := append([]byte{pktType}, wireU32(id)...)
	for _, f := range fields {
		body = append(body, f...)
	}
	pkt := append(wireU32(uint32(len(body))), body...)
	if _, err := ch.Write(pkt); err != nil {
		t.Fatalf("write packet type %d: %v", pktType, err)
	}
}

func wireRecv(t *testing.T, ch ssh.Channel, id uint32) (byte, []byte) {
	t.Helper()
	watchdog := time.AfterFunc(15*time.Second, func() { panic("sftp reply timed out after 15s") })
	defer watchdog.Stop()
	var lenBuf [4]byte
	if _, err := io.ReadFull(ch, lenBuf[:]); err != nil {
		t.Fatalf("read reply length: %v", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n < 5 || n > 1<<20 {
		t.Fatalf("implausible reply length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(ch, body); err != nil {
		t.Fatalf("read reply body: %v", err)
	}
	if got := binary.BigEndian.Uint32(body[1:5]); got != id {
		t.Fatalf("reply id mismatch: got %d, want %d (type %d)", got, id, body[0])
	}
	return body[0], body[5:]
}

func wireExpectStatus(t *testing.T, ch ssh.Channel, id uint32, code uint32) {
	t.Helper()
	typ, payload := wireRecv(t, ch, id)
	if typ != wireFxpStatus {
		t.Fatalf("reply to id %d is type %d, want STATUS (%d)", id, typ, wireFxpStatus)
	}
	if len(payload) < 4 {
		t.Fatalf("status payload too short: %v", payload)
	}
	if got := binary.BigEndian.Uint32(payload); got != code {
		t.Fatalf("status code = %d, want %d", got, code)
	}
}

func wireExpectHandle(t *testing.T, ch ssh.Channel, id uint32) string {
	t.Helper()
	typ, payload := wireRecv(t, ch, id)
	if typ != wireFxpHandle {
		if typ == wireFxpStatus && len(payload) >= 4 {
			t.Fatalf("reply to id %d is STATUS code %d, want HANDLE (%d)", id, binary.BigEndian.Uint32(payload), wireFxpHandle)
		}
		t.Fatalf("reply to id %d is type %d, want HANDLE (%d)", id, typ, wireFxpHandle)
	}
	if len(payload) < 4 {
		t.Fatalf("handle payload too short: %v", payload)
	}
	n := binary.BigEndian.Uint32(payload)
	if uint32(len(payload)) < 4+n {
		t.Fatalf("handle payload truncated: %v", payload)
	}
	return string(payload[4 : 4+n])
}

func TestSFTPWriteRenameRemoveWireTypes(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatalf("init round trip: %v", err)
	}

	// OPEN /a.txt with WRITE|CREAT|TRUNC (pflags 26), empty attrs.
	var openA []byte
	openA = append(openA, wireStr("/a.txt")...)
	openA = append(openA, wireU32(2|8|16)...)
	openA = append(openA, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 10, openA)
	handleA := wireExpectHandle(t, ch, 10)

	wireSend(t, ch, wireFxpWrite, 11, wireStr(handleA), wireU64(0), wireStr("payload A"))
	wireExpectStatus(t, ch, 11, wireStatusOK)

	wireSend(t, ch, wireFxpClose, 12, wireStr(handleA))
	wireExpectStatus(t, ch, 12, wireStatusOK)

	// RENAME /a.txt → /renamed.txt.
	wireSend(t, ch, wireFxpRename, 13, wireStr("/a.txt"), wireStr("/renamed.txt"))
	wireExpectStatus(t, ch, 13, wireStatusOK)

	// READ /renamed.txt: HANDLE (102), then DATA (104) with the moved content.
	var openR []byte
	openR = append(openR, wireStr("/renamed.txt")...)
	openR = append(openR, wireU32(1)...)
	openR = append(openR, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 14, openR)
	handleR := wireExpectHandle(t, ch, 14)
	wireSend(t, ch, wireFxpRead, 15, wireStr(handleR), wireU64(0), wireU32(100))
	typ, payload := wireRecv(t, ch, 15)
	if typ != wireFxpData {
		t.Fatalf("READ reply is type %d, want SSH_FXP_DATA (%d) — reply packet types must follow the SFTP spec", typ, wireFxpData)
	}
	if len(payload) < 4 || string(payload[4:]) != "payload A" {
		t.Fatalf("renamed content mismatch: %v", payload)
	}
	wireSend(t, ch, wireFxpClose, 16, wireStr(handleR))
	wireExpectStatus(t, ch, 16, wireStatusOK)

	// The old path must be gone: OPEN replies STATUS no-such-file (2).
	var openOld []byte
	openOld = append(openOld, wireStr("/a.txt")...)
	openOld = append(openOld, wireU32(1)...)
	openOld = append(openOld, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 17, openOld)
	typ, payload = wireRecv(t, ch, 17)
	if typ != wireFxpStatus || len(payload) < 4 || binary.BigEndian.Uint32(payload[:4]) != wireNoSuchFile {
		t.Fatalf("open of renamed-away path did not fail with no-such-file (type %d)", typ)
	}

	// REMOVE /renamed.txt, then open must fail again.
	wireSend(t, ch, wireFxpRemove, 18, wireStr("/renamed.txt"))
	wireExpectStatus(t, ch, 18, wireStatusOK)
	wireSend(t, ch, wireFxpOpen, 19, openR)
	typ, payload = wireRecv(t, ch, 19)
	if typ != wireFxpStatus || len(payload) < 4 || binary.BigEndian.Uint32(payload[:4]) != wireNoSuchFile {
		t.Fatalf("open of removed path did not fail with no-such-file (type %d)", typ)
	}
}

// Contract: SFTP directory operations follow draft-ietf-secsh-filexfer-02 on
// the wire — REALPATH replies NAME (103) with the normalized path, OPENDIR
// replies HANDLE (102), READDIR replies NAME packets carrying the directory
// children and a terminating STATUS EOF (1), MKDIR and RMDIR round-trip, and
// OPENDIR on a file is an error.

const (
	wireFxpRealpath = 16
	wireFxpOpenDir  = 11
	wireFxpReadDir  = 12
	wireFxpMkdir    = 14
	wireFxpRmdir    = 15
	wireFxpEof      = 1
)

func sftpParseNameEntries(t *testing.T, payload []byte) (uint32, []string) {
	t.Helper()
	count := binary.BigEndian.Uint32(payload)
	off := 4
	names := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		if off+4 > len(payload) {
			t.Fatalf("truncated NAME entry %d", i)
		}
		n := int(binary.BigEndian.Uint32(payload[off:]))
		off += 4
		if off+n > len(payload) {
			t.Fatalf("truncated NAME filename %d", i)
		}
		names = append(names, string(payload[off:off+n]))
		off += n
		if off+4 > len(payload) {
			t.Fatalf("truncated NAME longname %d", i)
		}
		n = int(binary.BigEndian.Uint32(payload[off:]))
		off += 4
		if off+n > len(payload) {
			t.Fatalf("truncated longname content %d", i)
		}
		off += n
		if off+4 > len(payload) {
			t.Fatalf("truncated NAME attrs %d", i)
		}
		flags := binary.BigEndian.Uint32(payload[off:])
		off += 4
		if flags&1 != 0 {
			off += 8
		}
		if flags&2 != 0 {
			off += 8
		}
		if flags&4 != 0 {
			off += 4
		}
		if flags&8 != 0 {
			off += 8
		}
		if flags&0x80000000 != 0 {
			ec := int(binary.BigEndian.Uint32(payload[off:]))
			off += 4
			for j := 0; j < ec; j++ {
				n = int(binary.BigEndian.Uint32(payload[off:]))
				off += 4 + n
				n = int(binary.BigEndian.Uint32(payload[off:]))
				off += 4 + n
			}
		}
	}
	return count, names
}

func sftpStatusMessage(payload []byte) string {
	if len(payload) < 8 {
		return ""
	}
	n := int(binary.BigEndian.Uint32(payload[4:8]))
	if n == 0 || 8+n > len(payload) {
		return ""
	}
	return string(payload[8 : 8+n])
}

func TestSFTPRealpathReaddirWireTypes(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, s, sshCfg)

	sshConn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatalf("init round trip: %v", err)
	}

	id := uint32(10)

	// Seed /dir/file.txt through the SFTP subsystem itself: MKDIR the parent
	// first, exactly as a real SFTP client does (the server correctly returns
	// SSH_FX_NO_SUCH_FILE for OPEN/CREATE under a missing parent, matching
	// OpenSSH sftp-server and the local backend).
	mkdirSeed := append(wireStr("/dir"), wireU32(0)...)
	wireSend(t, ch, wireFxpMkdir, id, mkdirSeed)
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++

	var openW []byte
	openW = append(openW, wireStr("/dir/file.txt")...)
	openW = append(openW, wireU32(2|8|16)...)
	openW = append(openW, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, id, openW)
	typ, payload := wireRecv(t, ch, id)
	if typ != wireFxpHandle {
		t.Fatalf("seed OPEN failed: type %d, payload %v, message %q", typ, payload, sftpStatusMessage(payload))
	}
	handleW := string(payload[4 : 4+binary.BigEndian.Uint32(payload)])
	id++

	wireSend(t, ch, wireFxpWrite, id, wireStr(handleW), wireU64(0), wireStr("hello"))
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++
	wireSend(t, ch, wireFxpClose, id, wireStr(handleW))
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++

	// REALPATH: NAME (103) with exactly one normalized entry.
	wireSend(t, ch, wireFxpRealpath, id, wireStr("/dir/file.txt"))
	typ, payload = wireRecv(t, ch, id)
	if typ != wireFxpName {
		t.Fatalf("REALPATH reply is type %d, want NAME (%d)", typ, wireFxpName)
	}
	count, names := sftpParseNameEntries(t, payload)
	if count != 1 || len(names) != 1 || names[0] != "/dir/file.txt" {
		t.Fatalf("REALPATH entries = %v (count %d), want [/dir/file.txt]", names, count)
	}
	id++

	// OPENDIR: HANDLE (102).
	wireSend(t, ch, wireFxpOpenDir, id, wireStr("/dir"))
	typ, payload = wireRecv(t, ch, id)
	if typ != wireFxpHandle {
		t.Fatalf("OPENDIR reply is type %d, want HANDLE (%d)", typ, wireFxpHandle)
	}
	dirHandle := string(payload[4 : 4+binary.BigEndian.Uint32(payload)])
	id++

	// READDIR: NAME (103) with the directory child.
	wireSend(t, ch, wireFxpReadDir, id, wireStr(dirHandle))
	typ, payload = wireRecv(t, ch, id)
	if typ != wireFxpName {
		t.Fatalf("READDIR reply is type %d, want NAME (%d)", typ, wireFxpName)
	}
	count, names = sftpParseNameEntries(t, payload)
	if count != 1 || len(names) != 1 || names[0] != "file.txt" {
		t.Fatalf("READDIR entries = %v (count %d), want [file.txt]", names, count)
	}
	id++

	// READDIR again: STATUS EOF (1).
	wireSend(t, ch, wireFxpReadDir, id, wireStr(dirHandle))
	typ, payload = wireRecv(t, ch, id)
	if typ != wireFxpStatus {
		t.Fatalf("READDIR EOF reply is type %d, want STATUS (%d)", typ, wireFxpStatus)
	}
	if binary.BigEndian.Uint32(payload) != wireFxpEof {
		t.Fatalf("READDIR EOF status = %d, want %d", binary.BigEndian.Uint32(payload), wireFxpEof)
	}
	id++

	// CLOSE the directory handle.
	wireSend(t, ch, wireFxpClose, id, wireStr(dirHandle))
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++

	// MKDIR / RMDIR round-trip.
	mkdir := append(wireStr("/newdir"), wireU32(0)...)
	wireSend(t, ch, wireFxpMkdir, id, mkdir)
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++
	wireSend(t, ch, wireFxpRmdir, id, wireStr("/newdir"))
	wireExpectStatus(t, ch, id, wireStatusOK)
	id++

	// OPENDIR on a file must be an error (STATUS, non-zero code).
	wireSend(t, ch, wireFxpOpenDir, id, wireStr("/dir/file.txt"))
	typ, payload = wireRecv(t, ch, id)
	if typ != wireFxpStatus {
		t.Fatalf("OPENDIR on a file replied type %d, want STATUS (%d)", typ, wireFxpStatus)
	}
	if binary.BigEndian.Uint32(payload) == wireStatusOK {
		t.Fatalf("OPENDIR on a file succeeded")
	}
}

func TestStatAttrsAndBogusHandles(t *testing.T) {
	s, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, s, sshCfg)
	sshConn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer sshConn.Close()
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatalf("init round trip: %v", err)
	}

	// Seed /g.txt (9 bytes) over the wire.
	var openG []byte
	openG = append(openG, wireStr("/g.txt")...)
	openG = append(openG, wireU32(2|8|16)...)
	openG = append(openG, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 20, openG)
	handleG := wireExpectHandle(t, ch, 20)
	wireSend(t, ch, wireFxpWrite, 21, wireStr(handleG), wireU64(0), wireStr("payload B"))
	wireExpectStatus(t, ch, 21, wireStatusOK)

	// FSTAT on the live handle → ATTRS (105): flags 13, size 9, exact payload length.
	wireSend(t, ch, wireFxpFStat, 22, wireStr(handleG))
	typ, payload := wireRecv(t, ch, 22)
	if typ != wireFxpAttrs {
		t.Fatalf("FAIL: fstat reply type = %d, want ATTRS (%d)", typ, wireFxpAttrs)
	}
	if len(payload) != 24 {
		t.Fatalf("FAIL: fstat attrs payload = %d bytes, want 24 (flags+size+perms+acmodtime): %x", len(payload), payload)
	}
	if flags := binary.BigEndian.Uint32(payload[0:4]); flags != 1|4|8 {
		t.Fatalf("FAIL: fstat attrs flags = %d, want %d", flags, 1|4|8)
	}
	if size := binary.BigEndian.Uint64(payload[4:12]); size != 9 {
		t.Fatalf("FAIL: fstat attrs size = %d, want 9", size)
	}

	// CLOSE, then STAT the path → ATTRS again.
	wireSend(t, ch, wireFxpClose, 23, wireStr(handleG))
	wireExpectStatus(t, ch, 23, wireStatusOK)
	wireSend(t, ch, wireFxpStat, 24, wireStr("/g.txt"))
	typ, payload = wireRecv(t, ch, 24)
	if typ != wireFxpAttrs {
		t.Fatalf("FAIL: stat reply type = %d, want ATTRS (%d)", typ, wireFxpAttrs)
	}
	if len(payload) != 24 {
		t.Fatalf("FAIL: stat attrs payload = %d bytes, want 24: %x", len(payload), payload)
	}
	if flags := binary.BigEndian.Uint32(payload[0:4]); flags != 1|4|8 {
		t.Fatalf("FAIL: stat attrs flags = %d, want %d", flags, 1|4|8)
	}

	// Bogus-handle error paths: all four handle-consuming ops agree on
	// SSH_FX_FAILURE (matches OpenSSH sftp-server for unknown handles).
	wireSend(t, ch, wireFxpFStat, 25, wireStr("bogus-handle"))
	wireExpectStatus(t, ch, 25, wireStatusFail)
	wireSend(t, ch, wireFxpClose, 26, wireStr("bogus-handle"))
	wireExpectStatus(t, ch, 26, wireStatusFail)
	wireSend(t, ch, wireFxpWrite, 27, wireStr("bogus-handle"), wireU64(0), wireStr("x"))
	wireExpectStatus(t, ch, 27, wireStatusFail)
	wireSend(t, ch, wireFxpRead, 28, wireStr("bogus-handle"), wireU64(0), wireU32(4))
	wireExpectStatus(t, ch, 28, wireStatusFail)
}

// TestUnsupportedOpsWireContract pins the round-49 contract: SETSTAT,
// FSETSTAT, READLINK, SYMLINK and EXTENDED reply STATUS with
// SSH_FX_OP_UNSUPPORTED (including truncated payloads — the handler must not
// crash and must echo the request id), unknown packet types reply
// SSH_FX_BAD_MESSAGE, and the connection stays fully functional afterwards.
func TestUnsupportedOpsWireContract(t *testing.T) {
	srv, sshCfg := sftpIdleServer(t, 30*time.Second)
	cc := sftpStartListener(t, srv, sshCfg)
	conn, ch, err := sftpClientHandshake(t, cc)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer conn.Close()
	if err := sftpInitRoundTrip(t, ch); err != nil {
		t.Fatalf("init round trip: %v", err)
	}

	// Truncated SETSTAT: id-only payload, no path or attrs. The handler must
	// reply STATUS(unsupported) with the id echoed — a crash here would kill
	// the connection goroutine.
	wireSend(t, ch, wireFxpSetstat, 31)
	wireExpectStatus(t, ch, 31, wireStatusUnsupported)

	// Well-formed payloads for all five unsupported ops.
	wireSend(t, ch, wireFxpSetstat, 32, wireStr("/f.txt"), wireU32(0), wireU64(0), wireU32(0), wireU32(0), wireU32(0), wireU32(0))
	wireExpectStatus(t, ch, 32, wireStatusUnsupported)
	wireSend(t, ch, wireFxpFsetstat, 33, wireStr("handle-x"), wireU32(0), wireU64(0), wireU32(0), wireU32(0), wireU32(0), wireU32(0))
	wireExpectStatus(t, ch, 33, wireStatusUnsupported)
	wireSend(t, ch, wireFxpReadlink, 34, wireStr("/link"))
	wireExpectStatus(t, ch, 34, wireStatusUnsupported)
	wireSend(t, ch, wireFxpSymlink, 35, wireStr("/link"), wireStr("/target"))
	wireExpectStatus(t, ch, 35, wireStatusUnsupported)
	wireSend(t, ch, wireFxpExtended, 36, wireStr("statvfs@openssh.com"), wireStr("/"))
	wireExpectStatus(t, ch, 36, wireStatusUnsupported)

	// Unknown packet type: the dispatch default replies SSH_FX_BAD_MESSAGE.
	wireSend(t, ch, 99, 37, wireStr("junk"))
	wireExpectStatus(t, ch, 37, wireBadMessage)

	// The connection must remain fully functional afterwards.
	wireSend(t, ch, wireFxpRealpath, 40, wireStr("/"))
	typ, payload := wireRecv(t, ch, 40)
	if typ != wireFxpName {
		t.Fatalf("REALPATH reply type = %d, want NAME (%d)", typ, wireFxpName)
	}
	_ = payload
}

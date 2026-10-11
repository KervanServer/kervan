package sftp

import (
	"encoding/binary"
	"testing"
	"time"
)

const wireFxpExtendedReply = 201

// TestOpenSSHExtensionsWireContract covers the extensions advertised in
// VERSION: posix-rename, statvfs, fstatvfs, fsync and limits.
func TestOpenSSHExtensionsWireContract(t *testing.T) {
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

	var open []byte
	open = append(open, wireStr("/src.txt")...)
	open = append(open, wireU32(2|8|16)...)
	open = append(open, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 1, open)
	handle := wireExpectHandle(t, ch, 1)
	wireSend(t, ch, wireFxpWrite, 2, wireStr(handle), wireU64(0), wireStr("data"))
	wireExpectStatus(t, ch, 2, wireStatusOK)

	wireSend(t, ch, wireFxpExtended, 3, wireStr("fsync@openssh.com"), wireStr(handle))
	wireExpectStatus(t, ch, 3, wireStatusOK)
	wireSend(t, ch, wireFxpExtended, 4, wireStr("fsync@openssh.com"), wireStr("nope"))
	wireExpectStatus(t, ch, 4, wireStatusFail)

	for id, req := range map[uint32][][]byte{
		5: {wireStr("statvfs@openssh.com"), wireStr("/")},
		6: {wireStr("fstatvfs@openssh.com"), wireStr(handle)},
	} {
		wireSend(t, ch, wireFxpExtended, id, req...)
		typ, payload := wireRecv(t, ch, id)
		if typ != wireFxpExtendedReply || len(payload) != 11*8 {
			t.Fatalf("statvfs reply %d: type %d, %d bytes", id, typ, len(payload))
		}
	}

	wireSend(t, ch, wireFxpClose, 7, wireStr(handle))
	wireExpectStatus(t, ch, 7, wireStatusOK)

	// posix-rename replaces an existing target.
	var open2 []byte
	open2 = append(open2, wireStr("/dst.txt")...)
	open2 = append(open2, wireU32(2|8|16)...)
	open2 = append(open2, wireU32(0)...)
	wireSend(t, ch, wireFxpOpen, 8, open2)
	h2 := wireExpectHandle(t, ch, 8)
	wireSend(t, ch, wireFxpClose, 9, wireStr(h2))
	wireExpectStatus(t, ch, 9, wireStatusOK)
	wireSend(t, ch, wireFxpExtended, 10, wireStr("posix-rename@openssh.com"), wireStr("/src.txt"), wireStr("/dst.txt"))
	wireExpectStatus(t, ch, 10, wireStatusOK)
	wireSend(t, ch, wireFxpStat, 11, wireStr("/src.txt"))
	wireExpectStatus(t, ch, 11, wireNoSuchFile)
	wireSend(t, ch, wireFxpStat, 12, wireStr("/dst.txt"))
	if typ, payload := wireRecv(t, ch, 12); typ != wireFxpAttrs || binary.BigEndian.Uint64(payload[4:12]) != 4 {
		t.Fatalf("renamed file: type %d payload %x", typ, payload)
	}

	wireSend(t, ch, wireFxpExtended, 13, wireStr("limits@openssh.com"))
	typ, payload := wireRecv(t, ch, 13)
	if typ != wireFxpExtendedReply || len(payload) != 32 {
		t.Fatalf("limits reply: type %d, %d bytes", typ, len(payload))
	}
	if read := binary.BigEndian.Uint64(payload[8:16]); read != limitReadLength {
		t.Fatalf("limits read length = %d", read)
	}
}

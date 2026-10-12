package sftp

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

// streamChannel replays a fixed client byte stream and discards replies.
type streamChannel struct {
	ssh.Channel
	r io.Reader
}

func (c streamChannel) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c streamChannel) Write(p []byte) (int, error) { return len(p), nil }
func (c streamChannel) Close() error                { return nil }
func (c streamChannel) CloseWrite() error           { return nil }
func (c streamChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}

// fuzzFS is a writable user VFS over a memory backend, with a small quota
// so quota paths are exercised too.
func fuzzFS(t testing.TB) vfs.FileSystem {
	t.Helper()
	mounts := vfs.NewMountTable()
	mounts.Mount("/", memory.New(), false)
	return vfs.NewUserVFS(mounts, &vfs.UserPermissions{
		Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true, Chmod: true,
	}, nil)
}

func packet(typ byte, fields ...[]byte) []byte {
	body := []byte{typ}
	for _, f := range fields {
		body = append(body, f...)
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	return append(out, body...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }
func str(s string) []byte { return append(u32(uint32(len(s))), s...) }

// FuzzSFTPHandler feeds arbitrary packet streams to the SFTP dispatcher.
// Properties: no panic, and the loop always terminates once input ends.
func FuzzSFTPHandler(f *testing.F) {
	const fileHandle, dirHandle = "H0000000000000001", "H0000000000000002"
	seeds := [][]byte{
		packet(fxpInit, u32(3)),
		bytes.Join([][]byte{
			packet(fxpInit, u32(3)),
			packet(fxpOpen, u32(1), str("/a"), u32(sshFxWrite|sshFxCreat|sshFxTrunc), u32(0)),
			packet(fxpWrite, u32(2), str(fileHandle), u64(0), str("hello")),
			packet(fxpRead, u32(3), str(fileHandle), u64(0), u32(10)),
			packet(fxpFstat, u32(4), str(fileHandle)),
			packet(fxpExtended, u32(5), str("fsync@openssh.com"), str(fileHandle)),
			packet(fxpClose, u32(6), str(fileHandle)),
			packet(fxpRename, u32(7), str("/a"), str("/b")),
			packet(fxpExtended, u32(8), str("posix-rename@openssh.com"), str("/b"), str("/c")),
			packet(fxpMkdir, u32(9), str("/d"), u32(0)),
			packet(fxpOpendir, u32(10), str("/")),
			packet(fxpReaddir, u32(11), str(dirHandle)),
			packet(fxpSetstat, u32(12), str("/c"), u32(0x1|0x4|0x8), u64(2), u32(0o600), u32(1), u32(2)),
			packet(fxpExtended, u32(13), str("statvfs@openssh.com"), str("/")),
			packet(fxpExtended, u32(14), str("limits@openssh.com")),
			packet(fxpRealpath, u32(15), str("../../x/./y")),
			packet(fxpRemove, u32(16), str("/c")),
			packet(fxpRmdir, u32(17), str("/d")),
		}, nil),
		packet(fxpOpen, u32(1)),                        // truncated
		packet(fxpWrite, u32(1), str("x"), u64(1<<63)), // huge offset, no data
		packet(fxpRead, u32(1), str("x"), u64(0), u32(0xffffffff)),
		packet(99, u32(1), str("junk")),
		{0, 0, 0, 0},             // zero length
		{0xff, 0xff, 0xff, 0xff}, // oversize length
		{0, 0x10, 0, 0, fxpInit}, // large declared length, short body
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h := &sftpHandler{
			ch:       streamChannel{r: bytes.NewReader(data)},
			fsys:     fuzzFS(t),
			username: "fuzz",
			handles:  map[string]any{},
		}
		_ = h.loop()
	})
}

// FuzzSCPSink feeds arbitrary SCP sink streams (the client side of
// "scp -t"). Properties: no panic, no path escape outside the target.
func FuzzSCPSink(f *testing.F) {
	for _, s := range []string{
		"C0644 5 a.txt\nhello\x00",
		"T1700000000 0 1700000000 0\nC0600 3 b\nabc\x00",
		"D0755 0 dir\nC0644 1 f\nx\x00E\n",
		"D0755 0 ../escape\nE\n",
		"C0644 99999999999999999999 big\n",
		"C0644 -1 neg\n",
		"C0644 1 ..\nx\x00",
		"E\nE\nE\n",
		"\x01warning\n\x02fatal\n",
		"Cxyz 1 a\nx",
	} {
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, recursive bool) {
		s := &Server{}
		fsys := fuzzFS(t)
		if err := fsys.MkdirAll("/target", 0o755); err != nil {
			t.Fatal(err)
		}
		_ = s.runSCPSink(streamChannel{r: bytes.NewReader(data)}, fsys, "/target", recursive, "fuzz", "", func() {})
		entries, err := fsys.ReadDir("/")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != "target" {
				t.Fatalf("scp sink wrote outside its target: /%s", e.Name())
			}
		}
	})
}

// FuzzParseSCPExec checks the exec command parser never panics and only
// accepts the scp modes it understands.
func FuzzParseSCPExec(f *testing.F) {
	for _, s := range []string{
		"scp -t /upload", "scp -f 'a b'", "scp -r -p -t \"/x y\"", "scp -d -t .",
		"scp -t", "scp -tf /", "ls -la", "scp -t '/unterminated", `scp -t a\ b`, "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, command string) {
		req, err := parseSCPExec(command)
		if err == nil && req.mode != scpModeSource && req.mode != scpModeSink {
			t.Fatalf("accepted %q with mode %q", command, req.mode)
		}
		_, _ = parseExecPayload([]byte(command))
		_, _ = splitShellWords(command)
	})
}

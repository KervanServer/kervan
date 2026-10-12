package ftp

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

// fuzzLogin is prepended to every input so the fuzzer explores the
// authenticated command set.
const fuzzLogin = "USER alice\r\nPASS pw12345\r\n"

func newFuzzServer(f *testing.F) *Server {
	f.Helper()
	st, err := store.Open(f.TempDir())
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 1_000_000, time.Minute)
	user, err := engine.CreateUser("alice", "pw12345", "/", false)
	if err != nil {
		f.Fatal(err)
	}
	// A minimum-cost hash keeps each iteration's login cheap.
	hash, err := bcrypt.GenerateFromPassword([]byte("pw12345"), bcrypt.MinCost)
	if err != nil {
		f.Fatal(err)
	}
	user.PasswordHash = string(hash)
	if err := repo.Update(user); err != nil {
		f.Fatal(err)
	}
	srv := NewServer(Config{
		ListenAddr:  "127.0.0.1",
		IdleTimeout: 200 * time.Millisecond,
		ActiveMode:  true,
		TLSConfig:   selfSignedTLSConfig(f),
		FTPSMode:    "explicit",
	}, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) {
		mounts := vfs.NewMountTable()
		mounts.Mount("/", memory.New(), false)
		return vfs.NewUserVFS(mounts, &vfs.UserPermissions{
			Upload: true, Download: true, Delete: true, Rename: true, CreateDir: true, ListDir: true, Chmod: true,
		}, nil), nil
	}, nil)
	srv.dataWait = 20 * time.Millisecond
	return srv
}

// FuzzFTPCommands runs arbitrary command streams through the control-
// connection loop. Properties: no panic, and the connection handler always
// returns once the client stops talking.
func FuzzFTPCommands(f *testing.F) {
	for _, s := range []string{
		"PWD\r\nCWD /\r\nCDUP\r\nMKD a\r\nRMD a\r\n",
		"TYPE I\r\nPASV\r\nLIST\r\nEPSV\r\nNLST\r\nMLSD /\r\nMLST /\r\n",
		"REST 5\r\nPASV\r\nRETR missing\r\nSTOR x\r\nAPPE x\r\nSTOU x\r\n",
		"RNFR a\r\nRNTO b\r\nRNTO c\r\nDELE b\r\nSIZE b\r\nMDTM b\r\n",
		"PORT 127,0,0,1,0,21\r\nEPRT |2|::1|21|\r\nEPRT |1|1.2.3.4|99999|\r\nPORT 1,2,3\r\n",
		"MFMT 20200102030405 x\r\nSITE CHMOD 777 x\r\nSITE HELP\r\nSTAT\r\nSTAT /\r\nHELP\r\n",
		"\xff\xf4\xff\xf2ABOR\r\nMODE Z\r\nSTRU P\r\nALLO 1\r\nXPWD\r\nXCUP\r\n",
		"HOST example.com\r\nUSER bob\r\nPASS wrong\r\nOPTS UTF8 ON\r\nFEAT\r\nSYST\r\nNOOP\r\n",
		"AUTH TLS\r\n\x16\x03\x01\x00\x05hello",
		"PBSZ 0\r\nPROT P\r\nPROT X\r\n",
		"CWD ../../../../etc\r\nRETR ../../etc/passwd\r\nMKD /a/b/c/../../d\r\n",
		"QUIT\r\n",
	} {
		f.Add([]byte(s))
	}
	srv := newFuzzServer(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		serverConn, clientConn := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			srv.handleConn(context.Background(), serverConn, false)
		}()
		go func() { _, _ = io.Copy(io.Discard, clientConn) }()
		go func() {
			_ = clientConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_, _ = clientConn.Write([]byte(fuzzLogin))
			_, _ = clientConn.Write(data)
			// Most inputs then end promptly; the rest hit the idle timeout.
			_, _ = clientConn.Write([]byte("\r\nQUIT\r\n"))
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("handler did not return for input %q", data)
		}
		_ = clientConn.Close()
	})
}

// FuzzFTPParsers covers the pure parsing helpers.
func FuzzFTPParsers(f *testing.F) {
	for _, s := range []string{"/a/b", "../..", "a//b/./c", "", "\x00", "127,0,0,1,4,1", "|2|::1|2121|", "|||6446|"} {
		f.Add("/home", s)
	}
	f.Fuzz(func(t *testing.T, cwd, arg string) {
		p := resolvePath(cwd, arg)
		if p == "" || p[0] != '/' {
			t.Fatalf("resolvePath(%q, %q) = %q, want an absolute path", cwd, arg, p)
		}
		_, _ = splitCommand(arg)
		_ = stripTelnet(arg)
		_ = isAbortCommand([]byte(arg))
	})
}

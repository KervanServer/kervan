package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	icrypto "github.com/kervanserver/kervan/internal/crypto"
)

// The interop tests drive a fully started App with the real client binaries
// operators use (OpenSSH sftp/scp, curl/libssh2). They exist because the
// wire-level unit tests once encoded a wrong reading of the SFTP draft and
// stayed green while every real client was broken. Each test skips when its
// client binary is not installed.

type interopEnv struct {
	cfgFTPPort, cfgSFTPPort int
	dir, keyPath            string
}

func startInteropApp(t *testing.T) interopEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := crossProtoConfig(dir)
	cfg.SFTP.HostKeyDir = filepath.Join(dir, "host_keys")
	if _, err := icrypto.EnsureHostKeys(cfg.SFTP.HostKeyDir); err != nil {
		t.Fatal(err)
	}
	// One reserved passive port keeps parallel test packages from racing on
	// the default 50000-50100 range.
	passive := strconv.Itoa(crossProtoFreePort())
	cfg.FTP.PassivePortRange = passive + "-" + passive

	srv, err := New(cfg, filepath.Join(dir, "kervan.yaml"), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := srv.auth.CreateUser("alice", crossProtoAlicePassword, "/", false); err != nil {
		t.Fatalf("seed alice: %v", err)
	}

	env := interopEnv{cfgFTPPort: cfg.FTP.Port, cfgSFTPPort: cfg.SFTP.Port, dir: dir}
	if keygen, err := exec.LookPath("ssh-keygen"); err == nil {
		env.keyPath = filepath.Join(dir, "id_ed25519")
		if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", env.keyPath).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
		pub, err := os.ReadFile(env.keyPath + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		user, err := srv.authRepo.GetByUsername("alice")
		if err != nil || user == nil {
			t.Fatalf("load alice: %v", err)
		}
		user.AuthorizedKeys = []string{strings.TrimSpace(string(pub))}
		if err := srv.authRepo.Update(user); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = srv.Close() })
	return env
}

func requireBinary(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not installed", name)
	}
	return p
}

func runInterop(t *testing.T, dir, bin string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeRandomFile(t *testing.T, path string, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	_, _ = rand.Read(data)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func assertSameFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: %d bytes differ from the %d uploaded", filepath.Base(path), len(got), len(want))
	}
}

func sshOpts(env interopEnv, portFlag string) []string {
	return []string{
		portFlag, strconv.Itoa(env.cfgSFTPPort),
		"-i", env.keyPath,
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
}

func TestInteropOpenSSHSFTPAndSCP(t *testing.T) {
	sftpBin := requireBinary(t, "sftp")
	scpBin := requireBinary(t, "scp")
	requireBinary(t, "ssh-keygen")
	env := startInteropApp(t)
	work := t.TempDir()
	payload := writeRandomFile(t, filepath.Join(work, "up.bin"), 3<<20+17)

	batch := filepath.Join(work, "batch")
	if err := os.WriteFile(batch, []byte(strings.Join([]string{
		"mkdir /dir",
		"put up.bin /dir/a.bin",
		"ls -l /dir",
		"rename /dir/a.bin /dir/b.bin",
		"get /dir/b.bin down.bin",
		"put -p up.bin /dir/p.bin",
		"rm /dir/b.bin",
		"rm /dir/p.bin",
		"rmdir /dir",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append(sshOpts(env, "-P"), "-b", batch, "alice@127.0.0.1")
	runInterop(t, work, sftpBin, args...)
	assertSameFile(t, filepath.Join(work, "down.bin"), payload)

	// scp over the SFTP protocol (OpenSSH >= 9 default) and legacy -O.
	for _, legacy := range []bool{false, true} {
		extra := []string{}
		name := "scp-sftp"
		if legacy {
			extra = append(extra, "-O")
			name = "scp-legacy"
		}
		up := append(append(append([]string{}, extra...), sshOpts(env, "-P")...), "-p", "up.bin", "alice@127.0.0.1:/"+name+".bin")
		runInterop(t, work, scpBin, up...)
		down := append(append(append([]string{}, extra...), sshOpts(env, "-P")...), "alice@127.0.0.1:/"+name+".bin", name+".out")
		runInterop(t, work, scpBin, down...)
		assertSameFile(t, filepath.Join(work, name+".out"), payload)
	}
}

func TestInteropCurlFTPAndSSH(t *testing.T) {
	curl := requireBinary(t, "curl")
	env := startInteropApp(t)
	work := t.TempDir()
	payload := writeRandomFile(t, filepath.Join(work, "up.bin"), 2<<20+3)
	user := "alice:" + crossProtoAlicePassword
	ftpURL := "ftp://127.0.0.1:" + strconv.Itoa(env.cfgFTPPort)

	base := []string{"-sS", "--max-time", "60", "-u", user}
	run := func(args ...string) string {
		return runInterop(t, work, curl, append(append([]string{}, base...), args...)...)
	}
	run("-T", "up.bin", ftpURL+"/epsv.bin")                                        // EPSV (curl default)
	run("--disable-epsv", ftpURL+"/epsv.bin", "-o", "pasv.out")                    // PASV
	run("-P", "127.0.0.1", "--disable-eprt", ftpURL+"/epsv.bin", "-o", "port.out") // PORT
	run("-P", "127.0.0.1", ftpURL+"/epsv.bin", "-o", "eprt.out")                   // EPRT
	for _, f := range []string{"pasv.out", "port.out", "eprt.out"} {
		assertSameFile(t, filepath.Join(work, f), payload)
	}
	// Resumed download: curl -C sends REST before RETR.
	run("-C", "1000", ftpURL+"/epsv.bin", "-o", "resumed.out")
	if got, _ := os.ReadFile(filepath.Join(work, "resumed.out")); !bytes.Equal(got, payload[1000:]) {
		t.Fatalf("REST resume returned %d bytes, want %d", len(got), len(payload)-1000)
	}
	if listing := run("-l", ftpURL+"/"); !strings.Contains(listing, "epsv.bin") {
		t.Fatalf("NLST missing upload: %q", listing)
	}

	versionOut := runInterop(t, work, curl, "-V")
	if !strings.Contains(versionOut, " sftp") || !strings.Contains(versionOut, " scp") {
		t.Log("curl built without libssh2; skipping SFTP/SCP part")
		return
	}
	sshURL := func(scheme, p string) string {
		return scheme + "://127.0.0.1:" + strconv.Itoa(env.cfgSFTPPort) + p
	}
	sshBase := []string{"-k"}
	run(append(sshBase, "-T", "up.bin", sshURL("sftp", "/s.bin"))...)
	run(append(sshBase, sshURL("sftp", "/s.bin"), "-o", "s.out")...)
	assertSameFile(t, filepath.Join(work, "s.out"), payload)
	run(append(sshBase, "-T", "up.bin", sshURL("scp", "/c.bin"))...)
	run(append(sshBase, sshURL("scp", "/c.bin"), "-o", "c.out")...) // libssh2 asks for -p times
	assertSameFile(t, filepath.Join(work, "c.out"), payload)
}

// writeTree creates a small nested tree under root and returns its files.
func writeTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"top.txt":              []byte("top level\n"),
		"sub/a.bin":            nil,
		"sub/deeper/b.txt":     []byte("deeper file\n"),
		"sub/deeper/empty.txt": {},
		"other/c with space":   []byte("spaced name\n"),
	}
	for rel, data := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if data == nil {
			files[rel] = writeRandomFile(t, full, 300<<10)
			continue
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "emptydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return files
}

func assertTree(t *testing.T, root string, want map[string][]byte) {
	t.Helper()
	for rel, data := range want {
		assertSameFile(t, filepath.Join(root, filepath.FromSlash(rel)), data)
	}
	if info, err := os.Stat(filepath.Join(root, "emptydir")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory not copied: %v", err)
	}
}

func TestInteropOpenSSHLegacyRecursiveSCP(t *testing.T) {
	scpBin := requireBinary(t, "scp")
	requireBinary(t, "ssh-keygen")
	env := startInteropApp(t)
	work := t.TempDir()
	want := writeTree(t, filepath.Join(work, "tree"))
	scp := func(args ...string) {
		runInterop(t, work, scpBin, append(append([]string{"-O", "-r", "-p"}, sshOpts(env, "-P")...), args...)...)
	}

	// Target does not exist: the uploaded directory becomes /copy.
	scp("tree", "alice@127.0.0.1:/copy")
	// Target exists: the directory is nested inside it, as with OpenSSH.
	scp("tree", "alice@127.0.0.1:/copy")

	scp("alice@127.0.0.1:/copy", "back")
	assertTree(t, filepath.Join(work, "back"), want)
	assertTree(t, filepath.Join(work, "back", "tree"), want)
}

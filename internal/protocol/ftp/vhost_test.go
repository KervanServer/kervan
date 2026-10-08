package ftp

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/session"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/store"
	"github.com/kervanserver/kervan/internal/vfs"
)

func startVHostFTP(t *testing.T, vhosts map[string]VirtualHost, tlsCfg *tls.Config) (addr string, implicitAddr string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	repo := auth.NewUserRepository(st)
	engine := auth.NewEngine(repo, "bcrypt", 100, time.Minute)
	for name, group := range map[string]string{"acme-ann": "acme", "globex-bob": "globex"} {
		u, err := engine.CreateUser(name, "StrongPass123!", "/", false)
		if err != nil {
			t.Fatal(err)
		}
		u.PrimaryGroup = group
		_ = repo.Update(u)
	}
	port, implicitPort := freeTCPPort(t), freeTCPPort(t)
	cfg := Config{
		ListenAddr: "127.0.0.1", Port: port, Banner: "default banner", IdleTimeout: 30 * time.Second,
		VirtualHosts: vhosts,
	}
	if tlsCfg != nil {
		cfg.TLSConfig, cfg.FTPSMode, cfg.FTPSImplicitPort = tlsCfg, "both", implicitPort
	}
	srv := NewServer(cfg, nil, engine, session.NewManager(), nil, func(*auth.User) (vfs.FileSystem, error) { return memory.New(), nil }, nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), net.JoinHostPort("127.0.0.1", strconv.Itoa(implicitPort))
}

type ftpClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialFTP(t *testing.T, addr string) (*ftpClient, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	cl := &ftpClient{t: t, conn: c, r: bufio.NewReader(c)}
	return cl, readFTPReply(t, cl.r)
}

func (c *ftpClient) cmd(line string) string { return ftpCmd(c.t, c.conn, c.r, line) }

// multiline sends cmd and reads a full multi-line reply ("211-" ... "211 ").
func (c *ftpClient) multiline(cmd string) string {
	_, _ = c.conn.Write([]byte(cmd + "\r\n"))
	var b strings.Builder
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.t.Fatalf("read %s reply: %v", cmd, err)
		}
		b.WriteString(line)
		if len(line) >= 4 && line[3] == ' ' {
			return b.String()
		}
	}
}

func (c *ftpClient) login(user string) string {
	c.cmd("USER " + user)
	return c.cmd("PASS StrongPass123!")
}

func TestFTPHostVirtualHosts(t *testing.T) {
	addr, _ := startVHostFTP(t, map[string]VirtualHost{
		"files.acme.test":   {Banner: "Welcome to Acme", AllowedGroups: []string{"acme"}},
		"files.globex.test": {AllowedGroups: []string{"globex"}},
	}, nil)

	c, banner := dialFTP(t, addr)
	if !strings.Contains(banner, "default banner") {
		t.Fatalf("banner = %q", banner)
	}
	if feat := c.multiline("FEAT"); !strings.Contains(feat, "\n HOST\r") {
		t.Fatalf("FEAT lacks HOST: %q", feat)
	}
	if reply := c.cmd("HOST unknown.test"); !strings.HasPrefix(reply, "504") {
		t.Fatalf("unknown host: %q", reply)
	}
	if reply := c.cmd("HOST bad_name!"); !strings.HasPrefix(reply, "501") {
		t.Fatalf("malformed host: %q", reply)
	}
	if reply := c.cmd("HOST FILES.ACME.TEST."); !strings.HasPrefix(reply, "220") || !strings.Contains(reply, "Welcome to Acme") {
		t.Fatalf("acme host: %q", reply)
	}
	// Globex's user may not log in on Acme's host...
	if reply := c.login("globex-bob"); !strings.HasPrefix(reply, "530") {
		t.Fatalf("cross-tenant login: %q", reply)
	}
	// ...but Acme's may, and HOST is refused afterwards.
	if reply := c.login("acme-ann"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("tenant login: %q", reply)
	}
	if reply := c.cmd("HOST files.globex.test"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("HOST after login: %q", reply)
	}

	// Without HOST the default host applies and is unrestricted.
	d, _ := dialFTP(t, addr)
	if reply := d.login("globex-bob"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("default host login: %q", reply)
	}
	// HOST after USER is refused.
	e, _ := dialFTP(t, addr)
	e.cmd("USER acme-ann")
	if reply := e.cmd("HOST files.acme.test"); !strings.HasPrefix(reply, "503") {
		t.Fatalf("HOST after USER: %q", reply)
	}
}

func TestFTPHostWithoutVirtualHostsAcceptsAnyName(t *testing.T) {
	addr, _ := startVHostFTP(t, nil, nil)
	c, _ := dialFTP(t, addr)
	if reply := c.cmd("HOST anything.example"); !strings.HasPrefix(reply, "220") {
		t.Fatalf("HOST: %q", reply)
	}
	if reply := c.login("globex-bob"); !strings.HasPrefix(reply, "230") {
		t.Fatalf("login: %q", reply)
	}
}

func TestFTPSImplicitSNISelectsVirtualHost(t *testing.T) {
	_, implicitAddr := startVHostFTP(t, map[string]VirtualHost{
		"files.acme.test": {Banner: "Welcome to Acme", AllowedGroups: []string{"acme"}},
	}, selfSignedTLSConfig(t))
	raw, err := net.DialTimeout("tcp", implicitAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn := tls.Client(raw, &tls.Config{ServerName: "files.acme.test", InsecureSkipVerify: true}) // #nosec G402 -- self-signed test cert
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	c := &ftpClient{t: t, conn: conn, r: bufio.NewReader(conn)}
	if banner := readFTPReply(t, c.r); !strings.Contains(banner, "Welcome to Acme") {
		t.Fatalf("SNI did not select the vhost banner: %q", banner)
	}
	if reply := c.login("globex-bob"); !strings.HasPrefix(reply, "530") {
		t.Fatalf("SNI vhost restriction not applied: %q", reply)
	}
}

// RFC 2389 §3.2: every feature line in the FEAT reply begins with a space.
func TestFEATLinesStartWithSpace(t *testing.T) {
	addr, _ := startVHostFTP(t, nil, nil)
	c, _ := dialFTP(t, addr)
	lines := strings.Split(strings.TrimSuffix(c.multiline("FEAT"), "\r\n"), "\r\n")
	for _, line := range lines[1 : len(lines)-1] {
		if !strings.HasPrefix(line, " ") || strings.HasPrefix(line, "  ") {
			t.Errorf("feature line %q must start with exactly one space", line)
		}
	}
}

package auth

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/store"
)

func TestAuthenticateLDAPAndCreateShadowUser(t *testing.T) {
	ldapSrv := newFakeLDAPServer(t)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	repo := NewUserRepository(st)
	engine := NewEngine(repo, "argon2id", 5, 15*time.Minute)
	engine.SetLDAPProvider(NewLDAPProvider(config.LDAPConfig{
		Enabled:           true,
		URL:               ldapSrv.url(),
		BindDN:            ldapSrv.serviceDN,
		BindPassword:      ldapSrv.servicePassword,
		BaseDN:            "dc=example,dc=com",
		UserFilter:        "(&(objectClass=person)(uid=%s))",
		UsernameAttribute: "uid",
		EmailAttribute:    "mail",
		GroupAttribute:    "memberOf",
		GroupMapping: map[string]string{
			"kervan-admins": "admin",
		},
		DefaultHomeDir: "/ldap/{username}",
		CacheTTL:       time.Minute,
	}))

	user, err := engine.Authenticate(context.Background(), "alice", ldapSrv.userPassword)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user == nil {
		t.Fatal("expected authenticated ldap user")
	}
	if user.AuthProvider != AuthProviderLDAP {
		t.Fatalf("expected ldap auth provider, got %q", user.AuthProvider)
	}
	if user.Type != UserTypeAdmin {
		t.Fatalf("expected admin role from ldap group mapping, got %q", user.Type)
	}
	if user.HomeDir != "/ldap/alice" {
		t.Fatalf("expected ldap home dir, got %q", user.HomeDir)
	}
	if user.Email != "alice@example.com" {
		t.Fatalf("expected ldap email, got %q", user.Email)
	}

	shadow, err := repo.GetByUsername("alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if shadow == nil || shadow.AuthProvider != AuthProviderLDAP {
		t.Fatalf("expected ldap shadow user, got %#v", shadow)
	}
}

func TestAuthenticatePrefersLocalUserOverLDAP(t *testing.T) {
	ldapSrv := newFakeLDAPServer(t)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	repo := NewUserRepository(st)
	engine := NewEngine(repo, "argon2id", 5, 15*time.Minute)
	engine.SetLDAPProvider(NewLDAPProvider(config.LDAPConfig{
		Enabled:           true,
		URL:               ldapSrv.url(),
		BindDN:            ldapSrv.serviceDN,
		BindPassword:      ldapSrv.servicePassword,
		BaseDN:            "dc=example,dc=com",
		UserFilter:        "(uid=%s)",
		UsernameAttribute: "uid",
		EmailAttribute:    "mail",
		GroupAttribute:    "memberOf",
		DefaultHomeDir:    "/ldap/{username}",
	}))

	if _, err := engine.CreateUser("alice", "LocalPass123!", "/", false); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := engine.Authenticate(context.Background(), "alice", ldapSrv.userPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected local auth failure to win, got %v", err)
	}

	user, err := engine.Authenticate(context.Background(), "alice", "LocalPass123!")
	if err != nil {
		t.Fatalf("Authenticate local: %v", err)
	}
	if user.AuthProvider != AuthProviderLocal {
		t.Fatalf("expected local auth provider, got %q", user.AuthProvider)
	}
}

type fakeLDAPServer struct {
	listener        net.Listener
	searchCount     atomic.Int32
	serviceDN       string
	servicePassword string
	userDN          string
	userPassword    string
}

func newFakeLDAPServer(t *testing.T) *fakeLDAPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ldap: %v", err)
	}
	srv := &fakeLDAPServer{
		listener:        ln,
		serviceDN:       "cn=service,dc=example,dc=com",
		servicePassword: "bind-secret",
		userDN:          "uid=alice,ou=people,dc=example,dc=com",
		userPassword:    "LDAPpass123!",
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handleConn(conn)
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })
	return srv
}

func (s *fakeLDAPServer) url() string {
	return "ldap://" + s.listener.Addr().String()
}

func (s *fakeLDAPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		msg, err := readLDAPMessage(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return
			}
			return
		}

		switch msg.ProtocolOp.tag {
		case 0x60:
			dn, password, parseErr := parseBindRequest(msg.ProtocolOp)
			if parseErr != nil {
				return
			}
			switch {
			case dn == s.serviceDN && password == s.servicePassword:
				_ = writeLDAPMessage(conn, bindResponseMessage(msg.MessageID, 0, ""))
			case dn == s.userDN && password == s.userPassword:
				_ = writeLDAPMessage(conn, bindResponseMessage(msg.MessageID, 0, ""))
			default:
				_ = writeLDAPMessage(conn, bindResponseMessage(msg.MessageID, 49, "invalid credentials"))
			}
		case 0x63:
			s.searchCount.Add(1)
			_ = writeLDAPMessage(conn, searchResultEntryMessage(msg.MessageID, s.userDN, map[string][]string{
				"uid":      {"alice"},
				"mail":     {"alice@example.com"},
				"memberOf": {"cn=kervan-admins,ou=groups,dc=example,dc=com"},
			}))
			_ = writeLDAPMessage(conn, searchResultDoneMessage(msg.MessageID, 0, ""))
		default:
			return
		}
	}
}

func TestParseLDAPFilterEscapesUsername(t *testing.T) {
	filter, err := parseLDAPFilter("(&(objectClass=person)(uid=alice\\2a))")
	if err != nil {
		t.Fatalf("parseLDAPFilter: %v", err)
	}
	if len(filter) == 0 {
		t.Fatal("expected encoded filter bytes")
	}
	if !strings.Contains(escapeLDAPFilterValue("alice*"), "\\2a") {
		t.Fatal("expected ldap filter escape to preserve wildcard safely")
	}
}

package auth

import (
	"errors"
	"testing"
	"time"
)

func TestUserCheckAccess(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)

	cases := []struct {
		name      string
		user      User
		remote    string
		protocols []string
		want      error
	}{
		{"no policy", User{}, "198.51.100.1:5000", []string{ProtocolFTP}, nil},
		{"expired", User{ExpiresAt: &past}, "198.51.100.1:5000", nil, ErrAccountExpired},
		{"expires later", User{ExpiresAt: &future}, "198.51.100.1:5000", nil, nil},
		{"ip in cidr", User{AllowedIPs: []string{"10.0.0.0/8"}}, "10.1.2.3:21", nil, nil},
		{"ip outside", User{AllowedIPs: []string{"10.0.0.0/8"}}, "192.0.2.1:21", nil, ErrIPNotAllowed},
		{"v4-mapped v6", User{AllowedIPs: []string{"192.0.2.1"}}, "[::ffff:192.0.2.1]:22", nil, nil},
		{"unparseable remote", User{AllowedIPs: []string{"192.0.2.1"}}, "pipe", nil, ErrIPNotAllowed},
		{"protocol allowed", User{AllowedProtocols: []string{ProtocolSFTP}}, "192.0.2.1:22", []string{ProtocolSFTP, ProtocolSCP}, nil},
		{"protocol denied", User{AllowedProtocols: []string{ProtocolFTPS}}, "192.0.2.1:21", []string{ProtocolFTP}, ErrProtocolNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.user.CheckAccess(c.remote, now, c.protocols...); !errors.Is(got, c.want) {
				t.Fatalf("CheckAccess = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNormalizeAccessPolicy(t *testing.T) {
	ips, protocols, err := NormalizeAccessPolicy(
		[]string{" 10.0.0.7/8 ", "192.0.2.1", "192.0.2.1/32", "", "::ffff:198.51.100.0/120"},
		[]string{"WEBUI", "sftp", "ftp", "sftp"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	wantIPs := []string{"10.0.0.0/8", "192.0.2.1", "198.51.100.0/24"}
	if len(ips) != len(wantIPs) {
		t.Fatalf("ips = %v, want %v", ips, wantIPs)
	}
	for i := range wantIPs {
		if ips[i] != wantIPs[i] {
			t.Fatalf("ips = %v, want %v", ips, wantIPs)
		}
	}
	if want := []string{"ftp", "sftp", "webui"}; len(protocols) != 3 || protocols[0] != want[0] || protocols[1] != want[1] || protocols[2] != want[2] {
		t.Fatalf("protocols = %v, want %v", protocols, want)
	}
	for _, bad := range []struct {
		ips, protocols []string
		max            int
	}{
		{[]string{"10.0.0.300"}, nil, 0},
		{nil, []string{"telnet"}, 0},
		{nil, nil, -1},
	} {
		if _, _, err := NormalizeAccessPolicy(bad.ips, bad.protocols, bad.max); err == nil {
			t.Fatalf("accepted invalid policy %+v", bad)
		}
	}
}

func TestExpiredAccountCannotAuthenticate(t *testing.T) {
	repo, _ := newGroupTestRepos(t)
	engine := NewEngine(repo, "argon2id", 5, time.Minute)
	user, err := engine.CreateUser("eve", "StrongPass123!", "/", false)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	user.ExpiresAt = &past
	if err := repo.Update(user); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Authenticate(t.Context(), "eve", "StrongPass123!"); !errors.Is(err, ErrAccountExpired) {
		t.Fatalf("Authenticate = %v, want ErrAccountExpired", err)
	}
	// Refusing an expired account must not count as a failed password.
	got, _ := repo.GetByUsername("eve")
	if got.FailedLogins != 0 {
		t.Fatalf("FailedLogins = %d, want 0", got.FailedLogins)
	}
}

package netguard

import (
	"testing"
	"time"
)

func TestIPFilterAllowDeny(t *testing.T) {
	f, err := NewIPFilter([]string{"10.0.0.0/8", "192.168.1.5"}, []string{"10.1.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"10.2.3.4:5000":          true,
		"10.1.2.3:5000":          false, // deny wins over allow
		"192.168.1.5:21":         true,
		"192.168.1.6:21":         false, // not in allow list
		"[::ffff:10.2.3.4]:22":   true,  // IPv4-mapped IPv6
		"[::ffff:10.1.2.3]:22":   false, // mapped address still denied
		"192.168.1.5":            true,  // bare host
		"not-an-ip:1":            false,
		"[fe80::1%eth0]:22":      false,
		"[2001:db8::1]:22":       false,
		"[::ffff:192.168.1.5]:1": true,
	}
	for remote, want := range cases {
		if got := f.AllowedRemote(remote); got != want {
			t.Errorf("AllowedRemote(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestIPFilterEmptyAndNil(t *testing.T) {
	var nilFilter *IPFilter
	if !nilFilter.AllowedRemote("1.2.3.4:1") {
		t.Fatal("nil filter must admit everything")
	}
	f, err := NewIPFilter(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.AllowedRemote("1.2.3.4:1") || !f.AllowedRemote("garbage") {
		t.Fatal("empty filter must admit everything")
	}
	f2, _ := NewIPFilter(nil, []string{"1.2.3.4"})
	if f2.AllowedRemote("1.2.3.4:9") {
		t.Fatal("denied ip admitted")
	}
	if !f2.AllowedRemote("1.2.3.5:9") {
		t.Fatal("deny-only filter must admit other ips")
	}
}

func TestIPFilterUpdate(t *testing.T) {
	f, _ := NewIPFilter(nil, nil)
	if err := f.Update(nil, []string{"bogus"}); err == nil {
		t.Fatal("expected error for invalid entry")
	}
	if !f.AllowedRemote("9.9.9.9:1") {
		t.Fatal("failed update must keep previous lists")
	}
	if err := f.Update([]string{"::1"}, nil); err != nil {
		t.Fatal(err)
	}
	if !f.AllowedRemote("[::1]:1") || f.AllowedRemote("127.0.0.1:1") {
		t.Fatal("update not applied")
	}
}

func TestLimiter(t *testing.T) {
	if NewLimiter(0) != nil {
		t.Fatal("limit 0 must be unlimited (nil)")
	}
	var unlimited *Limiter
	for i := 0; i < 10; i++ {
		if !unlimited.TryAcquire() {
			t.Fatal("nil limiter must always admit")
		}
	}
	l := NewLimiter(2)
	for i := 0; i < 2; i++ {
		if !l.TryAcquire() {
			t.Fatalf("slot %d unavailable", i)
		}
	}
	if l.TryAcquire() {
		t.Fatal("third acquire must fail")
	}
	l.Release()
	if !l.TryAcquire() {
		t.Fatal("slot not released")
	}
}

func TestAcceptBackoff(t *testing.T) {
	if AcceptBackoff(0) != 0 {
		t.Fatal("no backoff before first error")
	}
	if AcceptBackoff(1) != 5*time.Millisecond {
		t.Fatal("unexpected first backoff")
	}
	if AcceptBackoff(100) != time.Second {
		t.Fatal("backoff must cap at 1s")
	}
}

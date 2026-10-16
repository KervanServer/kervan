package auth

import (
	"testing"
	"time"
)

func TestIPThrottleBansAfterThreshold(t *testing.T) {
	th, err := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 3, Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 2; i++ {
		if th.RecordFailure("198.51.100.7:1234", now) {
			t.Fatal("banned too early")
		}
	}
	if err := th.Check("198.51.100.7:9999", now); err != nil {
		t.Fatal("not yet banned")
	}
	if !th.RecordFailure("198.51.100.7:1", now) {
		t.Fatal("third failure must ban")
	}
	if err := th.Check("198.51.100.7:2", now); err != ErrIPBanned {
		t.Fatalf("expected ban, got %v", err)
	}
	if err := th.Check("198.51.100.8:2", now); err != nil {
		t.Fatal("other address must not be banned")
	}
	if th.BannedCount(now) != 1 {
		t.Fatal("expected one banned address")
	}
	// Success while banned does not lift the ban.
	th.RecordSuccess("198.51.100.7:1")
	if th.Check("198.51.100.7:1", now) == nil {
		t.Fatal("success must not lift an active ban")
	}
	later := now.Add(time.Minute + time.Second)
	if err := th.Check("198.51.100.7:2", later); err != nil {
		t.Fatal("ban must expire")
	}
	// After expiry the counter restarts.
	if th.RecordFailure("198.51.100.7:1", later) {
		t.Fatal("counter must restart after ban expiry")
	}
}

func TestIPThrottleWhitelistDisabledAndSuccess(t *testing.T) {
	th, _ := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 1, Duration: time.Minute, Whitelist: []string{"10.0.0.0/8"}})
	now := time.Now()
	if th.RecordFailure("10.1.1.1:5", now) || th.Check("10.1.1.1:5", now) != nil {
		t.Fatal("whitelisted address must never be banned")
	}

	th2, _ := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 2, Duration: time.Minute})
	th2.RecordFailure("192.0.2.1:1", now)
	th2.RecordSuccess("192.0.2.1:1")
	if th2.RecordFailure("192.0.2.1:1", now) {
		t.Fatal("success must reset the failure count")
	}

	off, _ := NewIPThrottle(IPThrottleConfig{Enabled: false, Threshold: 1, Duration: time.Minute})
	off.RecordFailure("192.0.2.1:1", now)
	if off.Check("192.0.2.1:1", now) != nil {
		t.Fatal("disabled throttle must not ban")
	}
	var nilT *IPThrottle
	if nilT.Check("x", now) != nil || nilT.RecordFailure("x", now) {
		t.Fatal("nil throttle must be inert")
	}
}

// Contract (README, security.brute_force): IPv6 clients are grouped per /64
// and whitelist_ips exempts addresses from the ban. The two must compose: a
// whitelisted IPv6 range must never be banned, and matching must be done
// against the caller's address rather than the /64 bucket key.
func TestIPThrottleWhitelistExemptsIPv6(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	newThrottle := func() *IPThrottle {
		t.Helper()
		th, err := NewIPThrottle(IPThrottleConfig{
			Enabled:   true,
			Threshold: 2,
			Duration:  time.Minute,
			Whitelist: []string{"2001:db8::/64"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return th
	}

	// 2001:db8::abcd is inside 2001:db8::/64 (groups 2001,0db8,0000,0000).
	whitelisted := "[2001:db8::abcd]:22"
	th := newThrottle()
	for i := 0; i < 4; i++ {
		th.RecordFailure(whitelisted, now)
	}
	if err := th.Check(whitelisted, now); err != nil {
		t.Fatalf("whitelisted IPv6 address was banned: %v", err)
	}
	if n := th.BannedCount(now); n != 0 {
		t.Fatalf("whitelisted IPv6 failures must not be tracked at all, got %d tracked bans", n)
	}

	// Boundary: 2001:db8:0:1234::9 shares the 2001:db8: prefix but sits
	// outside 2001:db8::/64, so it must still be banned.
	outside := "[2001:db8:0:1234::9]:22"
	th2 := newThrottle()
	for i := 0; i < 4; i++ {
		th2.RecordFailure(outside, now)
	}
	if err := th2.Check(outside, now); err != ErrIPBanned {
		t.Fatalf("address outside the whitelisted /64 must still be banned, got %v", err)
	}

	// The /64 bucketing itself must be unchanged by the exemption.
	th3, _ := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 2, Duration: time.Minute})
	th3.RecordFailure("[2001:db8:1:2::1]:22", now)
	if !th3.RecordFailure("[2001:db8:1:2::ffff]:22", now) {
		t.Fatal("addresses in the same /64 must still share a counter")
	}
	if th3.Check("[2001:db8:1:3::1]:22", now) != nil {
		t.Fatal("different /64 must not be banned")
	}
}

func TestIPThrottleBucketsIPv6By64(t *testing.T) {
	th, _ := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 2, Duration: time.Minute})
	now := time.Now()
	th.RecordFailure("[2001:db8:1:2::1]:22", now)
	if !th.RecordFailure("[2001:db8:1:2::ffff]:22", now) {
		t.Fatal("addresses in the same /64 must share a counter")
	}
	if th.Check("[2001:db8:1:3::1]:22", now) != nil {
		t.Fatal("different /64 must not be banned")
	}
}

// TestIPThrottleZoneScopedIPv6IsThrottled covers a client whose RemoteAddr
// carries an IPv6 zone identifier ("[fe80::1%eth0]:5151", which the kernel
// produces for link-local peers). net.ParseIP rejects a zoned address, so
// throttleKey used to return "" — and Check/RecordFailure treat an empty key as
// "not throttleable", letting such a client evade the ban entirely.
func TestIPThrottleZoneScopedIPv6IsThrottled(t *testing.T) {
	th, err := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 3, Duration: time.Hour})
	if err != nil {
		t.Fatalf("new IPThrottle: %v", err)
	}
	now := time.Now()
	const zoneRemote = "[fe80::1%eth0]:5151"

	for i := 1; i <= 3; i++ {
		th.RecordFailure(zoneRemote, now)
		if err := th.Check(zoneRemote, now); err != nil && i < 3 {
			t.Fatalf("banned after %d failure(s), want no ban before threshold=3", i)
		}
	}
	if err := th.Check(zoneRemote, now); err == nil {
		t.Fatal("a zone-scoped IPv6 client must still be banned after threshold failed logins; " +
			"an IPv6 zone identifier must not make throttleKey return \"\" (treated as not throttleable)")
	}
}

// TestIPThrottleZoneScopedIPv6BucketsBy64 is the neighbouring valid case: a
// handled zone-scoped address must bucket per /64 like any other IPv6 client, so
// rotating addresses inside the prefix cannot evade the ban.
func TestIPThrottleZoneScopedIPv6BucketsBy64(t *testing.T) {
	th, err := NewIPThrottle(IPThrottleConfig{Enabled: true, Threshold: 3, Duration: time.Hour})
	if err != nil {
		t.Fatalf("new IPThrottle: %v", err)
	}
	now := time.Now()

	for _, host := range []string{
		"[fe80::1%eth0]:5001",
		"[fe80::2%eth0]:5002",
		"[fe80::3%eth0]:5003",
	} {
		th.RecordFailure(host, now)
	}
	if err := th.Check("[fe80::9%eth0]:5009", now); err == nil {
		t.Fatal("three failed logins from one /64 must ban a fourth address in that /64; " +
			"zone-scoped clients must be bucketed per /64 so address rotation cannot evade the ban")
	}
}

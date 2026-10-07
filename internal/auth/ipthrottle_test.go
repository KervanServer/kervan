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

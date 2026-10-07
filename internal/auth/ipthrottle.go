package auth

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/netguard"
)

// ErrIPBanned is returned when a client address is temporarily banned after
// too many failed logins.
var ErrIPBanned = errors.New("too many failed logins from this address")

// maxTrackedIPs bounds the failure table so a distributed attack cannot grow
// it without limit; stale entries are pruned first.
const maxTrackedIPs = 100_000

// IPThrottle bans a client address after a number of failed logins within the
// ban window, independently of which account was targeted. It is shared by
// every protocol, so failures on FTP also count against SFTP and the API.
type IPThrottle struct {
	mu        sync.Mutex
	enabled   bool
	threshold int
	duration  time.Duration
	whitelist *netguard.IPFilter
	entries   map[string]*ipFailures
}

type ipFailures struct {
	count       int
	firstAt     time.Time
	bannedUntil time.Time
}

// IPThrottleConfig mirrors security.brute_force.
type IPThrottleConfig struct {
	Enabled   bool
	Threshold int
	Duration  time.Duration
	Whitelist []string
}

func NewIPThrottle(cfg IPThrottleConfig) (*IPThrottle, error) {
	t := &IPThrottle{entries: make(map[string]*ipFailures)}
	if err := t.Configure(cfg); err != nil {
		return nil, err
	}
	return t, nil
}

// Configure applies new settings; existing failure counts are kept.
func (t *IPThrottle) Configure(cfg IPThrottleConfig) error {
	if t == nil {
		return nil
	}
	var whitelist *netguard.IPFilter
	if len(cfg.Whitelist) > 0 {
		f, err := netguard.NewIPFilter(cfg.Whitelist, nil)
		if err != nil {
			return err
		}
		whitelist = f
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = cfg.Enabled && cfg.Threshold > 0
	t.threshold = cfg.Threshold
	t.duration = cfg.Duration
	if t.duration <= 0 {
		t.duration = time.Hour
	}
	t.whitelist = whitelist
	if !t.enabled {
		t.entries = make(map[string]*ipFailures)
	}
	return nil
}

func (t *IPThrottle) exempt(ip string) bool {
	return t.whitelist != nil && t.whitelist.AllowedRemote(ip)
}

// Check returns ErrIPBanned while remote is banned.
func (t *IPThrottle) Check(remote string, now time.Time) error {
	if t == nil {
		return nil
	}
	ip := throttleKey(remote)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.enabled || ip == "" || t.exempt(ip) {
		return nil
	}
	if e := t.entries[ip]; e != nil && now.Before(e.bannedUntil) {
		return ErrIPBanned
	}
	return nil
}

// RecordFailure counts a failed login and reports whether it triggered a ban.
func (t *IPThrottle) RecordFailure(remote string, now time.Time) bool {
	if t == nil {
		return false
	}
	ip := throttleKey(remote)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.enabled || ip == "" || t.exempt(ip) {
		return false
	}
	e := t.entries[ip]
	if e == nil || (now.Sub(e.firstAt) > t.duration && !now.Before(e.bannedUntil)) {
		if e == nil && len(t.entries) >= maxTrackedIPs {
			t.pruneLocked(now)
			if len(t.entries) >= maxTrackedIPs {
				return false
			}
		}
		e = &ipFailures{firstAt: now}
		t.entries[ip] = e
	}
	e.count++
	if e.count >= t.threshold && !now.Before(e.bannedUntil) {
		e.bannedUntil = now.Add(t.duration)
		return true
	}
	return false
}

// RecordSuccess clears the failure count for remote unless it is banned.
func (t *IPThrottle) RecordSuccess(remote string) {
	if t == nil {
		return
	}
	ip := throttleKey(remote)
	t.mu.Lock()
	defer t.mu.Unlock()
	if e := t.entries[ip]; e != nil && e.bannedUntil.IsZero() {
		delete(t.entries, ip)
	}
}

// BannedCount reports how many addresses are currently banned.
func (t *IPThrottle) BannedCount(now time.Time) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, e := range t.entries {
		if now.Before(e.bannedUntil) {
			n++
		}
	}
	return n
}

func (t *IPThrottle) pruneLocked(now time.Time) {
	for ip, e := range t.entries {
		if now.Sub(e.firstAt) > t.duration && !now.Before(e.bannedUntil) {
			delete(t.entries, ip)
		}
	}
}

func throttleKey(remote string) string {
	host := strings.TrimSpace(remote)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	// Bucket IPv6 clients by /64: a single host typically controls a whole
	// /64 and could otherwise rotate addresses to evade the ban.
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

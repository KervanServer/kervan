// Package netguard enforces the network-level admission controls shared by
// every listener: the security.allowed_ips / security.denied_ips lists and the
// per-protocol max_connections caps.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// IPFilter decides whether a remote address may connect. A deny entry always
// wins; when the allow list is non-empty only matching addresses are admitted.
// A nil *IPFilter admits everything. The lists can be swapped at runtime.
type IPFilter struct {
	mu    sync.RWMutex
	allow []netip.Prefix
	deny  []netip.Prefix
}

// NewIPFilter builds a filter from IP or CIDR entries.
func NewIPFilter(allowed, denied []string) (*IPFilter, error) {
	f := &IPFilter{}
	if err := f.Update(allowed, denied); err != nil {
		return nil, err
	}
	return f, nil
}

// Update atomically replaces both lists. On error the previous lists are kept.
func (f *IPFilter) Update(allowed, denied []string) error {
	allow, err := parsePrefixes(allowed)
	if err != nil {
		return fmt.Errorf("allowed_ips: %w", err)
	}
	deny, err := parsePrefixes(denied)
	if err != nil {
		return fmt.Errorf("denied_ips: %w", err)
	}
	f.mu.Lock()
	f.allow, f.deny = allow, deny
	f.mu.Unlock()
	return nil
}

// Allowed reports whether ip may connect.
func (f *IPFilter) Allowed(ip netip.Addr) bool {
	if f == nil {
		return true
	}
	ip = ip.Unmap()
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range f.deny {
		if p.Contains(ip) {
			return false
		}
	}
	if len(f.allow) == 0 {
		return true
	}
	for _, p := range f.allow {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// AllowedRemote is Allowed for a "host:port" or bare host string, as found in
// net.Conn.RemoteAddr().String() or http.Request.RemoteAddr. Unparseable
// addresses are rejected whenever any list is configured.
func (f *IPFilter) AllowedRemote(remote string) bool {
	if f == nil {
		return true
	}
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		f.mu.RLock()
		empty := len(f.allow) == 0 && len(f.deny) == 0
		f.mu.RUnlock()
		return empty
	}
	return f.Allowed(ip)
}

func parsePrefixes(entries []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(entries))
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid entry %q", raw)
			}
			if p.Addr().Is4In6() {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid entry %q", raw)
		}
		ip = ip.Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out, nil
}

// Limiter caps concurrent connections. A nil *Limiter or a limit <= 0 is
// unlimited.
type Limiter struct {
	sem chan struct{}
}

// NewLimiter returns a limiter admitting at most max concurrent holders.
func NewLimiter(max int) *Limiter {
	if max <= 0 {
		return nil
	}
	return &Limiter{sem: make(chan struct{}, max)}
}

// TryAcquire takes a slot without blocking.
func (l *Limiter) TryAcquire() bool {
	if l == nil {
		return true
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns a slot taken by a successful TryAcquire.
func (l *Limiter) Release() {
	if l == nil {
		return
	}
	<-l.sem
}

// AcceptBackoff returns the delay before retrying after the n-th consecutive
// Accept error, so a persistent failure such as EMFILE does not spin a core.
func AcceptBackoff(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := 5 * time.Millisecond << min(n-1, 8)
	return min(d, time.Second)
}

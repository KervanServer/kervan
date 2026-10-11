package auth

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

var (
	ErrAccountExpired      = errors.New("account expired")
	ErrIPNotAllowed        = errors.New("sign-in not allowed from this address")
	ErrProtocolNotAllowed  = errors.New("sign-in not allowed over this protocol")
	ErrTooManySessions     = errors.New("too many concurrent sessions")
	errInvalidAccessPolicy = errors.New("invalid access policy")
)

// Protocol names used by AllowedProtocols. WebUI also covers the REST API.
const (
	ProtocolFTP   = "ftp"
	ProtocolFTPS  = "ftps"
	ProtocolSFTP  = "sftp"
	ProtocolSCP   = "scp"
	ProtocolWebUI = "webui"
)

// KnownProtocols lists the values accepted in User.AllowedProtocols.
var KnownProtocols = []string{ProtocolFTP, ProtocolFTPS, ProtocolSFTP, ProtocolSCP, ProtocolWebUI}

// Expired reports whether the account has passed its expiry time.
func (u *User) Expired(now time.Time) bool {
	return u.ExpiresAt != nil && !now.Before(*u.ExpiresAt)
}

// ProtocolAllowed reports whether the user may sign in over protocol.
func (u *User) ProtocolAllowed(protocol string) bool {
	return len(u.AllowedProtocols) == 0 || slices.Contains(u.AllowedProtocols, protocol)
}

// AnyProtocolAllowed reports whether at least one of protocols is allowed,
// for checks made before the exact protocol is known (SSH carries both SFTP
// and SCP).
func (u *User) AnyProtocolAllowed(protocols ...string) bool {
	return slices.ContainsFunc(protocols, u.ProtocolAllowed)
}

// IPAllowed reports whether remote ("host:port" or a bare address) matches
// AllowedIPs. Unparseable addresses are refused when a list is set.
func (u *User) IPAllowed(remote string) bool {
	if len(u.AllowedIPs) == 0 {
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
		return false
	}
	ip = ip.Unmap()
	for _, entry := range u.AllowedIPs {
		p, err := parseAccessPrefix(entry)
		if err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckAccess applies the per-user access policy (expiry, source address
// and protocol) to a sign-in that already passed authentication. protocols
// lists the protocols the connection may use; any one being allowed is
// enough.
func (u *User) CheckAccess(remote string, now time.Time, protocols ...string) error {
	if u.Expired(now) {
		return ErrAccountExpired
	}
	if !u.IPAllowed(remote) {
		return ErrIPNotAllowed
	}
	if len(protocols) > 0 && !u.AnyProtocolAllowed(protocols...) {
		return ErrProtocolNotAllowed
	}
	return nil
}

// IsAccessDenied reports whether err came from the per-user access policy,
// as opposed to bad credentials.
func IsAccessDenied(err error) bool {
	return errors.Is(err, ErrAccountExpired) || errors.Is(err, ErrIPNotAllowed) ||
		errors.Is(err, ErrProtocolNotAllowed) || errors.Is(err, ErrTooManySessions)
}

// NormalizeAccessPolicy validates and canonicalizes the access-policy fields
// supplied by an administrator.
func NormalizeAccessPolicy(ips, protocols []string, maxSessions int) ([]string, []string, error) {
	if maxSessions < 0 {
		return nil, nil, fmt.Errorf("%w: max_sessions must be 0 (unlimited) or positive", errInvalidAccessPolicy)
	}
	var outIPs []string
	for _, raw := range ips {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := parseAccessPrefix(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: allowed_ips entry %q is not an IP address or CIDR range", errInvalidAccessPolicy, raw)
		}
		canonical := p.String()
		if p.IsSingleIP() {
			canonical = p.Addr().String()
		}
		if !slices.Contains(outIPs, canonical) {
			outIPs = append(outIPs, canonical)
		}
	}
	var outProtocols []string
	for _, raw := range protocols {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if !slices.Contains(KnownProtocols, name) {
			return nil, nil, fmt.Errorf("%w: unknown protocol %q (use %s)", errInvalidAccessPolicy, raw, strings.Join(KnownProtocols, ", "))
		}
		if !slices.Contains(outProtocols, name) {
			outProtocols = append(outProtocols, name)
		}
	}
	slices.SortFunc(outProtocols, func(a, b string) int {
		return slices.Index(KnownProtocols, a) - slices.Index(KnownProtocols, b)
	})
	return outIPs, outProtocols, nil
}

func parseAccessPrefix(raw string) (netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "/") {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Prefix{}, err
	}
	ip = ip.Unmap()
	return netip.PrefixFrom(ip, ip.BitLen()), nil
}

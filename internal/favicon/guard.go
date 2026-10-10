// Package favicon finds, fetches and caches website icons (favicons)
// for bookmarks. It includes an SSRF guard so a user-supplied bookmark
// URL cannot make the server reach internal addresses.
package favicon

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedIP is returned (wrapped) when a dial is refused because the
// resolved address is a blocked IP (see IsBlockedIP).
var ErrBlockedIP = errors.New("favicon: blocked IP address")

// blockedPrefixes lists ranges netip's Is* helpers don't already cover.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network" source addresses
	netip.MustParsePrefix("100.64.0.0/10"),      // CGNAT shared address space (RFC 6598)
	netip.MustParsePrefix("255.255.255.255/32"), // limited broadcast
}

// IsBlockedIP reports whether ip is unsafe to connect to: loopback,
// private (RFC 1918 and fc00::/7 ULA), link-local, unspecified,
// multicast, CGNAT, the 0.0.0.0/8 block or the broadcast address.
// IPv4-mapped IPv6 addresses are unwrapped first so ::ffff:127.0.0.1 is
// treated exactly like 127.0.0.1.
func IsBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// newDialer returns a net.Dialer whose Control hook re-checks the actual
// address being dialed — after DNS resolution — and refuses blocked IPs.
// Checking in Control (rather than before dialing) also covers redirects
// and DNS-rebinding tricks: every connection attempt is vetted at the
// socket layer. Only tcp networks are permitted.
func newDialer(allowPrivate bool, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	d.Control = func(network, address string, _ syscall.RawConn) error {
		if !strings.HasPrefix(network, "tcp") {
			return fmt.Errorf("favicon: network %q not allowed", network)
		}
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("favicon: bad dial address %q: %w", address, err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return fmt.Errorf("favicon: unparsable dial host %q: %w", host, err)
		}
		if IsBlockedIP(ip) {
			return fmt.Errorf("%w: %s", ErrBlockedIP, ip)
		}
		return nil
	}
	return d
}

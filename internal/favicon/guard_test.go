package favicon

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		addr    string
		blocked bool
	}{
		// Loopback.
		{"127.0.0.1", true},
		{"127.55.0.9", true},
		{"::1", true},
		// RFC 1918 private.
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"192.168.0.1", true},
		{"192.169.0.1", false},
		// Unique-local v6 (fc00::/7).
		{"fc00::1", true},
		{"fd12:3456::1", true},
		// Link-local.
		{"169.254.1.1", true},
		{"fe80::1", true},
		// Unspecified.
		{"0.0.0.0", true},
		{"::", true},
		// The whole 0.0.0.0/8 block.
		{"0.1.2.3", true},
		// Multicast.
		{"224.0.0.1", true},
		{"239.255.255.250", true},
		{"ff02::1", true},
		// CGNAT 100.64.0.0/10.
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"100.63.255.255", false},
		{"100.128.0.1", false},
		// Broadcast.
		{"255.255.255.255", true},
		// IPv4-mapped IPv6 must unwrap before the checks.
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"::ffff:169.254.1.1", true},
		{"::ffff:8.8.8.8", false},
		// Public addresses pass.
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
		{"2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		ip, err := netip.ParseAddr(c.addr)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", c.addr, err)
		}
		if got := IsBlockedIP(ip); got != c.blocked {
			t.Errorf("IsBlockedIP(%s) = %v, want %v", c.addr, got, c.blocked)
		}
	}
}

func TestGuardedDialerControl(t *testing.T) {
	d := newDialer(false, time.Second)

	for _, address := range []string{
		"127.0.0.1:80",
		"10.1.2.3:443",
		"[::1]:8080",
		"[fe80::1]:443",
		"169.254.169.254:80",
	} {
		if err := d.Control("tcp4", address, nil); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("Control(%q) err = %v, want ErrBlockedIP", address, err)
		}
	}
	if err := d.Control("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public IP rejected: %v", err)
	}
	if err := d.Control("tcp6", "[2606:4700:4700::1111]:443", nil); err != nil {
		t.Errorf("public v6 rejected: %v", err)
	}
	// Non-tcp networks are refused outright.
	if err := d.Control("udp4", "8.8.8.8:53", nil); err == nil {
		t.Error("udp4 dial allowed, want refusal")
	}
	// allowPrivate disables the IP check.
	open := newDialer(true, time.Second)
	if err := open.Control("tcp4", "127.0.0.1:80", nil); err != nil {
		t.Errorf("allowPrivate dial rejected: %v", err)
	}
}

// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for deciding whether an address belongs to this machine.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package netid

import (
	"net"
	"testing"
	"time"
)

func TestIsLocalIPLoopback(t *testing.T) {
	// Loopback is filtered out of the advertisement candidates but is still an address this
	// machine holds, which is the question IsLocalIP answers.
	if !IsLocalIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("127.0.0.1 should be recognised as local")
	}
}

func TestIsLocalIPLoopbackV6(t *testing.T) {
	if !IsLocalIP(net.ParseIP("::1")) {
		t.Skip("host has no IPv6 loopback address")
	}
}

func TestIsLocalIPRemote(t *testing.T) {
	// These ranges are reserved for documentation (RFC 5737 / RFC 3849) and are never assigned
	// to a host, so a true result would mean the check is broken.
	for _, addr := range []string{"203.0.113.7", "198.51.100.4", "192.0.2.9", "2001:db8::1"} {
		if IsLocalIP(net.ParseIP(addr)) {
			t.Fatalf("%s should not be recognised as local", addr)
		}
	}
}

func TestIsLocalIPNil(t *testing.T) {
	if IsLocalIP(nil) {
		t.Fatal("nil should not be recognised as local")
	}
}

// TestIsLocalIPEveryEnumeratedAddress is the property that matters: every address this machine
// holds must be recognised, or a correctly configured node would mistake itself for a peer and
// discard its own identity.
func TestIsLocalIPEveryEnumeratedAddress(t *testing.T) {
	local := allLocalIPs()
	if len(local) == 0 {
		t.Skip("host reports no addresses")
	}
	for _, ip := range local {
		if !IsLocalIP(ip) {
			t.Fatalf("enumerated local address %s not recognised as local", ip)
		}
	}
}

func TestIsLocalIPPrimaryAddress(t *testing.T) {
	ip, err := PrimaryIP()
	if err != nil || ip == nil {
		t.Skip("host has no usable address")
	}
	if !IsLocalIP(ip) {
		t.Fatalf("primary address %s should be recognised as local", ip)
	}
}

// TestAllLocalIPsIncludesLoopback guards the deliberate difference from localCandidates, which
// filters loopback and bridges out. Ownership is a broader question than suitability.
func TestAllLocalIPsIncludesLoopback(t *testing.T) {
	if !containsIP(allLocalIPs(), net.ParseIP("127.0.0.1")) {
		t.Fatal("allLocalIPs should include loopback")
	}
}

func TestIsLocalHost(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		isLocal bool
		known   bool
	}{
		{"loopback literal", "127.0.0.1", true, true},
		{"documentation range", "203.0.113.7", false, true},
		{"ipv6 documentation range", "2001:db8::1", false, true},
		// RFC 2606 reserves .invalid so this never resolves; the result must be "unknown"
		// rather than "remote", or an unresolvable name becomes a bogus peer address.
		{"unresolvable name", "no-such-host.invalid", false, false},
		{"empty", "", false, false},
		{"whitespace", "   ", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isLocal, known := IsLocalHost(c.host)
			if isLocal != c.isLocal || known != c.known {
				t.Fatalf("IsLocalHost(%q) = (%v, %v), want (%v, %v)",
					c.host, isLocal, known, c.isLocal, c.known)
			}
		})
	}
}

// TestIsLocalHostResolvesNameToLocalAddress covers the hostname path end to end. "localhost"
// resolves to a loopback address on any sane host, so it must come back local.
func TestIsLocalHostResolvesNameToLocalAddress(t *testing.T) {
	isLocal, known := IsLocalHost("localhost")
	if !known {
		t.Skip("localhost does not resolve on this host")
	}
	if !isLocal {
		t.Fatal("localhost should resolve to an address of this machine")
	}
}

// TestIsLocalHostDoesNotHang bounds the failure mode that matters on a boot path: a name that
// does not resolve must not stall startup.
func TestIsLocalHostDoesNotHang(t *testing.T) {
	done := make(chan struct{})
	go func() {
		_, _ = IsLocalHost("no-such-host.invalid")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hostLookupTimeout + 5*time.Second):
		t.Fatal("IsLocalHost did not return within the lookup timeout")
	}
}

// TestIsLocalHostTrimsWhitespace confirms a stray space in a config value cannot flip the
// verdict to "another machine" and trigger an identity reset.
func TestIsLocalHostTrimsWhitespace(t *testing.T) {
	isLocal, known := IsLocalHost("  127.0.0.1  ")
	if !known || !isLocal {
		t.Fatalf("padded loopback should be local and known, got (%v, %v)", isLocal, known)
	}
}

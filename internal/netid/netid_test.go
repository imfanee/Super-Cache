// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
package netid

import (
	"net"
	"testing"
)

func TestClassifyOrdering(t *testing.T) {
	cases := []struct {
		ip   string
		want int
	}{
		{"10.0.0.5", classPrivateV4},
		{"172.16.4.1", classPrivateV4},
		{"192.168.1.7", classPrivateV4},
		{"100.64.3.9", classCGNATV4},
		{"fd00::1", classULAV6},
		{"89.167.15.232", classGlobalV4},
		{"2606:4700::1111", classGlobalV6},
		{"127.0.0.1", classUnusable},
		{"169.254.1.1", classUnusable},
		{"fe80::1", classUnusable},
		{"0.0.0.0", classUnusable},
		{"224.0.0.1", classUnusable},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test ip %q", tc.ip)
		}
		if got := classify(ip); got != tc.want {
			t.Errorf("classify(%s) = %d, want %d", tc.ip, got, tc.want)
		}
	}
}

func TestPrivateIPv4OutranksPublic(t *testing.T) {
	if classPrivateV4 >= classGlobalV4 {
		t.Fatal("private IPv4 must sort ahead of public IPv4")
	}
	if classCGNATV4 >= classGlobalV4 {
		t.Fatal("CGNAT IPv4 must sort ahead of public IPv4")
	}
}

func TestNodeIDFromIPIsDeterministic(t *testing.T) {
	ip := net.ParseIP("10.20.30.40")
	first := NodeIDFromIP(ip)
	for i := 0; i < 100; i++ {
		if got := NodeIDFromIP(ip); got != first {
			t.Fatalf("node id not stable: %q vs %q", got, first)
		}
	}
	if len(first) != 32 {
		t.Fatalf("node id width = %d, want 32 (must match legacy random ids)", len(first))
	}
}

// The 16-byte/32-hex shape is load-bearing: existing nodes, INFO output, and management
// responses all assume it, so switching to MD5 must not change the format.
func TestNodeIDWidthMatchesRandom(t *testing.T) {
	if len(RandomNodeID()) != len(NodeIDFromIP(net.ParseIP("10.0.0.1"))) {
		t.Fatal("derived and random node id widths differ")
	}
}

func TestNodeIDFromIPCanonicalizesIPv4Form(t *testing.T) {
	// The 4-byte and 16-byte representations of the same IPv4 address must not produce
	// different identities, or a node's id would depend on how its address was parsed.
	a := NodeIDFromIP(net.ParseIP("10.0.0.1"))
	b := NodeIDFromIP(net.IPv4(10, 0, 0, 1).To4())
	if a != b {
		t.Fatalf("IPv4 form affects node id: %q vs %q", a, b)
	}
}

func TestDistinctIPsGiveDistinctNodeIDs(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{"10.0.0.1", "10.0.0.2", "192.168.0.1", "100.64.0.1", "fd00::1"} {
		id := NodeIDFromIP(net.ParseIP(s))
		if prev, dup := seen[id]; dup {
			t.Fatalf("node id collision between %s and %s", prev, s)
		}
		seen[id] = s
	}
}

func TestIsBridgeInterface(t *testing.T) {
	for _, name := range []string{"docker0", "br-1a2b3c", "veth1234", "virbr0", "DOCKER0"} {
		if !isBridgeInterface(name) {
			t.Errorf("%q should be treated as a bridge interface", name)
		}
	}
	for _, name := range []string{"eth0", "ens5", "enp3s0", "bond0", "eth1"} {
		if isBridgeInterface(name) {
			t.Errorf("%q must not be treated as a bridge interface", name)
		}
	}
}

func TestDeriveNodeIDStableAcrossCalls(t *testing.T) {
	id1, ip1 := DeriveNodeID()
	id2, ip2 := DeriveNodeID()
	if len(id1) != 32 {
		t.Fatalf("derived id width = %d, want 32", len(id1))
	}
	if id1 != id2 {
		t.Fatalf("DeriveNodeID not stable: %q vs %q", id1, id2)
	}
	if (ip1 == nil) != (ip2 == nil) {
		t.Fatal("DeriveNodeID ip presence not stable")
	}
	if ip1 != nil && !ip1.Equal(ip2) {
		t.Fatalf("DeriveNodeID ip not stable: %v vs %v", ip1, ip2)
	}
	if ip1 != nil && id1 != NodeIDFromIP(ip1) {
		t.Fatal("derived id does not match its own reported ip")
	}
}

func TestPrimaryIPIsUsable(t *testing.T) {
	ip, err := PrimaryIP()
	if err != nil {
		t.Skip("no usable non-loopback address in this environment")
	}
	if classify(ip) == classUnusable {
		t.Fatalf("PrimaryIP returned unusable address %v", ip)
	}
}

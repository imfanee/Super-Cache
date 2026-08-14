// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for handling a configuration copied off another node.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"net"
	"strings"
	"testing"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/netid"
)

// 203.0.113.0/24 is TEST-NET-3 (RFC 5737): reserved for documentation, so it is never an
// address of the machine running these tests.
const remoteAddr = "203.0.113.7:7379"

const pinnedID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestStaleSelfAddr(t *testing.T) {
	cases := []struct {
		name      string
		advertise string
		want      string
	}{
		{"unset", "", ""},
		// Loopback is unambiguously this machine, so it is this node's own advertisement and
		// not a peer, however the configuration got here.
		{"own loopback address", "127.0.0.1:7379", ""},
		{"address of another machine", remoteAddr, remoteAddr},
		// .invalid is reserved by RFC 2606 and never resolves. An address we cannot evaluate
		// must not be promoted to a peer, nor used as grounds to discard a pinned identity.
		{"unresolvable host", "no-such-host.invalid:7379", ""},
		{"missing port", "203.0.113.7", ""},
		{"empty", "   ", ""},
		{"malformed", ":::", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: c.advertise}
			if got := staleSelfAddr(cfg); got != c.want {
				t.Fatalf("staleSelfAddr(%q) = %q, want %q", c.advertise, got, c.want)
			}
		})
	}
}

// TestStaleSelfAddrAcceptsThisMachinesRealAddress is the regression that matters most for
// existing deployments: a node correctly configured with its own routable address must never be
// mistaken for a copy, or it would reset its identity on every restart.
func TestStaleSelfAddrAcceptsThisMachinesRealAddress(t *testing.T) {
	ip, err := netid.PrimaryIP()
	if err != nil || ip == nil {
		t.Skip("host has no usable address")
	}
	cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: net.JoinHostPort(ip.String(), "7379")}
	if got := staleSelfAddr(cfg); got != "" {
		t.Fatalf("this machine's own primary address was treated as another machine's: %q", got)
	}
}

// TestResolveIdentityClonedConfig covers the whole point of the feature. A VM created from
// another node's snapshot carries that node's advertise_addr, which must become a dial
// candidate rather than this node's own advertisement — advertising it would point every peer
// back at the original and leave this node permanently unable to receive replication.
func TestResolveIdentityClonedConfig(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: remoteAddr}
	nodeID, advertise, seeds := resolveIdentity(cfg)
	if nodeID == "" {
		t.Fatal("expected a node identity")
	}
	if advertise == remoteAddr {
		t.Fatal("must not advertise an address belonging to another machine")
	}
	if len(seeds) != 1 || seeds[0] != remoteAddr {
		t.Fatalf("expected the copied address as a seed, got %v", seeds)
	}
}

// TestResolveIdentityClonedConfigKeepsPinnedNodeID states the trade deliberately. An address
// missing from this host's interfaces is not proof the file was copied: a floating address the
// node does not currently hold looks exactly the same. Overriding a pinned identity on that
// evidence would silently rename a correctly configured node, so the identity stands and the
// situation is reported instead.
func TestResolveIdentityClonedConfigKeepsPinnedNodeID(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: remoteAddr, NodeID: pinnedID}
	nodeID, advertise, seeds := resolveIdentity(cfg)
	if nodeID != pinnedID {
		t.Fatalf("a pinned node_id must be kept, got %q", nodeID)
	}
	// The advertisement is still corrected, since announcing an address this machine does not
	// hold would point every peer at the wrong node.
	if advertise == remoteAddr {
		t.Fatal("must not advertise an address belonging to another machine")
	}
	if len(seeds) != 1 || seeds[0] != remoteAddr {
		t.Fatalf("expected the copied address as a seed, got %v", seeds)
	}
}

// TestResolveIdentityPinnedNodeIDKept confirms the discard above is scoped to copied
// configurations and does not disturb an operator who pins an identity deliberately. The case
// with no advertise_addr at all matters most: there is no evidence of copying, so nothing may
// be second-guessed.
func TestResolveIdentityPinnedNodeIDKept(t *testing.T) {
	cases := []struct {
		name      string
		advertise string
	}{
		{"no advertise configured", ""},
		{"advertising our own address", "127.0.0.1:7379"},
		{"advertising an unresolvable name", "no-such-host.invalid:7379"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: c.advertise, NodeID: pinnedID}
			nodeID, _, seeds := resolveIdentity(cfg)
			if nodeID != pinnedID {
				t.Fatalf("expected pinned node_id to be kept, got %q", nodeID)
			}
			if len(seeds) != 0 {
				t.Fatalf("expected no seeds, got %v", seeds)
			}
		})
	}
}

// TestResolveIdentityKeepsOurOwnAdvertise is the plain, correct configuration.
func TestResolveIdentityKeepsOurOwnAdvertise(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: "127.0.0.1:7379"}
	_, advertise, seeds := resolveIdentity(cfg)
	if advertise != "127.0.0.1:7379" {
		t.Fatalf("expected our own configured advertise_addr to be kept, got %q", advertise)
	}
	if len(seeds) != 0 {
		t.Fatalf("expected no seeds, got %v", seeds)
	}
}

// TestResolveIdentityDerivedAdvertise covers the default configuration, where the advertisement
// comes from a local interface and therefore can never be a peer.
func TestResolveIdentityDerivedAdvertise(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379}
	nodeID, advertise, seeds := resolveIdentity(cfg)
	if nodeID == "" {
		t.Fatal("expected a node identity")
	}
	if len(seeds) != 0 {
		t.Fatalf("expected no seeds when advertise_addr is derived, got %v", seeds)
	}
	if advertise != "" && !strings.Contains(advertise, ":") {
		t.Fatalf("derived advertise_addr %q is not host:port", advertise)
	}
}

// TestResolveIdentityInvalidAdvertiseIsDropped confirms a malformed value is neither advertised
// nor treated as another machine's address.
func TestResolveIdentityInvalidAdvertiseIsDropped(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379, AdvertiseAddr: "not-a-valid-address"}
	_, advertise, seeds := resolveIdentity(cfg)
	if advertise != "" {
		t.Fatalf("expected invalid advertise_addr to be dropped, got %q", advertise)
	}
	if len(seeds) != 0 {
		t.Fatalf("expected no seeds from an invalid address, got %v", seeds)
	}
}

// TestResolveIdentityIsStable guards against the identity churning between restarts, which
// would break link de-duplication and leave stale peers behind on every reboot.
func TestResolveIdentityIsStable(t *testing.T) {
	cfg := &config.Config{PeerPort: 7379}
	first, firstAdv, _ := resolveIdentity(cfg)
	second, secondAdv, _ := resolveIdentity(cfg)
	if first != second {
		t.Fatalf("node identity is not stable: %q then %q", first, second)
	}
	if firstAdv != secondAdv {
		t.Fatalf("advertise address is not stable: %q then %q", firstAdv, secondAdv)
	}
}

// TestNewWiresSeeds checks the seed actually reaches the server that will dial it, rather than
// being computed and dropped.
func TestNewWiresSeeds(t *testing.T) {
	cfg := &config.Config{
		ClientPort:    0,
		PeerPort:      7379,
		SharedSecret:  strings.Repeat("s", 32),
		AdvertiseAddr: remoteAddr,
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(s.peerSeeds) != 1 || s.peerSeeds[0] != remoteAddr {
		t.Fatalf("expected seed %q on the server, got %v", remoteAddr, s.peerSeeds)
	}
}

// TestNewWithoutSeeds is the same path for an ordinary node.
func TestNewWithoutSeeds(t *testing.T) {
	cfg := &config.Config{
		ClientPort:   0,
		PeerPort:     7379,
		SharedSecret: strings.Repeat("s", 32),
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(s.peerSeeds) != 0 {
		t.Fatalf("expected no seeds, got %v", s.peerSeeds)
	}
}

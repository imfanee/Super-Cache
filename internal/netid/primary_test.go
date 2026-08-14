// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for choosing this node's address on the cluster network.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package netid

import (
	"net"
	"testing"
)

// cand builds a classified candidate the way localCandidates would.
func cand(s string) candidate {
	ip := normalize(net.ParseIP(s))
	return candidate{ip: ip, class: classify(ip)}
}

// sorted mirrors the ordering localCandidates guarantees, since selectPrimary relies on it.
func sorted(addrs ...string) []candidate {
	var out []candidate
	for _, a := range addrs {
		out = append(out, cand(a))
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].class < out[j-1].class; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestSelectPrimaryDualHomedPrefersPrivate is the case this function exists for. A host with a
// public and a private interface routes to the internet over the public one, so the default
// route's source address is public — but cluster traffic belongs on the private network.
func TestSelectPrimaryDualHomedPrefersPrivate(t *testing.T) {
	cands := sorted("10.0.0.11", "5.161.241.68")
	got := selectPrimary(cands, net.ParseIP("5.161.241.68"))
	if got.String() != "10.0.0.11" {
		t.Fatalf("dual-homed host should identify as its private address, got %s", got)
	}
}

// TestSelectPrimaryPublicOnlyUsesPublic confirms a single-homed public host is unaffected, so
// upgrading such a node does not change its identity.
func TestSelectPrimaryPublicOnlyUsesPublic(t *testing.T) {
	cands := sorted("5.161.241.68")
	got := selectPrimary(cands, net.ParseIP("5.161.241.68"))
	if got.String() != "5.161.241.68" {
		t.Fatalf("public-only host should identify as its public address, got %s", got)
	}
}

// TestSelectPrimaryPrivateOnlyUnchanged is the other single-homed shape.
func TestSelectPrimaryPrivateOnlyUnchanged(t *testing.T) {
	cands := sorted("10.0.0.11")
	got := selectPrimary(cands, net.ParseIP("10.0.0.11"))
	if got.String() != "10.0.0.11" {
		t.Fatalf("private-only host should identify as its private address, got %s", got)
	}
}

// TestSelectPrimaryRouteBreaksTieWithinClass is where the routing table is genuinely
// informative: several private interfaces, and the default route names the real one.
func TestSelectPrimaryRouteBreaksTieWithinClass(t *testing.T) {
	cands := sorted("10.0.0.11", "192.168.50.4", "172.16.3.9")
	got := selectPrimary(cands, net.ParseIP("192.168.50.4"))
	if got.String() != "192.168.50.4" {
		t.Fatalf("route source should break ties within the best class, got %s", got)
	}
}

// TestSelectPrimaryNoRouteFallsBackToBestClass covers a host with no default route.
func TestSelectPrimaryNoRouteFallsBackToBestClass(t *testing.T) {
	cands := sorted("10.0.0.11", "5.161.241.68")
	got := selectPrimary(cands, nil)
	if got.String() != "10.0.0.11" {
		t.Fatalf("without a route source the best class should win, got %s", got)
	}
}

// TestSelectPrimaryRouteOutsideBestClassIgnored states the rule directly: a route source in a
// worse class must never be selected while a better-classed address exists.
func TestSelectPrimaryRouteOutsideBestClassIgnored(t *testing.T) {
	cands := sorted("100.64.0.7", "5.161.241.68")
	got := selectPrimary(cands, net.ParseIP("5.161.241.68"))
	if got.String() != "100.64.0.7" {
		t.Fatalf("CGNAT address should outrank a public route source, got %s", got)
	}
}

// TestSelectPrimaryIsDeterministic guards the identity against changing between restarts when
// the host has several equally-classed addresses and no route source to choose among them.
func TestSelectPrimaryIsDeterministic(t *testing.T) {
	cands := sorted("10.0.0.11", "10.0.0.12", "10.0.0.13")
	first := selectPrimary(cands, nil)
	for i := 0; i < 20; i++ {
		if got := selectPrimary(cands, nil); !got.Equal(first) {
			t.Fatalf("selection is not deterministic: %s then %s", first, got)
		}
	}
}

// TestPrimaryIPPrefersPrivateOnThisHost applies the rule to the real machine: whenever this
// host has a private address, that is what it must identify as.
func TestPrimaryIPPrefersPrivateOnThisHost(t *testing.T) {
	cands := localCandidates()
	if len(cands) == 0 || cands[0].class != classPrivateV4 {
		t.Skip("host has no private IPv4 address")
	}
	ip, err := PrimaryIP()
	if err != nil {
		t.Fatalf("PrimaryIP: %v", err)
	}
	if classify(ip) != classPrivateV4 {
		t.Fatalf("host has a private address but identified as %s", ip)
	}
}

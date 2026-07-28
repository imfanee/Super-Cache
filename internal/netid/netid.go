// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Local address discovery and deterministic node identity derivation.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package netid

import (
	"crypto/md5" // #nosec G501 -- identity digest only, not a security primitive
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
)

// bridgePrefixes are interface name prefixes for container/VM bridges and virtual links.
// Their addresses are host-local plumbing (docker0 is typically 172.17.0.1) and must never
// be mistaken for the node's real address on the cluster network.
var bridgePrefixes = []string{
	"docker", "br-", "veth", "cni", "flannel", "virbr", "vmnet", "tun", "tap", "kube",
}

// addrClass ranks a candidate address; lower sorts first.
const (
	classPrivateV4 = iota // RFC1918
	classCGNATV4          // RFC6598 100.64.0.0/10, common in cloud fabrics
	classULAV6            // RFC4193 fc00::/7
	classGlobalV4         // routable IPv4 ("public IP" fallback)
	classGlobalV6         // routable IPv6
	classUnusable
)

type candidate struct {
	ip    net.IP
	class int
}

// cgnatNet is RFC6598 shared address space, which net.IP.IsPrivate does not cover.
var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func classify(ip net.IP) int {
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return classUnusable
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4.IsPrivate():
			return classPrivateV4
		case cgnatNet.Contains(v4):
			return classCGNATV4
		default:
			return classGlobalV4
		}
	}
	if ip.IsPrivate() {
		return classULAV6
	}
	return classGlobalV6
}

func isBridgeInterface(name string) bool {
	n := strings.ToLower(name)
	for _, p := range bridgePrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// localCandidates enumerates usable unicast addresses on up, non-bridge interfaces,
// ordered by class then by address bytes so the result is stable across restarts and
// across kernel interface reordering.
func localCandidates() []candidate {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []candidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isBridgeInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			cls := classify(ip)
			if cls == classUnusable {
				continue
			}
			out = append(out, candidate{ip: normalize(ip), class: cls})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].class != out[j].class {
			return out[i].class < out[j].class
		}
		return out[i].ip.String() < out[j].ip.String()
	})
	return out
}

// normalize returns the 4-byte form for IPv4 so String() and comparisons are canonical.
func normalize(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// routeSourceIP asks the kernel which source address it would use to reach a public
// destination. Connecting a UDP socket performs only a route lookup and sends no packet,
// so this is free and works even with no network reachability. Returns nil when there is
// no default route.
func routeSourceIP() net.IP {
	for _, probe := range []string{"8.8.8.8:53", "[2001:4860:4860::8888]:53"} {
		c, err := net.Dial("udp", probe)
		if err != nil {
			continue
		}
		ua, ok := c.LocalAddr().(*net.UDPAddr)
		_ = c.Close()
		if !ok || ua.IP == nil {
			continue
		}
		if classify(ua.IP) == classUnusable {
			continue
		}
		return normalize(ua.IP)
	}
	return nil
}

// PrimaryIP returns this node's stable address on the cluster network: the source address
// of the default route when that address is also present on a real interface, otherwise the
// best-classed enumerated address (private IPv4 first, public IPv4 as fallback).
func PrimaryIP() (net.IP, error) {
	cands := localCandidates()
	if len(cands) == 0 {
		return nil, fmt.Errorf("netid: no usable non-loopback address found")
	}
	// The routing table is the most accurate signal for which of several addresses is the
	// node's real identity, but only trust it when it agrees with a live interface.
	if src := routeSourceIP(); src != nil {
		for _, c := range cands {
			if c.ip.Equal(src) {
				return c.ip, nil
			}
		}
	}
	return cands[0].ip, nil
}

// NodeIDFromIP returns the 32-hex-character MD5 digest of the canonical IP string.
// MD5 is used purely to map an address to a fixed-width opaque identifier; it carries no
// security property here. The width matches the previous random node IDs, so anything that
// stores or displays a node ID is unaffected.
func NodeIDFromIP(ip net.IP) string {
	sum := md5.Sum([]byte(normalize(ip).String())) // #nosec G401 -- identity digest only
	return hex.EncodeToString(sum[:])
}

// RandomNodeID returns a random 32-hex-character identifier, used only when no usable
// local address exists to derive a stable one from.
func RandomNodeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// DeriveNodeID returns a node identity that is stable across restarts of this host.
//
// The identity is MD5(primary IP): the private IPv4 address when one exists, otherwise the
// public address. An autoscaled instance therefore keeps one identity for its whole life and
// a replacement instance with a new IP is correctly seen as a different node. The chosen IP
// is returned so callers can use it as the default peer advertisement host.
//
// Falls back to a random identity (and a nil IP) when the host has no usable address.
func DeriveNodeID() (string, net.IP) {
	ip, err := PrimaryIP()
	if err != nil || ip == nil {
		return RandomNodeID(), nil
	}
	return NodeIDFromIP(ip), ip
}

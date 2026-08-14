// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Local address discovery and deterministic node identity derivation.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package netid

import (
	"context"
	"crypto/md5" // #nosec G501 -- identity digest only, not a security primitive
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
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

// PrimaryIP returns this node's stable address on the cluster network: the best-classed
// enumerated address, which is a private IPv4 address whenever the host has one, with the
// routing table breaking ties between addresses of that same class.
//
// Address class is decided before the routing table on purpose. A host with separate public and
// private interfaces routes to the internet over the public one, so the source address of the
// default route is its public address, while cluster traffic belongs on the private network.
// Preferring the route source outright would give such a node a public identity and make it
// advertise a public address, sending replication over the internet — in plaintext unless peer
// TLS is configured, and at whatever the provider charges for egress.
//
// The routing table still decides between equally-classed addresses, which is where it is
// genuinely informative: a host with several private interfaces is best identified by the one
// its default route uses.
func PrimaryIP() (net.IP, error) {
	cands := localCandidates()
	if len(cands) == 0 {
		return nil, fmt.Errorf("netid: no usable non-loopback address found")
	}
	return selectPrimary(cands, routeSourceIP()), nil
}

// selectPrimary picks the node's address from class-sorted candidates, letting src break ties
// within the best class only. cands must be non-empty and ordered as localCandidates orders it.
func selectPrimary(cands []candidate, src net.IP) net.IP {
	// localCandidates sorts by class, so the first entry names the best class available.
	best := cands[0].class
	if src != nil {
		for _, c := range cands {
			if c.class != best {
				break
			}
			if c.ip.Equal(src) {
				return c.ip
			}
		}
	}
	return cands[0].ip
}

// allLocalIPs enumerates every address held by this machine, including loopback and bridge
// interfaces. This is deliberately broader than localCandidates: the question it answers is
// "does this host own this address", not "is this a good address to advertise on". An address
// on docker0 still belongs to this machine and must never be mistaken for a remote node.
func allLocalIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
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
			if ip != nil {
				out = append(out, normalize(ip))
			}
		}
	}
	return out
}

// containsIP reports whether have holds want.
func containsIP(have []net.IP, want net.IP) bool {
	want = normalize(want)
	for _, ip := range have {
		if ip.Equal(want) {
			return true
		}
	}
	return false
}

// IsLocalIP reports whether ip is held by any interface on this machine.
//
// A false result is what tells a node booted from another node's snapshot that the address its
// configuration calls "me" actually belongs to a different machine, which is very likely a live
// peer worth dialing.
func IsLocalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return containsIP(allLocalIPs(), ip)
}

// hostLookupTimeout bounds name resolution during identity resolution. This runs before the
// node serves anything, so an unreachable resolver must not be able to stall startup: an
// unanswered name is reported as unknown, which leaves the configured value untouched.
const hostLookupTimeout = 3 * time.Second

// IsLocalHost reports whether host — an IP literal or a resolvable name — refers to this
// machine, and whether that could be determined at all.
//
// The second return value is false when host is a name that does not resolve, or does not
// resolve promptly. Callers must treat that as "unknown" rather than "remote": guessing remote
// would turn an unresolvable name into a bogus peer address and discard a pinned identity on
// no evidence.
func IsLocalHost(host string) (isLocal bool, known bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		return false, false
	}
	local := allLocalIPs()
	if ip := net.ParseIP(host); ip != nil {
		return containsIP(local, ip), true
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostLookupTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return false, false
	}
	for _, ip := range ips {
		if containsIP(local, ip) {
			return true, true
		}
	}
	return false, true
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

// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Node identity helpers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/netid"
)

func randomNodeID() string {
	return netid.RandomNodeID()
}

// resolveIdentity determines this node's cluster identity and the address peers should dial
// to reach it.
//
// Both are derived from the node's primary IP by default so that an autoscaled instance
// booted from a snapshot image needs no per-node configuration: it discovers its own address
// at startup and keeps the same identity across restarts. Explicit config values win, except
// when the file describes a different machine — see staleSelfAddr.
//
// A node with no usable address still starts, with a random identity and no advertisement;
// it simply cannot be discovered by peers until it is given one.
//
// The third return value lists addresses worth dialing that are absent from the peers list. It
// holds the address the configuration claimed for this node when that address turns out to
// belong to a different machine. The machine the file was copied from was by definition healthy
// enough to be imaged, which makes it a better than average place to look for the cluster.
func resolveIdentity(cfg *config.Config) (nodeID string, advertise string, seeds []string) {
	ip, ipErr := netid.PrimaryIP()
	derived := ""
	if ipErr == nil && ip != nil {
		derived = net.JoinHostPort(ip.String(), strconv.Itoa(cfg.PeerPort))
	}

	stale := staleSelfAddr(cfg)

	nodeID = strings.TrimSpace(cfg.NodeID)
	switch {
	case nodeID != "" && stale != "":
		// A pinned identity travels with a copied file exactly as the address does. Two nodes
		// sharing an identity are folded into a single link by the replication de-duplication
		// and each treats the other as itself, so writes flow one way and the copy silently
		// never receives any. Deriving a fresh identity is the only safe reading.
		nodeID = ""
		slog.Error("ignoring node_id from a configuration that describes another machine; deriving a new identity",
			"configured_node_id", strings.TrimSpace(cfg.NodeID), "configured_advertise_addr", stale)
		fallthrough
	case nodeID == "":
		if ipErr == nil && ip != nil {
			nodeID = netid.NodeIDFromIP(ip)
			slog.Info("node identity derived from primary address", "node_id", nodeID, "ip", ip.String())
		} else {
			nodeID = netid.RandomNodeID()
			slog.Warn("no usable local address; node identity is random and will change on restart",
				"node_id", nodeID, "err", ipErr)
		}
	default:
		slog.Info("node identity pinned by configuration", "node_id", nodeID)
	}

	advertise = strings.TrimSpace(cfg.AdvertiseAddr)
	switch {
	case stale != "":
		// Advertising an address this machine does not hold points every peer at the machine
		// the file came from, so this node would never receive replication. It also makes the
		// node consider that address to be itself, which would discard the very seed below.
		advertise = derived
		slog.Warn("configured advertise_addr is not an address of this machine, so it is treated as "+
			"copied from another node; advertising this machine's own address instead",
			"configured_advertise_addr", stale, "advertise_addr", advertise)
	case advertise != "":
		slog.Info("peer advertisement pinned by configuration", "advertise_addr", advertise)
	case derived != "":
		advertise = derived
		slog.Info("peer advertisement derived from primary address", "advertise_addr", advertise)
	default:
		slog.Warn("no usable local address; this node will not advertise a peer address", "err", ipErr)
	}

	if advertise != "" {
		if err := config.ValidatePeerAddr(advertise); err != nil {
			slog.Error("derived advertise address is invalid; peers cannot discover this node",
				"advertise_addr", advertise, "err", err)
			advertise = ""
		}
	}

	if stale != "" {
		seeds = []string{stale}
	}
	return nodeID, advertise, seeds
}

// staleSelfAddr returns the advertise address written in the configuration file when that
// address belongs to some other machine, and "" otherwise.
//
// A non-empty result is the signature of a configuration copied from another node, or of a VM
// created from another node's snapshot: the file still names the original's address as its own.
//
// Nothing is reported when the file names no address, when the address is one this host holds,
// or when the host part cannot be resolved — an unresolvable name must not be turned into a
// bogus peer, nor into a reason to discard a pinned identity.
//
// This makes an address that this machine does not hold unusable as an advertisement, which
// rules out advertising an address only reachable from elsewhere, such as one in front of NAT
// or a load balancer. That trade is deliberate: a copied configuration is silent and likely,
// whereas a proxied peer port is loud and rare, and would need its own explicit setting.
func staleSelfAddr(cfg *config.Config) string {
	configured := strings.TrimSpace(cfg.AdvertiseAddr)
	if configured == "" {
		return ""
	}
	if err := config.ValidatePeerAddr(configured); err != nil {
		return ""
	}
	host, _, err := net.SplitHostPort(configured)
	if err != nil {
		return ""
	}
	isLocal, known := netid.IsLocalHost(host)
	if !known {
		slog.Warn("configured advertise_addr host could not be resolved; leaving it in place",
			"advertise_addr", configured)
		return ""
	}
	if isLocal {
		return ""
	}
	return configured
}

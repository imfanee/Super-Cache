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
// at startup and keeps the same identity across restarts. Explicit config values win, which
// covers nodes reachable only at an address they cannot observe locally (NAT, load balancer).
//
// A node with no usable address still starts, with a random identity and no advertisement;
// it simply cannot be discovered by peers until it is given one.
func resolveIdentity(cfg *config.Config) (nodeID string, advertise string) {
	ip, ipErr := netid.PrimaryIP()

	nodeID = strings.TrimSpace(cfg.NodeID)
	switch {
	case nodeID != "":
		slog.Info("node identity pinned by configuration", "node_id", nodeID)
	case ipErr == nil && ip != nil:
		nodeID = netid.NodeIDFromIP(ip)
		slog.Info("node identity derived from primary address", "node_id", nodeID, "ip", ip.String())
	default:
		nodeID = netid.RandomNodeID()
		slog.Warn("no usable local address; node identity is random and will change on restart",
			"node_id", nodeID, "err", ipErr)
	}

	advertise = strings.TrimSpace(cfg.AdvertiseAddr)
	switch {
	case advertise != "":
		slog.Info("peer advertisement pinned by configuration", "advertise_addr", advertise)
	case ipErr == nil && ip != nil:
		advertise = net.JoinHostPort(ip.String(), strconv.Itoa(cfg.PeerPort))
		slog.Info("peer advertisement derived from primary address", "advertise_addr", advertise)
	default:
		slog.Warn("no usable local address; this node will not advertise a peer address",
			"err", ipErr)
	}

	if advertise != "" {
		if err := config.ValidatePeerAddr(advertise); err != nil {
			slog.Error("derived advertise address is invalid; peers cannot discover this node",
				"advertise_addr", advertise, "err", err)
			advertise = ""
		}
	}
	return nodeID, advertise
}

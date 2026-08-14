// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Periodic peer discovery from providers outside the configuration file.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/discovery"
	"github.com/supercache/supercache/internal/peer"
)

// hcloudTokenEnv is the environment variable the Hetzner CLI uses, accepted here so a token need
// not be written into a machine image.
const hcloudTokenEnv = "HCLOUD_TOKEN"

// discoveryProviders builds the providers enabled by configuration. An empty result means peers
// come only from configuration and from nodes that connect here.
func discoveryProviders(c *config.Config) []discovery.Provider {
	if c == nil {
		return nil
	}
	token := strings.TrimSpace(c.HetznerAPIToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(hcloudTokenEnv))
	}
	if token == "" {
		return nil
	}
	if strings.TrimSpace(c.HetznerLabelSelector) == "" {
		slog.Warn("hetzner discovery has no label selector, so every server in the project is a " +
			"peer candidate; set hetzner_label_selector to scope it to this cluster")
	}
	return []discovery.Provider{&discovery.Hetzner{
		Token:         token,
		LabelSelector: strings.TrimSpace(c.HetznerLabelSelector),
		NetworkID:     c.HetznerNetworkID,
		Port:          c.PeerPort,
		BaseURL:       strings.TrimSpace(c.HetznerAPIURL),
	}}
}

// runPeerDiscovery re-lists peers from providers until the server stops.
//
// Listing repeatedly, rather than once at startup, is what lets a node rejoin after every
// address it knew has been replaced. Its own dial loops keep retrying the old addresses forever
// and no node that no longer exists will ever connect back to tell it otherwise, so without a
// fresh listing an isolated node stays isolated.
func (s *Server) runPeerDiscovery(ctx context.Context, providers []discovery.Provider) {
	interval := time.Duration(s.config().DiscoveryInterval) * time.Second
	if interval <= 0 {
		return
	}
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	slog.Info("peer discovery enabled", "providers", strings.Join(names, ","), "interval", interval)

	// The first listing runs immediately: at startup it is what a node created from an image
	// with a stale peer list depends on to find anyone at all.
	s.discoverOnce(ctx, providers)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.discoverOnce(ctx, providers)
		}
	}
}

// discoverOnce lists every provider and adds anything new.
func (s *Server) discoverOnce(ctx context.Context, providers []discovery.Provider) {
	for _, p := range providers {
		listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		addrs, err := p.Peers(listCtx)
		cancel()
		if err != nil {
			// A provider being unavailable is not fatal: configured peers and inbound
			// connections still work, and the next tick tries again.
			slog.Warn("peer discovery failed", "provider", p.Name(), "err", err)
			continue
		}
		s.discoveryListed.Store(true)
		added := 0
		for _, addr := range addrs {
			// AddPeerFrom rejects this node's own address and anything already known, so a
			// listing that is mostly unchanged costs nothing.
			if err := s.peer.AddPeerFrom(addr, peer.SourceDiscovery); err != nil {
				continue
			}
			added++
			slog.Info("discovered peer", "provider", p.Name(), "addr", addr)
		}
		// A listing that succeeded is authoritative about what exists, so anything it once
		// contributed and no longer names has been destroyed and can be dropped. Doing this
		// only after a successful call matters: a failed one says nothing at all.
		removed := s.peer.ForgetPeersMissingFrom(addrs)
		s.stats.setDiscoveredPeers(int64(len(addrs)))
		if added > 0 || len(removed) > 0 {
			slog.Info("peer discovery updated membership", "provider", p.Name(),
				"added", added, "removed", len(removed), "listed", len(addrs))
		}
	}
}

// runPeerReaper removes learned peers that have been unreachable long enough to be considered
// gone, for as long as the server runs.
//
// This is the half of membership that discovery cannot cover: a peer learned from an inbound
// connection was never in any listing, so only the passage of time can say it is no longer
// there. The window is deliberately far longer than a restart or a deploy.
func (s *Server) runPeerReaper(ctx context.Context) {
	after := time.Duration(s.config().PeerForgetAfter) * time.Second
	if after <= 0 {
		return
	}
	// Checking far more often than the window costs nothing and keeps removal prompt once an
	// address does age out.
	every := after / 10
	if every < time.Minute {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.peer.ForgetUnreachablePeers(after)
		}
	}
}

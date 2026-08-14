// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Removing peers that have gone away, so an autoscaled fleet does not accumulate dead addresses.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"log/slog"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/config"
)

// Peers are added by discovery and by nodes connecting here, and until now nothing ever removed
// one. Every instance an autoscaler destroyed left an address behind for good, persisted across
// restarts by the peer state file, each with a goroutine redialling it forever. Over a fleet
// that turns over regularly the list only grows.
//
// Forgetting is safe because it is recoverable rather than permanent: a node that comes back
// connects here and is learned again, and a provider that lists it again re-adds it. That is
// what makes it reasonable to act on evidence that is merely strong rather than certain.

// connectedAddrs reports the addresses currently carrying a live link.
func (s *Service) connectedAddrs() map[string]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]struct{}, len(s.out))
	for _, p := range s.out {
		out[config.NormalizePeerAddr(p.addr)] = struct{}{}
	}
	return out
}

// forgetCandidates returns the peers that may be removed, given a predicate over their record.
//
// One peer is always kept. A node that forgets its last peer has no way back into the cluster
// except by being contacted, so if both sides of a long partition reasoned their way to an empty
// list neither would ever reconnect. Keeping one thread costs nothing and removes that
// possibility entirely.
func (s *Service) forgetCandidates(want func(m *peerMeta, connected bool) bool) []string {
	connected := s.connectedAddrs()
	known := s.ConfigPeerAddrs()

	s.peerMetaMu.Lock()
	defer s.peerMetaMu.Unlock()

	var drop []string
	remaining := len(known)
	for _, addr := range known {
		if remaining <= 1 {
			break
		}
		key := config.NormalizePeerAddr(addr)
		m, ok := s.peerMeta[key]
		if !ok {
			// No record at all, so nothing is known about where it came from. Treat that as
			// operator intent rather than guessing.
			continue
		}
		if !m.source.forgettable() {
			continue
		}
		_, live := connected[key]
		if !want(m, live) {
			continue
		}
		drop = append(drop, addr)
		delete(s.peerMeta, key)
		remaining--
	}
	return drop
}

// ForgetUnreachablePeers removes learned peers that have not completed a handshake for longer
// than after. Addresses from the configuration file and from the management API are exempt.
//
// Returns the addresses removed.
func (s *Service) ForgetUnreachablePeers(after time.Duration) []string {
	if after <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-after).UnixMilli()
	drop := s.forgetCandidates(func(m *peerMeta, connected bool) bool {
		if connected {
			return false
		}
		// An address that has never once worked is judged from when it was added, otherwise
		// from when it last did.
		last := m.lastOK
		if last == 0 {
			last = m.added
		}
		return last < cutoff
	})
	for _, addr := range drop {
		slog.Info("forgetting peer that has been unreachable for too long; "+
			"it will be added again if it returns or is discovered", "addr", addr, "after", after)
		_ = s.RemovePeer(addr)
	}
	return drop
}

// ForgetPeersMissingFrom removes discovered peers that a fresh listing no longer contains.
//
// The listing is authoritative in a way a timeout is not: a machine absent from the inventory
// does not exist. Only addresses that discovery itself contributed are considered, because a
// peer learned from an inbound connection may legitimately sit outside the label selector, and
// a peer currently connected is kept whatever the listing says, since a live link is better
// evidence than any inventory.
//
// Called only after a listing that succeeded; a failed one says nothing.
func (s *Service) ForgetPeersMissingFrom(listed []string) []string {
	present := make(map[string]struct{}, len(listed))
	for _, a := range listed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		present[config.NormalizePeerAddr(a)] = struct{}{}
	}
	drop := s.forgetCandidates(func(m *peerMeta, connected bool) bool {
		if connected || m.source != SourceDiscovery {
			return false
		}
		_, ok := present[config.NormalizePeerAddr(m.addr)]
		return !ok
	})
	for _, addr := range drop {
		slog.Info("forgetting peer that discovery no longer lists", "addr", addr)
		_ = s.RemovePeer(addr)
	}
	return drop
}

// PeerSourceOf reports how an address became known, for tests and diagnostics.
func (s *Service) PeerSourceOf(addr string) (PeerSource, bool) {
	s.peerMetaMu.Lock()
	defer s.peerMetaMu.Unlock()
	m, ok := s.peerMeta[config.NormalizePeerAddr(addr)]
	if !ok {
		return "", false
	}
	return m.source, true
}

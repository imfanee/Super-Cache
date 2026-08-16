// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Runtime metrics for INFO and server observability.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/supercache/supercache/internal/commands"
	"github.com/supercache/supercache/internal/peer"
)

// stats implements commands.InfoProvider with atomic counters.
type stats struct {
	nodeID       string
	clientPort   int
	buildVersion string
	buildMu      sync.RWMutex
	start        time.Time

	activeClients atomic.Int64
	totalConns    atomic.Int64
	totalCmds     atomic.Int64

	keyspaceHits   atomic.Int64
	keyspaceMisses atomic.Int64

	peerMu       sync.RWMutex
	peerInbound  atomic.Int64 // updated by peer.Service (inbound replication conns)
	peerOutAddrs []string     // connected outbound mesh addresses

	bootstrapState atomic.Value // string: standalone | syncing | ready

	bootstrapBytes atomic.Int64
	bootstrapKeys  atomic.Int64

	bootstrapReplDepth atomic.Int64

	// Replication events this node failed to hand to a peer. Both are silent data loss: the
	// local write succeeded, the peer never saw it, and no receiver reads the sequence numbers
	// that would otherwise reveal the gap.
	replDropped    atomic.Int64
	replSendErrors atomic.Int64

	// Replication events a peer sent that never reached this node, detected by a skip in its
	// sequence numbers, plus stragglers arriving behind a sequence already seen.
	discoveredPeers  atomic.Int64
	resyncs          atomic.Int64
	replGapEvents    atomic.Int64
	replMissedEvents atomic.Int64
	replLateEvents   atomic.Int64
}

func newStats(nodeID string, clientPort int) *stats {
	s := &stats{
		nodeID:     nodeID,
		clientPort: clientPort,
		start:      time.Now(),
	}
	s.bootstrapState.Store("standalone")
	return s
}

func (s *stats) setBootstrapState(state string) {
	s.bootstrapState.Store(state)
}

func (s *stats) clientConnected() {
	s.activeClients.Add(1)
	s.totalConns.Add(1)
}

func (s *stats) clientDisconnected() {
	s.activeClients.Add(-1)
}

func (s *stats) commandExecuted() {
	s.totalCmds.Add(1)
}

// SetReplicationStats implements peer.PeerMetrics.
func (s *stats) SetReplicationStats(inboundConnected int, outboundConnectedAddrs []string) {
	s.peerInbound.Store(int64(inboundConnected))
	s.peerMu.Lock()
	s.peerOutAddrs = append([]string(nil), outboundConnectedAddrs...)
	s.peerMu.Unlock()
}

// SetBootstrapInboundQueueDepth implements peer.PeerMetrics (P2.3).
func (s *stats) SetBootstrapInboundQueueDepth(depth int) {
	s.bootstrapReplDepth.Store(int64(depth))
}

// AddReplicationDropped implements peer.PeerMetrics.
func (s *stats) AddReplicationDropped(n int64) {
	s.replDropped.Add(n)
}

// AddReplicationSendError implements peer.PeerMetrics.
func (s *stats) AddReplicationSendError(n int64) {
	s.replSendErrors.Add(n)
}

// setDiscoveredPeers records how many addresses the last discovery listing returned. A drop to
// zero on a fleet that should have peers means discovery is answering but finding nothing,
// which looks identical to a healthy standalone node unless it is measured.
func (s *stats) setDiscoveredPeers(n int64) {
	s.discoveredPeers.Store(n)
}

// addResync counts a dataset refetch triggered by confirmed replication loss. Any occurrence is
// worth an alert: it means events were lost, not merely delayed.
func (s *stats) addResync(n int64) {
	s.resyncs.Add(n)
}

// Resyncs returns how many times this node has refetched after confirmed loss.
func (s *stats) Resyncs() int64 {
	return s.resyncs.Load()
}

// DiscoveredPeers returns the size of the last discovery listing.
func (s *stats) DiscoveredPeers() int64 {
	return s.discoveredPeers.Load()
}

// AddReplicationGap implements peer.PeerMetrics. It records that a gap was observed, which is
// not the same as data being lost: the missing events are usually just moments behind.
func (s *stats) AddReplicationGap(missed int64) {
	s.replGapEvents.Add(1)
}

// AddReplicationLost implements peer.PeerMetrics. This counts events confirmed never to have
// arrived, which is the number worth alerting on.
func (s *stats) AddReplicationLost(n int64) {
	s.replMissedEvents.Add(n)
}

// AddReplicationLate implements peer.PeerMetrics.
func (s *stats) AddReplicationLate(n int64) {
	s.replLateEvents.Add(n)
}

// ReplicationGapEvents returns how many times a peer's sequence numbers skipped forward.
func (s *stats) ReplicationGapEvents() int64 {
	return s.replGapEvents.Load()
}

// ReplicationMissedEvents returns how many replication events never arrived, in total.
func (s *stats) ReplicationMissedEvents() int64 {
	return s.replMissedEvents.Load()
}

// ReplicationLateEvents returns events that arrived behind a sequence already seen.
func (s *stats) ReplicationLateEvents() int64 {
	return s.replLateEvents.Load()
}

// ReplicationDropped returns replication events discarded because a peer's queue was full.
func (s *stats) ReplicationDropped() int64 {
	return s.replDropped.Load()
}

// ReplicationSendErrors returns replication events whose socket write failed.
func (s *stats) ReplicationSendErrors() int64 {
	return s.replSendErrors.Load()
}

// BootstrapInboundQueueDepth returns the current inbound replication queue depth during bootstrap (0 when idle).
func (s *stats) BootstrapInboundQueueDepth() int64 {
	return s.bootstrapReplDepth.Load()
}

// InfoProvider methods (commands.InfoProvider).

func (s *stats) TCPPort() int { return s.clientPort }

func (s *stats) UptimeSeconds() int64 {
	return int64(time.Since(s.start).Seconds())
}

func (s *stats) ConnectedClients() int64 {
	return s.activeClients.Load()
}

func (s *stats) TotalConnections() int64 {
	return s.totalConns.Load()
}

func (s *stats) TotalCommands() int64 {
	return s.totalCmds.Load()
}

func (s *stats) OpsPerSec() int64 {
	u := time.Since(s.start).Seconds()
	if u < 1 {
		u = 1
	}
	return s.totalCmds.Load() / int64(u)
}

func (s *stats) KeyspaceHits() int64   { return s.keyspaceHits.Load() }
func (s *stats) KeyspaceMisses() int64 { return s.keyspaceMisses.Load() }

// RecordHit implements commands.KeyspaceRecorder.
func (s *stats) RecordHit(n int64) {
	if n > 0 {
		s.keyspaceHits.Add(n)
	}
}

// RecordMiss implements commands.KeyspaceRecorder.
func (s *stats) RecordMiss(n int64) {
	if n > 0 {
		s.keyspaceMisses.Add(n)
	}
}

func (s *stats) ConnectedPeers() int {
	return int(s.peerInbound.Load())
}

func (s *stats) PeerAddresses() []string {
	s.peerMu.RLock()
	defer s.peerMu.RUnlock()
	return append([]string(nil), s.peerOutAddrs...)
}

func (s *stats) NodeID() string { return s.nodeID }

// SetBuildVersion sets the process build label for INFO / mgmt (call from main after New).
func (s *stats) SetBuildVersion(v string) {
	s.buildMu.Lock()
	s.buildVersion = strings.TrimSpace(v)
	s.buildMu.Unlock()
}

// ServerVersion implements commands.InfoProvider.
func (s *stats) ServerVersion() string {
	s.buildMu.RLock()
	defer s.buildMu.RUnlock()
	return s.buildVersion
}

// BootstrapKeysApplied returns keys applied during the last bootstrap pull (telemetry).
func (s *stats) BootstrapKeysApplied() int64 {
	return s.bootstrapKeys.Load()
}

func (s *stats) BootstrapState() string {
	v := s.bootstrapState.Load()
	if v == nil {
		return "standalone"
	}
	return v.(string)
}

// ResetBootstrapStats implements peer.BootstrapObserver.
func (s *stats) ResetBootstrapStats() {
	s.bootstrapBytes.Store(0)
	s.bootstrapKeys.Store(0)
}

// AddBootstrapBytes implements peer.BootstrapObserver.
func (s *stats) AddBootstrapBytes(n int64) {
	if n > 0 {
		s.bootstrapBytes.Add(n)
	}
}

// AddBootstrapKeys implements peer.BootstrapObserver.
func (s *stats) AddBootstrapKeys(n int64) {
	if n > 0 {
		s.bootstrapKeys.Add(n)
	}
}

// BootstrapStatusMap returns P1.10 bootstrap pull telemetry (queue depth reserved for P2.3).
func (s *stats) BootstrapStatusMap(queueDepthConfig int) map[string]any {
	return map[string]any{
		"bytes_received":               s.bootstrapBytes.Load(),
		"keys_applied":                 s.bootstrapKeys.Load(),
		"queue_depth":                  s.bootstrapReplDepth.Load(),
		"bootstrap_queue_depth_config": queueDepthConfig,
	}
}

// Ensure stats implements InfoProvider at compile time.
var _ commands.InfoProvider = (*stats)(nil)

var _ commands.KeyspaceRecorder = (*stats)(nil)

var _ peer.PeerMetrics = (*stats)(nil)

var _ peer.BootstrapObserver = (*stats)(nil)

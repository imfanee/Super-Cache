// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Management socket Handler implementation (Phase 8) and P1 extended commands.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/mgmt"
)

func (s *Server) Ping() string {
	return "PONG"
}

// Info returns a JSON-serializable snapshot for the management INFO command.
func (s *Server) Info() map[string]any {
	c := s.config()
	m := map[string]any{
		"node_id":              s.stats.NodeID(),
		"supercache_version":   s.stats.ServerVersion(),
		"tcp_port":             s.stats.TCPPort(),
		"uptime_sec":           s.stats.UptimeSeconds(),
		"connected_clients":    s.stats.ConnectedClients(),
		"total_connections":    s.stats.TotalConnections(),
		"total_commands":       s.stats.TotalCommands(),
		"used_memory":          s.store.MemBytes(),
		"db_size":              s.store.DBSize(),
		"bootstrap":            s.stats.BootstrapState(),
		"replication_inbound":  s.stats.ConnectedPeers(),
		"replication_outbound": s.stats.PeerAddresses(),
	}
	if c.MetricsPort > 0 {
		bind := strings.TrimSpace(c.MetricsBind)
		if bind == "" {
			bind = "0.0.0.0"
		}
		m["metrics_bind"] = bind
		m["metrics_port"] = c.MetricsPort
	}
	m["client_tls_enabled"] = c.ClientTLSEnabled()
	m["peer_tls_enabled"] = c.PeerTLSEnabled()
	return m
}

// Status implements mgmt.ExtendedHandler with the audit-required STATUS payload fields.
func (s *Server) Status() map[string]any {
	return map[string]any{
		"node_id":           s.stats.NodeID(),
		"uptime_sec":        s.stats.UptimeSeconds(),
		"clients_connected": s.stats.ConnectedClients(),
		"peers_connected":   s.stats.ConnectedPeers() + len(s.stats.PeerAddresses()),
		"mem_used_bytes":    s.store.MemBytes(),
		"key_count":         s.store.DBSize(),
		"bootstrap_state":   s.stats.BootstrapState(),
	}
}

// PeersList implements mgmt.ExtendedHandler (P1.6; heartbeats when P2.2 lands).
func (s *Server) PeersList() []map[string]any {
	return s.peer.PeersInfo()
}

// PeersAdd implements mgmt.ExtendedHandler (P1.7).
func (s *Server) PeersAdd(addr string) error {
	return s.peer.AddPeer(addr)
}

// PeersRemove implements mgmt.ExtendedHandler (P1.7).
func (s *Server) PeersRemove(addr string) error {
	return s.peer.RemovePeer(addr)
}

// DebugKeyspace implements mgmt.ExtendedHandler (P1.9).
func (s *Server) DebugKeyspace(maxKeys int) (map[string]any, error) {
	if maxKeys <= 0 {
		maxKeys = 10
	}
	if maxKeys > 10000 {
		maxKeys = 10000
	}
	sample := s.store.SampleKeysForDebug(maxKeys)
	return map[string]any{
		"keys":        sample,
		"sample_size": len(sample),
	}, nil
}

// BootstrapStatus implements mgmt.ExtendedHandler (P1.10).
func (s *Server) BootstrapStatus() map[string]any {
	return s.stats.BootstrapStatusMap(s.config().BootstrapQueueDepth)
}

// RequestShutdown implements mgmt.ExtendedHandler (P1.8).
func (s *Server) RequestShutdown(graceful bool) error {
	if s.cancelRun == nil {
		return fmt.Errorf("shutdown not wired")
	}
	if graceful {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)
			s.cancelRun()
		}()
		return nil
	}
	s.cancelRun()
	return nil
}

var _ mgmt.Handler = (*Server)(nil)

var _ mgmt.ExtendedHandler = (*Server)(nil)

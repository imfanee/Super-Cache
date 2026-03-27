// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Graceful shutdown: drain outbound replication, then spill any remaining lines to JSON.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type replSpillEntry struct {
	Peer string   `json:"peer"`
	Wire wireRepl `json:"wire"`
}

type replSpillFile struct {
	NodeID          string           `json:"node_id"`
	GeneratedUnixMs int64            `json:"generated_unix_ms"`
	Entries         []replSpillEntry `json:"entries"`
}

// FinalizeGracefulShutdown runs after the client port is closed and handlers have drained.
// It waits for outbound replication queues to empty (bounded by ctx), then stops replication
// writers if any lines remain, collects those lines, and writes spillPath when non-empty.
func (s *Service) FinalizeGracefulShutdown(ctx context.Context, spillPath string) {
	s.DrainReplicationOutbound(ctx)

	s.mu.RLock()
	peers := append([]*outPeer(nil), s.out...)
	s.mu.RUnlock()

	var pending []replSpillEntry
	for _, p := range peers {
		if p.replCh == nil {
			continue
		}
		if len(p.replCh) == 0 {
			continue
		}
		if p.cancelSession != nil {
			p.cancelSession()
		}
		if p.writerDone != nil {
			select {
			case <-p.writerDone:
			case <-time.After(3 * time.Second):
				slog.Warn("replication writer did not exit in time for spill", "peer", p.addr)
			}
		}
	drain:
		for {
			select {
			case wr := <-p.replCh:
				pending = append(pending, replSpillEntry{Peer: p.addr, Wire: wr})
			default:
				break drain
			}
		}
	}

	if len(pending) == 0 {
		cfg := s.c()
		if len(cfg.Peers) > 0 && len(peers) == 0 {
			slog.Warn("graceful shutdown: no outbound peer connections; replication was not sent (configure peers or fix network)",
				"configured_peers", cfg.Peers)
		}
		return
	}

	if spillPath == "" {
		slog.Warn("graceful shutdown: pending replication could not be flushed; spill file disabled",
			"entries", len(pending))
		return
	}
	if err := writeReplSpillFile(spillPath, s.nodeID, pending); err != nil {
		slog.Error("repl spill write failed", "path", spillPath, "err", err)
		return
	}
	slog.Info("wrote pending replication spill file", "path", spillPath, "entries", len(pending))
}

// CloseListener stops accepting new inbound peer connections.
func (s *Service) CloseListener() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

// CloseActiveConnections forcefully closes outbound peer sessions.
func (s *Service) CloseActiveConnections() {
	s.mu.RLock()
	peers := append([]*outPeer(nil), s.out...)
	s.mu.RUnlock()
	for _, p := range peers {
		if p.cancelSession != nil {
			p.cancelSession()
		}
		if p.conn != nil {
			_ = p.conn.SetDeadline(time.Now().Add(-1 * time.Second))
			_ = p.conn.Close()
		}
	}
}

func writeReplSpillFile(path, nodeID string, entries []replSpillEntry) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.CreateTemp(dir, "supercache-repl-spill-*.json")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	doc := replSpillFile{
		NodeID:          nodeID,
		GeneratedUnixMs: time.Now().UnixMilli(),
		Entries:         entries,
	}
	if err := enc.Encode(&doc); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

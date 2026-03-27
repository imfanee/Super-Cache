// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Bootstrap pulls a full snapshot from a peer over the mesh protocol (Phase 7, P2.6).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/store"
)

// PullSnapshot pulls from a single address (wrapper for tests).
func (s *Service) PullSnapshot(ctx context.Context, addr string) error {
	return s.PullSnapshotFailover(ctx, []string{addr})
}

// PullSnapshotFailover tries each address in order until one succeeds (P2.6).
func (s *Service) PullSnapshotFailover(ctx context.Context, addrs []string) error {
	var lastErr error
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if err := s.pullSnapshotOnce(ctx, addr); err != nil {
			lastErr = err
			slog.Warn("bootstrap attempt failed", "addr", addr, "err", err)
			s.st.FlushDB()
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("bootstrap: all sources failed: %w", lastErr)
	}
	return fmt.Errorf("bootstrap: no sources")
}

func (s *Service) pullSnapshotOnce(ctx context.Context, addr string) error {
	if s.bootObs != nil {
		s.bootObs.ResetBootstrapStats()
	}
	conn, err := s.peerDialWithTimeout(ctx, addr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("bootstrap dial: %w", err)
	}
	defer conn.Close()

	br, err := s.handshakeOut(conn)
	if err != nil {
		return fmt.Errorf("bootstrap auth: %w", err)
	}
	if err := s.tryConsumePeerAnnounceBootstrap(conn, br); err != nil {
		return fmt.Errorf("bootstrap announce: %w", err)
	}
	reqPayload, err := json.Marshal(wireRepl{Op: wireOpBootstrap})
	if err != nil {
		return fmt.Errorf("bootstrap request: %w", err)
	}
	if err := WriteMessage(conn, PeerMessage{Version: 1, Type: MsgTypeBootstrapReq, NodeID: s.nodeID, Payload: reqPayload}); err != nil {
		return fmt.Errorf("bootstrap request: %w", err)
	}
	if s.bootObs != nil {
		s.bootObs.AddBootstrapBytes(int64(8 + len(reqPayload)))
	}

	s.st.FlushDB()
	depth := s.c().BootstrapQueueDepth
	if depth < 1 {
		depth = 1
	}
	batch := make([]store.SnapshotEntry, 0, depth)
	flush := func() error {
		n := len(batch)
		for _, ent := range batch {
			if err := s.st.ApplySnapshotEntry(ent); err != nil {
				return fmt.Errorf("bootstrap apply: %w", err)
			}
		}
		batch = batch[:0]
		if s.bootObs != nil && n > 0 {
			s.bootObs.AddBootstrapKeys(int64(n))
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := ReadMessage(br)
		if err != nil {
			if err := flush(); err != nil {
				return err
			}
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("bootstrap: unexpected EOF before end marker")
			}
			return fmt.Errorf("bootstrap read: %w", err)
		}
		msg = NormalizePeerMessage(msg)
		if s.bootObs != nil {
			// Approximate wire size (header + JSON body; exact size not tracked per frame here).
			s.bootObs.AddBootstrapBytes(int64(8 + len(msg.Payload) + 64))
		}
		switch msg.Type {
		case MsgTypeBootstrapDone:
			var end wireBootstrapEnd
			if json.Unmarshal(msg.Payload, &end) == nil && strings.EqualFold(end.Op, wireOpBootstrapEnd) {
				return flush()
			}
			return flush()
		case MsgTypeBootstrapChunk:
			var ent store.SnapshotEntry
			if err := json.Unmarshal(msg.Payload, &ent); err != nil {
				return fmt.Errorf("bootstrap entry: %w", err)
			}
			batch = append(batch, ent)
			if len(batch) >= depth {
				if err := flush(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("bootstrap: unexpected message type %q", msg.Type)
		}
	}
}

// tryConsumePeerAnnounceBootstrap reads PEER_ANNOUNCE when gossip_peers is enabled; tolerates timeout (older peers without gossip).
func (s *Service) tryConsumePeerAnnounceBootstrap(c net.Conn, br *bufio.Reader) error {
	if !s.c().GossipPeers {
		return nil
	}
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	msg, err := ReadMessage(br)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return nil
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeNodeList {
		return fmt.Errorf("invalid PEER_ANNOUNCE")
	}
	var pa wirePeerAnnounce
	if json.Unmarshal(msg.Payload, &pa) != nil || !strings.EqualFold(pa.Op, wireOpPeerAnnounce) {
		return fmt.Errorf("invalid PEER_ANNOUNCE")
	}
	for _, p := range pa.Peers {
		_ = s.AddPeer(p)
	}
	return nil
}

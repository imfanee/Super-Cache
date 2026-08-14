// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Outbound mesh session: heartbeats and optional PEER_ANNOUNCE consume (P2.1, P2.2, P2.4).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// ListenReady is closed after the peer TCP listener is bound successfully.
func (s *Service) ListenReady() <-chan struct{} {
	return s.listenReady
}

// consumeOptionalPeerAnnounce reads PEER_ANNOUNCE when gossip_peers is enabled (sent by peer before other frames).
func (s *Service) consumeOptionalPeerAnnounce(c net.Conn, br *bufio.Reader) error {
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
		return fmt.Errorf("peer: expected NODE_LIST when gossip_peers is enabled")
	}
	var pa wirePeerAnnounce
	if json.Unmarshal(msg.Payload, &pa) != nil || !strings.EqualFold(pa.Op, wireOpPeerAnnounce) {
		return fmt.Errorf("peer: expected PEER_ANNOUNCE when gossip_peers is enabled")
	}
	for _, p := range pa.Peers {
		_ = s.AddPeerFrom(p, SourceLearned)
	}
	return nil
}

func (s *Service) outboundPeerSession(ctx context.Context, addr string, c net.Conn, br *bufio.Reader, id peerIdentity) {
	depth := s.c().PeerQueueDepth
	if depth < 1 {
		depth = 1
	}
	op := &outPeer{
		addr:     addr,
		nodeID:   s.nodeID,
		remoteID: id.NodeID,
		conn:     c,
		w:        bufio.NewWriter(c),
		replCh:   make(chan *replFrame, depth),
		metrics:  s.metrics,
	}
	sessCtx, cancel := context.WithCancel(ctx)
	op.cancelSession = cancel
	op.writerDone = make(chan struct{})
	go func() {
		outboundReplWriter(sessCtx, op)
		close(op.writerDone)
	}()
	s.registerOut(op)
	defer func() {
		cancel()
		op.replStop.Store(true)
		_ = c.Close()
		// Remove this exact link, not every link matching addr: an accepted link from the same
		// peer carries the same advertised address and must survive this dial ending.
		s.unregisterLink(op)
	}()

	interval := time.Duration(s.c().HeartbeatInterval) * time.Second
	timeout := time.Duration(s.c().HeartbeatTimeout) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	if timeout <= interval {
		timeout = interval * 2
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-ticker.C:
				op.mu.Lock()
				hbPayload, _ := json.Marshal(wireHeartbeat{Op: wireOpHeartbeat})
				err := WriteMessage(op.w, PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: hbPayload})
				if err == nil {
					err = op.w.Flush()
				}
				op.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = c.SetReadDeadline(time.Now().Add(timeout))
		msg, err := ReadMessage(br)
		if err != nil {
			return
		}
		msg = NormalizePeerMessage(msg)
		if msg.Type == MsgTypeReplicate {
			// A peer that negotiated the duplex capability pushes its writes back down the
			// connection this node opened, instead of relying on a separate dial in the other
			// direction. Earlier builds fell through and dropped these frames silently.
			s.handleDuplexRepl(ctx, addr, msg)
			continue
		}
		if msg.Type != MsgTypeHeartbeat {
			continue
		}
		var probe wireHeartbeat
		_ = json.Unmarshal(msg.Payload, &probe)
		o := strings.ToUpper(strings.TrimSpace(probe.Op))
		switch o {
		case wireOpHeartbeatAck:
			op.lastHeartbeatAck.Store(time.Now().UnixMilli())
		case wireOpHeartbeat, "":
			op.lastHeartbeatAck.Store(time.Now().UnixMilli())
			op.mu.Lock()
			ackp, _ := json.Marshal(wireHeartbeat{Op: wireOpHeartbeatAck})
			_ = WriteMessage(op.w, PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: ackp})
			_ = op.w.Flush()
			op.mu.Unlock()
		default:
			op.lastHeartbeatAck.Store(time.Now().UnixMilli())
		}
	}
}

// handleDuplexRepl applies a replication frame received on a connection this node dialed. It
// routes through the same worker pool as accepted-connection replication so that bootstrap
// buffering and ordering behave identically on both paths.
func (s *Service) handleDuplexRepl(ctx context.Context, addr string, msg PeerMessage) {
	var wr wireRepl
	if err := json.Unmarshal(msg.Payload, &wr); err != nil {
		return
	}
	s.noteReplArrival(wr)
	if !s.enqueueInbound(ctx, addr, wr) {
		// No worker pool: the service was never started via Run (unit tests drive sessions
		// directly). Apply inline rather than dropping the write.
		if err := applyWireRepl(s.st, wr); err != nil {
			slog.Error("peer apply", "err", err)
		}
	}
}

func outboundReplWriter(ctx context.Context, op *outPeer) {
	drainDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			goto drain
		case f, ok := <-op.replCh:
			if !ok {
				return
			}
			flushOutboundReplLine(op, f)
		}
	}
drain:
	for len(op.replCh) > 0 && time.Now().Before(drainDeadline) {
		select {
		case f := <-op.replCh:
			flushOutboundReplLine(op, f)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// flushOutboundReplLine writes one pre-encoded frame. The bytes were serialised once for all
// peers, so this only copies them onto the socket.
func flushOutboundReplLine(op *outPeer, f *replFrame) {
	data, err := f.bytes()
	if err == nil {
		op.mu.Lock()
		_, err = op.w.Write(data)
		if err == nil {
			err = op.w.Flush()
		}
		op.mu.Unlock()
	}
	if err != nil {
		// The session read loop or heartbeat tears the link down, but this particular event is
		// gone and nothing downstream would otherwise record that it never arrived.
		if op.metrics != nil {
			op.metrics.AddReplicationSendError(1)
		}
		slog.Warn("replication write failed; event not delivered",
			"peer", op.addr, "op", f.wire.Op, "seq", f.wire.Seq, "err", err)
	}
}

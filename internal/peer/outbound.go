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
		_ = s.AddPeer(p)
	}
	return nil
}

func (s *Service) outboundPeerSession(ctx context.Context, addr string, c net.Conn, br *bufio.Reader) {
	depth := s.c().PeerQueueDepth
	if depth < 1 {
		depth = 1
	}
	op := &outPeer{
		addr:   addr,
		nodeID: s.nodeID,
		conn:   c,
		w:      bufio.NewWriter(c),
		replCh: make(chan wireRepl, depth),
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
		s.unregisterOut(addr)
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

func outboundReplWriter(ctx context.Context, op *outPeer) {
	drainDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			goto drain
		case wr, ok := <-op.replCh:
			if !ok {
				return
			}
			flushOutboundReplLine(op, wr)
		}
	}
drain:
	for len(op.replCh) > 0 && time.Now().Before(drainDeadline) {
		select {
		case wr := <-op.replCh:
			flushOutboundReplLine(op, wr)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func flushOutboundReplLine(op *outPeer, wr wireRepl) {
	op.mu.Lock()
	p, err := json.Marshal(wr)
	if err != nil {
		op.mu.Unlock()
		return
	}
	msg := PeerMessage{
		Version:   1,
		Type:      MsgTypeReplicate,
		NodeID:    op.nodeID,
		SeqNum:    wr.Seq,
		Timestamp: time.Now().UnixNano(),
		Payload:   p,
	}
	err = WriteMessage(op.w, msg)
	if err == nil {
		err = op.w.Flush()
	}
	op.mu.Unlock()
	_ = err // TCP write failure; session read loop or heartbeat will tear down.
}

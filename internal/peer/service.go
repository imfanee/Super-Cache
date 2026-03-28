// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Peer mesh: inbound listener, outbound dials, and asynchronous replication fan-out (P3).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
	"github.com/supercache/supercache/internal/tlsconfig"
)

// PeerMetrics receives replication connection counts for INFO (optional; may be nil).
type PeerMetrics interface {
	SetReplicationStats(inboundConnected int, outboundConnectedAddrs []string)
	SetBootstrapInboundQueueDepth(depth int)
}

// BootstrapObserver receives bootstrap pull progress (optional; may be nil).
type BootstrapObserver interface {
	ResetBootstrapStats()
	AddBootstrapBytes(n int64)
	AddBootstrapKeys(n int64)
}

// Service implements Replicator: fans out writes to configured peers and accepts inbound replication.
type Service struct {
	st      *store.Store
	cfg     atomic.Pointer[config.Config]
	metrics PeerMetrics
	bootObs BootstrapObserver
	nodeID  string

	replSeq atomic.Uint64

	mu  sync.RWMutex
	out []*outPeer

	listener    net.Listener
	peerDialTLS *tls.Config // non-nil when mesh uses TLS for outbound dials and bootstrap

	inboundCount atomic.Int64

	dialMu     sync.Mutex
	dialParent context.Context
	activeDial map[string]context.CancelFunc

	listenOnce  sync.Once
	listenReady chan struct{}

	bootstrapReplMu  sync.Mutex
	bootstrapReplBuf []wireRepl
	bootstrapMaxCap  int
	bootstrapActive  atomic.Bool

	inboundLastHB sync.Map // remote TCP address -> last heartbeat unix ms (inbound path)

	inboundCh chan inboundReplJob
	inboundWg sync.WaitGroup

	configMu sync.Mutex // serializes AddPeer/RemovePeer config mutations

	// onPublish delivers replicated PUBLISH messages to local subscribers (set by server).
	onPublish func(channel string, message []byte)
}

// inboundReplJob carries one replication frame for worker-pool apply (after handshake).
type inboundReplJob struct {
	s      *Service
	remote string
	wr     wireRepl
}

type outPeer struct {
	addr             string
	nodeID           string
	conn             net.Conn
	w                *bufio.Writer
	replCh           chan wireRepl
	replStop         atomic.Bool
	cancelSession    context.CancelFunc // outbound session (writer + heartbeat); nil if not set
	writerDone       chan struct{}      // closed when outboundReplWriter exits
	mu               sync.Mutex
	lastHeartbeatAck atomic.Int64 // unix ms; last HEARTBEAT or HEARTBEAT_ACK from peer
}

// NewService builds a peer mesh service. metrics and boot may be nil. nodeID identifies this instance on the wire (P3.2).
func NewService(cfg *config.Config, st *store.Store, metrics PeerMetrics, boot BootstrapObserver, nodeID string) *Service {
	s := &Service{
		st:          st,
		metrics:     metrics,
		bootObs:     boot,
		listenReady: make(chan struct{}),
		nodeID:      nodeID,
	}
	s.cfg.Store(cfg)
	return s
}

// SetConfig updates the active config pointer (hot reload).
func (s *Service) SetConfig(cfg *config.Config) {
	if cfg != nil {
		s.cfg.Store(cfg)
	}
}

func (s *Service) c() *config.Config {
	return s.cfg.Load()
}

func (s *Service) buildWireRepl(p ReplicatePayload) wireRepl {
	return wireRepl{
		Op:      p.Op,
		Key:     p.Key,
		Value:   p.Value,
		TTLms:   p.TTLms,
		Cnt:     p.Cnt,
		Dest:    p.Dest,
		Fields:  p.Fields,
		Members: p.Members,
		V:       ReplEnvelopeVersion,
		Seq:     s.replSeq.Add(1),
		Origin:  s.nodeID,
	}
}

// Replicate enqueues one JSON line per connected outbound peer (non-blocking; drops on full queue, P3.1, P3.3).
func (s *Service) Replicate(p ReplicatePayload) error {
	wr := s.buildWireRepl(p)
	s.mu.RLock()
	peers := append([]*outPeer(nil), s.out...)
	s.mu.RUnlock()

	for _, op := range peers {
		if op.replStop.Load() || op.replCh == nil {
			continue
		}
		select {
		case op.replCh <- wr:
		default:
			slog.Warn("replication outbound queue full; dropped event (re-bootstrap peers if state diverges)",
				"peer", op.addr, "op", wr.Op, "seq", wr.Seq)
		}
	}
	return nil
}

// DrainReplicationOutbound waits until outbound replication channels are empty or ctx is done (best-effort, P3.5).
func (s *Service) DrainReplicationOutbound(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		s.mu.RLock()
		peers := append([]*outPeer(nil), s.out...)
		s.mu.RUnlock()
		pending := 0
		for _, p := range peers {
			if p.replCh != nil {
				pending += len(p.replCh)
			}
		}
		if pending == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Run listens on peer_bind:peer_port and dials outbound peers until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	s.dialMu.Lock()
	s.dialParent = ctx
	s.activeDial = make(map[string]context.CancelFunc)
	s.dialMu.Unlock()

	cfg := s.c()
	s.peerDialTLS = nil
	if cfg.PeerTLSEnabled() {
		minV, err := tlsconfig.ParseMinVersion(cfg.PeerTLSMinVersion)
		if err != nil {
			return fmt.Errorf("peer tls min version: %w", err)
		}
		dialTLS, err := tlsconfig.LoadClientTLS(cfg.PeerTLSCAFile, minV)
		if err != nil {
			return fmt.Errorf("peer tls client: %w", err)
		}
		s.peerDialTLS = dialTLS
	}
	addr := fmt.Sprintf("%s:%d", cfg.PeerBind, cfg.PeerPort)
	var ln net.Listener
	var err error
	if cfg.PeerTLSEnabled() {
		minV, err := tlsconfig.ParseMinVersion(cfg.PeerTLSMinVersion)
		if err != nil {
			return fmt.Errorf("peer tls min version: %w", err)
		}
		tlsSrv, err := tlsconfig.LoadServerTLS(cfg.PeerTLSCertFile, cfg.PeerTLSKeyFile, minV)
		if err != nil {
			return fmt.Errorf("peer tls server: %w", err)
		}
		ln, err = tls.Listen("tcp", addr, tlsSrv)
		if err != nil {
			return fmt.Errorf("peer listen %s: %w", addr, err)
		}
		slog.Info("peer mesh listener using TLS", "addr", addr)
	} else {
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("peer listen %s: %w", addr, err)
		}
	}
	s.listener = ln
	s.listenOnce.Do(func() { close(s.listenReady) })

	nWorkers := runtime.NumCPU()
	if nWorkers < 1 {
		nWorkers = 1
	}
	s.inboundCh = make(chan inboundReplJob, nWorkers)
	for i := 0; i < nWorkers; i++ {
		s.inboundWg.Add(1)
		go func() {
			defer s.inboundWg.Done()
			s.inboundWorkerLoop(ctx)
		}()
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for _, peerAddr := range cfg.Peers {
		_ = s.ensureDial(peerAddr)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Wait for inbound workers to finish (they exit via ctx.Done).
			s.inboundWg.Wait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("peer accept: %w", err)
		}
		go s.serveInbound(ctx, conn)
	}
}

func (s *Service) serveInbound(ctx context.Context, c net.Conn) {
	defer c.Close()
	remote := c.RemoteAddr().String()
	br := bufio.NewReader(c)
	if err := s.runInboundHandshake(c, br); err != nil {
		return
	}
	if s.c().GossipPeers {
		peers := append([]string(nil), s.c().Peers...)
		paPayload, err := json.Marshal(wirePeerAnnounce{Op: wireOpPeerAnnounce, Peers: peers})
		if err != nil {
			return
		}
		if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeNodeList, NodeID: s.nodeID, Payload: paPayload}); err != nil {
			return
		}
	}

	s.inboundCount.Add(1)
	s.refreshMetrics()
	defer func() {
		s.inboundCount.Add(-1)
		s.refreshMetrics()
	}()

	timeout := time.Duration(s.c().HeartbeatTimeout) * time.Second
	if timeout < time.Second {
		timeout = time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = c.SetReadDeadline(time.Now().Add(timeout))
		msg, err := ReadMessage(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		prev := msg
		msg = NormalizePeerMessage(msg)
		if prev.Type == "" && msg.Type != "" {
			slog.Debug("peer wire: normalized legacy NDJSON-style envelope to framed MsgType", "type", msg.Type)
		}
		switch msg.Type {
		case MsgTypeHeartbeat:
			var probe wireHeartbeat
			_ = json.Unmarshal(msg.Payload, &probe)
			opName := strings.ToUpper(strings.TrimSpace(probe.Op))
			s.inboundLastHB.Store(remote, time.Now().UnixMilli())
			switch opName {
			case wireOpHeartbeatAck:
				continue
			case wireOpHeartbeat, "":
				bw := bufio.NewWriter(c)
				ackp, _ := json.Marshal(wireHeartbeat{Op: wireOpHeartbeatAck})
				_ = WriteMessage(bw, PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: ackp})
				_ = bw.Flush()
			default:
				continue
			}
			continue
		case MsgTypeBootstrapReq:
			if err := s.serveBootstrapSnapshot(c); err != nil {
				slog.Error("peer bootstrap snapshot", "err", err)
			}
			return
		case MsgTypeReplicate:
			var wr wireRepl
			if err := json.Unmarshal(msg.Payload, &wr); err != nil {
				continue
			}
			if strings.EqualFold(wr.Op, wireOpBootstrap) {
				if err := s.serveBootstrapSnapshot(c); err != nil {
					slog.Error("peer bootstrap snapshot", "err", err)
				}
				return
			}
			select {
			case s.inboundCh <- inboundReplJob{s: s, remote: remote, wr: wr}:
			case <-ctx.Done():
				return
			}
		default:
			continue
		}
	}
}

func (s *Service) inboundWorkerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-s.inboundCh:
			if !ok {
				return
			}
			if job.s.bootstrapActive.Load() {
				if err := job.s.enqueueBootstrapRepl(job.wr); err != nil {
					slog.Warn("bootstrap replication queue full", "remote", job.remote)
				}
				continue
			}
			if strings.EqualFold(job.wr.Op, "PUBLISH") {
				if job.s.onPublish != nil {
					job.s.onPublish(job.wr.Key, job.wr.Value)
				}
				continue
			}
			if err := applyWireRepl(job.s.st, job.wr); err != nil {
				slog.Error("peer apply", "err", err)
			}
		}
	}
}

func (s *Service) runInboundHandshake(c net.Conn, br *bufio.Reader) error {
	remote := c.RemoteAddr().String()
	msg, err := ReadMessage(br)
	if err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHello {
		s.logPeerAuthFailure(remote, "expected HELLO")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "expected HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return fmt.Errorf("expected HELLO")
	}
	var hello wireHello
	if err := json.Unmarshal(msg.Payload, &hello); err != nil {
		s.logPeerAuthFailure(remote, "invalid HELLO json")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "invalid HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return fmt.Errorf("hello json: %w", err)
	}
	if !strings.EqualFold(hello.Op, wireOpHello) {
		s.logPeerAuthFailure(remote, "expected HELLO")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "expected HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return fmt.Errorf("expected HELLO")
	}
	if hello.Ver != PeerProtocolVersion {
		s.logPeerAuthFailure(remote, "unsupported protocol version")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Ver: PeerProtocolVersion, Err: "unsupported protocol version"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return fmt.Errorf("unsupported protocol version")
	}
	nonce, err := randomNonce()
	if err != nil {
		return err
	}
	nonceHex := hex.EncodeToString(nonce)
	ackPayload, err := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: true, Ver: PeerProtocolVersion, Nonce: nonceHex})
	if err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: ackPayload}); err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	msg2, err := ReadMessage(br)
	if err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	msg2 = NormalizePeerMessage(msg2)
	if msg2.Type != MsgTypeAuth {
		s.logPeerAuthFailure(remote, "invalid AUTH frame")
		p, _ := json.Marshal(wireAck{Err: "invalid auth frame"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return fmt.Errorf("expected AUTH")
	}
	var proof wireAuthProof
	if err := json.Unmarshal(msg2.Payload, &proof); err != nil {
		s.logPeerAuthFailure(remote, "invalid AUTH frame")
		p, _ := json.Marshal(wireAck{Err: "invalid auth frame"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return fmt.Errorf("auth proof json: %w", err)
	}
	if !strings.EqualFold(proof.Op, wireOpAuth) || proof.Ver != PeerProtocolVersion {
		s.logPeerAuthFailure(remote, "invalid AUTH op or version")
		p, _ := json.Marshal(wireAck{Err: "bad auth"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return fmt.Errorf("bad auth proof")
	}
	if !verifyPeerHMAC(s.c().SharedSecret, proof.Hmac, nonce) {
		s.logPeerAuthFailure(remote, "hmac verification failed")
		p, _ := json.Marshal(wireAck{Err: "bad auth"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return fmt.Errorf("hmac mismatch")
	}
	okPayload, err := json.Marshal(wireAck{OK: true})
	if err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: okPayload}); err != nil {
		return fmt.Errorf("peer handshake: %w", err)
	}
	return nil
}

func (s *Service) logPeerAuthFailure(remote, reason string) {
	slog.Error("peer authentication failed", "remote", remote, "reason", reason)
}

func (s *Service) serveBootstrapSnapshot(c net.Conn) error {
	w := bufio.NewWriter(c)
	for ent := range s.st.Snapshot() {
		p, err := json.Marshal(ent)
		if err != nil {
			return fmt.Errorf("peer bootstrap: %w", err)
		}
		if err := WriteMessage(w, PeerMessage{Version: 1, Type: MsgTypeBootstrapChunk, NodeID: s.nodeID, Payload: p}); err != nil {
			return fmt.Errorf("peer bootstrap: %w", err)
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("peer bootstrap: %w", err)
		}
	}
	endPayload, err := json.Marshal(wireBootstrapEnd{Op: wireOpBootstrapEnd})
	if err != nil {
		return fmt.Errorf("peer bootstrap: %w", err)
	}
	if err := WriteMessage(w, PeerMessage{Version: 1, Type: MsgTypeBootstrapDone, Payload: endPayload}); err != nil {
		return fmt.Errorf("peer bootstrap: %w", err)
	}
	return w.Flush()
}

// handshakeOut performs HELLO + HMAC AUTH and returns a Reader positioned for the next peer frame.
func (s *Service) handshakeOut(c net.Conn) (*bufio.Reader, error) {
	secret := s.c().SharedSecret
	hPayload, err := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion})
	if err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, NodeID: s.nodeID, Payload: hPayload}); err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	br := bufio.NewReader(c)
	msg, err := ReadMessage(br)
	if err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHelloAck {
		return nil, fmt.Errorf("peer: expected HELLO_ACK")
	}
	var ack wireHelloAck
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		return nil, fmt.Errorf("hello_ack: %w", err)
	}
	if !strings.EqualFold(ack.Op, wireOpHelloAck) {
		return nil, fmt.Errorf("peer: expected HELLO_ACK")
	}
	if !ack.OK || ack.Err != "" {
		if ack.Err != "" {
			return nil, fmt.Errorf("peer hello: %s", ack.Err)
		}
		return nil, fmt.Errorf("peer hello failed")
	}
	nonce, err := hex.DecodeString(ack.Nonce)
	if err != nil {
		return nil, fmt.Errorf("peer nonce: %w", err)
	}
	proofPayload, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce)})
	if err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, NodeID: s.nodeID, Payload: proofPayload}); err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	msg3, err := ReadMessage(br)
	if err != nil {
		return nil, fmt.Errorf("peer handshake: %w", err)
	}
	msg3 = NormalizePeerMessage(msg3)
	if msg3.Type != MsgTypeAck {
		return nil, fmt.Errorf("peer: expected ACK after AUTH")
	}
	var final wireAck
	if err := json.Unmarshal(msg3.Payload, &final); err != nil {
		return nil, fmt.Errorf("auth ack: %w", err)
	}
	if !final.OK || final.Err != "" {
		if final.Err != "" {
			return nil, fmt.Errorf("peer auth: %s", final.Err)
		}
		return nil, fmt.Errorf("peer auth failed")
	}
	return br, nil
}

func (s *Service) dialLoop(ctx context.Context, addr string) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		c, err := s.peerDialWithTimeout(ctx, addr, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err := backoffSleep(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		attempt = 0
		br, err := s.handshakeOut(c)
		if err != nil {
			_ = c.Close()
			if err := backoffSleep(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		if err := s.consumeOptionalPeerAnnounce(c, br); err != nil {
			_ = c.Close()
			if err := backoffSleep(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		s.outboundPeerSession(ctx, addr, c, br)
		if ctx.Err() != nil {
			return
		}
		attempt = 0
	}
}

func (s *Service) registerOut(op *outPeer) {
	s.mu.Lock()
	s.out = append(s.out, op)
	s.mu.Unlock()
	s.refreshMetrics()
}

func (s *Service) unregisterOut(addr string) {
	s.mu.Lock()
	dst := s.out[:0]
	for _, p := range s.out {
		if p.addr != addr {
			dst = append(dst, p)
		}
	}
	s.out = dst
	s.mu.Unlock()
	s.refreshMetrics()
}

func (s *Service) refreshMetrics() {
	if s.metrics == nil {
		return
	}
	s.mu.RLock()
	addrs := make([]string, len(s.out))
	for i, p := range s.out {
		addrs[i] = p.addr
	}
	s.mu.RUnlock()
	ib := int(s.inboundCount.Load())
	s.metrics.SetReplicationStats(ib, addrs)
}

// SetBootstrapInboundActive enables inbound replication buffering during snapshot pull (P2.3).
func (s *Service) SetBootstrapInboundActive(active bool, maxCap int) {
	if !active {
		// Deactivate the flag first so no new items are enqueued, then clear the buffer.
		s.bootstrapActive.Store(false)
		s.bootstrapReplMu.Lock()
		s.bootstrapReplBuf = nil
		s.bootstrapReplMu.Unlock()
	} else {
		s.bootstrapReplMu.Lock()
		if maxCap < 1 {
			maxCap = 1
		}
		s.bootstrapMaxCap = maxCap
		s.bootstrapReplMu.Unlock()
		s.bootstrapActive.Store(true)
	}
	if s.metrics != nil {
		s.metrics.SetBootstrapInboundQueueDepth(s.bootstrapQueueLen())
	}
}

func (s *Service) bootstrapQueueLen() int {
	s.bootstrapReplMu.Lock()
	defer s.bootstrapReplMu.Unlock()
	return len(s.bootstrapReplBuf)
}

func (s *Service) enqueueBootstrapRepl(wr wireRepl) error {
	s.bootstrapReplMu.Lock()
	defer s.bootstrapReplMu.Unlock()
	max := s.bootstrapMaxCap
	if max < 1 {
		max = 1
	}
	if len(s.bootstrapReplBuf) >= max {
		return fmt.Errorf("bootstrap queue full")
	}
	s.bootstrapReplBuf = append(s.bootstrapReplBuf, wr)
	if s.metrics != nil {
		s.metrics.SetBootstrapInboundQueueDepth(len(s.bootstrapReplBuf))
	}
	return nil
}

// DrainBootstrapInboundQueue applies queued replication received during bootstrap (P2.3).
func (s *Service) DrainBootstrapInboundQueue(ctx context.Context) error {
	for round := 0; round < 500; round++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		drained := false
		for {
			s.bootstrapReplMu.Lock()
			if len(s.bootstrapReplBuf) == 0 {
				s.bootstrapReplMu.Unlock()
				break
			}
			wr := s.bootstrapReplBuf[0]
			s.bootstrapReplBuf = s.bootstrapReplBuf[1:]
			n := len(s.bootstrapReplBuf)
			s.bootstrapReplMu.Unlock()
			drained = true
			if err := applyWireRepl(s.st, wr); err != nil {
				return err
			}
			if s.metrics != nil {
				s.metrics.SetBootstrapInboundQueueDepth(n)
			}
		}
		if !drained {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return nil
}

// ConfigPeerAddrs returns a copy of configured peer addresses (P2.5).
func (s *Service) ConfigPeerAddrs() []string {
	return append([]string(nil), s.c().Peers...)
}

// ensureDial starts a reconnect loop for addr unless one is already active.
func (s *Service) ensureDial(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	s.dialMu.Lock()
	if s.activeDial == nil {
		s.dialMu.Unlock()
		return fmt.Errorf("peer dial map not initialized")
	}
	if _, ok := s.activeDial[addr]; ok {
		s.dialMu.Unlock()
		return nil
	}
	parent := s.dialParent
	if parent == nil {
		s.dialMu.Unlock()
		return fmt.Errorf("peer service not running")
	}
	childCtx, cancel := context.WithCancel(parent)
	s.activeDial[addr] = cancel
	s.dialMu.Unlock()
	p := addr
	go func() {
		s.dialLoop(childCtx, p)
		s.dialMu.Lock()
		if s.activeDial != nil {
			delete(s.activeDial, p)
		}
		s.dialMu.Unlock()
	}()
	return nil
}

// SyncPeersFromConfig ensures a dial loop exists for every configured peer (hot reload additive peers).
func (s *Service) SyncPeersFromConfig(peers []string) {
	for _, p := range peers {
		_ = s.ensureDial(strings.TrimSpace(p))
	}
}

// AddPeer appends a peer to the in-memory config and starts dialing it.
func (s *Service) AddPeer(addr string) error {
	addr = strings.TrimSpace(addr)
	if err := config.ValidatePeerAddr(addr); err != nil {
		return err
	}
	s.configMu.Lock()
	cfg := s.c()
	for _, p := range cfg.Peers {
		if p == addr {
			s.configMu.Unlock()
			return fmt.Errorf("peer %s already configured", addr)
		}
	}
	newCfg := *cfg
	newCfg.Peers = append(append([]string(nil), cfg.Peers...), addr)
	s.cfg.Store(&newCfg)
	s.configMu.Unlock()
	return s.ensureDial(addr)
}

// RemovePeer stops the dial loop for addr and removes it from the in-memory config.
func (s *Service) RemovePeer(addr string) error {
	addr = strings.TrimSpace(addr)
	s.dialMu.Lock()
	cancel := s.activeDial[addr]
	s.dialMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.configMu.Lock()
	cfg := s.c()
	newPeers := make([]string, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p != addr {
			newPeers = append(newPeers, p)
		}
	}
	newCfg := *cfg
	newCfg.Peers = newPeers
	s.cfg.Store(&newCfg)
	s.configMu.Unlock()
	return nil
}

// PeersInfo returns one row per configured peer with connection and last heartbeat (unix ms) when known (P2.2).
func (s *Service) PeersInfo() []map[string]any {
	cfg := s.c()
	connected := make(map[string]bool)
	lastHB := make(map[string]int64)
	s.mu.RLock()
	for _, p := range s.out {
		connected[p.addr] = true
		if ms := p.lastHeartbeatAck.Load(); ms > 0 {
			lastHB[p.addr] = ms
		}
	}
	s.mu.RUnlock()
	out := make([]map[string]any, 0, len(cfg.Peers))
	for _, addr := range cfg.Peers {
		row := map[string]any{
			"address":   addr,
			"connected": connected[addr],
		}
		if ms, ok := lastHB[addr]; ok {
			row["last_heartbeat_ms"] = ms
		} else {
			row["last_heartbeat_ms"] = nil
		}
		out = append(out, row)
	}
	return out
}

// SetOnPublish sets the callback for delivering replicated PUBLISH messages to local subscribers.
func (s *Service) SetOnPublish(fn func(channel string, message []byte)) {
	s.onPublish = fn
}

// Ensure Service implements Replicator.
var _ Replicator = (*Service)(nil)

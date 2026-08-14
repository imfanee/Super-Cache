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
	"strconv"
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
	// AddReplicationDropped counts replication events discarded because a peer's outbound queue
	// was full. Each one is a write the peer will never see.
	AddReplicationDropped(n int64)
	// AddReplicationSendError counts replication events whose socket write failed. The link is
	// torn down afterwards, but the event itself is gone.
	AddReplicationSendError(n int64)
	// AddReplicationGap reports that a peer's sequence numbers skipped forward, meaning missed
	// events never arrived. missed is how many.
	AddReplicationGap(missed int64)
	// AddReplicationLate counts events arriving with a sequence at or below one already seen.
	AddReplicationLate(n int64)
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

	// advertiseAddr is the host:port peers should dial to reach this node's listener. Sent in
	// every handshake so an acceptor can learn a node it was never configured with.
	advertiseAddr atomic.Pointer[string]

	replSeq atomic.Uint64

	// linkSeq assigns each registered link a monotonic number so that when two links reach the
	// same node the choice of which one carries replication is stable rather than arbitrary.
	linkSeq atomic.Uint64

	// targetsGen changes whenever a link is registered or removed, invalidating targetsCache.
	// Without the cache every write rebuilt the same selection from scratch.
	targetsGen   atomic.Uint64
	targetsCache atomic.Pointer[replTargets]

	// mu guards out, which holds every link this node may send replication on: outbound dials
	// plus, once the capability is negotiated, accepted connections. Two links to the same node
	// are de-duplicated at send time by remote node ID, never at registration time, so a link
	// dropping does not lose the other.
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

	// originSeq maps a peer's node ID to the highest replication sequence seen from it, so a
	// skipped sequence can be noticed. Values are *atomic.Uint64.
	originSeq sync.Map

	// gaps holds sequences that have not arrived yet, so loss is only reported once they have
	// had a chance to turn up late.
	gaps *gapTracker

	resyncMu sync.Mutex
	resync   ResyncRequester

	// inboundShards each have exactly one worker, and every event from a given origin is routed
	// to the same shard. Applying one origin's events concurrently reordered them, so a peer's
	// later write to a key could be overwritten by its own earlier one.
	inboundShards []chan inboundReplJob
	inboundWg     sync.WaitGroup

	configMu sync.Mutex // serializes AddPeer/RemovePeer config mutations

	// peerMeta records how each address became known and when it last worked, which is what
	// makes it possible to forget one that has gone away for good without also forgetting one
	// the operator asked for.
	peerMetaMu sync.Mutex
	peerMeta   map[string]*peerMeta
}

// PeerSource records how an address became known, which decides whether it may ever be
// forgotten automatically.
type PeerSource string

const (
	// SourceConfig is an address from the configuration file. Never forgotten: it is the
	// operator's stated intent, and a node that keeps retrying it costs one goroutine.
	SourceConfig PeerSource = "config"
	// SourceManual is an address added through the management API. Never forgotten, same
	// reasoning.
	SourceManual PeerSource = "manual"
	// SourceDiscovery is an address a provider listed. A later listing that omits it is
	// authoritative evidence the machine is gone.
	SourceDiscovery PeerSource = "discovery"
	// SourceLearned is an address learned from a peer that connected here, from a gossiped
	// list, or from a copied configuration. Only time can say whether it is still real.
	SourceLearned PeerSource = "learned"
)

// forgettable reports whether an address from this source may be removed automatically.
func (p PeerSource) forgettable() bool {
	return p == SourceDiscovery || p == SourceLearned
}

type peerMeta struct {
	addr   string
	source PeerSource
	added  int64 // unix ms
	lastOK int64 // unix ms of the last completed handshake; 0 when it has never worked
}

// inboundReplJob carries one replication frame for worker-pool apply (after handshake).
type inboundReplJob struct {
	s      *Service
	remote string
	wr     wireRepl
}

// replFrame is one replication event queued for delivery, shared by every link it is queued on.
//
// Encoding is done once, lazily, by whichever writer reaches it first; the rest reuse the same
// bytes. Both halves of that matter. Encoding once instead of once per peer keeps fan-out cost
// flat as the cluster grows, and doing it in a writer rather than in Replicate keeps it off the
// caller's path, which is a client's own write: encoding there would add the cost of a full
// marshal to every write even on a node with a single peer.
//
// wire is retained because a graceful shutdown spills undelivered events as structured JSON,
// which the encoded bytes cannot provide.
type replFrame struct {
	wire   wireRepl
	nodeID string
	ts     int64

	once sync.Once
	data []byte
	err  error
}

// bytes returns the complete framed message, encoding it on first use.
func (f *replFrame) bytes() ([]byte, error) {
	f.once.Do(func() {
		payload, err := json.Marshal(f.wire)
		if err != nil {
			f.err = fmt.Errorf("peer: marshal replication payload: %w", err)
			return
		}
		f.data, f.err = EncodeMessage(PeerMessage{
			Version:   1,
			Type:      MsgTypeReplicate,
			NodeID:    f.nodeID,
			SeqNum:    f.wire.Seq,
			Timestamp: f.ts,
			Payload:   payload,
		})
	})
	return f.data, f.err
}

// outPeer is one authenticated link this node may replicate over. Despite the name it covers
// both directions: a dial this node opened, and (when the duplex capability was negotiated) a
// connection this node accepted.
type outPeer struct {
	addr string
	// nodeID is the LOCAL node's identity, stamped into the envelope of frames sent on this
	// link so the receiver knows who sent them. The remote's identity is remoteID.
	nodeID string
	// remoteID is the peer's identity from the handshake, or "" for a peer too old to send one.
	// Links sharing a remoteID reach the same node and must not both carry the same write.
	remoteID string
	// inbound is true when this node accepted the connection rather than dialing it.
	inbound bool
	// linkSeq is the registration order, used as a stable tie-break between equivalent links.
	linkSeq uint64
	conn    net.Conn
	w       *bufio.Writer
	replCh  chan *replFrame
	// metrics records frames this link failed to deliver; nil when the service has none.
	metrics          PeerMetrics
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
		gaps:        newGapTracker(),
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

// SetAdvertiseAddr sets the host:port this node tells peers to dial. Empty means this node
// cannot be discovered automatically and must be added to peers by configuration.
func (s *Service) SetAdvertiseAddr(addr string) {
	addr = strings.TrimSpace(addr)
	s.advertiseAddr.Store(&addr)
}

// AdvertiseAddr returns the address this node advertises to peers, or "" when it has none.
func (s *Service) AdvertiseAddr() string {
	if p := s.advertiseAddr.Load(); p != nil {
		return *p
	}
	return ""
}

// NodeID returns this node's cluster identity.
func (s *Service) NodeID() string {
	return s.nodeID
}

// isSelf reports whether an identity or address refers to this node. Without this check a node
// that appears in its own learned peer list would dial itself and replicate every write back
// into its own store, duplicating list operations.
func (s *Service) isSelf(nodeID, addr string) bool {
	if nodeID != "" && nodeID == s.nodeID {
		return true
	}
	adv := s.AdvertiseAddr()
	if adv != "" && addr != "" &&
		config.NormalizePeerAddr(adv) == config.NormalizePeerAddr(addr) {
		return true
	}
	return false
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

// preferLink reports whether a should carry replication instead of b when both reach the same
// node. An outbound dial wins because it is the path every peer understands, including ones too
// old to accept writes on a socket they opened. Between two links of the same direction the
// older registration wins so the choice does not flap while both are alive.
func preferLink(a, b *outPeer) bool {
	if a.inbound != b.inbound {
		return !a.inbound
	}
	return a.linkSeq < b.linkSeq
}

// replicationTargets returns exactly one live link per remote node.
//
// A node can be reachable over two links at once: this node's outbound dial to it, and the
// connection it dialed to this node. Sending a write on both would apply it twice, which
// silently corrupts every non-idempotent operation (LPUSH, RPUSH, LINSERT, LREM, LPOP, RPOP).
// Selection happens per call rather than at registration so that losing one link immediately
// promotes the other with no gap.
func (s *Service) replicationTargets() []*outPeer {
	// Membership changes rarely and writes are constant, so the selection is cached and rebuilt
	// only when a link is registered or removed. The returned slice is shared and must be
	// treated as read-only by callers.
	gen := s.targetsGen.Load()
	if c := s.targetsCache.Load(); c != nil && c.gen == gen {
		return c.links
	}
	links := s.computeReplicationTargets()
	// A membership change racing this recompute leaves the entry tagged with the older
	// generation, so the next caller simply rebuilds rather than reading a stale selection.
	s.targetsCache.Store(&replTargets{gen: gen, links: links})
	return links
}

// replTargets is a cached link selection, valid only for the generation it was built from.
type replTargets struct {
	gen   uint64
	links []*outPeer
}

func (s *Service) computeReplicationTargets() []*outPeer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	best := make(map[string]*outPeer, len(s.out))
	order := make([]string, 0, len(s.out))
	for _, l := range s.out {
		if l == nil || l.replStop.Load() || l.replCh == nil {
			continue
		}
		key := l.remoteID
		if key == "" {
			// A peer that never identified itself cannot be de-duplicated by identity, so
			// fall back to its address. Such a peer is old enough that it has no duplex link
			// either, meaning there is only ever one link to it in the first place.
			key = "addr:" + l.addr
		}
		cur, ok := best[key]
		if !ok {
			best[key] = l
			order = append(order, key)
			continue
		}
		if preferLink(l, cur) {
			best[key] = l
		}
	}
	out := make([]*outPeer, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// Replicate enqueues one JSON line per connected peer, one link per peer (non-blocking; drops
// on full queue, P3.1, P3.3).
// buildReplFrame prepares one replication event for delivery. The event is not encoded here:
// see replFrame for why that is deferred to the first writer that sends it.
func (s *Service) buildReplFrame(wr wireRepl) *replFrame {
	return &replFrame{wire: wr, nodeID: s.nodeID, ts: time.Now().UnixNano()}
}

func (s *Service) Replicate(p ReplicatePayload) error {
	wr := s.buildWireRepl(p)
	targets := s.replicationTargets()
	if len(targets) == 0 {
		return nil
	}
	frame := s.buildReplFrame(wr)
	for _, op := range targets {
		select {
		case op.replCh <- frame:
		default:
			if op.metrics != nil {
				op.metrics.AddReplicationDropped(1)
			}
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
	// Each shard is drained by a single worker, which is what preserves per-origin order. The
	// buffer is generous because the sender is the goroutine reading a peer's connection, and
	// blocking it would also stall that connection's heartbeats.
	const inboundShardDepth = 1024
	s.inboundShards = make([]chan inboundReplJob, nWorkers)
	for i := 0; i < nWorkers; i++ {
		ch := make(chan inboundReplJob, inboundShardDepth)
		s.inboundShards[i] = ch
		s.inboundWg.Add(1)
		go func(c <-chan inboundReplJob) {
			defer s.inboundWg.Done()
			s.inboundWorkerLoop(ctx, c)
		}(ch)
	}

	go s.watchGaps(ctx)

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
	id, err := s.runInboundHandshakeInfo(c, br)
	if err != nil {
		return
	}

	// Learn how to reach the node that just connected. Without this an autoscaled node can
	// send us its writes but can never receive ours, because replication only ever flowed over
	// links this node dialed, and this node has no configuration naming an address that did
	// not exist when it started.
	s.learnPeerFromInbound(id, c)

	// The peer announce must be the first frame after the handshake, because a peer with
	// gossip enabled reads exactly one frame here and rejects the link if it is anything else.
	// The replication writer is therefore started only after this has been sent, or a write
	// racing ahead of it would drop the connection into a reconnect loop.
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

	// When the peer also applies replication on connections it opened, this accepted socket can
	// carry writes to it immediately, without waiting for the dial-back above to succeed, and
	// even when this node cannot reach it at all. Older peers drop such frames, so the link is
	// only registered once the capability has been negotiated.
	var link *outPeer
	if id.Duplex && !s.isSelf(id.NodeID, id.Advertise) {
		link = s.startInboundReplLink(ctx, c, id)
		defer s.stopInboundReplLink(link)
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
				// Writes to this socket must go through the link's writer once one exists,
				// otherwise the heartbeat ack and a concurrently written replication frame
				// would interleave and corrupt the framing.
				writeHeartbeatAck(c, link)
			default:
				continue
			}
			continue
		case MsgTypeLeave:
			s.handleLeave(msg, id.Advertise)
			continue
		case MsgTypeBootstrapReq:
			if s.refuseBootstrapWhileSyncing(remote) {
				return
			}
			// The snapshot stream writes to this socket directly, so the replication writer
			// must be fully stopped first or the two would interleave mid-frame.
			s.stopInboundReplLink(link)
			if err := s.serveBootstrapSnapshot(c); err != nil {
				slog.Error("peer bootstrap snapshot", "err", err)
			}
			return
		case MsgTypeReplicate:
			var wr wireRepl
			if err := json.Unmarshal(msg.Payload, &wr); err != nil {
				continue
			}
			s.noteReplArrival(wr)
			if strings.EqualFold(wr.Op, wireOpBootstrap) {
				if s.refuseBootstrapWhileSyncing(remote) {
					return
				}
				s.stopInboundReplLink(link)
				if err := s.serveBootstrapSnapshot(c); err != nil {
					slog.Error("peer bootstrap snapshot", "err", err)
				}
				return
			}
			if !s.enqueueInbound(ctx, remote, wr) {
				if err := applyWireRepl(s.st, wr); err != nil {
					slog.Error("peer apply", "err", err)
				}
			}
		default:
			continue
		}
	}
}

// refuseBootstrapWhileSyncing reports whether a snapshot request must be declined because this
// node has not finished its own bootstrap, and logs the refusal when it does.
//
// A node still syncing holds a partial store, so serving it as a snapshot would hand the
// requester a subset of the cluster's data and leave it permanently short of whatever had not
// arrived yet. This is reachable whenever several nodes that list each other start together.
//
// The refusal is a closed connection rather than a new frame, which the requester already
// reports as a failed attempt and retries against the next candidate. Peers too old to know
// about this behave identically, so nothing on the wire changes.
func (s *Service) refuseBootstrapWhileSyncing(remote string) bool {
	if !s.bootstrapActive.Load() {
		return false
	}
	slog.Warn("refusing bootstrap request while this node is still syncing; "+
		"the requester will try another source", "remote", remote)
	return true
}

// noteReplArrival records a replication event's sequence number and reports whether anything
// from that peer went missing on the way here.
//
// Every event carries its origin and a per-origin sequence, but until now nothing read them, so
// a dropped event left the two nodes silently disagreeing forever. Comparing each arrival with
// the last one from the same origin turns that into something countable.
//
// This must be called from the goroutine reading the connection, which sees one link's events in
// the order they were sent. The apply path cannot do it: inbound events are handed to a pool of
// workers that process them concurrently, so order there reflects scheduling, not the wire.
//
// A peer that restarts begins again at sequence 1, which is recognised rather than reported as
// an enormous backwards jump.
func (s *Service) noteReplArrival(wr wireRepl) {
	origin := strings.TrimSpace(wr.Origin)
	if origin == "" || wr.Seq == 0 || origin == s.nodeID {
		return
	}
	v, loaded := s.originSeq.LoadOrStore(origin, newSeqCounter(wr.Seq))
	if !loaded {
		return // first event from this peer: nothing to compare against
	}
	last := v.(*atomic.Uint64)
	prev := last.Load()
	switch {
	case wr.Seq == prev+1:
		last.Store(wr.Seq)
	case wr.Seq > prev+1:
		missed := wr.Seq - prev - 1
		last.Store(wr.Seq)
		if s.metrics != nil {
			s.metrics.AddReplicationGap(int64(missed))
		}
		slog.Warn("replication gap; waiting to see whether the missing events arrive late",
			"origin", origin, "missed", missed, "expected_seq", prev+1, "got_seq", wr.Seq)
		if s.gaps != nil && s.gaps.note(origin, prev, wr.Seq) {
			s.confirmLoss(origin, missed)
		}
	case wr.Seq == 1:
		// The peer restarted and its counter began again.
		last.Store(wr.Seq)
		slog.Info("peer replication sequence restarted", "origin", origin)
	default:
		// At or below a sequence already seen. Switching between two links to the same peer can
		// deliver a straggler from the old one after the new one has moved ahead, which fills a
		// gap rather than proving one.
		if s.gaps != nil {
			s.gaps.arrived(origin, wr.Seq)
		}
		if s.metrics != nil {
			s.metrics.AddReplicationLate(1)
		}
	}
}

func newSeqCounter(v uint64) *atomic.Uint64 {
	c := &atomic.Uint64{}
	c.Store(v)
	return c
}

// writeHeartbeatAck replies to a peer heartbeat on an accepted connection, serialising with the
// replication writer when this link also carries writes.
func writeHeartbeatAck(c net.Conn, link *outPeer) {
	ackp, err := json.Marshal(wireHeartbeat{Op: wireOpHeartbeatAck})
	if err != nil {
		return
	}
	msg := PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: ackp}
	if link == nil {
		bw := bufio.NewWriter(c)
		_ = WriteMessage(bw, msg)
		_ = bw.Flush()
		return
	}
	link.mu.Lock()
	defer link.mu.Unlock()
	if err := WriteMessage(link.w, msg); err == nil {
		_ = link.w.Flush()
	}
}

// learnPeerFromInbound records how to reach a node that connected to us, so replication can
// flow back to it over a link this node owns.
//
// The address comes from what the peer advertised. A peer too old to advertise anything still
// gets a usable address derived from its source IP and this node's peer port, which is correct
// whenever the fleet shares a peer port — the normal case — and simply fails to connect
// otherwise, exactly as it would have before.
func (s *Service) learnPeerFromInbound(id peerIdentity, c net.Conn) {
	if !s.c().AutoDiscoverPeersEnabled() {
		return
	}
	addr := strings.TrimSpace(id.Advertise)
	if addr == "" && c != nil && c.RemoteAddr() != nil {
		addr = s.fallbackAdvertiseAddr(c.RemoteAddr().String())
	}
	if addr == "" {
		return
	}
	if s.isSelf(id.NodeID, addr) {
		return
	}
	if err := s.AddPeerFrom(addr, SourceLearned); err != nil {
		// Already configured is the normal steady state, not a problem worth logging loudly.
		slog.Debug("peer not added from inbound handshake", "addr", addr, "err", err)
		return
	}
	slog.Info("learned peer from inbound connection", "addr", addr, "node_id", id.NodeID)
}

// fallbackAdvertiseAddr guesses a dialable address for a peer that did not advertise one, from
// its source IP and this node's peer port. The guess is right whenever the fleet shares a peer
// port, which is the normal deployment.
//
// A loopback or unspecified source is rejected: combined with our own peer port it would name
// this node, so dialing it would connect the node to itself.
func (s *Service) fallbackAdvertiseAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(s.c().PeerPort))
}

// startInboundReplLink registers an accepted connection as a replication target and starts its
// writer goroutine. Returns nil when the service is not running a session context.
func (s *Service) startInboundReplLink(ctx context.Context, c net.Conn, id peerIdentity) *outPeer {
	depth := s.c().PeerQueueDepth
	if depth < 1 {
		depth = 1
	}
	addr := strings.TrimSpace(id.Advertise)
	if addr == "" {
		addr = c.RemoteAddr().String()
	}
	link := &outPeer{
		addr:     addr,
		nodeID:   s.nodeID,
		remoteID: id.NodeID,
		inbound:  true,
		conn:     c,
		w:        bufio.NewWriter(c),
		replCh:   make(chan *replFrame, depth),
		metrics:  s.metrics,
	}
	sessCtx, cancel := context.WithCancel(ctx)
	link.cancelSession = cancel
	link.writerDone = make(chan struct{})
	go func() {
		outboundReplWriter(sessCtx, link)
		close(link.writerDone)
	}()
	s.registerOut(link)
	slog.Info("accepted peer link carrying replication", "peer", addr, "node_id", id.NodeID)
	return link
}

// stopInboundReplLink tears down an accepted replication link and waits for its writer to stop
// touching the socket. The wait matters because the caller may go on to write to the same
// connection directly (a bootstrap snapshot stream), and two writers would interleave and
// corrupt the framing. Safe to call more than once on the same link.
func (s *Service) stopInboundReplLink(link *outPeer) {
	if link == nil {
		return
	}
	// Stop accepting frames before cancelling, so nothing is queued that will never be sent.
	link.replStop.Store(true)
	s.unregisterLink(link)
	if link.cancelSession != nil {
		link.cancelSession()
	}
	if link.writerDone != nil {
		select {
		case <-link.writerDone:
		case <-time.After(2 * time.Second):
			slog.Warn("accepted peer link writer did not stop in time", "peer", link.addr)
		}
	}
}

// inboundShardFor picks the worker that applies a peer's events.
//
// Routing is by origin so that one peer's stream is always applied by one worker, in the order
// it was sent. Events from different origins have no defined order relative to each other, so
// spreading them across workers is free. A peer too old to name itself is routed by the
// connection it arrived on, which is equally stable.
func (s *Service) inboundShardFor(origin, remote string) chan inboundReplJob {
	if len(s.inboundShards) == 0 {
		return nil
	}
	key := strings.TrimSpace(origin)
	if key == "" {
		key = remote
	}
	if len(s.inboundShards) == 1 {
		return s.inboundShards[0]
	}
	var h uint32 = 2166136261 // FNV-1a
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return s.inboundShards[int(h%uint32(len(s.inboundShards)))]
}

// enqueueInbound hands one event to the worker that owns its origin. It reports false when the
// service has no workers, which happens in tests that drive sessions without starting Run.
func (s *Service) enqueueInbound(ctx context.Context, remote string, wr wireRepl) bool {
	ch := s.inboundShardFor(wr.Origin, remote)
	if ch == nil {
		return false
	}
	select {
	case ch <- inboundReplJob{s: s, remote: remote, wr: wr}:
	case <-ctx.Done():
	}
	return true
}

func (s *Service) inboundWorkerLoop(ctx context.Context, in <-chan inboundReplJob) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-in:
			if !ok {
				return
			}
			if job.s.bootstrapActive.Load() {
				if err := job.s.enqueueBootstrapRepl(job.wr); err != nil {
					slog.Warn("bootstrap replication queue full", "remote", job.remote)
				}
				continue
			}
			if err := applyWireRepl(job.s.st, job.wr); err != nil {
				slog.Error("peer apply", "err", err)
			}
		}
	}
}

// runInboundHandshake authenticates an accepted connection, discarding what it learns about
// the peer. Retained for tests and callers that do not need the peer's identity.
func (s *Service) runInboundHandshake(c net.Conn, br *bufio.Reader) error {
	_, err := s.runInboundHandshakeInfo(c, br)
	return err
}

// runInboundHandshakeInfo authenticates an accepted connection and reports what the peer said
// about itself: its identity, the address it can be reached at, and its capabilities.
func (s *Service) runInboundHandshakeInfo(c net.Conn, br *bufio.Reader) (peerIdentity, error) {
	var id peerIdentity
	remote := c.RemoteAddr().String()
	msg, err := ReadMessage(br)
	if err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHello {
		s.logPeerAuthFailure(remote, "expected HELLO")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "expected HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return id, fmt.Errorf("expected HELLO")
	}
	var hello wireHello
	if err := json.Unmarshal(msg.Payload, &hello); err != nil {
		s.logPeerAuthFailure(remote, "invalid HELLO json")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "invalid HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return id, fmt.Errorf("hello json: %w", err)
	}
	if !strings.EqualFold(hello.Op, wireOpHello) {
		s.logPeerAuthFailure(remote, "expected HELLO")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "expected HELLO"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return id, fmt.Errorf("expected HELLO")
	}
	// Accept a range rather than an exact match so that a future protocol bump can be rolled
	// out one node at a time instead of requiring the whole fleet to restart together.
	if hello.Ver < MinPeerProtocolVersion || hello.Ver > PeerProtocolVersion {
		s.logPeerAuthFailure(remote, "unsupported protocol version")
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Ver: PeerProtocolVersion, Err: "unsupported protocol version"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return id, fmt.Errorf("unsupported protocol version")
	}
	// The envelope also carries a node ID; fall back to it for peers that set only that field.
	id.NodeID = strings.TrimSpace(hello.NodeID)
	if id.NodeID == "" {
		id.NodeID = strings.TrimSpace(msg.NodeID)
	}
	id.Advertise = strings.TrimSpace(hello.Advertise)
	id.Duplex = hasCap(hello.Caps, CapDuplex)
	id.Version = hello.Ver

	nonce, err := randomNonce()
	if err != nil {
		return id, err
	}
	nonceHex := hex.EncodeToString(nonce)
	ackPayload, err := json.Marshal(wireHelloAck{
		Op:        wireOpHelloAck,
		OK:        true,
		Ver:       PeerProtocolVersion,
		Nonce:     nonceHex,
		NodeID:    s.nodeID,
		Advertise: s.AdvertiseAddr(),
		Caps:      localCaps(),
	})
	if err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: ackPayload}); err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	msg2, err := ReadMessage(br)
	if err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	msg2 = NormalizePeerMessage(msg2)
	if msg2.Type != MsgTypeAuth {
		s.logPeerAuthFailure(remote, "invalid AUTH frame")
		p, _ := json.Marshal(wireAck{Err: "invalid auth frame"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return id, fmt.Errorf("expected AUTH")
	}
	var proof wireAuthProof
	if err := json.Unmarshal(msg2.Payload, &proof); err != nil {
		s.logPeerAuthFailure(remote, "invalid AUTH frame")
		p, _ := json.Marshal(wireAck{Err: "invalid auth frame"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return id, fmt.Errorf("auth proof json: %w", err)
	}
	if !strings.EqualFold(proof.Op, wireOpAuth) ||
		proof.Ver < MinPeerProtocolVersion || proof.Ver > PeerProtocolVersion {
		s.logPeerAuthFailure(remote, "invalid AUTH op or version")
		p, _ := json.Marshal(wireAck{Err: "bad auth"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return id, fmt.Errorf("bad auth proof")
	}
	if !verifyPeerHMAC(s.c().SharedSecret, proof.Hmac, nonce) {
		s.logPeerAuthFailure(remote, "hmac verification failed")
		p, _ := json.Marshal(wireAck{Err: "bad auth"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return id, fmt.Errorf("hmac mismatch")
	}
	okPayload, err := json.Marshal(wireAck{OK: true})
	if err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: okPayload}); err != nil {
		return id, fmt.Errorf("peer handshake: %w", err)
	}
	return id, nil
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

// handshakeOut performs HELLO + HMAC AUTH and returns a Reader positioned for the next peer
// frame, discarding what it learns about the peer.
func (s *Service) handshakeOut(c net.Conn) (*bufio.Reader, error) {
	br, _, err := s.handshakeOutInfo(c)
	return br, err
}

// handshakeOutInfo performs HELLO + HMAC AUTH and additionally reports the peer's identity,
// address, and capabilities. A peer running an older build answers without those fields, which
// leaves the returned identity zeroed and keeps this node on the original dial-only behaviour.
func (s *Service) handshakeOutInfo(c net.Conn) (*bufio.Reader, peerIdentity, error) {
	return s.handshakeOutCaps(c, localCaps())
}

// handshakeOutCaps performs the outbound handshake advertising a specific capability set.
//
// Bootstrap dials pass no capabilities: that connection is a one-shot snapshot transfer whose
// stream would be corrupted by replication frames sharing the socket. It still sends identity
// and advertisement, so the snapshot source learns how to reach the joining node.
func (s *Service) handshakeOutCaps(c net.Conn, caps []string) (*bufio.Reader, peerIdentity, error) {
	var id peerIdentity
	secret := s.c().SharedSecret
	hPayload, err := json.Marshal(wireHello{
		Op:        wireOpHello,
		Ver:       PeerProtocolVersion,
		NodeID:    s.nodeID,
		Advertise: s.AdvertiseAddr(),
		Caps:      caps,
	})
	if err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, NodeID: s.nodeID, Payload: hPayload}); err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	br := bufio.NewReader(c)
	msg, err := ReadMessage(br)
	if err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHelloAck {
		return nil, id, fmt.Errorf("peer: expected HELLO_ACK")
	}
	var ack wireHelloAck
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		return nil, id, fmt.Errorf("hello_ack: %w", err)
	}
	if !strings.EqualFold(ack.Op, wireOpHelloAck) {
		return nil, id, fmt.Errorf("peer: expected HELLO_ACK")
	}
	if !ack.OK || ack.Err != "" {
		if ack.Err != "" {
			return nil, id, fmt.Errorf("peer hello: %s", ack.Err)
		}
		return nil, id, fmt.Errorf("peer hello failed")
	}
	nonce, err := hex.DecodeString(ack.Nonce)
	if err != nil {
		return nil, id, fmt.Errorf("peer nonce: %w", err)
	}
	proofPayload, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce)})
	if err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, NodeID: s.nodeID, Payload: proofPayload}); err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	msg3, err := ReadMessage(br)
	if err != nil {
		return nil, id, fmt.Errorf("peer handshake: %w", err)
	}
	msg3 = NormalizePeerMessage(msg3)
	if msg3.Type != MsgTypeAck {
		return nil, id, fmt.Errorf("peer: expected ACK after AUTH")
	}
	var final wireAck
	if err := json.Unmarshal(msg3.Payload, &final); err != nil {
		return nil, id, fmt.Errorf("auth ack: %w", err)
	}
	if !final.OK || final.Err != "" {
		if final.Err != "" {
			return nil, id, fmt.Errorf("peer auth: %s", final.Err)
		}
		return nil, id, fmt.Errorf("peer auth failed")
	}
	id.NodeID = strings.TrimSpace(ack.NodeID)
	if id.NodeID == "" {
		id.NodeID = strings.TrimSpace(msg.NodeID)
	}
	id.Advertise = strings.TrimSpace(ack.Advertise)
	id.Duplex = hasCap(ack.Caps, CapDuplex)
	id.Version = ack.Ver
	return br, id, nil
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
		br, id, err := s.handshakeOutInfo(c)
		if err != nil {
			_ = c.Close()
			if err := backoffSleep(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		// A configured address can turn out to be this node itself, most often when a learned
		// or gossiped peer list is fed back to its origin. Drop the dial rather than looping.
		if id.NodeID != "" && id.NodeID == s.nodeID {
			slog.Warn("peer dial reached this node; dropping self-connection", "addr", addr)
			_ = c.Close()
			return
		}
		s.markPeerReachable(addr)
		if err := s.consumeOptionalPeerAnnounce(c, br); err != nil {
			_ = c.Close()
			if err := backoffSleep(ctx, attempt); err != nil {
				return
			}
			attempt++
			continue
		}
		s.outboundPeerSession(ctx, addr, c, br, id)
		if ctx.Err() != nil {
			return
		}
		attempt = 0
	}
}

func (s *Service) registerOut(op *outPeer) {
	op.linkSeq = s.linkSeq.Add(1)
	s.mu.Lock()
	s.out = append(s.out, op)
	s.targetsGen.Add(1)
	s.mu.Unlock()
	s.refreshMetrics()
}

// unregisterOut removes dialed links to addr. Accepted links are skipped even when they carry
// the same address: a peer's advertised address equals the address we dial it on, so removing
// by address alone would tear down the accepted link as collateral when a dial ends.
func (s *Service) unregisterOut(addr string) {
	s.mu.Lock()
	dst := s.out[:0]
	for _, p := range s.out {
		if p.inbound || p.addr != addr {
			dst = append(dst, p)
		}
	}
	s.out = dst
	s.targetsGen.Add(1)
	s.mu.Unlock()
	s.refreshMetrics()
}

// unregisterLink removes one specific link. Accepted links share no stable address key with
// each other, so they must be removed by identity rather than by address.
func (s *Service) unregisterLink(target *outPeer) {
	if target == nil {
		return
	}
	s.mu.Lock()
	dst := s.out[:0]
	for _, p := range s.out {
		if p != target {
			dst = append(dst, p)
		}
	}
	s.out = dst
	s.targetsGen.Add(1)
	s.mu.Unlock()
	s.refreshMetrics()
}

func (s *Service) refreshMetrics() {
	if s.metrics == nil {
		return
	}
	s.mu.RLock()
	// Report only dialed links here. Accepted links are already counted by inboundCount, and
	// listing them as outbound connections would double-count every duplex peer in INFO.
	addrs := make([]string, 0, len(s.out))
	for _, p := range s.out {
		if !p.inbound {
			addrs = append(addrs, p.addr)
		}
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

// LiveLinkCount reports how many peer links are currently registered, in either direction.
//
// A node must have at least one before it pulls a snapshot. Replication arriving during the
// transfer is buffered and applied afterwards, but only frames that actually arrive: with no
// link there is nothing to buffer, and every write the source makes between the snapshot being
// taken and the link coming up is lost with nothing to detect it.
func (s *Service) LiveLinkCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.out)
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

// AddPeer appends a peer to the in-memory config and starts dialing it. The address is treated
// as operator intent and is never forgotten automatically.
func (s *Service) AddPeer(addr string) error {
	return s.AddPeerFrom(addr, SourceManual)
}

// NoteConfigPeers marks the addresses that came from the configuration file, so they are never
// removed by the cleanup below however long they stay unreachable.
func (s *Service) NoteConfigPeers(addrs []string) {
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		s.recordPeerMeta(a, SourceConfig)
	}
}

func (s *Service) recordPeerMeta(addr string, src PeerSource) {
	key := config.NormalizePeerAddr(addr)
	s.peerMetaMu.Lock()
	defer s.peerMetaMu.Unlock()
	if s.peerMeta == nil {
		s.peerMeta = make(map[string]*peerMeta)
	}
	if m, ok := s.peerMeta[key]; ok {
		// An address that is both configured and later discovered keeps the stronger claim.
		if !src.forgettable() {
			m.source = src
		}
		return
	}
	s.peerMeta[key] = &peerMeta{addr: addr, source: src, added: time.Now().UnixMilli()}
}

// markPeerReachable records that an address completed a handshake, which is the only evidence
// that it is a real peer rather than a leftover.
func (s *Service) markPeerReachable(addr string) {
	key := config.NormalizePeerAddr(addr)
	s.peerMetaMu.Lock()
	defer s.peerMetaMu.Unlock()
	if m, ok := s.peerMeta[key]; ok {
		m.lastOK = time.Now().UnixMilli()
	}
}

// AddPeerFrom is AddPeer, recording where the address came from.
func (s *Service) AddPeerFrom(addr string, src PeerSource) error {
	addr = strings.TrimSpace(addr)
	if err := config.ValidatePeerAddr(addr); err != nil {
		return err
	}
	if s.isSelf("", addr) {
		return fmt.Errorf("peer %s is this node", addr)
	}
	s.configMu.Lock()
	cfg := s.c()
	want := config.NormalizePeerAddr(addr)
	for _, p := range cfg.Peers {
		if p == addr || config.NormalizePeerAddr(p) == want {
			s.configMu.Unlock()
			return fmt.Errorf("peer %s already configured", addr)
		}
	}
	newCfg := *cfg
	newCfg.Peers = append(append([]string(nil), cfg.Peers...), addr)
	s.cfg.Store(&newCfg)
	s.configMu.Unlock()
	s.recordPeerMeta(addr, src)
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

// Ensure Service implements Replicator.
var _ Replicator = (*Service)(nil)

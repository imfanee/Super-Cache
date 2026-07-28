// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Mixed-version compatibility tests for the peer mesh.
//
// These exercise a fleet where some nodes predate node identity, peer advertisement, and the
// duplex capability. The "old" endpoints here are hand-rolled rather than driven through the
// current code, so they keep asserting the original wire behaviour even as this package
// changes: they send exactly the JSON an older build sent, and accept exactly what it accepted.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

// legacyPeerServer emulates a node built before this change: it answers HELLO with a
// HELLO_ACK carrying no node id, no advertisement, and no capabilities, and it silently
// ignores any replication frame pushed at it on a connection it did not open.
type legacyPeerServer struct {
	ln     net.Listener
	secret string

	mu        sync.Mutex
	helloSeen []wireHello
	replSeen  []wireRepl
}

func newLegacyPeerServer(t *testing.T, secret string) *legacyPeerServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ls := &legacyPeerServer{ln: ln, secret: secret}
	go ls.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return ls
}

func (l *legacyPeerServer) addr() string { return l.ln.Addr().String() }

func (l *legacyPeerServer) hellos() []wireHello {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]wireHello(nil), l.helloSeen...)
}

func (l *legacyPeerServer) repls() []wireRepl {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]wireRepl(nil), l.replSeen...)
}

func (l *legacyPeerServer) acceptLoop() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return
		}
		go l.serve(c)
	}
}

func (l *legacyPeerServer) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)

	msg, err := ReadMessage(br)
	if err != nil {
		return
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHello {
		return
	}
	var hello wireHello
	if json.Unmarshal(msg.Payload, &hello) != nil {
		return
	}
	l.mu.Lock()
	l.helloSeen = append(l.helloSeen, hello)
	l.mu.Unlock()

	// The original build required an exact version match and answered with these four
	// fields only.
	if hello.Ver != PeerProtocolVersion {
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Ver: PeerProtocolVersion, Err: "unsupported protocol version"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		return
	}
	nonce, err := randomNonce()
	if err != nil {
		return
	}
	ackp, _ := json.Marshal(wireHelloAck{
		Op:    wireOpHelloAck,
		OK:    true,
		Ver:   PeerProtocolVersion,
		Nonce: hex.EncodeToString(nonce),
	})
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: ackp}); err != nil {
		return
	}

	msg2, err := ReadMessage(br)
	if err != nil {
		return
	}
	msg2 = NormalizePeerMessage(msg2)
	if msg2.Type != MsgTypeAuth {
		return
	}
	var proof wireAuthProof
	if json.Unmarshal(msg2.Payload, &proof) != nil {
		return
	}
	if !verifyPeerHMAC(l.secret, proof.Hmac, nonce) {
		p, _ := json.Marshal(wireAck{Err: "bad auth"})
		_ = WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: p})
		return
	}
	okp, _ := json.Marshal(wireAck{OK: true})
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: okp}); err != nil {
		return
	}

	for {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
		m, err := ReadMessage(br)
		if err != nil {
			return
		}
		m = NormalizePeerMessage(m)
		switch m.Type {
		case MsgTypeReplicate:
			var wr wireRepl
			if json.Unmarshal(m.Payload, &wr) == nil {
				l.mu.Lock()
				l.replSeen = append(l.replSeen, wr)
				l.mu.Unlock()
			}
		case MsgTypeHeartbeat:
			ap, _ := json.Marshal(wireHeartbeat{Op: wireOpHeartbeatAck})
			bw := bufio.NewWriter(c)
			_ = WriteMessage(bw, PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: ap})
			_ = bw.Flush()
		}
	}
}

// legacyDial performs the handshake exactly as a pre-change node did: a HELLO carrying only op
// and ver, with no identity, advertisement, or capabilities.
func legacyDial(t *testing.T, addr, secret string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	hp, err := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	// A legacy node did put its (random, per-process) id in the envelope.
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, NodeID: "legacy-node", Payload: hp}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	br := bufio.NewReader(c)
	msg, err := ReadMessage(br)
	if err != nil {
		t.Fatalf("read hello_ack: %v", err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHelloAck {
		t.Fatalf("expected HELLO_ACK, got %q", msg.Type)
	}
	var ack wireHelloAck
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if !ack.OK {
		t.Fatalf("legacy HELLO rejected: %q", ack.Err)
	}
	nonce, err := hex.DecodeString(ack.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	pp, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce)})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, NodeID: "legacy-node", Payload: pp}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	msg3, err := ReadMessage(br)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	msg3 = NormalizePeerMessage(msg3)
	var final wireAck
	if err := json.Unmarshal(msg3.Payload, &final); err != nil {
		t.Fatal(err)
	}
	if !final.OK {
		t.Fatalf("legacy AUTH rejected: %q", final.Err)
	}
	return c, br
}

func newTestService(t *testing.T, secret, nodeID string, port int, peers []string) (*Service, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		SharedSecret: secret,
		ClientPort:   freeTCPPort(t),
		PeerPort:     port,
		Peers:        peers,
	}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	svc := NewService(cfg, st, nil, nil, nodeID)
	svc.SetAdvertiseAddr(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	return svc, cfg
}

func runService(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case err := <-done:
		t.Fatalf("service exited before listening: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for peer listener")
	}
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func linkCount(svc *Service) int {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	return len(svc.out)
}

// TestProtocolVersionStaysAtTwo guards the rollout rule. Announcing a higher version would be
// rejected outright by every node that has not been upgraded yet, because those builds compare
// the HELLO version for equality. New behaviour must ride on capabilities instead.
func TestProtocolVersionStaysAtTwo(t *testing.T) {
	if PeerProtocolVersion != 2 {
		t.Fatalf("PeerProtocolVersion = %d; raising it breaks every not-yet-upgraded peer during a rolling deploy", PeerProtocolVersion)
	}
	if MinPeerProtocolVersion > PeerProtocolVersion {
		t.Fatal("MinPeerProtocolVersion must not exceed PeerProtocolVersion")
	}
}

// TestLegacyHelloShapeUnchanged asserts the HELLO an older node sends still deserialises and
// that the new fields are absent from its JSON, which is what makes them safe to add.
func TestLegacyHelloShapeUnchanged(t *testing.T) {
	b, err := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, field := range []string{"node_id", "adv", "caps"} {
		if strings.Contains(got, field) {
			t.Fatalf("legacy-shaped HELLO must not contain %q: %s", field, got)
		}
	}
	var back wireHello
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.NodeID != "" || back.Advertise != "" || len(back.Caps) != 0 {
		t.Fatal("absent optional fields must decode as empty, marking the peer as legacy")
	}
	if hasCap(back.Caps, CapDuplex) {
		t.Fatal("a legacy peer must never be treated as duplex-capable")
	}
}

// TestNewDialsLegacyPeer covers one half of a rolling deploy: an upgraded node dialing a node
// that has not been upgraded yet. The handshake must succeed, duplex must not be negotiated,
// and replication must still flow over the dialed connection exactly as before.
func TestNewDialsLegacyPeer(t *testing.T) {
	secret := strings.Repeat("L", 32)
	legacy := newLegacyPeerServer(t, secret)

	svc, _ := newTestService(t, secret, "new-node-id", freeTCPPort(t), []string{legacy.addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svc, ctx)

	waitFor(t, "outbound link to legacy peer", func() bool { return linkCount(svc) >= 1 })

	hellos := legacy.hellos()
	if len(hellos) == 0 {
		t.Fatal("legacy peer saw no HELLO")
	}
	// The new fields are sent, and a legacy peer simply ignores them.
	if hellos[0].Ver != PeerProtocolVersion {
		t.Fatalf("HELLO ver = %d, want %d", hellos[0].Ver, PeerProtocolVersion)
	}

	svc.mu.RLock()
	link := svc.out[0]
	svc.mu.RUnlock()
	if link.inbound {
		t.Fatal("link to a legacy peer must be an outbound dial")
	}
	if link.remoteID != "" {
		t.Fatalf("legacy peer advertised no id, got remoteID %q", link.remoteID)
	}

	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k1", Value: []byte("v1")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "replication delivered to legacy peer", func() bool { return len(legacy.repls()) >= 1 })
	if got := legacy.repls()[0].Key; got != "k1" {
		t.Fatalf("legacy peer received key %q, want k1", got)
	}
}

// TestLegacyDialsNewPeer covers the other half of a rolling deploy: a node that has not been
// upgraded dialing an upgraded one. The upgraded node must accept it, must not register it as
// a duplex link (a legacy peer drops writes on a socket it opened), and must still learn a
// dial-back address for it so replication can reach it the original way.
func TestLegacyDialsNewPeer(t *testing.T) {
	secret := strings.Repeat("M", 32)
	port := freeTCPPort(t)
	svc, _ := newTestService(t, secret, "new-acceptor", port, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svc, ctx)

	c, _ := legacyDial(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), secret)
	defer c.Close()

	waitFor(t, "inbound connection counted", func() bool { return svc.inboundCount.Load() >= 1 })

	// Give the acceptor a moment to register a link if it were going to.
	time.Sleep(200 * time.Millisecond)
	svc.mu.RLock()
	for _, l := range svc.out {
		if l.inbound {
			svc.mu.RUnlock()
			t.Fatal("must not push replication to a peer that never advertised the duplex capability")
		}
	}
	svc.mu.RUnlock()

	// This peer connected over loopback, so no dial-back address can be guessed for it: the
	// guess would be this node's own address. Learning from a legacy peer is covered by
	// TestFallbackAdvertiseAddr, which exercises a realistic non-loopback source.
	for _, p := range svc.ConfigPeerAddrs() {
		if strings.HasPrefix(p, "127.0.0.1:") {
			t.Fatalf("guessed a loopback dial-back address %q, which names this node", p)
		}
	}
}

// TestFallbackAdvertiseAddr covers the dial-back address guessed for a peer that is too old to
// advertise one. This is what lets replication reach a not-yet-upgraded node during a rollout.
func TestFallbackAdvertiseAddr(t *testing.T) {
	secret := strings.Repeat("Z", 32)
	svc, cfg := newTestService(t, secret, "fb-node", freeTCPPort(t), nil)
	cfg.PeerPort = 7379
	svc.SetConfig(cfg)

	cases := []struct {
		remote string
		want   string
	}{
		// An ephemeral source port is replaced by the fleet's peer port.
		{"10.0.0.7:54321", "10.0.0.7:7379"},
		{"[fd00::5]:41000", "[fd00::5]:7379"},
		// Addresses that would resolve back to this node are refused.
		{"127.0.0.1:54321", ""},
		{"0.0.0.0:54321", ""},
		// Malformed input must not produce a peer entry.
		{"not-an-address", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := svc.fallbackAdvertiseAddr(tc.remote); got != tc.want {
			t.Errorf("fallbackAdvertiseAddr(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

// TestLegacyRejectsUnknownVersionStillHandshakes proves the accept-range did not loosen what we
// send: a legacy peer that demands an exact version match still completes the handshake, which
// is only true while PeerProtocolVersion is unchanged.
func TestLegacyRejectsUnknownVersionStillHandshakes(t *testing.T) {
	secret := strings.Repeat("N", 32)
	legacy := newLegacyPeerServer(t, secret)
	svc, _ := newTestService(t, secret, "ver-node", freeTCPPort(t), nil)

	c, err := net.DialTimeout("tcp", legacy.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, err := svc.handshakeOutInfo(c); err != nil {
		t.Fatalf("upgraded node failed to handshake with a legacy peer: %v", err)
	}
}

// TestVersionAcceptRange checks the acceptor's range check: versions inside the supported
// window are accepted, versions outside are refused with the original error.
func TestVersionAcceptRange(t *testing.T) {
	secret := strings.Repeat("O", 32)
	port := freeTCPPort(t)
	svc, _ := newTestService(t, secret, "range-node", port, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svc, ctx)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for _, tc := range []struct {
		ver    int
		accept bool
	}{
		{MinPeerProtocolVersion, true},
		{PeerProtocolVersion, true},
		{MinPeerProtocolVersion - 1, false},
		{PeerProtocolVersion + 1, false},
	} {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		hp, _ := json.Marshal(wireHello{Op: wireOpHello, Ver: tc.ver})
		if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hp}); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(c)
		msg, err := ReadMessage(br)
		if err != nil {
			_ = c.Close()
			t.Fatalf("ver %d: read hello_ack: %v", tc.ver, err)
		}
		var ack wireHelloAck
		if err := json.Unmarshal(NormalizePeerMessage(msg).Payload, &ack); err != nil {
			_ = c.Close()
			t.Fatal(err)
		}
		if ack.OK != tc.accept {
			t.Errorf("ver %d: accepted = %v, want %v (err %q)", tc.ver, ack.OK, tc.accept, ack.Err)
		}
		_ = c.Close()
	}
}

// TestDuplexMeshDeliversWriteExactlyOnce is the core safety property of full-duplex links.
//
// Two upgraded nodes each dial the other, so each ends up holding two links to its peer: its
// own outbound dial and the accepted connection carrying the peer's dial. Sending a write on
// both would apply it twice. LPUSH is used deliberately because it is not idempotent: a
// duplicate delivery leaves two elements in the list instead of one, which is exactly the
// corruption this de-duplication exists to prevent.
func TestDuplexMeshDeliversWriteExactlyOnce(t *testing.T) {
	secret := strings.Repeat("P", 32)
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)
	addrA := net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))
	addrB := net.JoinHostPort("127.0.0.1", strconv.Itoa(portB))

	svcA, _ := newTestService(t, secret, "node-a", portA, []string{addrB})
	svcB, _ := newTestService(t, secret, "node-b", portB, []string{addrA})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svcA, ctx)
	runService(t, svcB, ctx)

	// Each node should end up with two links to the other: one dialed, one accepted.
	waitFor(t, "both links established on A", func() bool { return linkCount(svcA) >= 2 })
	waitFor(t, "both links established on B", func() bool { return linkCount(svcB) >= 2 })

	svcA.mu.RLock()
	var sawInbound, sawOutbound bool
	for _, l := range svcA.out {
		if l.inbound {
			sawInbound = true
		} else {
			sawOutbound = true
		}
		if l.remoteID != "node-b" {
			t.Errorf("link remoteID = %q, want node-b", l.remoteID)
		}
	}
	svcA.mu.RUnlock()
	if !sawInbound || !sawOutbound {
		t.Fatalf("expected both an accepted and a dialed link (inbound=%v outbound=%v)", sawInbound, sawOutbound)
	}

	// Despite two live links, exactly one target is selected per write.
	if got := len(svcA.replicationTargets()); got != 1 {
		t.Fatalf("replicationTargets = %d, want 1 (a second target duplicates every write)", got)
	}
	if svcA.replicationTargets()[0].inbound {
		t.Fatal("the dialed link should be preferred, since every peer understands it")
	}

	if err := svcA.Replicate(ReplicatePayload{Op: "RPUSH", Key: "list1", Members: [][]byte{[]byte("one")}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "list replicated to B", func() bool {
		n, err := svcB.st.LLen("list1")
		return err == nil && n >= 1
	})
	// Settle, then confirm no second copy arrived over the other link.
	time.Sleep(300 * time.Millisecond)
	n, err := svcB.st.LLen("list1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("list length = %d, want 1; the write was delivered over both links", n)
	}
}

// TestDuplexReachesUnconfiguredJoiner is the autoscaling case: a new node knows one existing
// peer, and the existing node knows nothing about it. Writes made on the existing node must
// still reach the joiner, which was impossible when replication only flowed over dialed links.
func TestDuplexReachesUnconfiguredJoiner(t *testing.T) {
	secret := strings.Repeat("Q", 32)
	portSeed := freeTCPPort(t)
	portJoiner := freeTCPPort(t)
	addrSeed := net.JoinHostPort("127.0.0.1", strconv.Itoa(portSeed))

	// The seed has an empty peer list: it has never heard of the joiner.
	seed, _ := newTestService(t, secret, "seed-node", portSeed, nil)
	joiner, _ := newTestService(t, secret, "joiner-node", portJoiner, []string{addrSeed})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, seed, ctx)
	runService(t, joiner, ctx)

	waitFor(t, "seed registers a link to the joiner", func() bool { return linkCount(seed) >= 1 })

	if err := seed.Replicate(ReplicatePayload{Op: "SET", Key: "from-seed", Value: []byte("hello")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "joiner receives the seed's write", func() bool {
		v, err := joiner.st.Get("from-seed")
		return err == nil && string(v) == "hello"
	})

	// The seed should also have learned a dial-back address for the joiner.
	waitFor(t, "seed learns the joiner's advertised address", func() bool {
		for _, p := range seed.ConfigPeerAddrs() {
			if config.NormalizePeerAddr(p) == config.NormalizePeerAddr(net.JoinHostPort("127.0.0.1", strconv.Itoa(portJoiner))) {
				return true
			}
		}
		return false
	})
}

// TestReplicationTargetsDeduplicatesByNodeID checks the selection rule directly, including the
// fallback for peers that never sent an identity.
func TestReplicationTargetsDeduplicatesByNodeID(t *testing.T) {
	secret := strings.Repeat("R", 32)
	svc, _ := newTestService(t, secret, "self", freeTCPPort(t), nil)

	outbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan wireRepl, 4)}
	inbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", inbound: true, replCh: make(chan wireRepl, 4)}
	other := &outPeer{addr: "10.0.0.2:7379", remoteID: "peer-2", replCh: make(chan wireRepl, 4)}
	legacyA := &outPeer{addr: "10.0.0.3:7379", replCh: make(chan wireRepl, 4)}
	legacyB := &outPeer{addr: "10.0.0.4:7379", replCh: make(chan wireRepl, 4)}

	// Register the accepted link first so preference cannot be an artefact of ordering.
	for _, l := range []*outPeer{inbound, outbound, other, legacyA, legacyB} {
		svc.registerOut(l)
	}

	targets := svc.replicationTargets()
	if len(targets) != 4 {
		t.Fatalf("targets = %d, want 4 (peer-1 collapsed, peer-2, and two unidentified peers)", len(targets))
	}
	for _, tg := range targets {
		if tg == inbound {
			t.Fatal("accepted link chosen while a dialed link to the same node exists")
		}
	}

	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	if len(outbound.replCh) != 1 {
		t.Fatalf("dialed link received %d frames, want 1", len(outbound.replCh))
	}
	if len(inbound.replCh) != 0 {
		t.Fatalf("accepted duplicate link received %d frames, want 0", len(inbound.replCh))
	}
	// Peers too old to identify themselves cannot be collapsed by identity, and must each
	// still receive the write exactly once.
	if len(legacyA.replCh) != 1 || len(legacyB.replCh) != 1 {
		t.Fatalf("unidentified peers got %d and %d frames, want 1 each", len(legacyA.replCh), len(legacyB.replCh))
	}
}

// TestPreferredLinkFailsOverToAcceptedSocket checks that losing the dialed link promotes the
// accepted one rather than silently dropping the peer.
func TestPreferredLinkFailsOverToAcceptedSocket(t *testing.T) {
	secret := strings.Repeat("S", 32)
	svc, _ := newTestService(t, secret, "self", freeTCPPort(t), nil)

	outbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan wireRepl, 4)}
	inbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", inbound: true, replCh: make(chan wireRepl, 4)}
	svc.registerOut(outbound)
	svc.registerOut(inbound)

	svc.unregisterLink(outbound)

	targets := svc.replicationTargets()
	if len(targets) != 1 || targets[0] != inbound {
		t.Fatal("accepted link must take over once the dialed link is gone")
	}
	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	if len(inbound.replCh) != 1 {
		t.Fatalf("accepted link received %d frames, want 1", len(inbound.replCh))
	}
}

// TestStoppedLinkIsNotATarget makes sure a link being torn down cannot receive a write, which
// would otherwise be lost silently rather than going to the surviving link.
func TestStoppedLinkIsNotATarget(t *testing.T) {
	secret := strings.Repeat("T", 32)
	svc, _ := newTestService(t, secret, "self", freeTCPPort(t), nil)

	dead := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan wireRepl, 4)}
	dead.replStop.Store(true)
	alive := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", inbound: true, replCh: make(chan wireRepl, 4)}
	svc.registerOut(dead)
	svc.registerOut(alive)

	targets := svc.replicationTargets()
	if len(targets) != 1 || targets[0] != alive {
		t.Fatal("a stopped link must not be selected while a live link to the same node exists")
	}
}

// TestEndingDialKeepsAcceptedLink guards a subtle failure mode: a peer's advertised address is
// the same string this node dials it on, so removing links by address when a dial ends would
// also tear down the accepted link to that peer. The node would then quietly stop replicating
// to it until the peer happened to reconnect.
func TestEndingDialKeepsAcceptedLink(t *testing.T) {
	secret := strings.Repeat("A", 32)
	svc, _ := newTestService(t, secret, "self", freeTCPPort(t), nil)

	const shared = "10.0.0.1:7379"
	outbound := &outPeer{addr: shared, remoteID: "peer-1", replCh: make(chan wireRepl, 4)}
	inbound := &outPeer{addr: shared, remoteID: "peer-1", inbound: true, replCh: make(chan wireRepl, 4)}
	svc.registerOut(outbound)
	svc.registerOut(inbound)

	svc.unregisterOut(shared)

	targets := svc.replicationTargets()
	if len(targets) != 1 || targets[0] != inbound {
		t.Fatalf("accepted link was removed along with the dialed link; targets = %d", len(targets))
	}
	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	if len(inbound.replCh) != 1 {
		t.Fatalf("accepted link received %d frames, want 1", len(inbound.replCh))
	}
}

// TestSelfConnectionGuard covers a node finding its own address in a learned peer list. Without
// the guard it would dial itself and replicate every write back into its own store, duplicating
// non-idempotent operations.
func TestSelfConnectionGuard(t *testing.T) {
	secret := strings.Repeat("U", 32)
	port := freeTCPPort(t)
	svc, _ := newTestService(t, secret, "self-node", port, nil)
	self := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	if !svc.isSelf("self-node", "10.9.9.9:7379") {
		t.Fatal("must recognise itself by node id")
	}
	if !svc.isSelf("", self) {
		t.Fatal("must recognise itself by advertised address")
	}
	if svc.isSelf("other-node", "10.9.9.9:7379") {
		t.Fatal("must not mistake another node for itself")
	}
	if err := svc.AddPeer(self); err == nil {
		t.Fatal("AddPeer must refuse this node's own address")
	}
	for _, p := range svc.ConfigPeerAddrs() {
		if config.NormalizePeerAddr(p) == config.NormalizePeerAddr(self) {
			t.Fatal("this node's own address was added to its peer list")
		}
	}
}

// TestAddPeerRejectsDuplicateInDifferentForm prevents the same peer being dialed twice because
// its address was written differently in config and in an advertisement.
func TestAddPeerRejectsDuplicateInDifferentForm(t *testing.T) {
	secret := strings.Repeat("V", 32)
	svc, _ := newTestService(t, secret, "dup-node", freeTCPPort(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svc, ctx)

	if err := svc.AddPeer("10.20.30.40:7379"); err != nil {
		t.Fatalf("first add failed: %v", err)
	}
	if err := svc.AddPeer(" 10.20.30.40:7379 "); err == nil {
		t.Fatal("the same address with surrounding whitespace must be rejected as a duplicate")
	}
	n := 0
	for _, p := range svc.ConfigPeerAddrs() {
		if config.NormalizePeerAddr(p) == "10.20.30.40:7379" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("peer appears %d times in the list, want 1", n)
	}
}

// TestAutoDiscoverCanBeDisabled confirms the kill switch, so an operator can pin membership to
// the configured list if automatic learning is not wanted.
func TestAutoDiscoverCanBeDisabled(t *testing.T) {
	secret := strings.Repeat("W", 32)
	port := freeTCPPort(t)
	svc, cfg := newTestService(t, secret, "no-discover", port, nil)
	off := false
	cfg.AutoDiscoverPeers = &off
	svc.SetConfig(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, svc, ctx)

	c, _ := legacyDial(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), secret)
	defer c.Close()
	waitFor(t, "inbound connection counted", func() bool { return svc.inboundCount.Load() >= 1 })
	time.Sleep(200 * time.Millisecond)

	if got := len(svc.ConfigPeerAddrs()); got != 0 {
		t.Fatalf("peer list has %d entries, want 0 when auto discovery is disabled", got)
	}
}

// TestBootstrapDialAdvertisesNoCapabilities protects the snapshot stream. A bootstrap
// connection carries a snapshot written straight to the socket, so the source must not also
// push replication frames onto it; the joiner prevents that by advertising no capabilities.
func TestBootstrapDialAdvertisesNoCapabilities(t *testing.T) {
	secret := strings.Repeat("X", 32)
	legacy := newLegacyPeerServer(t, secret)
	svc, _ := newTestService(t, secret, "boot-node", freeTCPPort(t), nil)

	c, err := net.DialTimeout("tcp", legacy.addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, _, err := svc.handshakeOutCaps(c, nil); err != nil {
		t.Fatalf("bootstrap-style handshake failed: %v", err)
	}
	hellos := legacy.hellos()
	if len(hellos) == 0 {
		t.Fatal("no HELLO observed")
	}
	h := hellos[len(hellos)-1]
	if len(h.Caps) != 0 {
		t.Fatalf("bootstrap dial advertised capabilities %v; the snapshot stream would be interleaved with replication", h.Caps)
	}
	// Identity and advertisement must still be present so the source can learn the joiner.
	if h.NodeID != "boot-node" {
		t.Fatalf("bootstrap HELLO node_id = %q, want boot-node", h.NodeID)
	}
	if h.Advertise == "" {
		t.Fatal("bootstrap HELLO must still advertise a dial-back address")
	}
}

// TestDuplexBootstrapDoesNotCorruptSnapshot runs a real bootstrap pull against a node that has
// duplex enabled and is actively replicating, and checks the snapshot arrives intact.
func TestDuplexBootstrapDoesNotCorruptSnapshot(t *testing.T) {
	secret := strings.Repeat("Y", 32)
	portSrc := freeTCPPort(t)
	src, _ := newTestService(t, secret, "src-node", portSrc, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runService(t, src, ctx)

	const nKeys = 200
	for i := 0; i < nKeys; i++ {
		if err := src.st.Set(fmt.Sprintf("bk%d", i), []byte(fmt.Sprintf("value-%d", i)), time.Time{}); err != nil {
			t.Fatal(err)
		}
	}

	joiner, _ := newTestService(t, secret, "joiner-node", freeTCPPort(t), nil)
	runService(t, joiner, ctx)

	if err := joiner.PullSnapshot(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(portSrc))); err != nil {
		t.Fatalf("bootstrap pull failed: %v", err)
	}
	for i := 0; i < nKeys; i++ {
		v, err := joiner.st.Get(fmt.Sprintf("bk%d", i))
		if err != nil {
			t.Fatalf("key bk%d: %v", i, err)
		}
		if want := fmt.Sprintf("value-%d", i); string(v) != want {
			t.Fatalf("key bk%d = %q, want %q", i, v, want)
		}
	}
}

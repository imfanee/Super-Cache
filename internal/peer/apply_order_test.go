// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests that one peer's events are applied in the order that peer sent them.
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
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

func TestInboundShardForIsStablePerOrigin(t *testing.T) {
	s := &Service{inboundShards: make([]chan inboundReplJob, 8)}
	for i := range s.inboundShards {
		s.inboundShards[i] = make(chan inboundReplJob)
	}
	first := s.inboundShardFor("peer-a", "10.0.0.1:7379")
	for i := 0; i < 50; i++ {
		// A different source address must not move a peer to another worker, or a link switch
		// would start applying its events on two workers at once.
		if got := s.inboundShardFor("peer-a", "10.0.0.9:7379"); got != first {
			t.Fatal("an origin must always map to the same shard")
		}
	}
}

func TestInboundShardForFallsBackToRemote(t *testing.T) {
	s := &Service{inboundShards: make([]chan inboundReplJob, 8)}
	for i := range s.inboundShards {
		s.inboundShards[i] = make(chan inboundReplJob)
	}
	// A peer too old to name itself is still routed consistently, by its connection.
	first := s.inboundShardFor("", "10.0.0.1:7379")
	if got := s.inboundShardFor("", "10.0.0.1:7379"); got != first {
		t.Fatal("an unnamed peer must map to a stable shard")
	}
}

func TestInboundShardForSpreadsOrigins(t *testing.T) {
	s := &Service{inboundShards: make([]chan inboundReplJob, 8)}
	for i := range s.inboundShards {
		s.inboundShards[i] = make(chan inboundReplJob)
	}
	seen := make(map[chan inboundReplJob]bool)
	for i := 0; i < 200; i++ {
		seen[s.inboundShardFor("peer-"+strconv.Itoa(i), "")] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected origins to spread across shards, used %d", len(seen))
	}
}

func TestInboundShardForNoWorkers(t *testing.T) {
	s := &Service{}
	if s.inboundShardFor("peer-a", "10.0.0.1:7379") != nil {
		t.Fatal("expected no shard when the service has no workers")
	}
}

// TestOneOriginAppliedInOrder is the regression this exists for. A peer's consecutive writes to
// the same key must land in the order it made them: applying them concurrently let an earlier
// value overwrite a later one, so a key could settle on a value its owner had already replaced.
func TestOneOriginAppliedInOrder(t *testing.T) {
	secret := strings.Repeat("o", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
	config.ApplyDefaults(cfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PeerPort = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc := NewService(cfg, st, nil, nil, "receiver")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}

	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	br := authenticateAs(t, c, secret)

	// Every key is written twice in a row by the same origin. The second value must win on all
	// of them; with concurrent apply the first would sometimes land last.
	const keys = 400
	seq := uint64(0)
	send := func(key, val string) {
		seq++
		payload, err := json.Marshal(wireRepl{
			Op: "SET", Key: key, Value: []byte(val),
			V: ReplEnvelopeVersion, Seq: seq, Origin: "sender-node",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < keys; i++ {
		k := "ordered:" + strconv.Itoa(i)
		send(k, "first")
		send(k, "second")
	}
	_ = br

	deadline := time.Now().Add(10 * time.Second)
	var wrong []string
	for time.Now().Before(deadline) {
		wrong = wrong[:0]
		for i := 0; i < keys; i++ {
			v, err := st.Get("ordered:" + strconv.Itoa(i))
			if err != nil || v == nil {
				wrong = append(wrong, fmt.Sprintf("ordered:%d missing", i))
				continue
			}
			if string(v) != "second" {
				wrong = append(wrong, fmt.Sprintf("ordered:%d=%s", i, v))
			}
		}
		if len(wrong) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	shown := wrong
	if len(shown) > 5 {
		shown = shown[:5]
	}
	t.Fatalf("%d of %d keys did not settle on the later write: %v", len(wrong), keys, shown)
}

// authenticateAs completes the peer handshake and returns a reader positioned after it.
func authenticateAs(t *testing.T, c net.Conn, secret string) *bufio.Reader {
	t.Helper()
	hPayload, err := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion, NodeID: "sender-node"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hPayload}); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	msg, err := ReadMessage(br)
	if err != nil {
		t.Fatal(err)
	}
	var ack wireHelloAck
	if err := json.Unmarshal(NormalizePeerMessage(msg).Payload, &ack); err != nil {
		t.Fatal(err)
	}
	nonce, err := hex.DecodeString(ack.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce, "")})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proof}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(br); err != nil {
		t.Fatal(err)
	}
	return br
}

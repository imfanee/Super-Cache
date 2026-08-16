// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests that a node still syncing never hands out its partial store as a snapshot.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

// bootstrapProbe stands up a service holding one key, authenticates to it, sends a snapshot
// request, and reports whether any snapshot data came back.
func bootstrapProbe(t *testing.T, syncing bool) bool {
	t.Helper()
	secret := strings.Repeat("7", 32)
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
	if err := st.Set("k", []byte("v"), time.Time{}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(cfg, st, nil, nil, "src")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	if syncing {
		// Exactly what the server sets while it is pulling its own snapshot.
		svc.SetBootstrapInboundActive(true, 10)
	}

	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	hPayload, _ := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion})
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
	proof, _ := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce, "")})
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proof}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(br); err != nil {
		t.Fatal(err)
	}

	req, _ := json.Marshal(wireRepl{Op: wireOpBootstrap})
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeBootstrapReq, Payload: req}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 10; i++ {
		m, err := ReadMessage(br)
		if err != nil {
			return false
		}
		switch NormalizePeerMessage(m).Type {
		case MsgTypeBootstrapChunk:
			return true
		case MsgTypeBootstrapDone:
			return false
		}
	}
	return false
}

// TestSyncingNodeRefusesToServeSnapshot is the hazard created by treating peers as snapshot
// sources: several nodes that list each other may start together, and a node that served its
// own half-filled store would leave the requester permanently missing whatever had not arrived
// there yet. The requester sees a closed connection and moves to the next source.
func TestSyncingNodeRefusesToServeSnapshot(t *testing.T) {
	if bootstrapProbe(t, true) {
		t.Fatal("a node still syncing must not serve its partial store as a snapshot")
	}
}

// TestReadyNodeServesSnapshot is the control: the same request against a settled node returns
// data, so the test above is measuring the guard and not a broken handshake.
func TestReadyNodeServesSnapshot(t *testing.T) {
	if !bootstrapProbe(t, false) {
		t.Fatal("a settled node should serve its snapshot")
	}
}

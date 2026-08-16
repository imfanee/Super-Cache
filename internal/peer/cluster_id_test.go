// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests that a cluster identifier separates two clusters sharing a secret, without breaking
// authentication for nodes that do not have one yet.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
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

// TestUnboundProofIsUnchanged is the compatibility guarantee the whole design rests on: with no
// cluster identifier the proof must be byte-identical to what nodes computed before identifiers
// existed, or every un-upgraded node would start rejecting this one.
func TestUnboundProofIsUnchanged(t *testing.T) {
	secret := strings.Repeat("k", 32)
	nonce := []byte("0123456789abcdef0123456789abcdef")

	// Recomputed here the way the original code did, deliberately not by calling peerHMAC.
	want := hmacSHA256(secret, nonce)
	got := peerHMAC(secret, nonce, "")
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatal("an unbound proof must match the pre-cluster_id computation exactly")
	}
}

func TestBoundProofDiffersPerCluster(t *testing.T) {
	secret := strings.Repeat("k", 32)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	a := peerHMACHex(secret, nonce, "cluster-a")
	b := peerHMACHex(secret, nonce, "cluster-b")
	plain := peerHMACHex(secret, nonce, "")
	if a == b {
		t.Fatal("two clusters must not produce the same proof")
	}
	if a == plain || b == plain {
		t.Fatal("a bound proof must differ from an unbound one")
	}
}

func TestVerifyRejectsWrongCluster(t *testing.T) {
	secret := strings.Repeat("k", 32)
	nonce := []byte("0123456789abcdef0123456789abcdef")
	proof := peerHMACHex(secret, nonce, "cluster-a")
	if verifyPeerHMAC(secret, proof, nonce, "cluster-b") {
		t.Fatal("a proof from another cluster must not verify")
	}
	if !verifyPeerHMAC(secret, proof, nonce, "cluster-a") {
		t.Fatal("a proof from the same cluster must verify")
	}
}

// TestBindClusterIDNeedsBothSides covers the negotiation. A node only binds when the other end
// said it has an identifier too, which is what lets a fleet upgrade one node at a time.
func TestBindClusterIDNeedsBothSides(t *testing.T) {
	if got := bindClusterID([]string{CapCluster}, "mine"); got != "mine" {
		t.Fatalf("both sides ready should bind, got %q", got)
	}
	if got := bindClusterID([]string{CapDuplex}, "mine"); got != "" {
		t.Fatalf("a peer without the capability must not be bound against, got %q", got)
	}
	if got := bindClusterID([]string{CapCluster}, ""); got != "" {
		t.Fatalf("a node with no identifier cannot bind, got %q", got)
	}
	if got := bindClusterID(nil, "mine"); got != "" {
		t.Fatalf("a peer advertising nothing must not be bound against, got %q", got)
	}
}

func TestAuthCapsOnlyWhenConfigured(t *testing.T) {
	if got := authCaps(""); len(got) != 0 {
		t.Fatalf("no identifier means no capability, got %v", got)
	}
	if got := authCaps("  "); len(got) != 0 {
		t.Fatalf("whitespace is not an identifier, got %v", got)
	}
	if got := authCaps("prod"); len(got) != 1 || got[0] != CapCluster {
		t.Fatalf("expected the cluster capability, got %v", got)
	}
}

// clusterNode starts a service with the given cluster identifier and returns its peer port.
func clusterNode(t *testing.T, secret, clusterID, nodeID string) int {
	t.Helper()
	cfg := &config.Config{SharedSecret: secret, ClusterID: clusterID, ClientPort: 6379, PeerPort: 0}
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
	t.Cleanup(func() { st.Close() })

	svc := NewService(cfg, st, nil, nil, nodeID)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	return cfg.PeerPort
}

// tryHandshake authenticates to a node as a client carrying the given cluster identifier, and
// reports whether the handshake was accepted.
func tryHandshake(t *testing.T, port int, secret, clusterID string) bool {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	hPayload, err := json.Marshal(wireHello{
		Op: wireOpHello, Ver: PeerProtocolVersion, NodeID: "prober",
		Caps: authCaps(clusterID),
	})
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
	proof, err := json.Marshal(wireAuthProof{
		Op: wireOpAuth, Ver: PeerProtocolVersion,
		Hmac: peerHMACHex(secret, nonce, bindClusterID(ack.Caps, clusterID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proof}); err != nil {
		t.Fatal(err)
	}
	final, err := ReadMessage(br)
	if err != nil {
		return false
	}
	var res wireAck
	if err := json.Unmarshal(NormalizePeerMessage(final).Payload, &res); err != nil {
		return false
	}
	return res.OK
}

// TestDifferentClusterRejectedDespiteSameSecret is the property this feature exists for. A node
// built from another cluster's image carries that cluster's secret; without an identifier it
// authenticates and joins, taking the other cluster's data with it.
func TestDifferentClusterRejectedDespiteSameSecret(t *testing.T) {
	secret := strings.Repeat("s", 32)
	port := clusterNode(t, secret, "cluster-a", "node-a")

	if tryHandshake(t, port, secret, "cluster-b") {
		t.Fatal("a node from another cluster must not authenticate, even with the right secret")
	}
	if !tryHandshake(t, port, secret, "cluster-a") {
		t.Fatal("a node from the same cluster must authenticate")
	}
}

// TestNodeWithoutClusterIDStillJoins is the rollout path: until every node has an identifier the
// old behaviour must keep working, or upgrading one node would partition the fleet.
func TestNodeWithoutClusterIDStillJoins(t *testing.T) {
	secret := strings.Repeat("s", 32)
	port := clusterNode(t, secret, "cluster-a", "node-a")
	if !tryHandshake(t, port, secret, "") {
		t.Fatal("a node that has no cluster_id yet must still authenticate")
	}
}

// TestClusterNodeJoinsNodeWithoutOne is the same rollout, in the other direction.
func TestClusterNodeJoinsNodeWithoutOne(t *testing.T) {
	secret := strings.Repeat("s", 32)
	port := clusterNode(t, secret, "", "node-plain")
	if !tryHandshake(t, port, secret, "cluster-a") {
		t.Fatal("a node with a cluster_id must still authenticate to one without")
	}
}

func TestWrongSecretStillRejected(t *testing.T) {
	port := clusterNode(t, strings.Repeat("s", 32), "cluster-a", "node-a")
	if tryHandshake(t, port, strings.Repeat("x", 32), "cluster-a") {
		t.Fatal("the wrong secret must still be rejected")
	}
}

// TestClusterIDNeverAppearsOnTheWire guards the reason for binding rather than comparing: a node
// must not answer "which cluster are you?" to anyone who can reach the port.
func TestClusterIDNeverAppearsOnTheWire(t *testing.T) {
	const secretCluster = "top-secret-cluster-name"
	port := clusterNode(t, strings.Repeat("s", 32), secretCluster, "node-a")

	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hPayload, _ := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion, Caps: []string{CapCluster}})
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hPayload}); err != nil {
		t.Fatal(err)
	}
	msg, err := ReadMessage(bufio.NewReader(c))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(msg.Payload), secretCluster) {
		t.Fatalf("the cluster identifier leaked in the handshake: %s", msg.Payload)
	}
}

// hmacSHA256 reproduces the original proof computation, so the compatibility test above does not
// simply call the code it is checking.
func hmacSHA256(secret string, nonce []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write(nonce)
	return m.Sum(nil)
}

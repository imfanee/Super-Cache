// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Peer authentication: HMAC-SHA256 challenge–response (P0). No shared secret on the wire.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// PeerProtocolVersion is the version this node announces in HELLO (2 = magic-framed wire).
//
// This value must not be raised until every node in the fleet accepts a version range.
// Releases before MinPeerProtocolVersion existed rejected any HELLO whose version was not
// exactly equal to their own, so announcing a higher version would make every not-yet-upgraded
// node refuse this one for the entire rollout. New behaviour is therefore negotiated through
// the additive Caps field below, which older nodes silently ignore.
const PeerProtocolVersion = 2

// MinPeerProtocolVersion is the oldest HELLO version this node still accepts. Accepting a
// range rather than requiring equality is what allows a future version bump to be rolled out
// one node at a time.
const MinPeerProtocolVersion = 2

const (
	wireOpHello    = "HELLO"
	wireOpHelloAck = "HELLO_ACK"
)

// CapDuplex advertises that this node applies replication frames arriving on a connection it
// opened itself, so a peer may push writes back down the accepted socket instead of relying on
// a separate dial in the opposite direction. Nodes predating this capability silently drop such
// frames, which is why it must be negotiated before use.
const CapDuplex = "duplex"

// localCaps are the capabilities this build advertises to peers.
func localCaps() []string {
	return []string{CapDuplex}
}

// hasCap reports whether a peer advertised the named capability. An older peer sends no caps at
// all, so the zero value correctly reports no support.
func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if strings.EqualFold(strings.TrimSpace(c), want) {
			return true
		}
	}
	return false
}

// wireHello is the first client frame after TCP connect (outbound) or first frame read (inbound).
//
// NodeID, Advertise, and Caps are additive: an older peer unmarshals this struct without them
// and is unaffected, and its own HELLO leaves them empty here.
type wireHello struct {
	Op  string `json:"op"`
	Ver int    `json:"ver"`
	// NodeID is the sender's stable cluster identity, used to recognise two connections that
	// reach the same node so writes are never delivered twice.
	NodeID string `json:"node_id,omitempty"`
	// Advertise is the host:port at which the sender's peer listener can be reached. This is
	// what lets an acceptor learn how to reach a node it was never configured with.
	Advertise string `json:"adv,omitempty"`
	// Caps lists optional protocol capabilities the sender supports.
	Caps []string `json:"caps,omitempty"`
}

// wireHelloAck is the server response to HELLO (challenge) or an error before AUTH.
type wireHelloAck struct {
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Ver   int    `json:"ver,omitempty"`
	Nonce string `json:"nonce,omitempty"` // hex-encoded random bytes (server-generated challenge)
	Err   string `json:"err,omitempty"`
	// NodeID, Advertise, and Caps mirror the HELLO fields so both ends finish the handshake
	// knowing the other's identity, address, and capabilities. Older acceptors omit them.
	NodeID    string   `json:"node_id,omitempty"`
	Advertise string   `json:"adv,omitempty"`
	Caps      []string `json:"caps,omitempty"`
}

// peerIdentity is what a completed handshake tells us about the node at the other end.
type peerIdentity struct {
	// NodeID is the remote's stable identity, or "" when it is too old to send one.
	NodeID string
	// Advertise is the remote's dialable host:port, or "" when it did not supply one.
	Advertise string
	// Duplex is true when both ends support pushing replication over a single connection.
	Duplex bool
	// Version is the negotiated protocol version.
	Version int
}

// wireAuthProof is the client's proof of possession of the shared secret (HMAC over nonce).
type wireAuthProof struct {
	Op  string `json:"op"`
	Ver int    `json:"ver"`
	// Hmac is hex-encoded HMAC-SHA256(secret, nonce_bytes) where nonce_bytes = hex-decode(Nonce from HELLO_ACK).
	Hmac string `json:"hmac"`
}

func peerHMAC(secret string, nonce []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write(nonce)
	return m.Sum(nil)
}

func peerHMACHex(secret string, nonce []byte) string {
	return hex.EncodeToString(peerHMAC(secret, nonce))
}

func verifyPeerHMAC(secret, hmacHex string, nonce []byte) bool {
	want := peerHMAC(secret, nonce)
	got, err := hex.DecodeString(hmacHex)
	if err != nil || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(want, got) == 1
}

func randomNonce() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return b, nil
}

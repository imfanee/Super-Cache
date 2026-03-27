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
)

// PeerProtocolVersion is the supported mesh protocol version (HELLO/AUTH handshake; 2 = magic-framed wire).
const PeerProtocolVersion = 2

const (
	wireOpHello    = "HELLO"
	wireOpHelloAck = "HELLO_ACK"
)

// wireHello is the first client frame after TCP connect (outbound) or first frame read (inbound).
type wireHello struct {
	Op  string `json:"op"`
	Ver int    `json:"ver"`
}

// wireHelloAck is the server response to HELLO (challenge) or an error before AUTH.
type wireHelloAck struct {
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Ver   int    `json:"ver,omitempty"`
	Nonce string `json:"nonce,omitempty"` // hex-encoded random bytes (server-generated challenge)
	Err   string `json:"err,omitempty"`
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

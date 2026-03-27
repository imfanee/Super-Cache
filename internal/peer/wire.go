// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Framed JSON envelopes for peer replication (magic + length + JSON).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	wireOpAuth         = "AUTH"
	wireOpBootstrap    = "BOOTSTRAP"
	wireOpBootstrapEnd = "BOOTSTRAP_END"
	wireOpHeartbeat    = "HEARTBEAT"
	wireOpHeartbeatAck = "HEARTBEAT_ACK"
	wireOpPeerAnnounce = "PEER_ANNOUNCE"
)

// MaxPeerPayload is the maximum allowed JSON payload size for one framed message (32 MiB).
const MaxPeerPayload = 32 << 20

// ErrInvalidMagic is returned when the first four bytes of a frame are not PeerMagic.
var ErrInvalidMagic = errors.New("peer wire: invalid magic bytes")

// wireHeartbeat is a keepalive frame on the peer mesh (P2.2).
type wireHeartbeat struct {
	Op string `json:"op"`
}

// wirePeerAnnounce optionally follows handshake when gossip_peers is enabled (P2.4).
type wirePeerAnnounce struct {
	Op    string   `json:"op"`
	Peers []string `json:"peers"`
}

// wireAck is the response to successful AUTH proof.
type wireAck struct {
	OK  bool   `json:"ok,omitempty"`
	Err string `json:"err,omitempty"`
}

// wireBootstrapEnd terminates a snapshot stream after AUTH + BOOTSTRAP request.
type wireBootstrapEnd struct {
	Op string `json:"op"`
}

// Replication envelope and payload (P3.2). Extra fields are ignored by older peers.
const ReplEnvelopeVersion = 1

// wireRepl is one replication line (mirrors ReplicatePayload JSON shape).
type wireRepl struct {
	Op      string            `json:"op"`
	Key     string            `json:"key,omitempty"`
	Value   []byte            `json:"value,omitempty"`
	TTLms   int64             `json:"ttlms,omitempty"`
	Cnt     int64             `json:"cnt,omitempty"`
	Dest    string            `json:"dest,omitempty"`
	Fields  map[string][]byte `json:"fields,omitempty"`
	Members [][]byte          `json:"members,omitempty"`
	V       int               `json:"v,omitempty"`
	Seq     uint64            `json:"seq,omitempty"`
	Origin  string            `json:"origin,omitempty"`
}

// WriteMessage writes one framed PeerMessage: magic (4) + length (4) + JSON body.
func WriteMessage(w io.Writer, msg PeerMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("peer wire: marshal peer message: %w", err)
	}
	if len(body) > MaxPeerPayload {
		return fmt.Errorf("peer wire: marshal peer message: payload too large: %d", len(body))
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], PeerMagic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("peer wire: write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("peer wire: write payload: %w", err)
	}
	return nil
}

// ReadMessage reads one framed PeerMessage after the 8-byte header.
func ReadMessage(r io.Reader) (PeerMessage, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return PeerMessage{}, fmt.Errorf("peer wire: read header: %w", err)
	}
	if binary.BigEndian.Uint32(hdr[0:4]) != PeerMagic {
		return PeerMessage{}, fmt.Errorf("peer wire: read header: %w", ErrInvalidMagic)
	}
	n := binary.BigEndian.Uint32(hdr[4:8])
	if n > MaxPeerPayload {
		return PeerMessage{}, fmt.Errorf("peer wire: payload length %d exceeds max %d", n, MaxPeerPayload)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return PeerMessage{}, fmt.Errorf("peer wire: read payload: %w", err)
	}
	var msg PeerMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return PeerMessage{}, fmt.Errorf("peer wire: decode payload: %w", err)
	}
	return msg, nil
}

// readLine reads one newline-delimited JSON line (legacy tests only).
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read line: %w", err)
	}
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
}

// NormalizePeerMessage maps legacy NDJSON-style payloads (empty Type, "op" in JSON) to MsgType values for rolling upgrades.
func NormalizePeerMessage(msg PeerMessage) PeerMessage {
	if msg.Type != "" {
		return msg
	}
	var probe struct {
		Op string `json:"op"`
	}
	if len(msg.Payload) == 0 || json.Unmarshal(msg.Payload, &probe) != nil {
		return msg
	}
	op := strings.ToUpper(strings.TrimSpace(probe.Op))
	switch op {
	case wireOpHello:
		msg.Type = MsgTypeHello
	case wireOpHelloAck:
		msg.Type = MsgTypeHelloAck
	case wireOpAuth:
		msg.Type = MsgTypeAuth
	case wireOpHeartbeat, wireOpHeartbeatAck:
		msg.Type = MsgTypeHeartbeat
	case wireOpPeerAnnounce:
		msg.Type = MsgTypeNodeList
	case wireOpBootstrap:
		msg.Type = MsgTypeBootstrapReq
	case wireOpBootstrapEnd:
		msg.Type = MsgTypeBootstrapDone
	default:
		if op != "" {
			msg.Type = MsgTypeReplicate
		}
	}
	return msg
}

// writeLine writes one newline-delimited JSON line (legacy tests only).
func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		return fmt.Errorf("write nl: %w", err)
	}
	return nil
}

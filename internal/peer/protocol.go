// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Peer wire protocol definitions for Super-Cache clustering (magic-framed JSON envelopes).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import "encoding/json"

// PeerMagic is the 4-byte wire prefix for every framed peer message (ASCII "SCPH").
const PeerMagic uint32 = 0x53435048

// MsgType identifies the logical message kind inside a PeerMessage envelope.
type MsgType string

const (
	// MsgTypeHello is the initial HELLO handshake frame.
	MsgTypeHello MsgType = "HELLO"
	// MsgTypeHelloAck is the server challenge or error response to HELLO.
	MsgTypeHelloAck MsgType = "HELLO_ACK"
	// MsgTypeAuth carries the HMAC proof after HELLO_ACK.
	MsgTypeAuth MsgType = "AUTH"
	// MsgTypeAck is the final authentication result (wireAck JSON in Payload).
	MsgTypeAck MsgType = "ACK"
	// MsgTypeHeartbeat is a keepalive ping (payload optional).
	MsgTypeHeartbeat MsgType = "HB"
	// MsgTypeNodeList is an optional gossip peer list after handshake.
	MsgTypeNodeList MsgType = "NODE_LIST"
	// MsgTypeReplicate carries one replication operation (wireRepl JSON in Payload).
	MsgTypeReplicate MsgType = "REPL"
	// MsgTypeBootstrapReq requests a full snapshot stream from the peer.
	MsgTypeBootstrapReq MsgType = "BS_REQ"
	// MsgTypeBootstrapChunk is one snapshot entry during bootstrap.
	MsgTypeBootstrapChunk MsgType = "BS_CHUNK"
	// MsgTypeBootstrapDone terminates a snapshot stream.
	MsgTypeBootstrapDone MsgType = "BS_DONE"
)

// PeerMessage is the JSON payload after the 8-byte header (magic + length).
type PeerMessage struct {
	// Version is the envelope schema version (currently 1).
	Version uint8 `json:"v"`
	// Type is the logical message kind.
	Type MsgType `json:"t"`
	// NodeID is the sending node's identifier when known.
	NodeID string `json:"nid,omitempty"`
	// SeqNum is a monotonic sequence for replication frames when applicable.
	SeqNum uint64 `json:"seq,omitempty"`
	// Timestamp is Unix nanoseconds when the message was produced.
	Timestamp int64 `json:"ts,omitempty"`
	// Payload holds type-specific JSON (wire structs, snapshot entries, etc.).
	Payload json.RawMessage `json:"p,omitempty"`
}

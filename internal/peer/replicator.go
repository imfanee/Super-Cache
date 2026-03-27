// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Replicator defines cluster write replication for command handlers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

// ReplicatePayload describes a single replicated mutation.
type ReplicatePayload struct {
	// Op is the Redis-style command name (SET, DEL, HSET, ...).
	Op string
	// Key is the primary key affected.
	Key string
	// Value is the string payload for SET/APPEND-style operations and LREM element.
	Value []byte
	// TTLms is milliseconds until expiry for SET; for PEXPIREAT, absolute unix ms.
	TTLms int64
	// Cnt is EXPIRE seconds, PEXPIRE ms, EXPIREAT unix seconds, LPOP/RPOP count, LREM count, LSET index.
	Cnt int64
	// Dest is RENAME destination key.
	Dest string
	// Fields is used for hash updates.
	Fields map[string][]byte
	// Members is used for set and list operations.
	Members [][]byte
}

// Replicator propagates successful local writes to peers.
type Replicator interface {
	// Replicate sends a payload to all connected peers; standalone mode uses a no-op implementation.
	Replicate(p ReplicatePayload) error
}

// NoopReplicator discards replication payloads (standalone mode).
type NoopReplicator struct{}

// Replicate implements Replicator with a no-op.
func (NoopReplicator) Replicate(_ ReplicatePayload) error {
	return nil
}

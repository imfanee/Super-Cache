# PEER_PROTOCOL

## Introduction

This document specifies Super-Cache peer protocol v2 for authentication, replication, bootstrap, and membership coordination.

## Transport

- TCP over IPv4/IPv6
- Configurable bind address and port
- Default peer port: `7379`
- TLS optional via peer TLS config fields
- Persistent connections with reconnect backoff
- Reconnect backoff starts at 1s and caps near 30s

## Frame Format

Each message = fixed header + JSON payload.

Header (8 bytes):

- Bytes `0..3`: magic number `0x53435048` (`SCPH`)
- Bytes `4..7`: big-endian uint32 payload length

Constraints:

- Max payload size: `33554432` bytes (32 MiB)
- Oversized payload or bad magic closes message path with error

Go struct (`internal/peer/protocol.go`):

```go
type PeerMessage struct {
    Version   uint8           `json:"v"`
    Type      MsgType         `json:"type"`
    NodeID    string          `json:"node_id,omitempty"`
    SeqNum    uint64          `json:"seq,omitempty"`
    Timestamp int64           `json:"ts,omitempty"`
    Payload   json.RawMessage `json:"payload,omitempty"`
}
```

## Message Types

| Constant | String | Direction | Description |
|---|---|---|---|
| `MsgTypeHello` | `HELLO` | initiator -> acceptor | Start handshake |
| `MsgTypeHelloAck` | `HELLO_ACK` | acceptor -> initiator | Handshake challenge/ack |
| `MsgTypeAuth` | `AUTH` | initiator -> acceptor | HMAC proof |
| `MsgTypeAck` | `ACK` | acceptor -> initiator | Auth success |
| `MsgTypeHeartbeat` | `HB` | bidirectional | Liveness |
| `MsgTypeNodeList` | `NODE_LIST` | bidirectional | Membership gossip |
| `MsgTypeReplicate` | `REPL` | bidirectional | Write replication |
| `MsgTypeBootstrapReq` | `BS_REQ` | joiner -> source | Snapshot request |
| `MsgTypeBootstrapChunk` | `BS_CHUNK` | source -> joiner | Snapshot data |
| `MsgTypeBootstrapDone` | `BS_DONE` | source -> joiner | Snapshot complete |

## Authentication Handshake

Current v2 sequence:

1. Connector sends `HELLO` with node identity/capabilities.
2. Acceptor responds with `HELLO_ACK` and nonce challenge.
3. Connector computes `HMAC-SHA256(shared_secret, nonce)` and sends `AUTH`.
4. Acceptor verifies and sends `ACK`.
5. On success, peers can exchange `NODE_LIST` and `REPL`.

Why HMAC:

- Shared secret is never sent on wire.
- Challenge changes per connection, reducing replay viability.
- Verification is constant-format and robust for machine processing.

## Heartbeat

- Interval: `heartbeat_interval` (default 5s)
- Timeout: `heartbeat_timeout` (default 15s)
- Timeout must be greater than interval
- Missed heartbeats mark peer stale and trigger reconnect
- Heartbeat payload is empty/minimal

## Node Discovery

- Static peer list from config starts initial dial loops.
- Runtime `peers add/remove` mutates active peer set.
- `NODE_LIST` can propagate new membership details.
- Goal topology is a connected mesh.

## Write Replication

Replication payload includes operation metadata and value data. Write handlers apply locally first, then enqueue `REPL` to outbound peer queues.

Typical payload fields:

- `Op`
- `Key`
- `Value`
- `TTLms`
- `Fields`
- `Members`

Ordering and pressure behavior:

- `SeqNum` provides sender-local sequence context.
- Per-peer outbound channel is bounded (`peer_queue_depth`).
- Overflow drops replication message and emits warning.
- Receiver applies without re-propagating to prevent loops.

## Bootstrap Protocol

State machine:

- `idle`
- `requesting`
- `receiving`
- `draining`
- `ready`

Transitions:

1. **idle -> requesting**: joiner selects candidate and sends `BS_REQ`.
2. **requesting -> receiving**: source begins streaming `BS_CHUNK` batches.
3. **receiving**: joiner applies chunk entries; inbound `REPL` is queued.
4. **receiving -> draining**: source sends `BS_DONE`.
5. **draining -> ready**: queued writes are replayed in order.
6. **ready**: client listener opens and node serves traffic.

Abort/retry rules:

- Source disconnect mid-transfer: abort and try next peer.
- Write queue overflow (`bootstrap_queue_depth`): abort, clear partial state, retry next source.
- No candidate available: wait/retry on reconnect opportunities.

Snapshot entry struct:

```go
type SnapshotEntry struct {
    Key      string
    Type     DataType
    String   string
    Hash     map[string]string
    List     []string
    Set      []string
    Expires  int64
}
```

### Operator Impact of the Bootstrap Protocol

The bootstrap protocol is the mechanism that makes zero-downtime node addition possible and safe. Understanding how it works at the protocol level clarifies why certain operational behaviours occur and why the configuration parameters for bootstrap are sized the way they are.

When you add a new node to a running cluster the existing cluster is completely unaffected. The source node that serves the bootstrap snapshot does not pause, does not lock its store, and does not reduce its throughput. The Snapshot method streams entries from each shard individually, acquiring and releasing the per-shard read lock one shard at a time. This means client reads and writes on the source node continue normally throughout the entire bootstrap transfer. Snapshot streaming is designed to never hold more than one shard lock at a time, so the maximum interference to concurrent operations is bounded to one shard out of 256 for the brief duration of reading that shard's entries.

The new node does not appear in any peer's node list for client routing purposes until it has completed bootstrap and opened its client listener. This means you cannot accidentally route client traffic to a node that is still bootstrapping. The node is invisible to clients until it is ready.

The write queue that buffers replication messages during bootstrap is the critical safety mechanism. Every write that any node in the cluster accepts during the snapshot transfer is queued on the new node and applied in order after the snapshot completes. This guarantees that the new node's state after bootstrap is identical to what it would have been had the node been present for the entire duration of the snapshot transfer. There is no eventual consistency window after bootstrap completes. The node is immediately consistent.

The only failure mode that requires operator attention is write queue overflow. If your cluster is processing writes faster than the bootstrap_queue_depth allows for the duration of the bootstrap transfer, the bootstrap aborts and retries. The log will show a warning. The resolution is to increase bootstrap_queue_depth in the new node's config before starting it. Calculate the required depth as your observed write rate in operations per second multiplied by your observed bootstrap duration in seconds, then multiply by 1.5 for safety margin. The bootstrap duration depends on your dataset size and network bandwidth: a 10 gigabyte dataset on a 1 Gbps network takes approximately 80 to 100 seconds to transfer.

## Security Considerations

- `shared_secret` minimum length is 32 characters.
- Do not log secrets.
- Store secrets in dedicated secret manager.
- Treat peer secret like a database credential.
- Secret rotation requires coordinated restarts in v1.0.

## Operational Notes

- Verify peer path with `supercache-cli peers list`.
- Check bootstrap progression via `supercache-cli bootstrap-status`.
- For high write rates during bootstrap, increase `bootstrap_queue_depth`.


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

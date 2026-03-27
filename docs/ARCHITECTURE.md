# ARCHITECTURE

## Introduction

Technical reference for Super-Cache internals, protocol, lifecycle, and operations assumptions.

## System Overview

### Design Context

Distributed in-memory caching usually forces a choice between operational simplicity and built-in replication behavior. Memcached keeps the core server lean but does not provide native replication, pushing redundancy logic into application or orchestration layers. Standard Redis-family topologies provide replication, but they commonly introduce role-specific routing and operational coordination patterns.

Super-Cache addresses that trade-off by making every cluster node a peer with equal read/write capability. Nodes communicate over authenticated persistent TCP links and replicate writes asynchronously across the mesh. The design removes single-writer role dependency while keeping the data path in process and memory-resident.

For operators and application developers, this means no master election workflow, no mandatory write-routing proxy, and no minimum cluster size threshold before replication becomes useful. A single node works as a standalone cache, and additional peers extend availability and read scale through the same protocol path. RESP2 compatibility preserves existing client integrations while topology is handled server-side.

### Cluster Topology

Super-Cache uses a full-mesh peer-to-peer topology over authenticated TCP sessions. Each node maintains inbound and outbound peer paths, and a joining node can connect to an existing peer, bootstrap state, learn peer addresses via `NODE_LIST` gossip when enabled, and establish additional direct sessions. The resulting topology is a direct peer graph where each node can exchange replication frames with other nodes without an intermediary coordinator.

```text
                Authenticated P2P TCP
        +-----------------------------------+
        |                                   |
        v                                   |
   +-----------+   Authenticated P2P TCP   +-----------+
   |  Node A   |<-------------------------->|  Node B   |
   +-----------+                            +-----------+
      ^     ^                                  ^     ^
      |     |                                  |     |
    Client  |                                  |   Client
      |     |                                  |     |
      |     +-----------+  Authenticated P2P TCP     |
      |                 v                              |
      |            +-----------+                       |
      +----------->|  Node C   |<----------------------+
         Client    +-----------+
```

### Replication Model

Super-Cache replication is fire-and-send. When a client writes to any node, that node applies the mutation to its local 256-shard store, returns the command response to the client, and enqueues replication frames for asynchronous fan-out to connected peers. Client acknowledgement therefore tracks local in-memory apply latency, while peer convergence tracks network and queue conditions; on low-latency LANs this is commonly sub-millisecond to low-millisecond.

If a peer is temporarily unreachable, outbound per-peer queues buffer replication events up to `peer_queue_depth`. When that queue is exhausted, additional events are dropped and warnings are emitted so operators can detect divergence risk. Reconnected peers can be re-synchronized via bootstrap, which is a pragmatic trade-off for cache-oriented deployments where eventual consistency is preferred over synchronous write blocking.

### Bootstrap Protocol Summary

A joining node begins with an empty store, connects to an available peer, completes the HMAC-SHA256 handshake, and sends a bootstrap request. The source streams snapshot entries as bootstrap chunks, while the joiner buffers concurrent replication events in a bounded inbound queue. After snapshot completion, the queued replication events are drained and applied in order. Only then does the node transition to client-ready state and open its client listener, ensuring it is synchronized before serving traffic. For wire-level details and message semantics, see `docs/PEER_PROTOCOL.md`.

## Design Principles

1. Sharded concurrency over global lock.
2. Fire-and-send replication for low client write latency.
3. Authenticated peer mesh over centralized topology.
4. Bootstrap-before-serve for joining nodes.
5. Graceful shutdown with drain/finalize paths.
6. Static deployment via `CGO_ENABLED=0`.
7. Keep dependency surface small.

## Store Component

- 256 shards (`NumShards = 256`) with FNV-1a routing.
- `entry` supports string/hash/list/set values.
- Lazy expiry on access.
- Active expiry: 100ms ticker, sample 20 keys per shard, repeat when expired ratio >25%.
- Per-shard LRU lists with allkeys and volatile tracking.
- O(1) list memory approximation (`len * 32`) for throughput.
- `Snapshot()` stream and `ApplySnapshot()`/`ApplySnapshotEntry()` for bootstrap.

Files:

- `internal/store/store.go`
- `internal/store/expiry.go`
- `internal/store/eviction.go`
- `internal/store/store_collections.go`
- `internal/store/store_list_extra.go`

## RESP Engine Component

- Streaming parser on `bufio.Reader`.
- Types: simple string, error, integer, bulk, array.
- Supports inline commands and pipelining.
- Handles null bulk/array.
- Enforces 512MB bulk limit.
- Guards simple line size at 65536 bytes.
- Uses `sync.Pool` for small bulk buffer reuse.

Files:

- `internal/resp/parser.go`
- `internal/resp/writer.go`
- `internal/resp/value.go`

## Command Engine Component

- `Registry` maps command name to `CommandMeta`.
- `CommandMeta`: `Arity`, `FirstKey`, `LastKey`, `Step`, `Flags`, `Handler`.
- `CommandContext` includes store/session/writer/peer/config/info/keyspace/pubsub/registry.
- Commands grouped by: String, Hash, List, Set, Generic, Server, Pub/Sub, Transactions.
- Write commands call `Replicator.Replicate` after local apply.

Files:

- `internal/commands/*.go`

## Client Handler Component

Session fields include:

- `ID`, `Conn`, `Parser`, `Writer`
- `Authenticated`, `Name`, `DB`
- `InMulti`, `MultiQueue`, `WatchedKeys`
- `SubChannels`, `PSubPatterns`
- `CreatedAt`, `LastCmdAt`, `CmdCount`

Connection lifecycle:

1. accept
2. parse
3. auth gate
4. dispatch
5. write
6. close

`shuttingDown` is checked between commands. `gracefulCloseConn` uses half-close and short drain to reduce reset behavior.

Files:

- `internal/server/conn.go`
- `internal/client/session.go`

## Peer Subsystem Component

### Wire Protocol

- 8-byte header: magic + payload length
- Magic: `0x53435048` (`SCPH`)
- Length: big-endian uint32
- Payload: JSON `PeerMessage`
- Payload max: 32 MiB
- Invalid magic error: `ErrInvalidMagic`

`PeerMessage`:

- `Version uint8`
- `Type MsgType`
- `NodeID string`
- `SeqNum uint64`
- `Timestamp int64`
- `Payload json.RawMessage`

MsgType constants:

- `HELLO`, `HELLO_ACK`, `AUTH`, `ACK`
- `HB`, `NODE_LIST`, `REPL`
- `BS_REQ`, `BS_CHUNK`, `BS_DONE`

### Authentication Handshake

Actual v2 handshake:

1. Connector sends `HELLO`
2. Acceptor returns `HELLO_ACK` with random nonce
3. Connector sends `AUTH` with `HMAC-SHA256(secret, nonce)`
4. Acceptor verifies and returns `ACK`

HMAC protects secret disclosure and replay from static captures.

### Replication

- Local write applied first.
- Replication payload queued per peer channel.
- Queue overflow drops and logs warning.
- Receivers apply without re-propagation.

### Bootstrap Protocol

States:

- idle
- requesting
- receiving
- draining
- ready

Flow:

1. joiner sends `BS_REQ`
2. source streams snapshot entries as `BS_CHUNK`
3. joiner applies chunks and buffers inbound REPL
4. source sends `BS_DONE`
5. joiner drains queue then serves clients

Abort/retry occurs on source failure or queue overflow.

### Dynamic Membership

- Config peers start dial loops.
- `peers add/remove` mutate runtime peer set.
- Optional gossip (`NODE_LIST`) can announce peers.

### Heartbeat

- Default interval 5s
- Default timeout 15s
- Dead sessions reconnect with exponential backoff 1s..30s

Files:

- `internal/peer/service.go`
- `internal/peer/outbound.go`
- `internal/peer/bootstrap.go`
- `internal/peer/wire.go`
- `internal/peer/auth_crypto.go`

## Management Interface Component

- Unix socket API (`/var/run/supercache.sock` by default)
- Optional loopback TCP API
- NDJSON request/response protocol

Management handlers:

- `PING`, `INFO`
- `STATUS`, `RELOAD_CONFIG`
- `PEERS_LIST`, `PEERS_ADD`, `PEERS_REMOVE`
- `SHUTDOWN`, `DEBUG_KEYSPACE`, `BOOTSTRAP_STATUS`

Files:

- `internal/mgmt/*.go`
- `cmd/supercache-cli/main.go`

## Server Lifecycle

Startup order:

1. parse flags
2. load/validate config
3. set GOMAXPROCS
4. init store + peer service
5. start mgmt API
6. start peer listener
7. start peer dials
8. bootstrap if configured
9. open client listener

Shutdown order:

1. log "Shutdown initiated"
2. set `shuttingDown`
3. close listeners
4. close peer connections
5. wait client handlers
6. cancel run context
7. finalize peer replication
8. log "Shutdown complete"

## Concurrency Model

| Goroutine | Count | Responsibility |
|---|---:|---|
| main | 1 | process control |
| client listener | 1 | accept clients |
| client handlers | per connection | parse/dispatch/write |
| peer listener | 1 | accept peers |
| peer sessions | per peer | handshake/read/apply |
| outbound writer | per outbound peer | send REPL |
| reconnect loops | per configured peer | dial retry |
| heartbeat ticker | per outbound peer | HB traffic |
| inbound apply workers | NumCPU | apply replication jobs |
| expiry sweeper | 1 | active expiry |
| mgmt listeners | up to 2 | mgmt API |
| bootstrap stream workers | transient | snapshot sync |

## Data Flow Diagrams

Client write:

```text
SET -> parse -> dispatch -> store apply -> replicate enqueue -> response -> peer writers
```

Bootstrap:

```text
HELLO/AUTH -> BS_REQ -> BS_CHUNK* -> BS_DONE -> drain queue -> serve clients
```

Graceful shutdown:

```text
SIGTERM -> shuttingDown -> close listeners -> drain -> finalize -> exit
```

## Performance Characteristics

- 256 shards reduce lock contention.
- O(1) memory accounting update path.
- O(1) list memory approximation avoids list-length scans on every mutation.
- Distinct vs shared contention benchmark: `1222 ns/op` vs `1338 ns/op` (~9.5% overhead).

## Security Model

- Peer auth via HMAC-SHA256 and random nonce.
- Shared secret min 32 chars.
- Optional client AUTH password.
- Optional TLS support for client and peer listeners.
- Mgmt socket protected by FS permissions + secret.

## Known Limitations in v1.0

- No persistence.
- No server-side sharding.
- No Redis Cluster protocol.
- No Lua scripting.
- No ACL model.
- Pub/Sub node-local only.
- `SELECT` only DB 0.

## Future Roadmap

- Persistence snapshot/AOF style options.
- Sharding mode with key-space partitioning.
- Expanded TLS and secret rotation tooling.
- Additional Redis command families.


## Licence and Intellectual Property

Super-Cache is distributed under the Super-Cache Software Licence. The software is free to use in unmodified form for any purpose. Modification of the source code requires a Customisation Licence or a paid development engagement with the author. See the LICENSE file for full terms. Contact Faisal Hanif at imfanee@gmail.com for licensing enquiries.

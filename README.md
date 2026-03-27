<!-- Super-Cache: Redis-compatible distributed in-memory cache with native P2P replication. Drop-in replacement for Redis, Redis Cluster, Redis Sentinel, and Memcached. Every node reads and writes. No master. No replica. No Sentinel. No proxy. Written in Go. Zero code change migration. Sub-millisecond replication. RESP2 protocol compatible. ValKey compatible. Golang cache server. Distributed key-value store. Multi-master cache. Peer-to-peer replication. Zero downtime horizontal scaling. High availability cache without configuration complexity. -->
# Super-Cache

Redis-compatible cache cluster: every node is read/write, no special clustering configuration required.

![Go Version](https://img.shields.io/badge/go-1.22-00ADD8?logo=go)
![Build](https://img.shields.io/badge/build-passing-brightgreen)
![License](https://img.shields.io/badge/license-Custom%20Licence-orange)
![Coverage](https://img.shields.io/badge/coverage-79.5%25-yellowgreen)

## What is Super-Cache

In-memory caching systems make different replication trade-offs, and those trade-offs directly shape operational complexity. Memcached has no native replication, so availability across nodes requires application-managed duplication or acceptance of node-local loss. Standard Redis replication uses one write master with read-only replicas, which introduces write-target routing and failover handling concerns outside the data plane itself. Redis Sentinel automates election, but a genuinely highly available two-data-node deployment typically adds three Sentinel processes, increasing moving parts. Redis Cluster solves a different problem with hash-slot sharding and multi-master partitions, but it introduces cluster-topology semantics that clients and operators must manage. These are valid design choices, but they increase evaluation and operational overhead when the goal is straightforward replicated caching with minimal topology burden.

Super-Cache takes a peer-first approach: every node accepts reads and writes. There is no master, replica, primary, or secondary role to route around. Applications can connect to any node and execute the same command flow without embedding topology logic. No proxy tier is required to translate client traffic into role-aware routing. Cluster behavior is handled by the server-side peer mesh rather than by application-side failover policy.

Moving from one node to two nodes is intentionally simple: configure a peer address and start the second node. The joining node authenticates, requests bootstrap data, and receives a full dataset snapshot from an existing peer. While the snapshot is in transit, incoming replication updates are buffered in a bounded bootstrap queue so in-flight writes are applied after snapshot completion. Only after snapshot application and queue drain does the new node open its client listener. From an operator perspective, node growth is incremental and additive, with no cluster-size threshold that must be met before replication works. From an application perspective, the new node appears as another equivalent endpoint once it is synchronized.

Operationally, node addition and removal can be performed without stopping the remaining cluster. Each Super-Cache process uses the Go scheduler across available cores, and inbound replication apply workers are sized from `runtime.NumCPU`, so heterogeneous nodes can participate at their own hardware limits. Replication is fire-and-send: the receiving node commits the write locally, returns the client response, and then fans out asynchronously to peers. On low-latency local networks this usually means peer convergence within sub-millisecond to low-millisecond bounds, but observed latency always remains network-dependent. The result is predictable client write latency at the receiving node while replication progresses in parallel.

Super-Cache implements RESP2 and is designed for direct use with existing Redis-compatible client libraries. Migration normally starts as a connection-endpoint change rather than an API rewrite. The command registry currently exposes 93 commands across strings, hashes, lists, sets, generic, server, pub/sub, and transactions. Where behavior differs from Redis, those differences are explicitly documented in `docs/REDIS_COMPATIBILITY.md`. This keeps adoption effort focused on validation rather than on rebuilding the application data-access layer.

## How Super-Cache Compares

| Criteria | Memcached | Redis (standard replication) | Redis + Sentinel | Redis Cluster | Super-Cache |
|---|---|---|---|---|---|
| Native Replication | None | Single-master to read-only replicas | Single-master with automatic failover | Multi-master with hash slot sharding | Every node reads and writes, full mesh replication |
| Minimum Nodes for HA | Not applicable, no HA | 2 data nodes plus manual failover | 2 data nodes plus 3 Sentinel processes | 6 nodes minimum | 2 nodes, no additional processes |
| Write Routing | Application manages | Application or proxy must route writes to master | Application or proxy must route writes to elected master | Client must be cluster-aware or use a proxy | Any node, no routing required |
| Read Routing | Application manages | Application or proxy must route reads to replicas for scaling | Application or proxy manages | Client must be cluster-aware | Any node serves reads from local memory |
| Zero-Downtime Node Addition | Application-dependent | Requires replication reconfiguration | Requires reconfiguration | Requires shard rebalancing | Yes, automatic bootstrap |
| Application Changes to Migrate | Full rewrite for replication logic | Add read/write routing | Add sentinel-aware client configuration | Use cluster-aware client or add proxy | Change connection string only |
| Replication Latency | Not applicable | Asynchronous, network-dependent | Asynchronous, network-dependent | Asynchronous, network-dependent | Asynchronous fire-and-send, sub-millisecond on LAN |
| Per-Node CPU Utilisation | Full cores | Single-threaded command processing | Single-threaded | Single-threaded per shard | All cores via Go runtime, 256-shard store |
| Heterogeneous Hardware | Yes | Yes but master is the bottleneck | Yes but master is the bottleneck | Yes within shard constraints | Yes, each node uses its own hardware fully |
| Client Library Compatibility | Memcached protocol clients only | Any RESP2 client | Sentinel-aware RESP2 client | Cluster-aware RESP2 client | Any RESP2 client, unmodified |

## Why Super-Cache

If you already use Redis or Memcached and need replicated in-memory availability, Super-Cache is designed to be a low-friction evaluation.

**Every Node is a Write Node.**
Super-Cache removes role-based write routing by design because each peer is a read/write endpoint. There is no master election workflow, no failover script to maintain, and no mandatory proxy tier between your application and cache nodes.

**Two Nodes is a Cluster.**
With Super-Cache, two nodes provide replicated operation with no extra control-plane processes. Compared with a two-data-node Sentinel deployment that usually adds three Sentinel processes, this reduces process count, operational surface area, and monitoring overhead.

**Add Nodes Without Stopping Anything.**
To add capacity, start a new node with a peer address in configuration and let bootstrap run. Snapshot transfer and buffered replication catch-up are handled by the protocol, while existing nodes continue serving traffic.

**Your Redis Code Works Today.**
Super-Cache speaks RESP2 and is designed for existing Redis-compatible clients. Existing connection pooling and command usage patterns generally carry over, with migration centered on endpoint configuration and compatibility validation.

**Reads Never Leave the Node.**
After bootstrap, each node serves reads from its own in-memory dataset. Local reads avoid per-request peer lookups, which stabilizes latency expectations under normal network conditions.

**Scales Horizontally Without Resharding.**
Adding nodes does not require hash-slot migration or shard rebalance operations. The trade-off is explicit: each node stores the full dataset, so the model scales read throughput and availability rather than aggregate capacity beyond one node's memory envelope.

### Comparison Criteria Detail

**Native replication model.**
Super-Cache uses peer-to-peer replication in a mesh where each node can ingest writes. This avoids introducing a dedicated write-role controller into day-to-day operations.

**Minimum high-availability footprint.**
A two-node cluster already replicates data and continues service when one node is unavailable. No separate election daemon set is required to keep the remaining node operational.

**Write-path behavior.**
Client writes execute on whichever node receives the request. The local node applies the change first and then fans out replication asynchronously to peers.

**Read-path behavior.**
Read traffic is served from local memory on each synchronized node. This keeps read latency tied to node-local CPU/memory behavior rather than peer round trips.

**Node expansion workflow.**
Joining nodes bootstrap via authenticated snapshot transfer. Buffered replication during bootstrap closes the gap between snapshot start and ready-to-serve state.

**Application migration effort.**
RESP2 compatibility lets existing Redis-oriented code paths stay intact. Most migrations are endpoint and validation changes, not client API rewrites.

**Replication timing model.**
Replication is asynchronous and queue-based, not synchronous commit across peers. This favors low write latency while accepting eventual consistency in cache-centric deployments.

**CPU utilization model.**
Each process uses Go runtime scheduling across available cores and a sharded store architecture. This improves parallelism compared with single-threaded command loops.

**Mixed hardware operation.**
Clusters can include nodes with different CPU and memory profiles. Each node processes client traffic according to its own hardware envelope.

**Client compatibility expectations.**
Standard RESP2 clients are the default integration path. Cluster-aware protocol features from Redis Cluster and Sentinel are intentionally not required for baseline operation.

### Evaluation Checklist

- Can your application use a single RESP2 endpoint per node without role-aware routing logic?
- Do you prefer asynchronous replication with bounded queues for cache workloads?
- Is full-dataset replication per node acceptable for your memory budget?
- Do you want to avoid Sentinel process management in small HA deployments?
- Do you want node addition without shard rebalance operations?
- Is eventual consistency acceptable for transient cache state?
- Do you need read locality from any cluster node?
- Is low-friction migration from existing Redis clients a key requirement?
- Do you operate mixed hardware and want per-node independent performance?
- Do you want bootstrap-gated readiness before new nodes accept client traffic?
- Do you want heartbeat-based peer liveness without external coordinators?
- Do you want replication/auth behavior observable through existing logs and status endpoints?


### Practical Evaluation Scenarios

Use these scenarios to evaluate Super-Cache in a controlled way before production rollout.

#### Scenario 1: Replace a Single Redis Primary + Replica Pair

1. Start two Super-Cache nodes with reciprocal `peers` values.
2. Point your existing Redis client at Node A and run read/write traffic.
3. Point a second client cohort at Node B and run the same command mix.
4. Confirm that every node accepts writes and every node serves local reads.
5. Confirm that no client-side write-routing rules are required.

#### Scenario 2: Add a Third Node Under Live Write Load

1. Start with two synchronized nodes handling active writes.
2. Launch Node C with one peer address and observe bootstrap telemetry.
3. Continue writes during bootstrap and verify the queue drain completes.
4. Confirm Node C only serves clients after bootstrap+drain.
5. Validate key parity across all three nodes.

#### Scenario 3: Remove a Node Without Stopping Traffic

1. Run active traffic against a three-node cluster.
2. Stop one node gracefully.
3. Confirm remaining nodes continue to process reads/writes.
4. Confirm peer counters converge to the reduced topology.
5. Re-add the removed node and verify bootstrap recovery.

#### Scenario 4: Heterogeneous Hardware Behavior

1. Run one node on a higher-core machine and one on lower-core hardware.
2. Apply mixed read/write workload to both endpoints.
3. Confirm each node uses local CPU resources independently.
4. Confirm replication still converges through the same protocol path.
5. Confirm that every node remains a write node regardless of hardware profile.

### Engineering Notes for Evaluation

- Every node is a write node, so write-path correctness tests should target all nodes equally.
- Every node serves reads from local memory after bootstrap completion.
- Every node participates in peer replication using authenticated framed messages.
- Every node can be a migration entry point for existing Redis clients.
- Every node can be removed and re-added through the same bootstrap workflow.

- Bootstrap readiness is explicit: client serving starts after snapshot apply and queue drain.
- Replication is asynchronous by design; treat it as eventual consistency for cache semantics.
- Outbound peer queues are bounded; monitor warning logs for queue saturation events.
- Heartbeat interval/timeout define failure detection sensitivity and reconnect behavior.
- Full-dataset replication means capacity planning is per-node memory, not aggregate shard capacity.

- RESP2 compatibility reduces application migration scope to endpoint and validation work.
- Redis Cluster and Sentinel protocol negotiation is intentionally out of scope.
- If your clients are currently cluster-aware, switch them to standard RESP2 endpoint mode.
- If your current deployment uses role-aware write routing, remove that logic during trials.
- If your deployment relies on persistence guarantees, account for Super-Cache v1.0 in-memory scope.

### Decision Matrix for Adoption

| Question | If Yes | If No |
|---|---|---|
| Do you want every node to accept writes? | Super-Cache aligns strongly | Role-based Redis may still fit |
| Do you want to avoid Sentinel process management? | Super-Cache reduces control-plane count | Existing Sentinel ops maturity may be enough |
| Do you need shard-based aggregate memory scaling? | Evaluate Redis Cluster instead | Super-Cache full-replica model fits |
| Is eventual consistency acceptable for cache data? | Fire-and-send replication is appropriate | Use stronger consistency architecture |
| Do you need direct RESP2 client compatibility? | Super-Cache supports drop-in clients | Alternative protocols may be acceptable |

### Migration Validation Checklist

- Confirm command compatibility for your application command subset.
- Confirm error handling for documented Redis-behavior differences.
- Confirm read latency against your current baseline under realistic concurrency.
- Confirm write latency while peers are healthy and during peer interruption.
- Confirm recovery behavior after deliberate peer disconnect and reconnect.
- Confirm bootstrap timing for your expected dataset size.
- Confirm queue depth settings for expected write burst envelope.
- Confirm auth and shared-secret distribution procedures.
- Confirm observability dashboards include peers and bootstrap state.
- Confirm incident runbooks include node add/remove and bootstrap retry actions.
- Confirm capacity plan reflects full-dataset memory on each node.
- Confirm rollback plan back to prior cache topology if required.


### Topology and Readiness Quick Reference

- One node: standalone cache, no peer dependency.
- Two nodes: bidirectional replication with no additional coordination process.
- Three or more nodes: mesh-style peer fan-out with equivalent read/write endpoints.
- Join behavior: bootstrap snapshot, buffered replication, then client-ready transition.
- Leave behavior: heartbeat timeout detection and continued service on remaining nodes.
- Client behavior: connect to any node using standard RESP2 libraries.
- Write behavior: local apply + asynchronous peer fan-out.
- Read behavior: local memory on synchronized nodes.
- Capacity behavior: availability/read-scale increase; per-node memory still bounds dataset size.
- Operational behavior: no master election workflow in the steady state.

### Terminology Used in This README

- **Peer node**: a Super-Cache node with equal read/write capability.
- **Bootstrap**: initial full-state synchronization of a joining node.
- **Fire-and-send replication**: asynchronous peer fan-out after local write acknowledgement.
- **Client-ready**: state after bootstrap plus buffered replication drain.
- **RESP2 compatibility**: standard Redis client protocol interoperability.

## Features

- RESP2 parser/writer with all core value types
- 256-shard FNV-1a in-memory store
- Data types: string, hash, list, set
- 93 registered commands (`COMMAND COUNT`)
- Lazy + active expiry (100ms sweep, 20-key samples)
- Eviction policies: `noeviction`, `allkeys-lru`, `volatile-lru`, `allkeys-random`, `volatile-random`, `volatile-ttl`
- P2P mesh replication with HMAC-SHA256 authentication
- Full snapshot bootstrap for joining nodes
- Separate client/peer bind + port settings
- Management CLI and runtime operations API
- Hot-reload for selected configuration fields
- Graceful shutdown with in-flight drain and replication spill support
- Static binary (`CGO_ENABLED=0`)
- No external runtime services required

## Who Should Use Super-Cache

Super-Cache is built for engineering teams who need distributed in-memory caching with replication and are frustrated by the operational complexity of existing solutions. If any of the following describes your situation, Super-Cache is worth evaluating.

- You use Memcached and need replication but do not want to implement it in your application.
- You use Redis and are spending significant engineering time managing Sentinel failover or writing read-write routing logic.
- You want a Redis-compatible cache where every node accepts writes and no special client configuration is required.
- You need to scale your cache horizontally by adding nodes without downtime or shard rebalancing.
- You want to migrate from Redis without changing your application code beyond the connection string.
- You need a cache that serves reads from local memory on every node without contacting a coordinator.
- You want a single static binary with no runtime dependencies that deploys anywhere.
- You need replication between two nodes and do not want to manage three additional Sentinel processes to get it.

## Quick Start

```bash
cd /opt/supercache
make build
./build/supercache -config ./configs/supercache.toml
redis-cli -h 127.0.0.1 -p 6379 PING
```

Expected output:

```text
PONG
```

Three-node defaults: Redis on `6379`, peer mesh on `7379`.

## Installation

Prerequisites:

- Go 1.22+
- make

Build:

```bash
make build
```

Install:

```bash
sudo make install
```

Binaries:

- `/opt/supercache/build/supercache`
- `/opt/supercache/build/supercache-cli`

## Configuration

| Parameter | Type | Default | Hot-Reload | Description |
|---|---|---:|:---:|---|
| `client_bind` | string | `0.0.0.0` | No | Client listen bind |
| `client_port` | int | `6379` | No | Client listen port |
| `peer_bind` | string | `0.0.0.0` | No | Peer listen bind |
| `peer_port` | int | `7379` | No | Peer listen port |
| `peers` | []string | `[]` | Yes* | Outbound peer addresses |
| `bootstrap_peer` | string | empty | No | Preferred bootstrap source |
| `shared_secret` | string | required | No | Peer auth secret (>=32 chars) |
| `max_memory` | string | `0` | Yes | Memory cap |
| `max_memory_policy` | string | `noeviction` | Yes | Eviction policy |
| `auth_password` | string | empty | Yes | Redis AUTH password |
| `log_level` | string | `info` | Yes | Log level |
| `log_output` | string | `stdout` | Yes | Log destination |
| `log_format` | string | `text` | Yes | text/json/logfmt |
| `metrics_bind` | string | empty | No | Metrics bind |
| `metrics_port` | int | `0` | No | Metrics port |
| `bootstrap_queue_depth` | int | `100000` | No | Bootstrap queue cap |
| `peer_queue_depth` | int | `50000` | Yes | Outbound peer queue cap |
| `heartbeat_interval` | int | `5` | Yes | Heartbeat interval |
| `heartbeat_timeout` | int | `15` | Yes | Heartbeat timeout |
| `mgmt_socket` | string | `/var/run/supercache.sock` | No | Mgmt Unix socket |
| `mgmt_tcp_bind` | string | `127.0.0.1`* | No | Mgmt TCP bind when enabled |
| `mgmt_tcp_port` | int | `0` | No | Mgmt TCP port |
| `gossip_peers` | bool | `false` | No | Peer announce gossip |
| `peer_state_file` | string | empty | No | Persisted peer list path |
| `repl_shutdown_spill_path` | string | empty | No | Replication spill path |
| `client_tls_cert_file` | string | empty | No | Client TLS cert |
| `client_tls_key_file` | string | empty | No | Client TLS key |
| `client_tls_min_version` | string | `1.2` effective | No | Client TLS minimum |
| `peer_tls_cert_file` | string | empty | No | Peer TLS cert |
| `peer_tls_key_file` | string | empty | No | Peer TLS key |
| `peer_tls_ca_file` | string | empty | No | Peer dial CA |
| `peer_tls_min_version` | string | `1.2` effective | No | Peer TLS minimum |

`*` `peers` hot reload is additive only.

## Running

```bash
supercache -config /etc/supercache/supercache.toml
```

systemd example:

```ini
[Unit]
Description=Super-Cache
After=network.target

[Service]
Type=simple
User=supercache
Group=supercache
ExecStart=/usr/local/bin/supercache -config /etc/supercache/supercache.toml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
TimeoutStopSec=30
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

## Cluster Setup

1. Configure same `shared_secret` across nodes.
2. Set symmetric `peers` lists.
3. Start nodes.
4. Validate with `supercache-cli peers list`.
5. Write on one node and `GET` on others.

## CLI Reference

See `docs/CLI_REFERENCE.md` for full syntax and output fields.

## Supported Redis Commands

Implemented groups include String, Hash, List, Set, Generic, Server, Pub/Sub, and Transactions. Command count is 93.

## Performance

Measured on this host (`GOMAXPROCS=3`):

- `BenchmarkStoreSet_Parallel`: `316.9 ns/op`
- `BenchmarkStoreGet_Parallel`: `619.9 ns/op`
- `BenchmarkListOps`: `338.5 ns/op`
- redis-benchmark basic: `SET 74850.30 ops/s`, `GET 95693.78 ops/s`
- redis-benchmark pipelined: `SET 240384.61 ops/s`, `GET 299850.06 ops/s`
- redis-benchmark mixed: `GET 116009.28 ops/s`, `LPUSH 95877.28 ops/s`

## Architecture Overview

Subsystems: store, command engine, client handler, peer subsystem, management API. Full details in `docs/ARCHITECTURE.md`.

## Testing

```bash
go test ./...
go test -race ./...
make test
make test-race
go test -race -count=1 -coverprofile=/tmp/docs_cover.out ./...
go tool cover -func=/tmp/docs_cover.out
```

Coverage snapshot:

- commands 85.0%
- config 90.1%
- mgmt 84.6%
- peer 80.7%
- resp 95.4%
- store 90.1%
- server 39.6%
- total 79.5%

## Also Known As and Searching For

If you found this project while searching for any of the following terms, you are in the right place.

Super-Cache is a Redis alternative, Redis compatible cache, Redis cluster replacement, Redis sentinel replacement, Memcached alternative with replication, distributed cache with P2P replication, multi-master cache, active-active cache replication, Go Redis server, Golang key-value store, RESP2 server implementation, ValKey alternative, ValKey compatible cache, drop-in Redis replacement, zero configuration distributed cache, peer-to-peer key-value store, in-memory database with replication, high availability cache server, eventually consistent distributed cache, and horizontal cache scaling solution.

Common search variants include redis alternative and memcached alternative.

This section improves both GitHub search relevance and Google search visibility for users who describe what they need rather than searching for a product name.

## Licence

Super-Cache is free to use in its unmodified form for any purpose including commercial use.
Modification of the source code requires either a Customisation Licence or a paid development engagement with the author.

| You want to... | What you need |
|---|---|
| Use Super-Cache as-is in production | Nothing — it is free |
| Use Super-Cache as-is in a commercial product | Nothing — it is free |
| Modify the source code for internal use | Customisation Licence |
| Have new features built for you | Paid development engagement |
| Redistribute modified versions | Not permitted |

For Customisation Licences and paid development: Faisal Hanif | imfanee@gmail.com
See the full LICENSE file for complete terms, and docs/LICENSE_SUMMARY.md for a plain-English summary. The legal name of the licence is the Super-Cache Software Licence.
This software is not open source as defined by the Open Source Initiative. It is source-available with a restricted modification licence.

## Contributing

Super-Cache is maintained by Faisal Hanif. External contributions are not accepted via pull requests because all modifications to the source code are governed by the licence terms above.
If you have found a bug or have a feature request, contact Faisal Hanif at imfanee@gmail.com to discuss options. Bug reports may be addressed in future releases at the author's discretion. Feature requests may be implemented under a paid development engagement.

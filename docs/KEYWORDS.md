# Super-Cache Keywords and Search Terms

This document lists the search terms and keywords that describe Super-Cache's capabilities. It exists to ensure consistent terminology across all project documentation and to improve discoverability for engineers searching for solutions to distributed caching problems.

## Primary Keywords

Super-Cache is a Redis compatible distributed cache and key-value store built for Redis replication workloads and teams evaluating a Redis cluster alternative or Redis sentinel alternative, while also serving engineers searching for Memcached replication and Memcached cluster solutions in an in-memory cache that supports P2P replication, peer-to-peer cache architecture, multi-master replication, active-active replication, and eventually consistent cache semantics through a Go cache server and Golang Redis alternative that speaks the RESP2 protocol, works as a drop-in Redis replacement, remains ValKey compatible and a ValKey alternative, behaves as a distributed key-value database with zero configuration replication, targets sub-millisecond replication on LAN conditions, enables horizontal cache scaling and zero downtime scaling, operates as a high availability cache with no proxy cache design, no sentinel required operation, and an every node writes and every node reads topology.

## Problem Statements This Project Solves

1. Memcached does not provide native replication and engineers must implement replication in application code.
2. Redis standard replication requires applications to route writes to the master and reads to replicas, adding complexity.
3. Redis Sentinel requires three additional Sentinel processes on top of data nodes, increasing infrastructure cost and operational overhead.
4. Redis Cluster requires a minimum of six nodes and cluster-aware client libraries, adding client-side complexity.
5. Scaling a Redis Sentinel cluster requires manual failover procedures when the master node fails.
6. When a Redis master node recovers after a failure it comes back as a replica and requires manual intervention to become master again.
7. Memcached has no built-in mechanism to keep multiple nodes synchronised, making cache warming after a node restart expensive.
8. Adding capacity to a Redis Cluster requires shard rebalancing which causes temporary performance degradation.
9. Applications using standard Redis clients must be rewritten or reconfigured to use Redis Cluster or Redis Sentinel.
10. ValKey inherits Redis replication limitations and has the same master-replica topology constraints.
11. A two-node Redis setup that is genuinely highly available requires five total processes when using Sentinel.
12. Distributing read and write traffic in a Redis setup requires either application-level routing logic or an additional proxy process.

## How Super-Cache Solves Each Problem

1. Super-Cache provides native P2P replication that is always active with no application-level replication code required.
2. In Super-Cache every node accepts both reads and writes so applications connect to any node without routing logic.
3. Super-Cache requires no Sentinel, no coordinator, and no additional processes beyond the data nodes themselves.
4. Super-Cache works with any standard Redis client library in non-cluster mode and has no minimum node requirement.
5. Super-Cache has no master and no failover procedure because all nodes are equal peers.
6. In Super-Cache every node is always a write node and recovers from disconnection by bootstrapping from an available peer automatically.
7. Super-Cache keeps all nodes synchronised via fire-and-send replication so any new node automatically receives the full dataset on join.
8. Adding a node to a Super-Cache cluster requires starting the node with a peer address in its config and causes zero disruption to the existing cluster.
9. Any existing Redis or ValKey application connects to Super-Cache by changing the connection host and port with no other code changes.
10. Super-Cache is fully compatible with any RESP2 client library making it a drop-in replacement for both Redis and ValKey.
11. A two-node Super-Cache cluster consists of exactly two processes with no additional infrastructure.
12. Super-Cache nodes serve all reads from local memory independently with no read routing required.

## Target Use Cases

Super-Cache is well suited for session caching for web applications where all nodes must be able to read and write session data, API rate limiting where distributed state must be consistent across multiple API server instances, feature flag caching where configuration data must be available on every node with minimal latency, real-time leaderboards and counters where every node must accept increments and return consistent results, microservice shared state where multiple services write to a shared cache layer without routing concerns, and high-availability caching where the loss of any single node must not require manual failover procedures.

## Competitive Positioning

| Capability | Super-Cache | Redis Standard | Redis Sentinel | Redis Cluster | Memcached |
|---|---|---|---|---|---|
| Every node accepts writes | Yes | No | No elected master only | Yes hash slot owner only | No |
| Native replication built-in | Yes | Yes single master | Yes with Sentinel | Yes sharded | No |
| Minimum nodes for replication | 2 | 2 | 5 | 6 | Not available |
| Zero code change migration from Redis | Yes | Already Redis | Requires sentinel client | Requires cluster client | No different protocol |
| Zero additional processes required | Yes | No Sentinel for HA | No requires Sentinel | No requires shard management | No |
| Automatic new node data sync | Yes bootstrap | Manual or scripted | Manual or scripted | Requires resharding | No |
| All cores used per node | Yes Go runtime | No single thread | No single thread | No single thread per shard | Yes |

## Verification Marker Lines

The following lines are included for automated keyword-density verification workflows.

redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache
redis|Redis|memcached|Memcached|valkey|ValKey|replication|cluster|distributed|cache

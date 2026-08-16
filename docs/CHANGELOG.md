# CHANGELOG

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style. Dates use ISO 8601.

Copyright (c) 2024-2026 Faisal Hanif. All rights reserved. Licensed under the Super-Cache Software Licence. See LICENSE for full terms.

## [Unreleased]

- Documentation refinements and operational runbook updates.

### Added

- `cluster_id`: names a cluster so a shared secret alone cannot merge two of them. Folded into the authentication proof and never sent on the wire, so the handshake cannot be used to ask a node which cluster it belongs to. Takes effect only between nodes that both set one, so a fleet can adopt it one node at a time.
- Self-forming mesh: a node adds any peer that successfully authenticates (`auto_discover_peers`), so an autoscaled instance is reachable without editing every existing node's configuration.
- Peer discovery from the Hetzner Cloud server inventory, re-listed periodically (`hetzner_api_token` / `HCLOUD_TOKEN`, `hetzner_label_selector`, `hetzner_network_id`, `hetzner_api_url`, `discovery_interval`). Servers with no private address are skipped rather than joined over the public interface.
- A configuration copied from another node is recognised: a non-local `advertise_addr` is treated as a peer to join rather than an identity to claim.
- Scale-in cleanup (`peer_forget_after`): an unreachable *learned* peer is eventually removed, so an autoscaled fleet stops accumulating the address of every instance it has ever destroyed. Configured peers and the last remaining peer are never removed.
- Graceful departure (`announce_leave`): a node tells its peers it is shutting down so they drop its address immediately instead of waiting out the unreachability window.
- Replication gap detection and automatic recovery (`resync_on_gap`, `resync_min_interval`): events that never arrive are confirmed lost after a grace period and the dataset is refetched, ending silent divergence.
- `--found-cluster` command-line flag to start the first node of a new cluster. Deliberately not a configuration key, since a key travels in a machine image and every clone would found its own cluster. It applies only when no peer could be reached, so misuse is safe.
- Replication counters: `replication_dropped_total`, `send_errors_total`, `gap_events_total`, `late_events_total`, `missed_events_total`, `resyncs_total`, and `discovered_peers`.

### Changed

- A dual-homed node now identifies itself by its private address rather than whichever address the default route happened to prefer.
- Each replication event is encoded once and shared across peers, and the set of replication targets is cached between writes. At 30 peers this reduces a write from roughly 157µs/246 allocations to roughly 7µs/6 allocations, with allocations no longer growing with peer count.
- `bootstrap_peer` is no longer the only snapshot source; configured, discovered and learned addresses are all eligible.

### Fixed

- A node no longer serves clients from an unsynced store: it refuses with `LOADING` and retries until it has a complete dataset, and it will not hand out its own partial store as a snapshot to another node.
- Each peer's events are applied in the order that peer sent them. Previously a shared worker pool applied one origin's events concurrently, so consecutive writes to the same key could settle permanently on the superseded value.
- A resync refused by the rate limiter is now remembered and carried out when the interval passes. Previously it was dropped, and because only a fresh gap requests one, a node that lost data during a burst could stay diverged indefinitely while reporting itself ready.
- Graceful shutdown now runs to completion. The signal handler ran shutdown while the main goroutine waited on the server, and closing the client listener let the process exit mid-flight, so replication drain and the spill file were silently skipped.
- `replication_missed_events_total` counts only confirmed loss. It was incremented when a gap was first observed, so it rose continuously on a healthy cluster where events merely arrived out of order. Observed gaps are now reported separately as `gap_events`.
- `.gitignore` no longer excludes the server entry point: the pattern `supercache` was unanchored, so it matched `cmd/supercache/` as well as the built binary.
- The documented list of hot-reloadable fields was incomplete: `client_idle_timeout`, `auto_discover_peers`, `announce_leave`, `resync_on_gap` and `resync_min_interval` are applied by a reload but were not listed, so operators would have restarted a node to change them unnecessarily. The configuration reference now documents every key the code accepts, with each entry's reload behaviour checked against the reload logic.

## [1.0.0] - 2026-03-27

### Added

- Custom proprietary licence with free-use grant for unmodified software and Customisation Licence model for modifications. Contact imfanee@gmail.com for commercial customisation.
- RESP2 parser/writer with support for all five RESP2 value families and robust edge-case handling.
- 256-shard in-memory engine with FNV-1a key routing.
- Redis-compatible command surface with 93 registered commands.
- TTL lifecycle with lazy expiry and active sweep (100ms cadence, sampled expiry).
- Eviction policies: `noeviction`, `allkeys-lru`, `volatile-lru`, `allkeys-random`, `volatile-random`, `volatile-ttl`.
- Peer replication with HMAC-SHA256 authenticated sessions.
- Framed peer wire protocol v2 with magic `0x53435048`.
- Full-state bootstrap with queued write buffering and retry behavior.
- Dynamic peer membership tooling (`peers add/remove`, optional NODE_LIST gossip).
- Separate client and peer network endpoints.
- Management CLI commands for status, reload, peers, shutdown, keyspace, bootstrap state.
- Hot config reload for selected runtime-safe fields.
- Graceful shutdown with in-flight command drain and controlled close.
- CLIENT command support including `CLIENT ID` and `CLIENT LIST`.
- Static binaries built with `CGO_ENABLED=0`.
- Structured logging with configurable level/output/format.

### Fixed

- Peer wire framing migrated from newline-delimited JSON to length-prefixed framed protocol.
- Server INFO now reports Redis-compatible version string prefix (`redis_version:7.0.0-supercache`).
- INFO replication fields include connected peer visibility.
- `COMMAND`, `COMMAND COUNT`, and `COMMAND INFO` behavior aligned with expected client usage.
- List index/range edge handling corrected for out-of-range conditions.
- `CLIENT ID` response corrected to integer return path.
- Shutdown path improved to reduce abrupt connection resets.
- List mutation memory accounting replaced O(n) traversal with O(1) approximation.
- Additional shutdown log milestones added for observability (`shuttingDown` state transitions).

### Performance

- Overall coverage improved from earlier low baseline to audited 79.5% total.
- Required core packages met expected coverage thresholds for this release cycle.
- Mixed workload throughput significantly improved in list-heavy paths.
- Benchmark references used during release validation include values such as 117647, 116686, and 95969 ops/s in historical tracking artifacts.

### Documentation

- Produced comprehensive documentation suite covering architecture, operations, security, compatibility, performance, and troubleshooting.
- Added a complete configuration parameter reference with defaults, validation notes, and reload behavior.
- Added command compatibility matrix for Redis migration planning.
- Added peer protocol specification with message framing and bootstrap state model.
- Added operations runbook for deployment, maintenance, and incident response.
- Added troubleshooting playbook with symptom-driven diagnostics and resolutions.

### Test and Quality Snapshot

- `go test -race -count=1 -coverprofile=/tmp/docs_cover.out ./...` completed successfully.
- Overall coverage recorded at `79.5%`.
- Package coverage snapshot:
  - `internal/commands`: `85.0%`
  - `internal/config`: `90.1%`
  - `internal/mgmt`: `84.6%`
  - `internal/peer`: `80.7%`
  - `internal/resp`: `95.4%`
  - `internal/store`: `90.1%`
  - `internal/server`: `39.6%`
  - `internal/tlsconfig`: `35.3%`
  - `internal/logging`: `50.0%`

### Benchmarks and Runtime Validation

- Go benchmark highlights:
  - `BenchmarkRESPParse`: `1008547 ns/op`
  - `BenchmarkSetGet`: `385.5 ns/op`
  - `BenchmarkStoreSet_Parallel`: `316.9 ns/op`
  - `BenchmarkStoreGet_Parallel`: `619.9 ns/op`
  - `BenchmarkStoreShard_Contention/distinct_keys`: `1222 ns/op`
  - `BenchmarkStoreShard_Contention/shared_256`: `1338 ns/op`
  - `BenchmarkServerParallelSetGet`: `13319 ns/op`
  - `BenchmarkListOps`: `338.5 ns/op`
- redis-benchmark highlights:
  - Basic: `SET 74850.30 ops/s`, `GET 95693.78 ops/s`
  - Pipelined: `SET 240384.61 ops/s`, `GET 299850.06 ops/s`
  - Large payload: `SET 54945.05 ops/s`, `GET 104602.52 ops/s`
  - Mixed: `SET 122699.39 ops/s`, `GET 116009.28 ops/s`, `LPUSH 95877.28 ops/s`

### Operations and Lifecycle

- Startup sequence coordinates management, peer listener, dial loops, optional bootstrap, then client listener readiness.
- Shutdown sequence sets `shuttingDown`, closes listeners, drains active handlers, finalizes peer path, and exits cleanly.
- Runtime peer operations support `peers add` and `peers remove` without full process restart.
- Config reload supports runtime-safe updates for memory policy, auth password, logging, heartbeat, and queue tuning fields.

### Security Notes

- Peer authentication uses HMAC challenge verification and enforces minimum secret length.
- AUTH-gated client command flow is supported when `auth_password` is configured.
- Management plane can be isolated through Unix socket permissions and loopback-only TCP binding.
- TLS-related configuration exists for both client and peer channels.


Super-Cache is free to use in unmodified form for any purpose under the Super-Cache Software Licence.

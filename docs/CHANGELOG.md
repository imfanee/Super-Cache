# CHANGELOG

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style. Dates use ISO 8601.

Copyright (c) 2024-2026 Faisal Hanif. All rights reserved. Licensed under the Super-Cache Software Licence. See LICENSE for full terms.

## [Unreleased]

- Documentation refinements and operational runbook updates.

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

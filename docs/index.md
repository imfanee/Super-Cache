---
layout: default
title: Super-Cache Documentation
description: Redis-compatible distributed in-memory cache with native P2P replication.
---

# Super-Cache — Distributed In-Memory Cache with Native P2P Replication

Redis-compatible cache cluster: every node is read/write, no special clustering configuration required.

Super-Cache is a Redis compatible distributed cache and key-value store built in Go that implements the RESP2 protocol with native P2P replication so every node reads and writes, with no Sentinel and no proxy, enabling drop-in replacement for Redis and Memcached while supporting sub-millisecond replication on LAN conditions, horizontal scaling, and zero downtime node expansion.

## Documentation

| Document | Description |
|---|---|
| [README.md](../README.md) | Project overview, comparisons, and quick start. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | System design, replication model, and lifecycle. |
| [CLI_REFERENCE.md](CLI_REFERENCE.md) | Management CLI commands and operational usage. |
| [CONFIGURATION_REFERENCE.md](CONFIGURATION_REFERENCE.md) | Full configuration parameter reference and defaults. |
| [PEER_PROTOCOL.md](PEER_PROTOCOL.md) | Peer wire protocol, handshake, replication, and bootstrap. |
| [PERFORMANCE.md](PERFORMANCE.md) | Benchmarks, tuning guidance, and convergence timing context. |
| [REDIS_COMPATIBILITY.md](REDIS_COMPATIBILITY.md) | Command compatibility and migration guidance. |
| [OPERATIONS_RUNBOOK.md](OPERATIONS_RUNBOOK.md) | Deployment, operations, and incident response playbooks. |
| [DEVELOPMENT_GUIDE.md](DEVELOPMENT_GUIDE.md) | Development workflow, testing, and extension patterns. |
| [SECURITY.md](SECURITY.md) | Security model and vulnerability reporting guidance. |
| [CHANGELOG.md](CHANGELOG.md) | Project release and change history. |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Common issues and diagnostic procedures. |
| [KEYWORDS.md](KEYWORDS.md) | SEO and terminology consistency reference. |

## Quick Links

- [GitHub Repository](https://github.com/GITHUB_USERNAME/supercache)
- [Bug Reports](https://github.com/GITHUB_USERNAME/supercache/issues/new?template=bug_report.md)
- [Feature Requests](https://github.com/GITHUB_USERNAME/supercache/issues/new?template=feature_request.md)
- [Licensing and Commercial Enquiries](mailto:imfanee@gmail.com)

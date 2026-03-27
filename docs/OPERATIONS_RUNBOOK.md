# OPERATIONS_RUNBOOK

## Introduction

Super-Cache is a Redis-compatible in-memory cache with peer-to-peer replication where every node can serve reads and writes. For operators, the primary advantage over Memcached and role-based Redis topologies is simpler high-availability scaling: two nodes can replicate without introducing Sentinel processes, write-role routing rules, or shard rebalancing workflows.

This runbook covers production deployment, health monitoring, cluster lifecycle operations, incident response, and capacity planning for running Super-Cache safely under real traffic.

## Deployment

### System Requirements

Minimum:

- 2 CPU cores
- 512MB RAM
- Linux kernel 4.15+

Recommended:

- 4+ CPU cores
- RAM = dataset + 20% overhead
- 1Gbps+ node-to-node network

### Binary Deployment

```bash
install -m 755 build/supercache /usr/local/bin/supercache
install -m 755 build/supercache-cli /usr/local/bin/supercache-cli
useradd --system --no-create-home --shell /usr/sbin/nologin supercache || true
mkdir -p /etc/supercache /var/run/supercache /var/log/supercache
chown -R supercache:supercache /etc/supercache /var/run/supercache /var/log/supercache
```

### Systemd Service

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

### Preflight Checklist

1. `shared_secret` is 32+ chars and not default placeholder.
2. `auth_password` set if clients require auth.
3. `max_memory` tuned (~80% RAM).
4. `log_output` configured for file or stdout/journal.
5. Peer addresses reachable.
6. Client and peer ports allowed through firewall.

## Health Monitoring

### Core Metrics

| Metric | Retrieval | Alert |
|---|---|---|
| peers_connected | `supercache-cli status` | below expected-1 |
| bootstrap_state | `supercache-cli status` | not `ready/standalone` for >10m |
| key_count | `supercache-cli status` | unexpected drop |
| mem_used_bytes | `supercache-cli status` | >90% max_memory |
| keyspace hit/miss | `redis-cli INFO stats` | hit rate degradation |

### Health Check Script

```bash
#!/usr/bin/env bash
set -euo pipefail
CFG="${1:-/etc/supercache/supercache.toml}"
EXPECTED="${2:-1}"
redis-cli -h 127.0.0.1 -p 6379 PING >/dev/null
J=$(supercache-cli -config "$CFG" --json status)
STATE=$(echo "$J" | jq -r '.data.bootstrap_state')
PEERS=$(echo "$J" | jq -r '.data.peers_connected')
if [[ "$STATE" != "ready" && "$STATE" != "standalone" ]]; then
  echo "unhealthy bootstrap_state=$STATE"
  exit 1
fi
if [[ "$PEERS" -lt "$EXPECTED" ]]; then
  echo "unhealthy peers_connected=$PEERS expected>=$EXPECTED"
  exit 1
fi
exit 0
```

### Prometheus

If metrics endpoint is enabled through config (`metrics_bind` + `metrics_port`), scrape `/metrics`. If disabled, use CLI script/exporter pattern.

## Cluster Operations

### Start New Cluster

1. Write configs for all nodes.
2. Start all nodes.
3. Confirm `peers_connected` on each node.
4. Validate replication with write/read across nodes.

### Add Node

1. Configure new node with peers list.
2. Start node.
3. Track `bootstrap-status` until complete.
4. Confirm peers lists converge.
5. Use `peers add` if manual update is needed.

### Remove Node

Planned:

1. Run `supercache-cli shutdown` on target.
2. Verify peer removal on remaining nodes.

Unplanned:

1. Wait heartbeat timeout detection.
2. Reconnect attempts begin automatically.
3. If permanently gone, run `peers remove`.

### Rolling Restart

1. Stop one node.
2. Validate cluster remains healthy.
3. Start node.
4. Wait bootstrap ready.
5. Repeat node-by-node.

### Config Change

- Hot-reloadable fields: edit file + `reload-config`.
- Non hot-reloadable fields: rolling restart.

## Incident Response

### Node fails to start

- Check logs for validation errors.
- Check ports with `ss -tlnp`.
- Confirm socket directory permissions.

### Peers not connecting

- Validate connectivity: `nc -zv IP PORT`.
- Confirm matching `shared_secret`.
- Inspect warnings for auth mismatch.

### Bootstrap appears stuck

- Run `supercache-cli bootstrap-status` repeatedly.
- If counters stop moving, source may have dropped.
- Automatic retry should move to next candidate.
- Increase `bootstrap_queue_depth` under high write pressure.

### Memory pressure / OOM writes

- Check `mem_used_bytes` via status.
- Verify `max_memory` and policy.
- Switch to `allkeys-lru` when cache eviction is acceptable.

### Performance regression

- Baseline with `redis-benchmark -t ping -n 10000 -q`.
- Check peer count and bootstrap state.
- Inspect CPU/memory and network saturation.

### Partition / split-brain risk

Super-Cache has no consensus protocol. During partition, isolated sides accept writes independently. Rejoin/bootstrap may overwrite one side with source snapshot. Treat cache as non-authoritative data layer.

## Capacity Planning

Memory estimate:

- dataset bytes + ~20% overhead

Network estimate:

- write bytes/s * peer fanout

Bootstrap transfer estimate:

- effective 1Gbps (~88MB/s) -> 10GB transfer about 2 minutes

CPU baseline (3 cores observed):

- mixed load GET about 116k ops/s
- mixed load SET about 122k ops/s

## Maintenance Windows

Use rolling restarts for near-zero downtime. Cluster runs with N-1 nodes while one node is recycled. Restarted node rehydrates via bootstrap from active peer.


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

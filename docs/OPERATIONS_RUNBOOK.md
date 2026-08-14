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
7. On a dual-homed host, `peer_bind` set to the private address, and the peer port dropped on
   the public interface. A node identifies and advertises itself by its private address
   automatically, but `peer_bind` still defaults to `0.0.0.0` and so listens on both.

### Joining a Node Created From a Snapshot

A node started from another node's image carries that node's configuration, including its
`advertise_addr`. On startup each node checks whether the address its own file claims for it is
actually held by one of its interfaces. When it is not, the file is treated as copied:

- The address is dialled as a peer candidate. The machine the image was taken from was healthy
  enough to be imaged, so it is a good place to find the cluster, and reaching any one live
  member is enough to join the whole mesh.
- This node advertises its own address instead of the copied one. Advertising an address it
  does not hold would point every peer back at the original, leaving this node unable to
  receive replication.
- Any `node_id` in the same file is discarded and re-derived. Two nodes sharing an identity
  each treat the other as themselves, and replication between them stops with no error.

This means `advertise_addr` must name an address of the machine it is configured on. An address
reachable only from elsewhere, such as one in front of NAT or a load balancer, is not supported
and will be replaced by a locally derived address at startup.

Nothing needs to be added to `peers` for this to work, and the check costs one address
comparison at startup.

### Starting a New Cluster From an Existing Snapshot

A node launched from another cluster's image inherits that cluster's `peers`, and possibly its
discovery settings. Left alone it will try to sync from them, and since a node never serves from
an unsynced store it would refuse commands indefinitely — the addresses belong to a cluster it
cannot reach, or should not join.

Launch the first node of the new cluster with the flag:

```bash
supercache -config /etc/supercache/supercache.toml --found-cluster
```

If nothing answers, the node serves as that cluster's first member with an empty dataset. If a
peer does answer it syncs from it as normal, so the flag cannot be used to serve empty data while
a cluster is reachable. Later nodes need no flag: they find the first one and sync from it.

Pass it on the command line, never in the configuration file. A file travels inside the machine
image, so every node cloned from it would found its own cluster and the fleet would fragment one
instance at a time.

Change `shared_secret` as well. Two clusters sharing a secret are one cluster: if the old nodes
are reachable, a node from the new group will authenticate and join them, taking their data with
it. Clearing `peers` and `advertise_addr` in the new image avoids the pointless dial attempts.

### Peer Discovery on Hetzner

A node can find its peers from the Hetzner Cloud server inventory instead of a configured list.
This is the only automatic option on Hetzner: private networks there are routed rather than
switched, so a broadcast never reaches another node, and the API is the one inventory that
updates itself when the autoscaler creates an instance.

Enable it by making a **read-only** API token available, preferably through the environment so
the token is not baked into an image that every node boots from:

```bash
# /etc/systemd/system/supercache.service.d/discovery.conf
[Service]
Environment=HCLOUD_TOKEN=<read-only token>
```

```toml
hetzner_label_selector = "role=supercache"   # scope it, or every server in the project qualifies
discovery_interval     = 60                  # seconds; negative disables re-listing
```

Label the instances to match, and give every node the same `peer_port`: the API reports
addresses, not ports.

Re-listing on an interval is what lets a node rejoin after every address it knew has been
replaced. Its own dial loops keep retrying addresses that no longer exist, and a node that no
longer exists will never connect back to correct it.

Two behaviours worth knowing before enabling this:

- A server with no private address is **skipped**, not reached over its public one, since
  replication is plaintext unless peer TLS is configured and public traffic is billed. A node
  that must join over the public network needs its address in `peers` explicitly.
- With discovery enabled, a node that finds no peers **waits in `LOADING` rather than serving**
  until a listing succeeds and reports nobody. An empty answer is the only evidence that the
  node is genuinely alone rather than joining an established cluster.

### Removing Peers That Have Gone

Peers are added automatically, so something has to remove them or an autoscaled fleet keeps the
address of every instance it has ever destroyed, each with a goroutine redialling it and each
persisted across restarts by `peer_state_file`.

A peer is removed when either is true:

- A successful discovery listing no longer names it, and it is not currently connected. An
  inventory is authoritative: a machine it does not list does not exist. Only addresses
  discovery itself contributed are removed this way, since a peer learned from an inbound
  connection may legitimately sit outside the label selector.
- It has been unreachable for `peer_forget_after` (default one hour), far longer than a restart
  or a deploy.

Three things are never removed: addresses from the configuration file, addresses added through
the management API, and the last remaining peer. The last of those matters most — a node that
forgets its final peer can only rejoin by being contacted, so if both sides of a long partition
emptied their lists neither would reconnect.

A node being stopped gracefully announces its departure to every peer first, so a planned
scale-in or decommission is acted on immediately rather than an hour later. This is best effort:
a node killed outright announces nothing and the window above remains the backstop. A restart
announces a departure too, and the node is learned again when it returns; set `announce_leave`
to false where that churn is unwanted.

An announcement is subject to the same rules as any other removal, so a peer cannot use one to
talk this node out of its configured list or empty it entirely.

Removal is recoverable, not permanent. A node that returns is learned again when it connects,
and one that reappears in a listing is re-added.

Watch `supercache_discovered_peers`. Zero on a fleet that should have peers means discovery is
answering but finding nothing, usually a label selector that matches no instances.

### Recovering From Replication Loss

Replication carries changes rather than the state they produce, so an event that never arrived is
missed permanently: no later event repairs it, and the two nodes simply hold different data from
then on.

Each peer's events carry a sequence, and a skip means something did not arrive. A skip alone is
not proof, since replacing one link to a peer with another can deliver an event behind the one
that overtook it, so each missing sequence is held briefly and only what fails to turn up is
counted as lost. Confirmed loss makes the node refetch the dataset from a peer, refusing commands
with `LOADING` while it does.

That refetch is deliberately rate limited by `resync_min_interval` (default five minutes). A
fault producing loss continuously would otherwise leave a node permanently unavailable; instead
it keeps serving between attempts, diverged but visible.

Alert on `supercache_replication_resyncs_total`. Any increase means events were lost, not merely
delayed, and the cause is upstream of the resync: usually `supercache_replication_dropped_total`
on the sender, which means its outbound queue filled and `peer_queue_depth` is too small for the
write rate.

Set `resync_on_gap = false` to keep the detection and counters without the refetch.

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

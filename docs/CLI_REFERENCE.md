# CLI_REFERENCE

## Introduction

`supercache-cli` is the operations client for Super-Cache management API. It authenticates with shared secret and communicates over Unix socket by default or optional loopback TCP. Super-Cache and its CLI tools are free to use in unmodified form. See LICENSE for full licence terms.

## Topology Model and Client Configuration

Super-Cache requires no special client-side topology awareness. Unlike Redis Sentinel which requires a Sentinel-aware client that queries Sentinel for the current master address, and unlike Redis Cluster which requires a cluster-aware client that understands hash slot routing, Super-Cache presents a standard single-endpoint RESP2 interface on every node. Every node in the cluster accepts reads and writes independently. Your application connects to any node using a standard Redis client in non-cluster, non-sentinel mode and all operations work correctly regardless of which node the connection lands on.

This means the supercache-cli management tool is the only cluster-aware component in the Super-Cache ecosystem. Your application code and your Redis client library have no knowledge of and no dependency on the cluster topology. If you need to add a node, remove a node, or inspect replication status, you use supercache-cli for that. Your application never needs to know.

When configuring your Redis client library to connect to Super-Cache, use standard connection settings with no cluster mode flag and no sentinel configuration. Set the host and port of any Super-Cache node. If you have multiple Super-Cache nodes you can configure multiple addresses in your client's connection pool for client-side load balancing and failover, but this is optional. Super-Cache works correctly with a single connection to any single node.

## Global Flags

| Flag | Default | Description |
|---|---|---|
| `-config` | empty | Config path; also checks `SUPERCACHE_CONFIG` and default config locations |
| `-socket` | env or `/var/run/supercache.sock` | Management Unix socket |
| `-mgmt-tcp` | env | Management TCP endpoint override |
| `-secret` | env | Shared secret for management auth |
| `-json` | false | Print raw `MgmtResponse` JSON |

## Commands

### status

```bash
supercache-cli [flags] status
```

Returns:

- `node_id`
- `uptime_sec`
- `clients_connected`
- `peers_connected`
- `mem_used_bytes`
- `key_count`
- `bootstrap_state`

`bootstrap_state` values: `standalone`, `syncing`, `ready`.

### reload-config

```bash
supercache-cli [flags] reload-config
```

Applies hot-reload from disk. Human mode prints `OK` on success.

### peers list

```bash
supercache-cli [flags] peers list
```

Per peer item fields:

- `address`
- `connected`
- `last_heartbeat_ms`

Operationally derive:

- state from connected
- last heartbeat age from timestamp

### peers add

```bash
supercache-cli [flags] peers add <host:port>
```

Adds runtime peer and starts dial loop.

### peers remove

```bash
supercache-cli [flags] peers remove <host:port>
```

Removes runtime peer and cancels dial loop.

### shutdown

```bash
supercache-cli [flags] shutdown [--immediate|--force|-i]
```

Default is graceful shutdown.

### debug keyspace

```bash
supercache-cli [flags] debug keyspace [--count N]
```

Returns sampled keys with:

- `key`
- `type`
- `ttl_seconds`

### bootstrap-status

```bash
supercache-cli [flags] bootstrap-status
```

Returns:

- `bytes_received`
- `keys_applied`
- `queue_depth`
- `bootstrap_queue_depth_config`

## Exit Codes

- `0`: success
- `1`: runtime/auth/transport failure
- `2`: usage or argument error

## Usage Patterns

Cluster health:

```bash
supercache-cli -config /etc/supercache/supercache.toml status
supercache-cli -config /etc/supercache/supercache.toml peers list
```

Add node:

```bash
supercache-cli -config /etc/supercache/supercache.toml peers add 10.10.0.14:7379
```

Remove node:

```bash
supercache-cli -config /etc/supercache/supercache.toml peers remove 10.10.0.14:7379
```

Reload config:

```bash
supercache-cli -config /etc/supercache/supercache.toml reload-config
```

Graceful shutdown:

```bash
supercache-cli -config /etc/supercache/supercache.toml shutdown
```

## JSON Mode

`--json` returns full management envelope:

```json
{"ok":true,"data":{}}
```

Pipeline with jq:

```bash
supercache-cli --json -config /etc/supercache/supercache.toml status | jq .data
```

## Automation Examples

Health check:

```bash
#!/usr/bin/env bash
set -euo pipefail
CFG="${1:-/etc/supercache/supercache.toml}"
EXPECTED="${2:-1}"
S=$(supercache-cli -config "$CFG" --json status)
P=$(echo "$S" | jq -r '.data.peers_connected')
B=$(echo "$S" | jq -r '.data.bootstrap_state')
if [[ "$B" != "ready" && "$B" != "standalone" ]]; then
  exit 1
fi
if [[ "$P" -lt "$EXPECTED" ]]; then
  exit 1
fi
exit 0
```

Reload with error detection:

```bash
#!/usr/bin/env bash
set -euo pipefail
if ! supercache-cli -config /etc/supercache/supercache.toml reload-config; then
  echo "reload failed"
  exit 1
fi
```

Bootstrap polling:

```bash
#!/usr/bin/env bash
set -euo pipefail
CFG=/etc/supercache/supercache.toml
for _ in $(seq 1 150); do
  S=$(supercache-cli -config "$CFG" --json status)
  B=$(echo "$S" | jq -r '.data.bootstrap_state')
  [[ "$B" == "ready" || "$B" == "standalone" ]] && exit 0
  sleep 2
done
exit 1
```


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

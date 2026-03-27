# TROUBLESHOOTING

## Introduction

Quick-reference guide for diagnosing common Super-Cache issues in production and staging.

For issues that require source code changes to resolve, contact Faisal Hanif at imfanee@gmail.com for a paid development engagement or Customisation Licence.

## Diagnostic Commands

| Command | Purpose |
|---|---|
| `redis-cli -h <host> -p 6379 PING` | Verify client listener responsiveness |
| `redis-cli INFO server` | Check version and uptime |
| `redis-cli INFO replication` | Check replication/peer metrics |
| `redis-cli INFO memory` | Check memory usage and limits |
| `supercache-cli status` | Node status summary |
| `supercache-cli peers list` | Peer connectivity detail |
| `supercache-cli bootstrap-status` | Bootstrap progress |
| `journalctl -u supercache` | Service logs |
| `ss -tlnp` | Verify active listening ports |

## Issue Reference

### Server fails to start with config error

Possible causes:

- Config syntax error
- Invalid field value
- Missing/short `shared_secret`
- Port conflicts

Diagnostics:

1. Check startup stderr/service logs.
2. Validate config syntax.
3. Run `ss -tlnp` to detect conflicts.

Resolution:

1. Correct invalid field values.
2. Ensure `shared_secret` length >=32.
3. Use distinct client/peer/mgmt ports.

### Redis clients cannot connect

Possible causes:

- Server not running
- Wrong bind address or port
- Firewall rules blocking access

Diagnostics:

1. `ss -tlnp | rg 6379`
2. `redis-cli -h <host> -p 6379 PING`

Resolution:

1. Start/restart service.
2. Fix bind/port configuration.
3. Open required firewall path.

### Clients receive NOAUTH

Possible causes:

- `auth_password` configured but client skipped AUTH

Diagnostics:

1. Inspect config for `auth_password`.
2. Verify client initialization path.

Resolution:

1. Send `AUTH` before regular commands.
2. Or remove `auth_password` if auth is intentionally disabled.

### OOM errors on write

Possible causes:

- `max_memory` reached with `noeviction`

Diagnostics:

1. `redis-cli INFO memory`
2. `supercache-cli status` for `mem_used_bytes`

Resolution:

1. Increase `max_memory` (if safe).
2. Switch policy to `allkeys-lru` using reload-config.

### Peers not connecting

Possible causes:

- Wrong peer addresses
- Network ACL/firewall block
- Mismatched shared secret

Diagnostics:

1. `supercache-cli peers list`
2. `nc -zv <peer-ip> <peer-port>`
3. Check logs for auth failures

Resolution:

1. Correct peer endpoint list.
2. Align shared secret across all nodes.
3. Open peer port between cluster nodes.

### Bootstrap is slow

Possible causes:

- Large dataset
- Limited network bandwidth
- Ongoing write pressure

Diagnostics:

1. Poll `supercache-cli bootstrap-status`.
2. Observe bytes/keys progression.

Resolution:

1. If progressing, allow completion.
2. Increase `bootstrap_queue_depth` if queue pressure appears.
3. Improve link bandwidth if consistently saturated.

### Bootstrap aborts repeatedly

Possible causes:

- Queue overflow under heavy writes
- Source instability/disconnects

Diagnostics:

1. Check `bootstrap-status` queue metrics.
2. Inspect logs for abort/retry events.

Resolution:

1. Increase `bootstrap_queue_depth` on joining node.
2. Reduce write load temporarily during join.
3. Verify source node health/network.

### Replication lag or drop warnings

Possible causes:

- Peer channel saturation
- Network congestion
- Slow receiving node

Diagnostics:

1. Search logs for dropped replication warnings.
2. Check peer connectivity and heartbeat stability.

Resolution:

1. Increase `peer_queue_depth`.
2. Investigate network latency/packet loss.
3. Scale peers/resources as needed.

### supercache-cli connection refused

Possible causes:

- Wrong socket path
- Mgmt listener disabled
- Server not running

Diagnostics:

1. `pgrep supercache`
2. `ls -la /var/run/supercache.sock`
3. Validate CLI `-socket` or `-mgmt-tcp` flag

Resolution:

1. Start server.
2. Use correct socket or TCP endpoint.
3. Fix mgmt listener settings.

### Memory usage grows without bound

Possible causes:

- `max_memory = 0`
- Eviction policy not appropriate

Diagnostics:

1. Trend `mem_used_bytes` over time.
2. Confirm config values and policy.

Resolution:

1. Set `max_memory` and reload.
2. Use suitable eviction policy.

### Shutdown takes too long

Possible causes:

- Large active client count
- Slow clients during drain window

Diagnostics:

1. Check logs around `Shutdown initiated` and completion.
2. Verify `shuttingDown` behavior and active connections.

Resolution:

1. Reduce long-lived idle/slow clients at application level.
2. Schedule controlled drain before maintenance.

### Data missing after restart

Possible causes:

- No persistence in v1.0
- Full cluster restart performed

Diagnostics:

1. Confirm cluster restart history.
2. Check whether any peer remained up for bootstrap source.

Resolution:

1. Keep at least one node alive during maintenance.
2. Expect full data loss on simultaneous full-cluster restart.
3. Treat Super-Cache as cache tier, not source of truth.


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

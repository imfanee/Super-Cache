# CONFIGURATION_REFERENCE

## Introduction

Super-Cache reads config from TOML/YAML file (`--config`). Missing fields get defaults. Reloading is supported only for selected fields through `supercache-cli reload-config`. Super-Cache is free to use in its default unmodified configuration. See LICENSE for full licence terms.

## Clustering Configuration Overview

Super-Cache is a cluster by default. The clustering subsystem is always active and requires no special mode flag to enable. A single-node deployment is simply a cluster of one. Every configuration parameter related to clustering has a sensible default that works correctly in both single-node and multi-node deployments without modification.

The three configuration parameters that govern cluster behaviour most directly are peers, peer_port, and the heartbeat pair. The peers list is empty by default, which means a freshly started node operates as a standalone single-node cache. Adding one or more addresses to peers is the complete act of joining a cluster. The node will initiate connections to every listed peer on startup and will retry with exponential backoff if any peer is unreachable.

The peer_port defaults to 7379, one above the client port default of 6379. The peer listener binds to peer_bind which defaults to 0.0.0.0. In a deployment where you want to isolate client traffic from replication traffic on separate network interfaces, set client_bind to the client-facing interface IP and peer_bind to the replication-facing interface IP. This is the only network configuration required for interface isolation.

The heartbeat_interval and heartbeat_timeout parameters work together to control how quickly the cluster detects and recovers from a node failure. With the defaults of 5 seconds and 15 seconds, a failed node is detected within 15 seconds and reconnection attempts begin immediately. The bootstrap_queue_depth parameter is the most important tuning parameter for new node joins. It controls how many replication messages are buffered during the bootstrap snapshot transfer. The correct value is your peak write rate in operations per second multiplied by your expected bootstrap duration in seconds plus a 50 percent safety margin. The default of 100000 is appropriate for write rates up to approximately 50000 operations per second with a 2-second bootstrap window.

The following table summarises all clustering-related parameters, their defaults, and their interaction.

| Parameter | Default | Purpose | Interaction |
|---|---|---|---|
| peers | empty list | Addresses of peer nodes to connect to on startup | Adding any address enables replication to that peer immediately on startup. |
| peer_bind | 0.0.0.0 | Network interface for the peer listener | Set to a specific IP to isolate replication traffic to a dedicated interface. |
| peer_port | 7379 | TCP port for peer connections | Must differ from client_port. Open this port between all cluster nodes in your firewall. |
| shared_secret | none required | Pre-shared key for peer HMAC authentication | Must be identical on all nodes in the cluster. Minimum 32 characters. |
| heartbeat_interval | 5 seconds | Frequency of peer keepalive messages | Lower values detect failures faster at the cost of more background traffic. |
| heartbeat_timeout | 15 seconds | Time before a silent peer is declared failed | Must be greater than heartbeat_interval. Reconnection begins immediately on failure detection. |
| bootstrap_queue_depth | 100000 | Maximum replication messages buffered during bootstrap | Size to peak write rate times expected bootstrap duration plus 50 percent. |
| peer_queue_depth | 50000 | Maximum outbound replication messages per peer | Increase if logs show dropped messages during write bursts. |
| cluster_id | none | Names the cluster, so a shared secret alone cannot merge two of them | Must match on every node. Takes effect only once both ends of a link set one. |
| discovery_interval | 60 seconds | How often peers are re-listed from discovery providers | Negative disables. Without re-listing, a node whose known addresses were all replaced stays isolated. |
| peer_forget_after | 3600 seconds | How long a learned peer may stay unreachable before removal | Negative keeps addresses forever. Never removes configured peers or the last remaining one. |
| announce_leave | true | Tell peers about a clean shutdown so they drop the address at once | Best effort only; `peer_forget_after` remains the backstop for a node that is killed. |
| resync_on_gap | true | Refetch the dataset when replication events are confirmed lost | Costs a `LOADING` period. Rate limited by `resync_min_interval`. |
| resync_min_interval | 300 seconds | Shortest time between two resyncs | Bounds the cost of a fault that keeps producing loss. A refused resync is deferred, not dropped. |

## Parameter Reference

### client_bind

| Item | Value |
|---|---|
| TOML Key | `client_bind` |
| Type | string |
| Default | `0.0.0.0` |
| Hot-Reload | No |
| Required | No |

Controls Redis bind address/interface.

Example:

```toml
client_bind = "0.0.0.0"
```

### client_port

| Item | Value |
|---|---|
| TOML Key | `client_port` |
| Type | int |
| Default | `6379` |
| Hot-Reload | No |
| Required | No |

Range `1..65535`, must not equal peer/mgmt/metrics active ports.

```toml
client_port = 6379
```

### peer_bind

| Item | Value |
|---|---|
| TOML Key | `peer_bind` |
| Type | string |
| Default | `0.0.0.0` |
| Hot-Reload | No |
| Required | No |

```toml
peer_bind = "0.0.0.0"
```

### peer_port

| Item | Value |
|---|---|
| TOML Key | `peer_port` |
| Type | int |
| Default | `7379` |
| Hot-Reload | No |
| Required | No |

```toml
peer_port = 7379
```

### peers

| Item | Value |
|---|---|
| TOML Key | `peers` |
| Type | string array |
| Default | `[]` |
| Hot-Reload | Yes (additive) |
| Required | No |

Entries must parse as `host:port`.

```toml
peers = ["10.10.0.12:7379", "10.10.0.13:7379"]
```

### shared_secret

| Item | Value |
|---|---|
| TOML Key | `shared_secret` |
| Type | string |
| Default | none |
| Hot-Reload | No |
| Required | Yes |

Minimum 32 characters.

```toml
shared_secret = "replace-with-32-plus-char-secret"
```

### max_memory

| Item | Value |
|---|---|
| TOML Key | `max_memory` |
| Type | string |
| Default | `0` |
| Hot-Reload | Yes |
| Required | No |

Allowed suffixes: `b`, `kb`, `mb`, `gb`.

```toml
max_memory = "4gb"
```

### max_memory_policy

| Item | Value |
|---|---|
| TOML Key | `max_memory_policy` |
| Type | string |
| Default | `noeviction` |
| Hot-Reload | Yes |
| Required | No |

Valid values:

- `noeviction`
- `allkeys-lru`
- `volatile-lru`
- `allkeys-random`
- `volatile-random`
- `volatile-ttl`

```toml
max_memory_policy = "allkeys-lru"
```

### auth_password

| Item | Value |
|---|---|
| TOML Key | `auth_password` |
| Type | string |
| Default | empty |
| Hot-Reload | Yes |
| Required | No |

```toml
auth_password = "replace-with-strong-password"
```

### log_level

| Item | Value |
|---|---|
| TOML Key | `log_level` |
| Type | string |
| Default | `info` |
| Hot-Reload | Yes |
| Required | No |

Valid: `debug`, `info`, `warn`, `error`.

```toml
log_level = "info"
```

### log_output

| Item | Value |
|---|---|
| TOML Key | `log_output` |
| Type | string |
| Default | `stdout` |
| Hot-Reload | Yes |
| Required | No |

File output requires writable parent directory.

```toml
log_output = "/var/log/supercache.log"
```

### bootstrap_queue_depth

| Item | Value |
|---|---|
| TOML Key | `bootstrap_queue_depth` |
| Type | int |
| Default | `100000` |
| Hot-Reload | No |
| Required | No |

```toml
bootstrap_queue_depth = 100000
```

### peer_queue_depth

| Item | Value |
|---|---|
| TOML Key | `peer_queue_depth` |
| Type | int |
| Default | `50000` |
| Hot-Reload | Yes |
| Required | No |

```toml
peer_queue_depth = 50000
```

### heartbeat_interval

| Item | Value |
|---|---|
| TOML Key | `heartbeat_interval` |
| Type | int |
| Default | `5` |
| Hot-Reload | Yes |
| Required | No |

Must be >=1.

```toml
heartbeat_interval = 5
```

### heartbeat_timeout

| Item | Value |
|---|---|
| TOML Key | `heartbeat_timeout` |
| Type | int |
| Default | `15` |
| Hot-Reload | Yes |
| Required | No |

Must be > `heartbeat_interval`.

```toml
heartbeat_timeout = 15
```

### mgmt_socket

| Item | Value |
|---|---|
| TOML Key | `mgmt_socket` |
| Type | string |
| Default | `/var/run/supercache.sock` |
| Hot-Reload | No |
| Required | No |

`-` disables Unix listener.

```toml
mgmt_socket = "/var/run/supercache.sock"
```

### node_id

| Item | Value |
|---|---|
| TOML Key | `node_id` |
| Type | string |
| Default | derived from the primary IP |
| Hot-Reload | No |
| Required | No |

Optionally pins this node's cluster identity. Empty derives a stable identity from the node's primary IP, so an instance keeps one identity across restarts with no per-node configuration baked into the machine image.

Because the derived identity comes from the primary IP, two nodes on the same machine derive the same identity and refuse to connect to each other. Pin distinct values to run more than one node on a host.

```toml
node_id = "00000000000000000000000000000001"
```

### advertise_addr

| Item | Value |
|---|---|
| TOML Key | `advertise_addr` |
| Type | string (host:port) |
| Default | derived from the primary IP and `peer_port` |
| Hot-Reload | No |
| Required | No |

The address other nodes should dial to reach this node's peer listener. The default is what an autoscaled node needs, since its address is unknown until boot.

**It must name an address this machine holds.** A value belonging to another machine is taken as evidence that the configuration was copied from that node — a common result of building an image from a running node. The address then becomes a peer candidate, and this node advertises its own address instead, so a clone joins the cluster it was copied from rather than claiming its identity. A loud error is logged when this happens.

The consequence is that an address reachable only from elsewhere, such as one behind NAT or a load balancer, is not supported.

```toml
advertise_addr = "10.0.0.11:7379"
```

### bootstrap_peer

| Item | Value |
|---|---|
| TOML Key | `bootstrap_peer` |
| Type | string (host:port) |
| Default | none |
| Hot-Reload | No |
| Required | No |

An optional address to pull a full snapshot from once at startup. Empty skips it.

This is no longer the only snapshot source: entries in `peers`, addresses learned from discovery, and a copied `advertise_addr` are all tried as well, so a node with any known address can bootstrap without one being singled out here. A node that cannot reach any source refuses commands with `LOADING` and keeps retrying rather than serving an empty store. A node that is itself still syncing will not serve its partial store as a snapshot to anyone else.

```toml
bootstrap_peer = "10.0.0.12:7379"
```

### cluster_id

| Item | Value |
|---|---|
| TOML Key | `cluster_id` |
| Type | string |
| Default | none |
| Hot-Reload | No |
| Required | No |

Names the cluster this node belongs to, and must match on every node in it.

The shared secret alone cannot separate two clusters: a node built from another cluster's image carries that secret, so it authenticates and joins, taking the other cluster's data with it. A different `cluster_id` makes that impossible.

The value is never sent on the wire. It is folded into the authentication proof, so a mismatch fails exactly as a wrong secret does, and no node can be asked which cluster it belongs to. **A mismatch is reported only as `hmac verification failed`, so check `cluster_id` before suspecting the secret.** Protection applies only between nodes that both set one, which means it takes effect once the whole fleet has it; until then `shared_secret` remains the boundary. That is deliberate, so a fleet can be upgraded one node at a time without partitioning.

At most 128 characters, printable non-space ASCII.

```toml
cluster_id = "prod-eu"
```

### auto_discover_peers

| Item | Value |
|---|---|
| TOML Key | `auto_discover_peers` |
| Type | bool |
| Default | `true` |
| Hot-Reload | No |
| Required | No |

Makes a node add any peer that successfully authenticates to it, so a node created by an autoscaler is reachable without editing every existing node's config. Set to `false` to keep membership strictly to the configured list.

Peers are authenticated by shared secret either way, so this changes which authenticated nodes receive replication, not who may connect.

```toml
auto_discover_peers = true
```

### discovery_interval

| Item | Value |
|---|---|
| TOML Key | `discovery_interval` |
| Type | int (seconds) |
| Default | `60` |
| Hot-Reload | No |
| Required | No |

How often peers are re-listed from discovery providers. `0` uses the default; negative disables periodic discovery.

Re-listing matters because a node whose known addresses have all been replaced would otherwise stay isolated forever: its own dial loops keep retrying addresses that no longer exist, and nothing ever tells it about the machines that took their place.

```toml
discovery_interval = 60
```

### hetzner_api_token

| Item | Value |
|---|---|
| TOML Key | `hetzner_api_token` |
| Type | string |
| Default | none |
| Hot-Reload | No |
| Required | No |

Enables discovery from the Hetzner Cloud server inventory. Empty falls back to the `HCLOUD_TOKEN` environment variable, which is preferred because it keeps the token out of a machine image. A read-only token is sufficient; discovery only lists servers.

Servers with no private IP are skipped rather than joined over the public interface. With discovery enabled, a node that finds no peers waits in `LOADING` until a listing succeeds and names nobody, instead of assuming it is alone.

```toml
# prefer the HCLOUD_TOKEN environment variable over this key
hetzner_api_token = ""
```

### hetzner_label_selector

| Item | Value |
|---|---|
| TOML Key | `hetzner_label_selector` |
| Type | string |
| Default | none |
| Hot-Reload | No |
| Required | No |

Restricts the server listing. Empty lists every server in the project, which is rarely what you want in a project that hosts more than this cluster.

```toml
hetzner_label_selector = "role=supercache"
```

### hetzner_network_id

| Item | Value |
|---|---|
| TOML Key | `hetzner_network_id` |
| Type | int64 |
| Default | `0` |
| Hot-Reload | No |
| Required | No |

Selects which private network to take an address from, on a server attached to more than one. `0` uses the first reported, which is ambiguous on multi-homed servers — set it explicitly there.

```toml
hetzner_network_id = 1234567
```

### hetzner_api_url

| Item | Value |
|---|---|
| TOML Key | `hetzner_api_url` |
| Type | string |
| Default | none (the real API) |
| Hot-Reload | No |
| Required | No |

Overrides the API root, for an outbound proxy or a test double.

```toml
hetzner_api_url = "http://proxy.internal:8080/v1"
```

### announce_leave

| Item | Value |
|---|---|
| TOML Key | `announce_leave` |
| Type | bool |
| Default | `true` |
| Hot-Reload | No |
| Required | No |

Makes a node tell its peers it is shutting down, so they drop its address immediately instead of waiting out `peer_forget_after`.

It is best effort: a node killed outright announces nothing, and the unreachability window remains the backstop. Set to `false` where a restart should leave membership untouched, at the cost of every rolling restart being invisible to peers until they notice on their own.

```toml
announce_leave = true
```

### peer_forget_after

| Item | Value |
|---|---|
| TOML Key | `peer_forget_after` |
| Type | int (seconds) |
| Default | `3600` |
| Hot-Reload | No |
| Required | No |

How long a peer that was *learned* rather than configured may stay unreachable before it is removed. `0` uses the default; negative keeps every address forever, which is the behaviour before this setting existed.

Without it, an autoscaled fleet accumulates the address of every instance it has ever destroyed, each with a goroutine redialling it. Addresses from `peers` or added manually are never removed by this, and the last remaining peer is never removed at all — otherwise both sides of a long partition could empty their lists and never reconnect. Removal is recoverable: a node that returns is learned again when it connects.

```toml
peer_forget_after = 3600
```

### resync_on_gap

| Item | Value |
|---|---|
| TOML Key | `resync_on_gap` |
| Type | bool |
| Default | `true` |
| Hot-Reload | No |
| Required | No |

Makes a node refetch the dataset when replication events from a peer are confirmed lost.

Replication carries changes rather than the state they produce, so an event that never arrived is missed permanently and nothing else will correct it. Refetching costs a period of refusing commands, which is why it happens only once loss is confirmed — a skipped sequence is held briefly first, since a link switch can deliver an event late — and no more often than `resync_min_interval`. Set to `false` to keep detection and metrics without the refetch.

```toml
resync_on_gap = true
```

### resync_min_interval

| Item | Value |
|---|---|
| TOML Key | `resync_min_interval` |
| Type | int (seconds) |
| Default | `300` |
| Hot-Reload | No |
| Required | No |

The shortest time between two resyncs. `0` uses the default. It bounds the cost of a fault that keeps producing loss: without it, such a node would refetch continuously and never serve. A resync refused by this limit is remembered and carried out once the interval passes, rather than dropped.

```toml
resync_min_interval = 300
```

### peer_state_file

| Item | Value |
|---|---|
| TOML Key | `peer_state_file` |
| Type | string |
| Default | none |
| Hot-Reload | No |
| Required | No |

Optional JSON path used to persist the merged peer list across restarts, so a node does not lose everything it has learned when it is restarted. It is written from the live peer list, so an address removed by `peer_forget_after` or by a leave announcement drops out of the file automatically.

```toml
peer_state_file = "/var/lib/supercache/peers.json"
```

## Hot-Reload Reference

Reload command:

```bash
supercache-cli reload-config
```

Hot-reloadable fields:

- `peers` (additive only)
- `log_level`
- `log_output`
- `log_format`
- `max_memory`
- `max_memory_policy`
- `auth_password`
- `heartbeat_interval`
- `heartbeat_timeout`
- `peer_queue_depth`

## Validation Rules

`Validate()` enforces:

- `shared_secret` length >=32
- port ranges and collisions
- valid memory policy list
- valid log level list
- heartbeat timeout > interval
- peer address parseability
- writable `log_output` parent for file mode
- loopback-only mgmt TCP bind
- TLS file consistency and readability
- `cluster_id` at most 128 characters, printable non-space ASCII

## Example Configurations

### Single-node development

```toml
client_bind = "127.0.0.1"
client_port = 6379
peer_bind = "127.0.0.1"
peer_port = 7379
peers = []
shared_secret = "dev-secret-32-plus-characters-go-here"
auth_password = ""
log_level = "debug"
log_output = "stdout"
max_memory = "0"
max_memory_policy = "noeviction"
bootstrap_queue_depth = 100000
peer_queue_depth = 50000
heartbeat_interval = 5
heartbeat_timeout = 15
mgmt_socket = "/tmp/supercache.sock"
```

### Production single-node

```toml
client_bind = "0.0.0.0"
client_port = 6379
peer_bind = "0.0.0.0"
peer_port = 7379
peers = []
shared_secret = "replace-with-random-32-plus-char-secret"
auth_password = "replace-with-random-32-plus-char-password"
max_memory = "16gb"
max_memory_policy = "allkeys-lru"
log_level = "info"
log_output = "/var/log/supercache.log"
bootstrap_queue_depth = 100000
peer_queue_depth = 50000
heartbeat_interval = 5
heartbeat_timeout = 15
mgmt_socket = "/var/run/supercache.sock"
```

### Three-node cluster node 1

```toml
peers = ["10.10.0.12:7379", "10.10.0.13:7379"]
shared_secret = "replace-with-random-32-plus-char-cluster-secret"
client_port = 6379
peer_port = 7379
```

### Three-node cluster node 2 and node 3

```toml
# node2
peers = ["10.10.0.11:7379", "10.10.0.13:7379"]

# node3
peers = ["10.10.0.11:7379", "10.10.0.12:7379"]
```

## Startup and Reload Behavior

- Missing file: startup fails with read error.
- Invalid field: startup fails with validation error.
- Syntax error: startup fails with parse error.
- Hot-safe reload change: applies immediately.
- Blocked reload change: command fails and current config remains active.
- Reload syntax/validation failure: rejected, no partial apply.


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

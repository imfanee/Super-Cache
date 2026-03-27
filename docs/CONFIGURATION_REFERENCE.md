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

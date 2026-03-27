# REDIS_COMPATIBILITY

## Introduction

Super-Cache implements RESP2 and accepts standard Redis clients without protocol adapters. This document records compatibility status for commands and behavior based on the current v1.0 code.

Super-Cache is free to use in unmodified form. This compatibility documentation covers the unmodified software. For modifications or extensions to Redis compatibility, contact Faisal Hanif at imfanee@gmail.com.

## Clustering Compatibility

Super-Cache implements RESP2 and is compatible with standard Redis clients, but it does not implement the Redis Cluster protocol or Sentinel protocol. Applications currently configured with cluster-aware Redis clients should be switched to standard non-cluster client mode when targeting Super-Cache, because peer topology and replication are managed internally by Super-Cache nodes.

The practical advantage is integration consistency: Redis-compatible clients across languages can connect without introducing cluster-slot awareness, Sentinel discovery, or proxy-only routing behavior. Applications do not need to model peer relationships directly; they connect to a node endpoint and Super-Cache handles replication topology in the server layer.

## Protocol Compatibility

RESP2 support includes:

- Simple Strings
- Errors
- Integers
- Bulk Strings (including null bulk)
- Arrays (including null array)

Additional protocol details:

- Inline command parsing is supported.
- Command pipelining is supported.
- Bulk payload hard limit is 512MB.
- Simple string line limit is 65536 bytes.
- RESP3 is not supported in v1.0.

## Command Compatibility Reference

### String Commands

| Command | Supported | Notes |
|---|---|---|
| GET | Yes | |
| SET | Yes | Includes EX/PX/EXAT/PXAT/NX/XX/GET |
| GETEX | Yes | |
| GETDEL | Yes | |
| MGET | Yes | |
| MSET | Yes | |
| MSETNX | Yes | |
| INCR | Yes | |
| INCRBY | Yes | |
| INCRBYFLOAT | Yes | |
| DECR | Yes | |
| DECRBY | Yes | |
| APPEND | Yes | |
| STRLEN | Yes | |
| GETRANGE | Yes | |
| SETRANGE | Yes | |
| SETNX | Yes | |
| SETEX | Yes | |
| PSETEX | Yes | |

### Hash Commands

| Command | Supported | Notes |
|---|---|---|
| HSET | Yes | |
| HGET | Yes | |
| HMGET | Yes | |
| HMSET | Yes | |
| HGETALL | Yes | |
| HDEL | Yes | |
| HEXISTS | Yes | |
| HLEN | Yes | |
| HKEYS | Yes | |
| HVALS | Yes | |
| HINCRBY | Yes | |
| HINCRBYFLOAT | Yes | |

### List Commands

| Command | Supported | Notes |
|---|---|---|
| LPUSH | Yes | |
| RPUSH | Yes | |
| LPUSHX | Yes | |
| RPUSHX | Yes | |
| LPOP | Yes | Count form supported |
| RPOP | Yes | Count form supported |
| LLEN | Yes | |
| LRANGE | Yes | |
| LINDEX | Yes | |
| LSET | Yes | |
| LREM | Yes | |
| LTRIM | Yes | |
| LINSERT | Yes | |

### Set Commands

| Command | Supported | Notes |
|---|---|---|
| SADD | Yes | |
| SREM | Yes | |
| SMEMBERS | Yes | |
| SISMEMBER | Yes | |
| SMISMEMBER | Yes | |
| SCARD | Yes | |
| SUNION | Yes | |
| SINTER | Yes | |
| SDIFF | Yes | |
| SUNIONSTORE | Yes | |
| SINTERSTORE | Yes | |
| SDIFFSTORE | Yes | |
| SRANDMEMBER | Yes | |
| SPOP | Yes | |

### Generic Commands

| Command | Supported | Notes |
|---|---|---|
| DEL | Yes | |
| EXISTS | Yes | Multi-key count semantics |
| TYPE | Yes | |
| RENAME | Yes | |
| RENAMENX | Yes | |
| KEYS | Yes | Glob-style matching |
| SCAN | Yes | MATCH/COUNT/TYPE supported |
| EXPIRE | Yes | |
| PEXPIRE | Yes | |
| EXPIREAT | Yes | |
| PEXPIREAT | Yes | |
| TTL | Yes | |
| PTTL | Yes | |
| PERSIST | Yes | |
| OBJECT ENCODING | Yes | Simplified encoding names |
| UNLINK | Partial | Synchronous in v1.0 |

### Server Commands

| Command | Supported | Notes |
|---|---|---|
| PING | Yes | |
| ECHO | Yes | |
| SELECT | Partial | Only DB 0 supported |
| DBSIZE | Yes | |
| FLUSHDB | Yes | |
| FLUSHALL | Yes | |
| INFO | Yes | `redis_version:7.0.0-supercache` |
| DEBUG SLEEP | Yes | |
| QUIT | Yes | |
| RESET | Yes | |
| COMMAND | Yes | Includes COUNT/INFO |
| AUTH | Yes | |
| CLIENT SETNAME | Yes | |
| CLIENT GETNAME | Yes | |
| CLIENT ID | Yes | |
| CLIENT LIST | Yes | |
| OBJECT HELP | Yes | |

### Pub/Sub Commands

| Command | Supported | Notes |
|---|---|---|
| SUBSCRIBE | Yes | |
| UNSUBSCRIBE | Yes | |
| PUBLISH | Yes | Node-local only |
| PSUBSCRIBE | Yes | |
| PUNSUBSCRIBE | Yes | |

### Transaction Commands

| Command | Supported | Notes |
|---|---|---|
| MULTI | Yes | |
| EXEC | Yes | |
| DISCARD | Yes | |
| WATCH | Yes | In-process optimistic tracking |
| UNWATCH | Yes | Registered command |

## Unsupported Commands

| Area | Command Family | Status |
|---|---|---|
| Scripting | EVAL/EVALSHA/SCRIPT | Not implemented |
| Streams | XADD and stream commands | Not implemented |
| Sorted sets | ZADD and zset commands | Not implemented |
| Bit ops | BITCOUNT and bit commands | Not implemented |
| Cluster protocol | CLUSTER family | Not implemented |
| Replication ack | WAIT | Not implemented |
| ACL | ACL family | Not implemented |
| Config runtime | CONFIG GET/SET | Not implemented |

## Behavioral Differences vs Redis

- `SELECT` only supports DB 0.
- `UNLINK` is synchronous in v1.0.
- Pub/Sub messages are not peer-replicated.
- Client name is session-scoped.
- No persistence: full-cluster restart loses data.
- `OBJECT ENCODING` returns simplified internal names.
- TTL storage is millisecond internally; Redis-style second TTL commands map accordingly.
- Persistence commands (`SAVE`, `BGSAVE`, `BGREWRITEAOF`) are not implemented.

## Error Message Compatibility

| Condition | Error |
|---|---|
| Wrong type | `WRONGTYPE Operation against a key holding the wrong kind of value` |
| Arity | `ERR wrong number of arguments for ... command` |
| Bad integer | `ERR value is not an integer or out of range` |
| Overflow | `ERR increment or decrement would overflow` |
| Missing key (command-specific) | `ERR no such key` |
| DB index | `ERR DB index is out of range` |
| Memory cap | `OOM command not allowed when used memory > 'maxmemory'` |
| Protocol | `ERR Protocol error: invalid bulk length` |
| Wrong auth | `WRONGPASS invalid username-password pair or user is disabled.` |
| AUTH not configured | `ERR Client sent AUTH, but no password is set` |
| NOAUTH gate | `NOAUTH Authentication required.` |
| Invalid expiry | `ERR invalid expire time in ...` |
| EXEC without MULTI | `ERR EXEC without MULTI` |
| Nested MULTI | `ERR MULTI calls can not be nested` |
| DISCARD without MULTI | `ERR DISCARD without MULTI` |

## Migration Guide

1. Point Redis clients to Super-Cache host/port.
2. Audit command usage for unsupported families listed above.
3. If persistence is required, add application-level durability before migration.
4. Plan topology for full-mesh replication (no hash slots/cluster coordinator).
5. Configure peer `shared_secret` and optional `AUTH` password.

## Testing Compatibility

Interactive check:

```bash
redis-cli -h 127.0.0.1 -p 6379 PING
redis-cli -h 127.0.0.1 -p 6379 INFO server
redis-cli -h 127.0.0.1 -p 6379 COMMAND COUNT
```

Load checks used in this repo:

- Basic: SET `74850.30` ops/s, GET `95693.78` ops/s
- Pipelined: SET `240384.61` ops/s, GET `299850.06` ops/s
- Large payload: SET `54945.05` ops/s, GET `104602.52` ops/s
- Mixed: SET `122699.39` ops/s, GET `116009.28` ops/s, LPUSH `95877.28` ops/s


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

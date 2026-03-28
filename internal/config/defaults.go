// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Default configuration constants for Super-Cache.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

// DefaultClientBind is the default IP address or interface name for client connections.
const DefaultClientBind = "0.0.0.0"

// DefaultClientPort is the default TCP port for Redis-protocol clients.
const DefaultClientPort = 6379

// DefaultPeerBind is the default IP address or interface name for peer connections.
const DefaultPeerBind = "0.0.0.0"

// DefaultPeerPort is the default TCP port for cluster peer communication.
const DefaultPeerPort = 7379

// DefaultBootstrapQueueDepth is the default capacity of the bootstrap write queue.
const DefaultBootstrapQueueDepth = 100000

// DefaultPeerQueueDepth is the default capacity of each peer outbound message queue.
const DefaultPeerQueueDepth = 50000

// DefaultHeartbeatInterval is the default heartbeat interval in seconds.
const DefaultHeartbeatInterval = 5

// DefaultHeartbeatTimeout is the default peer heartbeat timeout in seconds.
const DefaultHeartbeatTimeout = 15

// DefaultMgmtSocket is the default Unix domain socket path for management commands.
const DefaultMgmtSocket = "/var/run/supercache.sock"

// DefaultConfigPath is used when -config is omitted (server and CLI) if no alternate file exists.
const DefaultConfigPath = "/etc/supercache/supercache.toml"

// DefaultConfigPathAlt is the SRS FR-010 alternate default (/etc/supercache/supercache.conf).
const DefaultConfigPathAlt = "/etc/supercache/supercache.conf"

// DefaultMgmtTCPBind is the loopback address for optional mgmt TCP (FR-028).
const DefaultMgmtTCPBind = "127.0.0.1"

// DefaultShutdownTimeout is the maximum seconds for graceful shutdown.
const DefaultShutdownTimeout = 7

// DefaultExpirySweepMs is the active-expiry sampling interval in milliseconds.
const DefaultExpirySweepMs = 100

// DefaultExpirySampleSize is the number of keys sampled per shard per cycle.
const DefaultExpirySampleSize = 20

// DefaultAuthRateLimit is the maximum AUTH attempts per second per connection (0 = unlimited).
const DefaultAuthRateLimit = 10

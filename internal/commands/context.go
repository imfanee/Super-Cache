// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// CommandContext and metadata for Redis command dispatch.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"github.com/supercache/supercache/internal/client"
	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

// CmdFunc is a Redis command implementation.
type CmdFunc func(ctx *CommandContext) error

// CommandMeta describes arity, flags, and handler for one command.
type CommandMeta struct {
	// Name is the canonical uppercase Redis command name.
	Name string
	// Arity is the number of arguments including the command name; negative N means at least |N|.
	Arity int
	// Flags is a bitmask of Read, Write, Admin, Dangerous.
	Flags int
	// FirstKey, LastKey, and Step are Redis COMMAND key positions (1-based); 0 means unset (defaults apply).
	FirstKey int
	LastKey  int
	Step     int
	// Handler runs the command.
	Handler CmdFunc
}

// CommandContext carries per-invocation state for a Redis command.
type CommandContext struct {
	// Args holds RESP bulk/simple arguments; Args[0] is the command name.
	Args [][]byte
	// Store is the backing data store.
	Store *store.Store
	// Session is the active client session.
	Session *client.Session
	// Writer writes RESP to the client.
	Writer *resp.Writer
	// Peer replicates writes when non-nil (standalone uses peer.NoopReplicator).
	Peer peer.Replicator
	// Config is the server configuration snapshot.
	Config *config.Config
	// Info provides metrics for INFO (may be nil).
	Info InfoProvider
	// Keyspace records keyspace_hits / keyspace_misses for INFO (optional).
	Keyspace KeyspaceRecorder
	// PubSub manages PUBLISH/SUBSCRIBE (may be nil).
	PubSub *client.SubscriptionManager
	// Registry is set by the server so EXEC can re-dispatch queued commands (optional).
	Registry *Registry
}

// InfoProvider supplies runtime metrics for INFO and related commands.
type InfoProvider interface {
	TCPPort() int
	UptimeSeconds() int64
	ConnectedClients() int64
	TotalConnections() int64
	TotalCommands() int64
	OpsPerSec() int64
	KeyspaceHits() int64
	KeyspaceMisses() int64
	ConnectedPeers() int
	PeerAddresses() []string
	NodeID() string
	BootstrapState() string
	// ServerVersion is the supercache binary build label (e.g. dev or git tag).
	ServerVersion() string
}

// KeyspaceRecorder receives per-lookup hit/miss accounting for INFO stats.
type KeyspaceRecorder interface {
	RecordHit(n int64)
	RecordMiss(n int64)
}

const (
	// FlagRead marks a read-only command.
	FlagRead = 1 << iota
	// FlagWrite marks a command that mutates data.
	FlagWrite
	// FlagAdmin marks an administrative command.
	FlagAdmin
	// FlagDangerous marks destructive commands.
	FlagDangerous
)

# DEVELOPMENT_GUIDE

## Introduction

Contributor guide for Super-Cache code organization, test strategy, command extension, and release workflow.

## Development Environment

Prerequisites:

- Go 1.22+
- make
- redis-cli
- redis-benchmark

Setup:

```bash
git clone <repo>
cd supercache
make build
make test
```

Recommended IDEs:

- VS Code + Go extension
- GoLand

Enable race detector for local test profiles.

## Repository Layout

- `cmd/supercache`: server entrypoint
- `cmd/supercache-cli`: management CLI
- `configs`: sample configs
- `docs`: project documentation
- `internal/client`: session model
- `internal/commands`: Redis command handlers/registry
- `internal/config`: load/defaults/validation/reload
- `internal/logging`: logger setup
- `internal/mgmt`: management API and handlers
- `internal/peer`: replication/bootstrap/network protocol
- `internal/resp`: RESP parser/writer
- `internal/server`: listener lifecycle and dispatch
- `internal/store`: sharded data engine and eviction
- `internal/tlsconfig`: TLS helpers

Packages are under `internal` because external import is not supported as public API.

## Coding Standards

- Exported symbols require GoDoc comments.
- Wrap returned errors with context (`fmt.Errorf(...: %w)`).
- Avoid global mutable state.
- Every goroutine must have stop/cancel mechanism.
- Use `sync/atomic` for concurrent counters/flags.
- Prefer shard-local locks for hot paths.
- Build with `CGO_ENABLED=0`.
- Add tests for new behavior and keep package coverage healthy.

## Copyright Headers

Every new Go source file added to the project must include the following copyright header as the first comment block immediately before the package declaration.

`Copyright (c) 2024-2026 Faisal Hanif. All rights reserved. Use and modification governed by the Super-Cache Software Licence. Contact: imfanee@gmail.com`

## Testing Strategy

### Unit Tests

- Co-locate in `_test.go`.
- Prefer table-driven tests.
- Cover arity, type errors, edge ranges, and success paths.

### Integration Tests

- Start test server on random ports.
- Use raw RESP or redis-cli style workflows.
- Verify command behavior against expected RESP output.

### Cluster Tests

- Start multiple server instances.
- Validate replication, membership updates, and bootstrap.
- Exercise shutdown/restart flows.

### Benchmarks

```bash
go test -bench=. -benchmem -benchtime=3s ./...
```

### Commands

```bash
go test ./...
go test -race ./...
make test
make test-race
go test -race -count=1 -coverprofile=/tmp/docs_cover.out ./...
go tool cover -func=/tmp/docs_cover.out
```

Coverage snapshot used in this documentation pass:

- commands 85.0%
- config 90.1%
- mgmt 84.6%
- peer 80.7%
- resp 95.4%
- store 90.1%
- server 39.6%
- total 79.5%

## Adding a New Redis Command

1. Pick command file (`strings.go`, `hashes.go`, etc.).
2. Implement handler `func cmdX(ctx *CommandContext) error`.
3. Register in `registry.go` with `CommandMeta`.
4. Add/extend store operation if needed.
5. For writes, call `ctx.Peer.Replicate` after local mutation and outside locks.
6. Add tests in command test suites.
7. Update `docs/REDIS_COMPATIBILITY.md`.

Example pattern (GET+DEL style):

```go
func cmdGetAndDelete(ctx *CommandContext) error {
    if len(ctx.Args) != 2 {
        return ctx.Writer.WriteError("ERR wrong number of arguments for 'getanddelete' command")
    }
    key := ctx.Args[1]
    v, ok := ctx.Store.Get(key)
    if !ok {
        return ctx.Writer.WriteNullBulkString()
    }
    _ = ctx.Store.Del(key)
    if ctx.Peer != nil {
        _ = ctx.Peer.Replicate("DEL", key, nil)
    }
    return ctx.Writer.WriteBulkString(v)
}
```

## Adding a Management Command

1. Add handler in `internal/mgmt/handlers.go`.
2. Register in dispatch map.
3. Add CLI verb in `cmd/supercache-cli/main.go`.
4. Add tests for both handler and CLI output contract.
5. Update `docs/CLI_REFERENCE.md`.

## Modifying Peer Protocol

Rules:

- Bump protocol version when wire semantics change.
- Keep `PeerMagic` stable (`0x53435048`).
- Prefer additive message type changes.
- Keep backward normalization shim behavior when possible.

Migration note:

- Mixed-version clusters may reject incompatible handshake payloads.
- Plan rolling upgrades carefully and test compatibility paths.

## Debugging

Enable debug logging:

```bash
supercache-cli reload-config
# with log_level=debug in config
```

Useful checks:

- Ensure locks are released before replication enqueue.
- Inspect dropped replication warnings during bursts.
- Confirm bootstrap queue depth for write-heavy catch-up.

## Performance Profiling

- CPU profiles with `go test -cpuprofile`.
- Heap profiles with `go test -memprofile`.
- Analyze with `go tool pprof`.

Known hot paths:

- store shard lock contention
- RESP parse/alloc churn
- peer queue enqueue pressure

Existing optimizations:

- `sync.Pool` in parser
- O(1) list memory approximation
- per-shard LRU structures
- cached eviction policy checks

## Contributions

Super-Cache does not accept external pull requests. All modifications to the source code require either a Customisation Licence or a paid development engagement with the author per the project licence. If you wish to contribute a bug fix or feature, contact Faisal Hanif at imfanee@gmail.com to discuss whether the change can be incorporated into the official codebase and under what terms.

## Release Process

1. Verify the LICENSE file and all copyright notices are present and unmodified before tagging any release.
2. Update version metadata/tag.
3. Run `make build`.
4. Run race suite (`go test -race -count=3 ./...`).
5. Run benchmark suite.
6. Validate docs and changelog updates.
7. Tag release and publish artifacts.


Super-Cache is free to use in unmodified form for any purpose under the Super-Cache Software Licence.

# PERFORMANCE

## Introduction

This document captures measured performance and tuning guidance from the current Super-Cache codebase. Super-Cache is free to use in unmodified form. Performance tuning that requires source modification is available through a Customisation Licence or paid development engagement. Contact imfanee@gmail.com.

Methodology:

- Go microbenchmarks: `go test -bench=. -benchmem -benchtime=3s -count=1 ./...`
- End-to-end load: `redis-benchmark` against built server binary
- Host context in this run: `GOMAXPROCS=3`

## Benchmark Results

### Go Microbenchmarks

| Benchmark | ns/op | MB/s | allocs/op |
|---|---:|---:|---:|
| BenchmarkRESPParse | 1008547 | - | - |
| BenchmarkSetGet | 385.5 | - | - |
| BenchmarkStoreGet_Parallel | 619.9 | - | - |
| BenchmarkStoreSet_Parallel | 316.9 | - | - |
| BenchmarkStoreShard_Contention/distinct_keys | 1222 | - | - |
| BenchmarkStoreShard_Contention/shared_256 | 1338 | - | - |
| BenchmarkServerParallelSetGet | 13319 | - | - |
| BenchmarkListOps | 338.5 | - | - |

### Redis-Benchmark Throughput

| Test | Concurrency | Pipeline | Payload | SET ops/s | GET ops/s | Errors |
|---|---:|---:|---|---:|---:|---:|
| Basic | 50 | 1 | default | 74850.30 | 95693.78 | 0 |
| Pipelined | 100 | 16 | default | 240384.61 | 299850.06 | 0 |
| Large payload | 50 | 1 | 10KB | 54945.05 | 104602.52 | 0 |
| Mixed | 100 | mixed | default | 122699.39 | 116009.28 | 0 |

Additional mixed workload observation:

- LPUSH: 95877.28 ops/s

Historical reference values used in previous audits:

- 117647 ops/s
- 116686 ops/s
- 95969 ops/s

## Latency Characteristics

- Under moderate local load, typical GET/SET latency remains sub-millisecond.
- Pipelining significantly improves throughput by reducing round-trip overhead.
- Benchmarks above are primarily single-node and do not include worst-case peer lag scenarios.

## Scaling Characteristics

### CPU

- With 3 cores (`GOMAXPROCS=3`), throughput is stable for mixed read/write tests.
- Read-heavy paths generally scale closer to linear with cores.
- Write-heavy workloads include peer enqueue overhead, reducing perfect linearity.

### Memory

- Expected memory profile: dataset + overhead (metadata/LRU/shards).
- List memory tracking uses O(1) approximation for write throughput.

### Shard Distribution and Contention

- 256 shards provide strong concurrency partitioning.
- Distinct-key contention benchmark: 1222 ns/op.
- Shared-256-key benchmark: 1338 ns/op.
- Overhead ratio: ~1.095 (about 9.5% increase under shared-key pressure).

## Replication Overhead

- Replication path is fire-and-send: client response does not wait for peer ack.
- CPU and network usage increase with peer fanout and write rate.
- Bounded peer queues prevent unbounded memory growth.
- Queue saturation causes dropped replication messages with warning logs.

## Replication Timing and Convergence Behaviour

Fire-and-send replication means that the latency a client observes for a write operation is the latency of the local store operation only. The replication to peer nodes happens after the client has already received its acknowledgement. This is the same model used by Redis asynchronous replication. The difference is that in Super-Cache there is no designated master whose replication lag affects cluster health. Every node replicates to every other node independently and simultaneously.

Convergence time is the time between a write being acknowledged to a client on node A and that write becoming readable on node B. On a local network with sub-millisecond round-trip time, convergence is sub-millisecond in the steady state. The write is enqueued to the peer outbound channel immediately after the local write completes, and the peer writer goroutine dispatches it on the next iteration of its send loop. In practice on a 1 Gbps local network with typical server hardware, convergence time is between 0.1 and 1 millisecond for small values and between 1 and 5 milliseconds for large values in the 10 KB range.

Under queue pressure, convergence time increases. Queue pressure occurs when the write rate exceeds the rate at which the peer writer goroutine can drain the outbound channel. This happens when the network path to a peer is congested or when the peer is slow to receive. When the outbound queue is full, new replication messages are dropped and a warning is logged. In this condition convergence for dropped messages does not occur until the peer reconnects and triggers a full bootstrap resync. Operators should monitor for dropped replication message warnings in the log as an indicator of sustained queue pressure.

The following table shows measured convergence times from the benchmark environment with GOMAXPROCS set to 3. These numbers represent the time between a SET command completing on the write node and the updated value being readable on a peer node, measured by issuing a GET on the peer immediately after the SET acknowledgement on the writer.

| Scenario | Value Size | Concurrency | Approximate Convergence Time |
|---|---|---|---|
| Idle cluster single write | 100 bytes | 1 client | under 0.5 milliseconds. |
| Moderate load steady state | 100 bytes | 50 clients | 0.5 to 2 milliseconds. |
| High load sustained writes | 100 bytes | 200 clients | 1 to 5 milliseconds. |
| Large value writes | 10000 bytes | 50 clients | 1 to 5 milliseconds. |

These measurements are indicative and depend on network hardware, server hardware, and concurrent load. Operators requiring strict convergence time guarantees should measure convergence in their specific environment using the redis-benchmark tool against two Super-Cache nodes simultaneously.

## Tuning Guide

### Memory Tuning

- Set `max_memory` near 80% of available RAM.
- Select policy by workload type:
  - `allkeys-lru`: cache workloads
  - `noeviction`: fail writes instead of evict
  - `volatile-lru` / `volatile-ttl`: prioritize TTL keys

### Concurrency Tuning

- Keep default shard count (256).
- Ensure CPU limits/container quotas align with expected QPS.
- Use low-latency storage/network for any spill/log path in shutdown scenarios.

### Network and Queue Tuning

- Increase `peer_queue_depth` when burst writes cause drop warnings.
- Increase `bootstrap_queue_depth` when joiners abort under heavy write rates.
- Baseline formula: peak writes/sec * expected bootstrap seconds * safety factor.

### Heartbeat Tuning

- Default LAN recommendation: interval 5s, timeout 15s.
- WAN/high-latency links: increase timeout to reduce false disconnects.

## Known Performance Limitations

- Hot-list workloads on a single key serialize per shard.
- No data partitioning across nodes in v1.0; each node stores full keyspace.
- Bootstrap under very high write rates can require larger queue depth and retries.

## Repro Commands

```bash
go test -bench=. -benchmem -benchtime=3s -count=1 ./...
redis-benchmark -h 127.0.0.1 -p 6379 -t set,get -n 200000 -c 50 -q
redis-benchmark -h 127.0.0.1 -p 6379 -t set,get -n 200000 -c 100 -P 16 -q
redis-benchmark -h 127.0.0.1 -p 6379 -t set,get -n 100000 -c 50 -d 10240 -q
```


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.

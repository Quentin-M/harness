# Engineering

When rules conflict, the higher one wins:

    1 verifiable      every behavior proven by an automated e2e/integration
                      test; every dependency failure injectable
    2 correct
    3 fast by design  structures and budgets chosen before code,
                      benchmarks prove them
    4 simple          smallest design that meets 1-3

## 1. Design for verification

| Rule | How |
|---|---|
| Pure core, I/O at the edges | logic takes values, returns values/events; adapters own sockets, disks, clocks |
| Inject every nondeterminism | clock, rng, ids, network, storage behind small interfaces |
| Real dependencies in tests | testcontainers: PG, Kafka, Scylla, etcd; fake only third parties |
| Fault hooks on every dependency | toxiproxy latency/drop/reset, kill -9, disk full, partition, slow consumer, clock skew |
| Deterministic replay | journal inputs; replay reproduces state and bugs |
| Assert behavior | a test must fail if the code under test returns zero values |
| Generative tests | property tests + fuzzing on codecs, parsers, state machines |
| Benchmarks are tests | committed, budget-asserted, compared to main with benchstat |
| Observable by default | p50/p99/p999, allocs, queue depth exported for the verifier |

## 2. Budget first (designer writes it, before code)

    path          p99   throughput  allocs/op  memory  access pattern
    order.match   5µs   1M/s        0          64MB    point + range by price

    faults        dependency × failure → expected behavior
    pg            conn reset mid-txn   → retry once, then reject, no partial write

## 3. Data structures (pick by access pattern)

| Access pattern | Use |
|---|---|
| dense integer keys | array index |
| point lookup | open-addressing hash map (swiss table), sharded by key under concurrency |
| ordered, range scans | B-tree with cache-line nodes; skip list when concurrent |
| min/max, scheduling | binary heap; timers on a hierarchical timing wheel |
| thread handoff 1→1 | SPSC ring buffer |
| N→1, N→M | MPSC / MPMC ring buffer |
| multi-stage pipeline | LMAX disruptor: one ring, sequence barriers |
| membership, dense | bitset, roaring bitmap |
| membership, sparse | bloom or cuckoo filter in front of the real lookup |
| counts, cardinality | count-min sketch, HyperLogLog |
| read-mostly shared state | copy-on-write snapshot, swapped atomically |
| flags, small enums | bitfields |

Every hot-path op is O(1) or O(log n), with capacity bounded by the budget.

## 4. Memory

- Stack over heap. Check escapes: `go build -gcflags=-m`.
- Preallocate from the budget. Hot path: 0 allocs per message.
- Reuse: object pools, free lists, per-batch arenas, recycled buffers.
  Zig: explicit allocators (FixedBuffer, Arena).
- Zero-copy: slice views, flyweight decoders, mmap, sendfile/splice.
- Layout: SoA for scans, AoS for per-entity access. Hot fields share one
  64B line. Fields written by different threads padded to 128B.

## 5. Concurrency

- One writer per piece of state. Pass ownership through queues.
- Shard by key first. Sharded locks where sharing is unavoidable.
- Pipelines: stage threads over bounded rings, natural batching,
  backpressure upstream.
- Atomics for counters, flags, sequences: acquire on read, release on
  publish, seq_cst only when proven necessary, fences around publication.
- Lock, atomic or lock-free: chosen by the contention in the budget.
- Latency-critical threads: pinned cores, NUMA-local memory, busy-spin.
- Go: fixed worker pools over ring buffers; no goroutine or channel per
  message on the hot path.

## 6. Compute & I/O

- Stream through short fixed buffers. Memory stays bounded by the buffer
  size, whatever the input size.
- Hot path makes 0 roundtrips: co-locate, cache in process, batch and pipeline the rest.
- Batch syscalls (io_uring, epoll, vectored I/O). The hot loop never
  calls syscalls, logs or allocates.
- SIMD over arrays; GPU for bulk parallel math.
- Predictable branches: sorted data, branchless selects, hot/cold split, PGO.
- Prices and money: fixed-point integers.

## 7. Wire

| Use | Format |
|---|---|
| hot path, internal | SBE: fixed layout, zero-copy decode |
| service contracts | Protobuf |
| humans, third parties | JSON, at the edge |

## 8. Cache & distribution

- In-process cache before any db/redis roundtrip: bounded, TTL, W-TinyLFU.
- Invalidate by event: distributed invalidation messages, versioned keys.
- Fan-out: Kafka when consumers need replay on start; Aeron or pod-to-pod
  gossip/pub-sub otherwise.

## 9. Storage

| Need | Use |
|---|---|
| db, queue, lock, notify | PostgreSQL: `SKIP LOCKED` queues, advisory locks, LISTEN/NOTIFY |
| coordination needing strong resiliency | etcd |
| large scale | ScyllaDB: one table per query, partition key from access pattern |

Schemas are append-only: insert versions and events; current state is a
projection.

PostgreSQL:

- HOT updates: updated columns stay unindexed; fillfactor 70-90 on
  update-heavy tables.
- Index per query: btree default, hash for equality, BRIN for append-only
  time order, GIN for jsonb/arrays/search; partial and `INCLUDE` for
  index-only scans.
- Time partitions, keyset pagination, prepared statements, COPY for bulk,
  pgbouncer.
- Every new or changed query ships its `EXPLAIN (ANALYZE, BUFFERS)`.

## 10. Languages

Go by default. Zig for the hottest paths and native libraries.

## 11. Gates (verifier blocks merge on any ✗)

    behavior  e2e + integration red→green; screenshots/video read back
    faults    every fault-matrix row passes
    perf      within budget; benchstat vs main shows no regression;
              allocs/op in budget; no seq scan on hot tables;
              HOT ratio holds (n_tup_hot_upd / n_tup_upd)
    simple    KISS, minimal diff, works standalone end to end
    scope     matches the brief; everything else filed as a follow-up
    leaks     no secrets or local-only detail in diff, PR text or media

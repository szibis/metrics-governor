# Phase 0: Profiling & Baseline Analysis

**Date**: 2026-02-15
**Platform**: darwin/arm64, Apple M3 Max, 14 cores
**Go Version**: 1.25.7

---

## 1. Benchmark Baseline Summary

### Pipeline End-to-End (the most representative hot path)

| Benchmark | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| BufferToExport 10dp | 273 | 15 | 0 |
| BufferToExport 100dp | 1,050 | 42 | 0 |
| BufferToExport 1000dp | 8,071 | 324 | 0 |
| EndToEnd 1k dps | 346 | 14 | 0 |
| EndToEnd 10k dps | 1,151 | 46 | 0 |
| EndToEnd 50k dps | 4,578 | 181 | 0 |

**Key finding**: The pipeline hot path shows **zero heap allocations** per operation in steady state. This means sync.Pool + vtprotobuf pooling is already highly effective.

### Compression Performance (100KB payloads)

| Algorithm | Compress ns/op | Compress MB/s | Decompress MB/s | Round-trip allocs |
|-----------|---------------:|--------------:|----------------:|------------------:|
| Snappy | 132,047 | 775 | 690 | 2 |
| Zstd | 241,104 | 425 | 134 | 21 |
| Gzip | 898,945 | 114 | 64 | 132 |
| Zlib | 1,072,111 | 96 | — | — |

**Pooled concurrent** (10KB, 14 goroutines):
| Algorithm | ns/op | MB/s |
|-----------|------:|-----:|
| Snappy | 3,201 | 3,199 |
| Zstd | 5,207 | 1,967 |
| Gzip | 26,290 | 390 |

### Protobuf (PRW WriteRequest)

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| Marshal | 20,282 | 37,088 | 601 |
| Unmarshal | 34,402 | 56,032 | 408 |

### Processing Engine

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| SampleHead | 6,407 | 1,616 | 151 |
| Downsample_Avg | 114,605 | 92,280 | 2,315 |
| Aggregate_SmallGroups | 42,187 | 14,497 | 801 |
| Aggregate_LargeGroups | 5,030,560 | 2,097,093 | 90,338 |
| Transform_RegexExtract | 164,887 | 37,240 | 1,151 |
| Drop | 34,253 | 5,216 | 151 |
| HighCardinality | 22,490,283 | 3,462,182 | 305,429 |

### Queue Performance

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| FastQueue Push (1KB) | 84 | 48 | 1 |
| FastQueue ConcurrentPush | 1,369 | 1,236 | 3 |
| FastQueue ConcurrentPushPop | 1,034 | 1,072 | 2 |
| FastQueue DiskRecovery | 10,238,487 | 272,539 | 124 |

### String Interning

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| PoolIntern (hit) | 15.08 | 0 | 0 |
| PoolInternBytes (hit) | 12.81 | 0 | 0 |
| PoolInternMiss | 25.87 | 3 | 0 |
| Realistic_WithInterning | 17,701 | 16,384 | 1 |
| Realistic_WithoutInterning | 16,920 | 33,984 | 1,001 |

### Circuit Breaker / Exporter

| Operation | ns/op | B/op | allocs/op |
|-----------|------:|-----:|----------:|
| CB_Allow_Closed | 1.760 | 0 | 0 |
| CB_Allow_Open | 31.27 | 0 | 0 |
| CB_Allow_Parallel | 0.187 | 0 | 0 |
| CB_RecordSuccess | 1.171 | 0 | 0 |
| Export_Throughput_Parallel | 79.00 | 0 | 0 |

---

## 2. CPU Profile Analysis

### Compression CPU Profile (500s sample, 801s total CPU)

| Category | Flat Time | % of Total | Function |
|----------|----------:|-----------:|----------|
| **Memory management** | 121.77s | **15.2%** | `runtime.madvise` |
| **Goroutine scheduling** | 78.05s | **9.8%** | `runtime.pthread_cond_signal` |
| **Deflate compression** | 48.13s | **6.0%** | `compress/flate.(*compressor).deflate` |
| **Thread scheduling** | 46.82s | **5.9%** | `runtime.pthread_cond_wait` |
| **I/O polling** | 43.40s | **5.4%** | `runtime.kevent` |
| **Zstd encoding** | 41.89s | **5.2%** | `zstd.(*doubleFastEncoder).EncodeNoHist` |
| **Thread sleep** | 36.18s | **4.5%** | `runtime.usleep` |
| **Deflate findMatch** | 34.72s | **4.3%** | `compress/flate.(*compressor).findMatch` |
| **Zstd hashing** | 26.51s | **3.3%** | `zstd/internal/xxhash.writeBlocks` |
| **Memory copy** | 23.30s | **2.9%** | `runtime.memmove` |

**Compression breakdown**:
- Deflate/gzip: ~22% of CPU (flate.deflate + findMatch + reset + huffman)
- Zstd: ~15% of CPU (EncodeNoHist + xxhash + blockEnc.encode)
- Snappy/S2: ~2.5% of CPU (encodeBlockSnappyGo64K)
- Runtime overhead (madvise + scheduling): ~37%

### Exporter CPU Profile (375s sample, 517s total CPU)

| Category | Flat Time | % of Total | Function |
|----------|----------:|-----------:|----------|
| **Syscalls (I/O)** | 143.84s | **27.8%** | `syscall.syscall` |
| **Goroutine scheduling** | 55.18s | **10.7%** | `runtime.pthread_cond_signal` |
| **Thread sleep** | 38.38s | **7.4%** | `runtime.usleep` |
| **Thread wait** | 33.77s | **6.5%** | `runtime.pthread_cond_wait` |
| **I/O polling** | 27.02s | **5.2%** | `runtime.kevent` |
| **Memory management** | 16.65s | **3.2%** | `runtime.madvise` |
| **Atomic loads** | 14.59s | **2.8%** | `sync/atomic.(*Int32).Load` |
| **Atomic adds** | 14.03s | **2.7%** | `sync/atomic.(*Int64).Add` |
| **GC scanning** | 10.37s | **2.0%** | `runtime.scanobject` |
| **String ops** | 8.44s | **1.6%** | `strings.ToLower` |

**Export path breakdown**:
- I/O (syscalls + writes): **~28%**
- Go runtime scheduling: **~25%** (cond_signal + usleep + cond_wait + kevent)
- GC: **~5%** (scanobject + findObject + typePointers)
- Actual export logic: **~8%** (legacyExport + preparerLoop)
- String operations: **~3%** (ToLower + Index)

---

## 3. Memory Allocation Profile Analysis

### Exporter Top Allocators (by object count)

| Rank | Function | Objects | % | Category |
|------|----------|--------:|--:|----------|
| 1 | `makeResilienceRequest` | 6,592,171 | **60.9%** | Test harness |
| 2 | `reflect.copyVal` | 589,832 | 5.5% | JSON encoding |
| 3 | `fmt.Sprintf` | 425,990 | 3.9% | Logging/formatting |
| 4 | `logging.(*Logger).log` | 294,925 | 2.7% | Structured logging |
| 5 | `logging.F` | 258,545 | 2.4% | Log field creation |
| 6 | `logging.callerComponent` | 211,932 | 2.0% | Caller info |
| 7 | `json.Marshal` | 207,935 | 1.9% | JSON serialization |
| 8 | `FastQueue.Push` | 196,615 | 1.8% | Queue operations |
| 9 | `SendQueue.Pop` | 176,959 | 1.6% | Queue operations |

**Key insight**: 60.9% of allocations come from `makeResilienceRequest` (test fixture), not production code. In production, the top allocators are **logging** (~8%) and **JSON marshaling** (~4%).

### Pipeline Top Allocators (by object count)

| Rank | Function | Objects | % | Category |
|------|----------|--------:|--:|----------|
| 1 | `stats.processFull` | 54,791,867 | **37.9%** | Stats collection |
| 2 | `strings.Builder.grow` | 26,695,608 | **18.4%** | String building |
| 3 | `logging.F` | 13,091,094 | 9.0% | Log fields |
| 4 | `stats.NewCollector` | 9,860,711 | 6.8% | Collector init |
| 5 | `stats.Degrade` | 7,340,143 | 5.1% | Stats degradation |
| 6 | `reflect.copyVal` | 7,045,227 | 4.9% | JSON encoding |

**Key insight**: Stats collection (`processFull`) and string building dominate allocations in the pipeline. The `processFull` path allocates heavily because it tracks per-metric label cardinality using maps.

---

## 4. Decision Gate

### Where CPU Time Actually Goes

| Category | Compression Profile | Exporter Profile | Weighted Estimate |
|----------|--------------------:|------------------:|------------------:|
| **Compression algorithms** | ~40% | 0% | ~15% |
| **Syscalls / I/O** | 0% | **~28%** | ~15% |
| **Go runtime (scheduling)** | ~25% | ~25% | ~25% |
| **Go runtime (GC)** | ~15% (madvise) | ~5% | ~10% |
| **Go runtime (memory mgmt)** | ~5% | ~3% | ~4% |
| **Application logic** | ~12% | ~8% | ~10% |
| **Protobuf (encode/decode)** | 0% | ~3% (UnmarshalVT) | ~5% |
| **String operations** | 0% | ~3% | ~3% |
| **Regex** | 0% | 0% | <1% |

### Decision Matrix

| CPU Profile Shows | Threshold | Actual | Recommendation |
|-------------------|-----------|--------|----------------|
| >30% in protobuf | 30% | **~5%** | NOT Path B (protobuf not bottleneck) |
| >20% in compression | 20% | **~15%** | BORDERLINE - compression is significant but not dominant |
| >20% in GC | 20% | **~10-15%** | Path A: PGO + allocation reduction |
| >40% in syscalls | 40% | **~15-28%** | I/O is significant in export path |
| >15% in regex | 15% | **<1%** | NOT relevant |

### Primary Recommendation: **Path A (PGO + Allocation Reduction)**

**Rationale**:

1. **The pipeline hot path already has ZERO allocations per datapoint**. The sync.Pool + vtprotobuf optimizations have already eliminated allocations from the critical path.

2. **Runtime overhead dominates**: ~25% of CPU goes to goroutine scheduling (`pthread_cond_signal/wait`) and ~10-15% to memory management (`madvise`, GC scanning). PGO can reduce scheduling overhead by 2-7% through better branch prediction and inlining decisions.

3. **Compression is the single largest CPU consumer** at ~40% in the compression profile, but this is expected for a metrics proxy. The algorithms used (klauspost/compress zstd, s2 snappy) are already highly optimized Go implementations. A Rust FFI for compression would save at most the runtime overhead portion (~25% of compression time), not the algorithmic work.

4. **I/O bound in the export path**: 28% of exporter CPU is in `syscall.syscall` — this is network I/O and cannot be optimized by changing languages.

5. **Stats collection (`processFull`) is the allocation hotspot** for the pipeline, accounting for 37.9% of objects. This is a good candidate for allocation reduction (pre-sized maps, reusable buffers).

### Secondary Opportunities

| Opportunity | Impact | Effort | Priority |
|-------------|--------|--------|----------|
| **Implement Go PGO** | 2-7% throughput | Low (generate profiles, rebuild) | HIGH |
| **Reduce stats.processFull allocations** | ~5% memory pressure reduction | Medium | HIGH |
| **Reduce logging allocations** | ~3% allocation reduction | Low (avoid F() in hot path) | MEDIUM |
| **Replace fmt.Sprintf in hot paths** | ~1% | Low | LOW |
| **Rust FFI for compression** | ~5-10% on compression-heavy workloads | Very High | LOW |
| **Rust FFI for protobuf** | <5% | Very High | NOT RECOMMENDED |

### What NOT to Optimize

1. **Protobuf decoding**: vtprotobuf + sync.Pool already achieves near-optimal performance. Prost (Rust) is actually slower than vtprotobuf by default and requires heavy optimization to match.

2. **Circuit breaker / export path logic**: 0.187 ns/op parallel, 1.76 ns/op single-threaded — these are already at the theoretical minimum (single atomic load).

3. **String interning**: 12.8-15 ns/op for hits, 25.9 ns for misses. Already highly efficient with sync.Map.

4. **FastQueue in-memory path**: 84 ns/op for push with only 1 allocation. Already optimal.

---

## 5. Files Generated

### Benchmark Baselines
- `test/compare/results/baseline-all.txt` — Full suite (all packages)
- `test/compare/results/baseline-hotpath.txt` — Hot path specific
- `test/compare/results/baseline-compression.txt` — Compression algorithms
- `test/compare/results/baseline-proto.txt` — Protobuf vtprotobuf
- `test/compare/results/baseline-contention.txt` — Concurrent access
- `test/compare/results/baseline-processing.txt` — Processing engine
- `test/compare/results/baseline-intern.txt` — String interning
- `test/compare/results/baseline-pipeline.txt` — Pipeline end-to-end

### CPU Profiles
- `test/compare/results/exporter-cpu.prof` — Export path CPU
- `test/compare/results/compression-cpu.prof` — Compression CPU

### Memory Profiles
- `test/compare/results/exporter-mem.prof` — Export path allocations
- `test/compare/results/pipeline-mem.prof` — Pipeline allocations

### Interactive Analysis
```bash
# View CPU flame graph
go tool pprof -http=:8080 test/compare/results/compression-cpu.prof

# View allocation flame graph
go tool pprof -http=:8080 test/compare/results/pipeline-mem.prof
```

---

## 6. Known Issues Found

1. **BenchmarkProcess_ConfigReload** panics with nil pointer dereference in `ruleactivity.(*Activity).RecordMatch` — the benchmark doesn't properly initialize the Activity field during config reload. This is a test bug, not a production bug.

2. **processing-cpu.prof** is empty because the sampling benchmarks crash before the profile is written. The processing CPU profile needs to be generated separately excluding `BenchmarkProcess_ConfigReload`.

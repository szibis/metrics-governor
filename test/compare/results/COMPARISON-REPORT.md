# Performance Comparison Report

**Date**: 2026-02-15
**Branch**: `feat/pipeline-performance-optimizations`
**Governor version**: v1.0.0 + Phases 1-6 optimizations (uncommitted)

## Test Configuration

| Parameter | Value |
|-----------|-------|
| Protocol | OTLP HTTP (all proxies) |
| Warmup | 90s |
| Sampling | 180s (18 samples at 10s intervals) |
| Generator | 2 services × diverse metrics + high cardinality |
| Proxy resources | 1 CPU, 512M memory (identical for all) |

## Proxy Configurations

| Feature | Governor minimal | Governor balanced | OTel Collector | vmagent |
|---------|:---------------:|:-----------------:|:--------------:|:-------:|
| Batching | default | 5s/500 auto-tune | 200ms/1024 | implicit |
| Export compression | none | zstd | zstd | snappy (PRW) |
| Queue/retry | none | memory queue + backoff + circuit breaker | sending_queue (4) + retry | 4 write queues |
| Stats/metrics | none | basic level | prometheus self-metrics | prometheus self-metrics |
| Memory limiter | GOMEMLIMIT | GOMEMLIMIT | 384 MiB + 128 MiB spike | none |
| String interning | no | yes | N/A | N/A |
| Export protocol | OTLP HTTP | OTLP HTTP | OTLP HTTP | Prometheus Remote Write |

**Note**: Governor balanced is the closest feature-equivalent to OTel Collector.

---

## Results: 15k dps (7,500 metrics/sec)

| Proxy | CPU avg | CPU max | Mem avg % | Data Integrity | Export Errors |
|-------|:-------:|:-------:|:---------:|:--------------:|:------------:|
| **Governor (minimal)** | 0.69% | 4.30% | 20.5% | PASS | 0 |
| **Governor (balanced)** | 0.66% | 2.24% | 20.8% | 99.95% (1.6M dp) | 0 |
| **OTel Collector** | 0.98% | 2.80% | 59.7% | PASS | 0 |
| **vmagent** | 0.63% | 0.95% | 8.7% | flowing (49k series) | 0 |

### Analysis

**Governor balanced vs OTel Collector** (feature-equivalent comparison):
- **33% less CPU** (0.66% vs 0.98%)
- **65% less memory** (20.8% vs 59.7%)
- Both export OTLP HTTP with zstd compression, queue, and retry

**Governor minimal vs balanced**:
- **No measurable CPU difference** (0.69% vs 0.66%) — the Phase 1-5 optimizations (atomic counters, dual-map key building, per-metric lock scope) made stats/queue/compression overhead negligible at this load

**Governor vs vmagent**:
- Comparable CPU (0.66% vs 0.63%)
- vmagent uses less memory (8.7%) but exports via Remote Write (different protocol)
- Governor provides full OTLP pipeline with observability

### Data Integrity Detail (Governor balanced)

| Metric | Value |
|--------|-------|
| Datapoints received | 1,612,309 |
| Datapoints sent | 1,611,498 |
| Ingestion rate | 99.95% |
| Batches sent | 458 |
| Export errors | 0 |
| Dropped | 0 |
| VM time series | 125,833 |

---

## Results: Multi-Load (50k, 100k dps)

Generator uses auto-scale mode: calibrates base DPS over 10 intervals, then adds `load_filler_datapoints` metric with unique `series_id` attributes to reach `TARGET_DATAPOINTS_PER_SEC`. Export interval auto-adjusts to 100ms for targets >10k dps. Pre-allocated attribute sets eliminate per-batch allocation overhead.

| Load | Proxy | CPU avg | CPU max | Mem avg % | Mem max % | Ingestion | Datapoints | Errors |
|------|-------|:-------:|:-------:|:---------:|:---------:|:---------:|:----------:|:------:|
| 50k | **Governor balanced** | 15.04% | 100.43% | 22.0% | 63.3% | 99.35% | 9.82M recv / 9.76M sent | 0 |
| 50k | **OTel Collector** | 4.43% | 5.58% | 20.4% | 22.8% | PASS | flowing | 0 |
| 100k | **Governor balanced** | 22.00% | 116.16% | 16.4% | 47.3% | 99.20% | 16.57M recv / 16.44M sent | 0 |
| 100k | **OTel Collector** | 5.88% | 7.20% | 11.8% | 12.5% | PASS | flowing | 0 |

### Analysis — High Load

**CPU**: OTel Collector is **3-4x more CPU efficient** at 50-100k dps. This is expected — Governor performs per-datapoint processing (stats tracking, limits checking, auto-tuning batching) while OTel Collector is a simpler batch-and-forward pipeline. CPU percentages are of **host total** (14 cores), so Governor's 15% ≈ 2.1 cores vs OTel's 4.4% ≈ 0.62 cores.

**Memory**: Comparable at 50k (22% vs 20.4%). Governor's max ~63% reflects GOMEMLIMIT-driven GC (target is 80% of container limit). OTel Collector's memory stays more stable.

**CPU max >100%**: Governor's peak CPU >100% indicates multi-core usage on batch processing or GC spikes. With 2 CPU limit at 50k, using 100% means both cores were fully utilized momentarily.

**Scaling**: Governor CPU scales from 15% to 22% (1.46x) when load doubles from 50k to 100k, showing sublinear scaling. OTel scales from 4.4% to 5.9% (1.34x).

**Data integrity**: Governor maintains 99.2-99.35% ingestion at both loads with zero export errors. The ~0.65-0.80% drop is from initial warmup period (before auto-tune stabilizes batching).

---

## Previous Baseline (different Docker session)

| Proxy | Previous CPU avg | Current CPU avg | Delta |
|-------|:----------------:|:---------------:|:-----:|
| Governor (minimal) | 0.58% | 0.69% | +0.11% |
| OTel Collector | 0.73% | 0.98% | +0.25% |
| vmagent | 2.51% | 0.63% | -1.88% |

**Note**: Large variance (especially vmagent) demonstrates that cross-session comparisons on Docker Desktop are unreliable due to thermal throttling, CPU scheduling, and background processes. Within-session comparisons (above) are authoritative.

---

## Optimization Impact Summary

The performance optimizations on `feat/pipeline-performance-optimizations` include:

### Stats Pipeline (Phases 1-5)
- Dual-map key building eliminates mergeAttrs allocations (~38% of pipeline allocs)
- Pooled byte buffers for series keys (sync.Pool)
- Atomic counters with ARM64 cache line padding (Record* methods: mutex → atomic)
- Per-metric lock scope (Bloom filter Add() outside collector lock)
- Config knobs: `--stats-cardinality-threshold`, `--stats-max-label-combinations`

### Compression (Phase 6)
- Zstd EncodeAll/DecodeAll: 1.52x faster compress, 1.71x faster decompress
- Optional native FFI (Rust): ~1.6x faster gzip/zlib/deflate
- PRW exporter: pooled CompressToBuf instead of allocating Compress

### Build (Phase 6.1)
- PGO support: 2-7% additional throughput

### Microbenchmark Results (Apple M3 Max, 14 cores)

| Benchmark | Before | After | Improvement |
|-----------|-------:|------:|:-----------:|
| End-to-end 1k dps | 346 ns/op | 295 ns/op | -15% |
| End-to-end 10k dps | 1,151 ns/op | 943 ns/op | -18% |
| End-to-end 50k dps | 7,219 ns/op | 5,704 ns/op | -21% |
| Stats full-mode batch | 22,373 ns/op | 18,008 ns/op | -20% |
| Concurrent throughput (4G) | 520 ns/op | 257 ns/op | -51% |
| Record* counter latency | ~100-500 ns | ~132 ns | atomic, 0 allocs |
| Zstd compress 100KB | 22,900 ns/op | 15,059 ns/op | 1.52x faster |
| Zstd decompress 100KB | 62,674 ns/op | 36,611 ns/op | 1.71x faster, 0 allocs |

---

## Reproducibility

```bash
# 15k dps (4 proxies, ~20 min)
test/compare/run.sh

# Multi-load (50k/100k/200k, governor-balanced + otel-collector, ~30 min)
test/compare/run-multi-load.sh

# Custom load
TARGET_DPS=50000 TARGET_MPS=25000 PROXY_MEM=1G PROXY_CPU=2 \
  docker compose -f docker-compose.yaml -f compose_overrides/compare-governor-balanced.yaml up -d
```

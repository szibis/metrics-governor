---
title: "Configuration Profiles"
sidebar_position: 3
description: "Pre-built profiles for common deployment patterns"
---

## Table of Contents

- [Quick Start](#quick-start)
- [Profiles](#profiles)
  - [minimal — Development & Testing](#minimal--development--testing)
  - [balanced — Production Default](#balanced--production-default)
  - [safety — Maximum Data Safety + Cost Efficiency](#safety--maximum-data-safety--cost-efficiency)
  - [observable — Full Observability + Cost Efficiency](#observable--full-observability--cost-efficiency)
  - [resilient — Durable + Fast](#resilient--durable--fast)
  - [performance — High Throughput](#performance--high-throughput)
- [Cost Efficiency](#cost-efficiency)
- [Overriding Profile Values](#overriding-profile-values)
- [Introspection](#introspection)
- [Choosing a Profile](#choosing-a-profile)
- [Profile Comparison — Full Parameter Table](#profile-comparison--full-parameter-table)
- [Resource Sizing at Reference Workload](#resource-sizing-at-reference-workload)
- [Consolidated Parameters](#consolidated-parameters)
- [Migration from Manual Config](#migration-from-manual-config)

---

## Quick Start

metrics-governor ships with six built-in profiles that bundle sensible defaults for common deployment scenarios.

```yaml
exporter:
  endpoint: "otel-collector:4317"
# That's it — balanced profile is applied automatically.
```

## Profiles

### `minimal` — Development & Testing

**Resource target:** 0.25–0.5 CPU, 64–256 MB RAM, no disk
**Max throughput:** ~15k dps

Best for: local development, CI testing, non-critical metrics, sidecar mode.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | Off | No persistence overhead |
| Adaptive workers | Off | Single worker, no pool |
| Batch auto-tune | Off | Fixed small batches |
| Pipeline split | Off | Single path |
| String interning | Off | Saves interning table memory |
| Compression | None | Saves CPU |
| Circuit breaker | Off | Direct export, fail fast |
| Stats | None | Minimal overhead |

**Prerequisites:** None

### `balanced` — Production Default

**Resource target:** 1–2 CPU, 64–256 MB RAM, no disk required
**Max throughput:** ~150k dps

Best for: production infrastructure monitoring, medium cardinality. The default profile — optimized for the best balance of performance and memory efficiency.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Memory** (in-memory only) | Fast, no disk overhead — lowest memory footprint |
| Adaptive workers | **On** | Self-tunes worker count |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | Off | Not needed at this scale |
| String interning | On | Reduces GC pressure |
| Compression | Snappy | Fast, low CPU |
| Circuit breaker | On (5 failures) | Protect downstream |
| Stats | Basic | Per-metric counts |
| Bloom persistence | Off | Memory queue — no disk backend |

**Memory optimization** (v1.0.3): The balanced profile uses reduced memory allocation percentages (buffer 7%, queue 5%) and a smaller buffer pre-allocation (2000) compared to other profiles. GOGC=100 halves GC headroom while Green Tea GC (`GOEXPERIMENT=greenteagc`) compensates for frequency. Result: **48% lower memory** at 50k dps (37.5%→19.5%) with only +0.19pp CPU increase.

**Measured performance** (4-core, 1 GB container, OTLP gRPC→HTTP):

| Load | CPU avg | Memory avg | Ingestion |
|------|---------|-----------|-----------|
| 50k dps | 4.51% | 19.5% | 99.25% |
| 100k dps | 6.47% | 18.4% | 99.53% |

**Prerequisites:**
- Recommended: 256+ MB memory for adaptive tuning overhead

### `safety` — Maximum Data Safety + Cost Efficiency

**Resource target:** 1–2 CPU, 256–768 MB RAM, **disk required** (8 GB PVC)
**Max throughput:** ~120k dps | **Cost efficiency:** High

Best for: regulated environments, financial metrics, data you cannot afford to lose, cost optimization.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Disk** (pure disk mode) | Full crash recovery — every batch persisted |
| Adaptive workers | **On** | Self-tunes worker count |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | Off | Simplicity over throughput |
| String interning | On | Reduces GC pressure |
| Compression | **Zstd** export, snappy queue | Best ratio = less bandwidth cost |
| Circuit breaker | On (5 failures) | Protect downstream |
| Stats | **Full** | Per-metric cardinality tracking for cost visibility |
| Bloom persistence | **On** | Survive restarts without re-learning |

**Prerequisites:**
- **Required:** PersistentVolumeClaim for disk queue (8+ GB recommended)
- Recommended: 768+ MB memory for full stats + disk cache

**Cost efficiency:** Full per-metric stats reveal your most expensive metrics. Zstd compression reduces egress bandwidth by 60–80%. Disk persistence ensures no duplicate sends to paid backends after crashes. Spending more on proxy resources typically saves 10–100x in backend SaaS costs.

### `observable` — Full Observability + Cost Efficiency

**Resource target:** 1–1.75 CPU, 256–640 MB RAM, **disk required** (8 GB PVC)
**Max throughput:** ~100k dps | **Cost efficiency:** High

Best for: teams that need per-metric cost tracking with hybrid queue durability.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Hybrid** (memory L1 + disk spillover) | Memory-speed normally, disk safety for spikes |
| Adaptive workers | **On** | Self-tunes worker count |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | Off | Not needed at this scale |
| String interning | On | Reduces GC pressure |
| Compression | **Zstd** export, snappy queue | Bandwidth savings |
| Circuit breaker | On (5 failures) | Protect downstream |
| Stats | **Full** | Per-metric cardinality tracking finds cost outliers |
| Bloom persistence | **On** | Survive restarts without re-learning |

**Prerequisites:**
- **Required:** PersistentVolumeClaim for hybrid queue spillover (8+ GB recommended)
- Recommended: 512+ MB memory for full stats

**Cost efficiency:** Full per-metric stats reveal exactly where storage costs come from. Hybrid queue avoids duplicate sends to paid backends during spikes. Zstd reduces network egress. Reject policy ensures no silent drops that trigger re-sends.

### `resilient` — Durable + Fast

**Resource target:** 0.5–1 CPU, 192–512 MB RAM, **disk required** (12 GB PVC)
**Max throughput:** ~200k dps | **Cost efficiency:** Medium

Best for: production workloads that need disk spillover without the overhead of full stats.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Hybrid** (memory L1 + disk spillover) | Memory-speed normally, disk safety for spikes |
| Adaptive workers | **On** | Self-tunes worker count |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | Off | Simpler, more predictable |
| String interning | On | Reduces GC pressure |
| Compression | Snappy | Fast compression, low CPU |
| Circuit breaker | On (5 failures) | Protect downstream |
| Stats | Basic | Per-metric counts (lower overhead than full) |
| Bloom persistence | **On** | Survive restarts without re-learning |

**Prerequisites:**
- **Required:** PersistentVolumeClaim for hybrid queue spillover (12+ GB recommended)
- Recommended: 512+ MB memory

### `performance` — High Throughput

**Resource target:** 2–4 CPU, 256 MB–2 GB RAM, **disk required** (12 GB PVC)
**Max throughput:** ~500k+ dps | **Cost efficiency:** Low

Best for: IoT, high-volume telemetry, APM at scale.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Hybrid** (memory L1 + disk spillover) | Persistent, survives restarts |
| Adaptive workers | **On** | Dynamic scaling |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | **On** | Separates CPU/IO work |
| String interning | On | Reduces GC pressure |
| Compression | Zstd | Best ratio for large batches |
| Circuit breaker | On (3 failures) | Aggressive protection |
| Stats | Basic | Per-metric counts |
| Bloom persistence | **On** | Survive restarts without re-learning |

**Prerequisites:**
- **Required:** PersistentVolumeClaim for disk queue
- **Required:** At least 2 CPU cores for pipeline split
- Recommended: SSD/NVMe storage
- Recommended: 1+ GB memory

## Cost Efficiency

Profiles like `safety` and `observable` use more proxy resources (disk, CPU, memory) but can dramatically reduce backend costs:

| Feature | Cost Impact | How |
|---------|------------|-----|
| Full stats (`stats_level: full`) | See exactly which metrics/tenants cost most | Per-metric cardinality tracking reveals top-cost outliers |
| Zstd compression | Reduce egress bandwidth by 60–80% | Best compression ratio for metric payloads |
| Disk persistence | Prevent duplicate sends | Crash recovery avoids re-sending same data to paid backends |
| Limits enforcement | Cap cardinality growth | Stop runaway label values before they reach storage |
| Bloom filters | Track unique series efficiently | Memory-efficient cardinality estimation |

> **Rule of thumb:** A metrics-governor instance costing $5–20/month in compute can save $500–2000/month in backend SaaS costs (Datadog, Grafana Cloud, New Relic) by identifying and reducing expensive metrics before ingestion.

## Overriding Profile Values

Any parameter can be added alongside a profile — user values always win:

```yaml
profile: performance
parallelism: 4                  # override derived workers
queue:
  max_bytes: 4Gi               # override profile default
  circuit_breaker:
    threshold: 10              # override resilience preset
buffer:
  batch_size: 2000             # override profile default
exporter:
  compression: snappy          # override zstd with snappy
```

Resolution order: **Profile defaults → YAML overrides → CLI flags**

## Introspection

```bash
# See all values a profile sets
metrics-governor --show-profile balanced

# See the final merged config (profile + your overrides + auto-derivation)
metrics-governor --show-effective-config

# See deprecation status
metrics-governor --show-deprecations
```

## Choosing a Profile

| If you need... | Use |
|----------------|-----|
| Lowest resource usage | `minimal` |
| Production defaults with self-tuning | `balanced` |
| Maximum data safety + cost visibility | `safety` |
| Per-metric cost tracking + hybrid durability | `observable` |
| Disk spillover without full stats overhead | `resilient` |
| Maximum throughput with pipeline split | `performance` |
| Full manual control | Any profile + override everything |

## Profile Comparison — Full Parameter Table

| Parameter | `minimal` | `balanced` | `safety` | `observable` | `resilient` | `performance` |
|-----------|-----------|-----------|----------|-------------|------------|---------------|
| Workers | 1 | max(2, N/2) | max(2, N/2) | max(2, N/2) | max(2, N/2) | N × 2 |
| Pipeline split | off | off | off | off | off | **on** |
| Adaptive workers | off | **on** | **on** | **on** | **on** | **on** |
| Batch auto-tune | off | **on** | **on** | **on** | **on** | **on** |
| Buffer size | 1,000 | 2,000 | 5,000 | 5,000 | 5,000 | 50,000 |
| Batch size | 200 | 500 | 500 | 500 | 500 | 1,000 |
| Flush interval | 10s | 5s | 5s | 5s | 5s | 2s |
| Queue type | memory | memory | **disk** | disk | disk | disk |
| Queue mode | memory | memory | **disk** | **hybrid** | **hybrid** | **hybrid** |
| Queue enabled | false | true | true | true | true | true |
| Queue max size | 1,000 | 5,000 | 10,000 | 10,000 | 10,000 | 50,000 |
| Queue max bytes | 64 MB | 256 MB | **8 GB** | **8 GB** | **12 GB** | 2 GB |
| Export compression | none | snappy | **zstd** | **zstd** | snappy | zstd |
| Queue compression | none | snappy | snappy | snappy | snappy | snappy |
| String interning | off | on | on | on | on | on |
| Memory limit ratio | 0.90 | 0.80 | 0.80 | 0.80 | 0.82 | 0.80 |
| Circuit breaker | off | on (5) | on (5) | on (5) | on (5) | on (3) |
| Backoff | off | on (2.0x) | on (2.0x) | on (2.0x) | on (2.0x) | on (3.0x) |
| Cardinality mode | exact | bloom | bloom | bloom | bloom | hybrid |
| Buffer full policy | reject | reject | reject | reject | reject | drop_oldest |
| Limits dry-run | true | false | false | false | false | false |
| Request body limit | 4 MB | 16 MB | 16 MB | 16 MB | 16 MB | 64 MB |
| Bloom persistence | off | off | **on** | **on** | **on** | on |
| Stats level | none | basic | **full** | **full** | basic | basic |
| Warmup | off | on | on | on | on | on |
| Cost efficiency | — | Medium | **High** | **High** | Medium | Low |

## Resource Sizing at Reference Workload

Reference workload: **100,000 datapoints/sec**, **10,000 unique metric names**, **~10 avg cardinality per metric** (100k unique series).

| Profile | CPU (request) | CPU (limit) | Memory (request) | Memory (limit) | Disk (PVC) | Max throughput |
|---------|-------------|-------------|-----------------|---------------|------------|---------------|
| `minimal` | 0.1 | 0.5 | 64 MB | 256 MB | — | ~15k dps |
| `balanced` | 0.5 | 1.0 | 64 MB | 256 MB | 2 GB | ~150k dps |
| `safety` | 0.75 | 2.0 | 256 MB | 768 MB | 8 GB | ~120k dps |
| `observable` | 0.5 | 1.75 | 256 MB | 640 MB | 8 GB | ~100k dps |
| `resilient` | 0.5 | 1.0 | 192 MB | 512 MB | 12 GB | ~200k dps |
| `performance` | 1.0 | 2.0 | 256 MB | 2 GB | 12 GB | ~500k+ dps |

*CPU request = 50–60% of limit (Burstable QoS). Memory request = actual working set. Memory limit = 2x working set headroom. Disk = 1 hour outage buffer at reference workload.*

Disk queue sizing for 1 hour outage buffer:
- **zstd** (~5x compression): 100k dps → ~1.5 MB/s → ~5.4 GB/hour → **8 GB** with headroom (safety, observable)
- **snappy** (~3x compression): 100k dps → ~2.5 MB/s → ~9 GB/hour → **12 GB** with headroom (resilient, performance)

## Consolidated Parameters

Profiles work alongside four consolidated parameters that replace groups of related settings:

### `--parallelism` (int, 0 = auto)

Derives all 6 worker-related counts from a single value:

```
base = parallelism (or NumCPU if 0)
QueueWorkers       = base × 2
ExportConcurrency  = base × 4
PreparerCount      = base
SenderCount        = base × 2
MaxConcurrentSends = max(2, base/2)
GlobalSendLimit    = base × 8
```

### `--memory-budget-percent` (float, 0.0–0.5)

Splits the memory budget evenly between buffer and queue:

```
BufferMemoryPercent = budget / 2
QueueMemoryPercent  = budget / 2
```

### `--export-timeout` (duration)

Derives the full timeout cascade from a single base value:

```
ExporterTimeout          = base       (30s)
QueueDirectExportTimeout = base / 6   (5s)
QueueRetryTimeout        = base / 3   (10s)
QueueDrainEntryTimeout   = base / 6   (5s)
QueueDrainTimeout        = base       (30s)
QueueCloseTimeout        = base × 2   (60s)
FlushTimeout             = base       (30s)
```

### `--resilience-level` (low/medium/high)

Sets backoff, circuit breaker, and drain parameters as a preset.

| Setting | low | medium | high |
|---------|-----|--------|------|
| Backoff multiplier | 1.5 | 2.0 | 3.0 |
| Circuit breaker | off | on (5 failures) | on (3 failures) |
| CB reset timeout | 60s | 30s | 15s |
| Batch drain size | 5 | 10 | 25 |
| Burst drain size | 50 | 100 | 250 |

See [DEPRECATIONS.md](https://github.com/szibis/metrics-governor/blob/main/DEPRECATIONS.md) for the full mapping from old to new parameters.

> **Operational deployment guidance** — For auto-derivation engine details, resilience auto-sensing (AIMD batch tuning, adaptive worker scaling, circuit breaker), HPA/VPA best practices, Deployment vs StatefulSet, DaemonSet mode, and bare metal deployment, see [Production Guide](/docs/operations/production-guide).

## Stability Settings Per Profile

Each profile includes tuned stability parameters that control how the proxy behaves under pressure:

| Setting | minimal | balanced | safety | observable | resilient | performance |
|---|---|---|---|---|---|---|
| GOGC | 100 | 100 | 200 | 200 | 200 | 400 |
| Load shedding threshold | 0.80 | 0.85 | 0.90 | 0.85 | 0.90 | 0.95 |
| Spillover threshold | — | 80% | — | 90% | 80% | 80% |
| Stats degradation | auto | auto | auto | auto | auto | auto |
| Meta sync interval | — | 1s | 1s | 5s | 3s | 2s |

**GOGC**: Controls GC frequency — higher values mean fewer GC cycles and less CPU spent on garbage collection, but more memory used between collections. This is safe because `GOMEMLIMIT` (auto-configured from container memory) provides a hard ceiling that prevents OOM kills regardless of GOGC. The `balanced` profile uses GOGC=100 (GC when heap doubles) combined with `greenteagc` to achieve ~48% lower memory than GOGC=200 with only +0.19pp CPU. The `performance` profile uses GOGC=400 (GC when heap grows 5x) for minimum GC overhead. Override with the `GOGC` environment variable.

**Load shedding**: When the pipeline health score exceeds this threshold, receivers return `ResourceExhausted` (gRPC) or `429 Too Many Requests` (HTTP). Higher-capacity profiles tolerate more pressure before shedding.

**Stats degradation**: Under memory pressure, stats automatically downgrade: `full` → `basic` → `none`. The core proxy function (receive → queue → export) is never affected.

See [stability-guide.md](/docs/advanced/stability-guide) for tuning details.

## Migration from Manual Config

If you already have a config file with manual tuning:

1. Set `profile: balanced` (or whichever matches your use case — consider `safety` or `observable` for cost optimization)
2. Remove params that match profile defaults (use `--show-profile` to check)
3. Keep only your intentional overrides
4. Use `--show-effective-config` to verify the final result

**Example migration:**

```yaml
# Before: 30+ manual parameters
exporter:
  endpoint: "otel-collector:4317"
  timeout: 30s
  compression:
    type: snappy
buffer:
  size: 5000
  batch_size: 500
  flush_interval: 5s
exporter:
  queue:
    enabled: true
    workers: 8
    max_size: 5000
    backoff:
      enabled: true
      multiplier: 2.0
    circuit_breaker:
      enabled: true
      threshold: 5

# After: profile + 1 override
profile: balanced
exporter:
  endpoint: "otel-collector:4317"
# Everything else matches the balanced profile defaults
```

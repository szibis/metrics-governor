# Configuration Profiles

## Table of Contents

- [Quick Start](#quick-start)
- [Profiles](#profiles)
  - [minimal — Development & Testing](#minimal--development--testing)
  - [balanced — Production Default](#balanced--production-default)
  - [performance — High Throughput](#performance--high-throughput)
- [Overriding Profile Values](#overriding-profile-values)
- [Introspection](#introspection)
- [Choosing a Profile](#choosing-a-profile)
- [Profile Comparison — Full Parameter Table](#profile-comparison--full-parameter-table)
- [Consolidated Parameters](#consolidated-parameters)
- [Migration from Manual Config](#migration-from-manual-config)

---

## Quick Start

metrics-governor ships with three built-in profiles that bundle sensible defaults for common deployment scenarios.

```yaml
exporter:
  endpoint: "otel-collector:4317"
# That's it — balanced profile is applied automatically.
```

## Profiles

### `minimal` — Development & Testing

**Resource target:** 0.25–0.5 CPU, 128–256 MB RAM, no disk
**Max throughput:** ~10k dps

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

**Prerequisites:** None

### `balanced` — Production Default

**Resource target:** 1–2 CPU, 256 MB–1 GB RAM, no disk required
**Max throughput:** ~100k dps

Best for: production infrastructure monitoring, medium cardinality.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | Memory | Absorbs spikes without disk I/O |
| Adaptive workers | **On** | Self-tunes worker count |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | Off | Not needed at this scale |
| String interning | On | Reduces GC pressure |
| Compression | Snappy | Fast, low CPU |
| Circuit breaker | On (5 failures) | Protect downstream |

**Prerequisites:**
- Recommended: 512+ MB memory for adaptive tuning overhead

### `performance` — High Throughput

**Resource target:** 4+ CPU, 1–4 GB RAM, **disk required**
**Max throughput:** ~500k+ dps

Best for: IoT, high-volume telemetry, APM at scale.

| Feature | Status | Why |
|---------|--------|-----|
| Queue | **Disk** | Persistent, survives restarts |
| Adaptive workers | **On** | Dynamic scaling |
| Batch auto-tune | **On** | AIMD batch sizing |
| Pipeline split | **On** | Separates CPU/IO work |
| String interning | On | Reduces GC pressure |
| Compression | Zstd | Best ratio for large batches |
| Circuit breaker | On (3 failures) | Aggressive protection |

**Prerequisites:**
- **Required:** PersistentVolumeClaim for disk queue
- **Required:** At least 2 CPU cores for pipeline split
- Recommended: SSD/NVMe storage
- Recommended: 1+ GB memory

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
| Maximum throughput with persistence | `performance` |
| Full manual control | Any profile + override everything |

## Profile Comparison — Full Parameter Table

| Parameter | `minimal` | `balanced` | `performance` |
|-----------|-----------|-----------|---------------|
| Workers | 1 | max(2, NumCPU/2) | NumCPU × 2 |
| Pipeline split | off | off | **on** |
| Adaptive workers | off | **on** | **on** |
| Batch auto-tune | off | **on** | **on** |
| Buffer size | 1,000 | 5,000 | 50,000 |
| Batch size | 200 | 500 | 1,000 |
| Flush interval | 10s | 5s | 2s |
| Queue type | memory | memory | **disk** |
| Queue enabled | false | true | true |
| Queue max size | 1,000 | 5,000 | 50,000 |
| Queue max bytes | 64 MB | 256 MB | 2 GB |
| Compression | none | snappy | zstd |
| String interning | off | on | on |
| Memory limit ratio | 0.90 | 0.85 | 0.80 |
| Circuit breaker | off | on (5) | on (3) |
| Backoff | off | on (2.0x) | on (3.0x) |
| Cardinality mode | exact | bloom | hybrid |
| Buffer full policy | reject | reject | drop_oldest |
| Limits dry-run | true | false | false |
| Request body limit | 4 MB | 16 MB | 64 MB |
| Bloom persistence | off | off | on |
| Warmup | off | on | on |

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

See [DEPRECATIONS.md](../DEPRECATIONS.md) for the full mapping from old to new parameters.

> **Operational deployment guidance** — For auto-derivation engine details, resilience auto-sensing (AIMD batch tuning, adaptive worker scaling, circuit breaker), HPA/VPA best practices, Deployment vs StatefulSet, DaemonSet mode, and bare metal deployment, see [Production Guide](production-guide.md).

## Migration from Manual Config

If you already have a config file with manual tuning:

1. Set `profile: balanced` (or whichever matches your use case)
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

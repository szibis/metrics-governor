# Stability Tuning Guide

This guide covers the stability mechanisms built into metrics-governor and how to tune them for your workload.

## Overview

Metrics-governor includes four layers of stability protection:

1. **Graduated spillover** — memory queue overflow to disk with hysteresis
2. **Load shedding** — receiver-level request rejection under pipeline pressure
3. **Stats degradation** — automatic stats level reduction under memory pressure
4. **GOGC tuning** — per-profile GC aggressiveness for memory control

These mechanisms work together to keep the proxy functioning under overload conditions. The core data path (receive, queue, export) is always preserved — only observability overhead is shed.

## Spillover Cascade Prevention

### How It Works

When the memory queue fills past the spillover threshold, batches overflow to disk. The graduated spillover system uses multiple thresholds with hysteresis to prevent oscillation:

| Queue Utilization | Behavior |
|---|---|
| < 80% (default) | Memory-only — normal operation |
| 80-90% | 50% of batches spill to disk (alternating) |
| 90-95% | All new batches go to disk |
| > 95% | Spill + load shedding at queue level |
| Recovery < 70% | Resume memory-only (hysteresis gap) |

The hysteresis gap (80% trigger, 70% recovery) prevents rapid mode switching that can cause CPU spikes.

### Tuning

| Parameter | Config Key | Default | Profiles Using |
|---|---|---|---|
| Spillover threshold | `queue.hybrid_spillover_pct` | 80% | observable, resilient, performance |
| Hysteresis recovery | — | 70% (fixed at threshold - 10%) | — |

**When to raise the threshold**: If you have ample memory and want to avoid disk I/O. Set to 90% if memory queue can handle temporary bursts.

**When to lower the threshold**: If disk I/O is fast (NVMe) and you want early spillover to prevent memory pressure. Set to 60-70%.

### Monitoring

- `metrics_governor_spillover_active` — gauge: 1 when spillover is active
- `metrics_governor_queue_memory_bytes` / `metrics_governor_queue_memory_max_bytes` — memory queue utilization
- `metrics_governor_queue_bytes` / `metrics_governor_queue_max_bytes` — disk queue utilization

## Load Shedding

### How It Works

Each receiver (gRPC, HTTP, Prometheus Remote Write) checks the pipeline health score before accepting a request. When the score exceeds the profile's threshold:

- **gRPC**: Returns `codes.ResourceExhausted` — standard OTLP retry signal
- **HTTP**: Returns `429 Too Many Requests` with `Retry-After: 5` header
- **PRW**: Returns `429 Too Many Requests` with `Retry-After: 5` header

The pipeline health score is a weighted composite:

| Component | Weight | Source |
|---|---|---|
| Queue pressure | 35% | Queue utilization ratio |
| Buffer pressure | 30% | Buffer utilization ratio |
| Export latency | 20% | EWMA export latency (normalized to 0-1) |
| Circuit breaker | 15% | 1.0 if open, 0.0 if closed |

### Per-Profile Thresholds

| Profile | Threshold | Rationale |
|---|---|---|
| minimal | 0.80 | Small buffer, no queue fallback |
| balanced | 0.85 | Memory-only queue, moderate buffer |
| observable | 0.85 | Hybrid queue but full stats overhead |
| safety | 0.90 | Full disk persistence, high tolerance |
| resilient | 0.90 | 12 GB queue buffer, high tolerance |
| performance | 0.95 | Maximum headroom, pipeline split |

### Client-Side Configuration

Upstream OTLP senders should use retry with exponential backoff:

```yaml
# OpenTelemetry Collector exporter config
exporters:
  otlp:
    endpoint: metrics-governor:4317
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 30s
      max_elapsed_time: 300s
```

### Monitoring

- `metrics_governor_receiver_load_shedding_total{protocol}` — counter by protocol (grpc/http/prw)
- `metrics_governor_pipeline_health_score` — gauge: 0.0 (healthy) to 1.0 (overloaded)

## Stats Degradation

### How It Works

Under memory pressure, the stats collector automatically downgrades its processing level:

```
full → basic → none
```

| Level | CPU Cost | Memory | What's Tracked |
|---|---|---|---|
| `full` | ~35% at 100k dps | ~50 MB | Cardinality (Bloom filter), per-metric stats, label stats |
| `basic` | ~5% at 100k dps | ~10 MB | Aggregate counters only |
| `none` | 0% | 0 MB | Nothing — all stats collection disabled |

The `Degrade()` method uses a lock-free atomic CAS loop, so it's safe to call from any goroutine without contention.

### What Happens at Each Level

- **full → basic**: Cardinality tracking stops. Bloom filter stops being updated. Per-metric and per-label counters freeze at their last values. Aggregate pipeline counters (datapoints received/sent) continue.
- **basic → none**: All stats collection stops. `Process()` becomes a no-op (zero CPU cost). The proxy continues forwarding data normally.

### Preservation Guarantees

- **Existing data is preserved** — degradation never clears accumulated stats
- **Configured level is remembered** — `ConfiguredLevel()` returns the original level
- **Data forwarding is unaffected** — the core receive → queue → export path is independent of stats

### Monitoring

- `metrics_governor_stats_level_current` — gauge: 2=full, 1=basic, 0=none
- `metrics_governor_stats_degradation_total` — counter of degradation events

## Per-Profile GOGC Tuning

### How GOGC and GOMEMLIMIT Work Together

The governor uses two Go runtime knobs for memory management:

- **GOMEMLIMIT** = hard memory ceiling (prevents OOM kills). Auto-configured from container memory via cgroups at 85% of the limit.
- **GOGC** = GC frequency within that ceiling (trades CPU for memory headroom). Higher GOGC = fewer GC cycles = less CPU.

When GOMEMLIMIT is set, GOGC becomes purely a CPU-vs-latency tradeoff. GOMEMLIMIT ensures memory never exceeds the container limit regardless of GOGC value.

| Profile | GOGC | GC Triggers When Heap... | CPU Impact |
|---|---|---|---|
| minimal | 100 | Doubles (2x) | Moderate — Go default, suitable for small containers |
| balanced | 200 | Triples (3x) | Low — ~45% less GC CPU than GOGC=50 |
| safety | 200 | Triples (3x) | Same as balanced |
| observable | 200 | Triples (3x) | Same as balanced |
| resilient | 200 | Triples (3x) | Same as balanced |
| performance | 400 | Grows 5x | Minimal — least GC CPU, most memory headroom |

**Why these values?** CPU profiling at 50k dps showed GC consumed 44.5% of CPU with GOGC=50. Since GOMEMLIMIT already prevents OOM, that GC frequency was pure waste. Raising GOGC to 200 cut governor CPU by ~45%.

### Override

Set the `GOGC` environment variable to override the profile default:

```bash
GOGC=300 ./metrics-governor --profile balanced
```

The environment variable takes precedence — the profile value is only used when `GOGC` is not set.

## Alerting

Three alerts cover stability mechanisms:

| Alert | Severity | Fires When |
|---|---|---|
| `SpilloverCascade` | warning | `spillover_active == 1` for 2m |
| `LoadSheddingActive` | warning | `load_shedding_total` rate > 0 for 2m |
| `StatsDegraded` | warning | stats level < configured for 5m |

See [alerting.md](alerting.md) for full runbooks.

## Troubleshooting

### Pipeline Won't Recover After Spike

**Symptoms**: Load shedding stays active after input drops. Queue depth stays high.

**Cause**: Export backend is slow or circuit breaker is open. The queue can't drain fast enough.

**Fix**: Check export latency EWMA and circuit breaker state. Fix the backend. The pipeline auto-recovers when the queue drains below the hysteresis threshold.

### Stats Degraded and Won't Recover

**Symptoms**: `stats_level_current` is 0 or 1 but memory pressure has dropped.

**Cause**: Stats degradation is currently one-way (it doesn't auto-upgrade). This is intentional — upgrading stats level while under load could trigger another memory spike.

**Fix**: Restart the process to restore full stats. Or accept the degraded level until the next restart.

### High CPU Despite Load Shedding

**Symptoms**: Load shedding is active but CPU is still high.

**Cause**: Stats processing (full level) is consuming CPU independent of data forwarding. Snappy/zstd compression on the disk queue also adds CPU.

**Fix**: If memory allows, let stats degrade automatically. Otherwise, configure `stats_level: basic` in the profile to reduce CPU overhead.

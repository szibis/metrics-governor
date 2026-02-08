# Production Guide

## Table of Contents

- [Production Readiness Checklist](#production-readiness-checklist)
- [Deployment Sizing](#deployment-sizing)
- [Cost Analysis](#cost-analysis)
  - [Infrastructure Cost per Tier](#infrastructure-cost-per-tier)
  - [Multi-Provider Ingestion Cost Comparison](#multi-provider-ingestion-cost-comparison)
  - [Feature-by-Feature Cost Savings](#feature-by-feature-cost-savings)
  - [Cross-AZ / Network Traffic Savings](#cross-az--network-traffic-savings)
  - [Worked ROI Examples](#worked-roi-examples)
- [Auto-Derivation Engine](#auto-derivation-engine)
  - [Detected Resources](#detected-resources)
  - [CPU-Based Derivations](#cpu-based-derivations)
  - [Memory-Based Derivations](#memory-based-derivations)
- [Memory & CPU Tuning](#memory--cpu-tuning)
  - [GOMEMLIMIT and Cgroups Auto-detection](#gomemlimit-and-cgroups-auto-detection)
  - [CPU Sizing Formula](#cpu-sizing-formula)
- [I/O Optimization](#io-optimization)
  - [Disk I/O (Persistent Queue)](#disk-io-persistent-queue)
  - [Network I/O (Compression)](#network-io-compression)
- [Limits Enforcer Tuning](#limits-enforcer-tuning)
  - [Stats Threshold](#stats-threshold)
  - [Rule Cache Sizing](#rule-cache-sizing)
  - [Cardinality Tracker Mode](#cardinality-tracker-mode)
  - [Hybrid Mode — Memory Savings & Auto-Switching](#hybrid-mode--memory-savings--auto-switching)
- [Export Pipeline Tuning](#export-pipeline-tuning)
  - [Batch Size Guidance](#batch-size-guidance)
  - [Worker Pool](#worker-pool)
- [Kubernetes Configuration](#kubernetes-configuration)
  - [Resources by Tier](#resources-by-tier)
  - [Health Probes](#health-probes)
  - [HPA & VPA — Autoscaling Best Practices](#hpa--vpa--autoscaling-best-practices)
  - [PodDisruptionBudget](#poddisruptionbudget)
  - [ConfigMap Reload](#configmap-reload)
  - [Storage Classes](#storage-classes)
  - [Deployment vs StatefulSet](#deployment-vs-statefulset)
  - [Traffic Distribution & Topology-Aware Routing](#traffic-distribution--topology-aware-routing)
  - [DaemonSet Mode](#daemonset-mode)
  - [Complete Helm Values Example (Medium Tier)](#complete-helm-values-example-medium-tier)
- [Bare Metal / VM Deployment](#bare-metal--vm-deployment)
  - [systemd Service](#systemd-service)
  - [Disk Sizing for Queue](#disk-sizing-for-queue)
  - [Config Reload on Bare Metal](#config-reload-on-bare-metal)
  - [Security Hardening](#security-hardening)
  - [Differences from Kubernetes](#differences-from-kubernetes)
- [Resilience Tuning & Auto-Sensing](#resilience-tuning--auto-sensing)
  - [Adaptive Batch Tuning (AIMD)](#adaptive-batch-tuning-aimd)
  - [Adaptive Worker Scaling (AIMD)](#adaptive-worker-scaling-aimd)
  - [Circuit Breaker with Auto-Reset](#circuit-breaker-with-auto-reset)
  - [Resilience Level Presets](#resilience-level-presets)
  - [Failover Queue](#failover-queue)
  - [How the Adaptive Systems Work Together](#how-the-adaptive-systems-work-together)
- [Prometheus Scrape Optimization](#prometheus-scrape-optimization)
- [Alerting Essentials](#alerting-essentials)
  - [1. Export Failures Sustained](#1-export-failures-sustained)
  - [2. Queue Near Full](#2-queue-near-full)
  - [3. High Memory Usage](#3-high-memory-usage)
  - [4. Readiness Probe Failing](#4-readiness-probe-failing)
  - [5. Cardinality Limit Breached](#5-cardinality-limit-breached)
- [Real-World Configuration Examples](#real-world-configuration-examples)
  - [Example 1: Small SaaS](#example-1-small-saas)
  - [Example 2: Mid-Size Platform](#example-2-mid-size-platform)
  - [Example 3: Large Enterprise](#example-3-large-enterprise)
- [Complete CLI Reference](#complete-cli-reference)

This guide consolidates all production tuning, sizing, cost analysis, and operational best practices for metrics-governor into a single reference. It covers everything from initial sizing to advanced pipeline tuning.

> **Other references**: [Resilience](resilience.md) (circuit breaker, backoff, failover queue), [Performance](performance.md) (internals, benchmarks), [Limits](limits.md) (rule syntax, adaptive limiting), [Configuration](configuration.md) (full YAML and CLI reference).

---

## Production Readiness Checklist

Before going live, verify every item:

| # | Item | Check |
|---|------|-------|
| 1 | **Limits dry-run disabled** | `limits.dry_run: false` — enforcement is active |
| 2 | **GOMEMLIMIT set** | `memory.limit_ratio: 0.9` or explicit `GOMEMLIMIT` env var |
| 3 | **Persistent queue enabled** | `queue.enabled: true` with adequate disk |
| 4 | **TLS enabled** | Receiver and exporter TLS configured for production traffic |
| 5 | **Health probes configured** | `/live` for liveness, `/ready` for readiness |
| 6 | **PDB in place** | `minAvailable: 1` (or appropriate for replica count) |
| 7 | **Resource requests/limits set** | CPU and memory requests match sizing tier |
| 8 | **Monitoring active** | ServiceMonitor or scrape config collecting metrics |
| 9 | **Alerting configured** | At least the 5 critical alerts from [Alerting Essentials](#alerting-essentials) |
| 10 | **Limits config tested** | Run with `dry_run: true` for 24h, review logs before enforcing |

---

## Deployment Sizing

Six tiers covering common deployment scales:

| Tier | Throughput | CPU Request | Memory Request | GOMEMLIMIT | Replicas | Queue Disk |
|------|-----------|-------------|----------------|------------|----------|------------|
| **Starter** | < 100K dps/min | 100m | 128Mi | 115Mi | 1 | - |
| **Small** | 100K-1M dps/min | 250m | 512Mi | 460Mi | 1 | 1Gi |
| **Medium** | 1M-5M dps/min | 500m | 1Gi | 900Mi | 2 | 5Gi |
| **Large** | 5M-20M dps/min | 1 | 2Gi | 1.7Gi | 2-4 | 10Gi |
| **XL** | 20M-100M dps/min | 2 | 4Gi | 3.4Gi | 4-8 | 20Gi |
| **Enterprise** | 100M+ dps/min | 4 | 8Gi | 6.8Gi | 8+ or DaemonSet | 50Gi |

> **DaemonSet mode**: For Enterprise tier, consider `kind: daemonset` to run one pod per node, eliminating cross-AZ traffic costs and reducing latency. See [Kubernetes Configuration](#traffic-distribution--daemonset-mode).

---

## Cost Analysis

### Infrastructure Cost per Tier

Approximate monthly costs based on AWS/GCP on-demand pricing (2025):

| Tier | Compute | Memory | Disk | Network | **Total Monthly** |
|------|---------|--------|------|---------|-------------------|
| Starter (1 pod) | ~$5 | ~$1 | $0 | ~$1 | **~$7** |
| Small (1 pod) | ~$25 | ~$2 | ~$0.32 | ~$2 | **~$30** |
| Medium (2 pods) | ~$100 | ~$16 | ~$3 | ~$9 | **~$130** |
| Large (4 pods) | ~$400 | ~$64 | ~$13 | ~$45 | **~$520** |
| XL (8 pods) | ~$800 | ~$128 | ~$50 | ~$90 | **~$1,070** |
| Enterprise (DaemonSet) | Varies by node count | | | | Varies |

### Multi-Provider Ingestion Cost Comparison

What you pay your metrics backend provider before metrics-governor optimizations:

| Provider | Pricing Model | 100K Series | 500K Series | 2M Series | 10M Series |
|----------|--------------|-------------|-------------|-----------|------------|
| **Grafana Cloud** | ~$8/1K active series/mo | $800 | $4,000 | $16,000 | $80,000 |
| **Datadog** | ~$5/100 custom metrics/mo | $5,000 | $25,000 | $100,000 | $500,000 |
| **New Relic** | ~$0.30/GB ingested | $300 | $1,500 | $6,000 | $30,000 |
| **Splunk Observability** | ~$5/host + $0.10/1K DPM | Varies | Varies | Varies | Varies |
| **Self-hosted Mimir/Thanos** | ~$0.50/1K series/mo (infra) | $50 | $250 | $1,000 | $5,000 |
| **Self-hosted VictoriaMetrics** | ~$0.30/1K series/mo (infra) | $30 | $150 | $600 | $3,000 |

> Prices are approximate and based on publicly available pricing as of 2025. Actual costs depend on contract terms, commitments, and volume discounts.

### Feature-by-Feature Cost Savings

How each metrics-governor feature reduces your metrics bill:

| Feature | Typical Reduction | Savings on 500K Series (Grafana Cloud $4K/mo) |
|---------|-------------------|-----------------------------------------------|
| **Adaptive Limiting** | 15-30% series | $600-$1,200/mo |
| **Stats Threshold** | 40-50% scrape bytes | TSDB storage savings |
| **Cardinality Tracking** | Prevents 10-100x blow-ups | Prevents $4K-$40K incidents |
| **Processing Rules** | 50-90% for processed metrics | $200-$800/mo |
| **Relabeling** | 10-20% series | $400-$800/mo |
| **Multi-tenancy Quotas** | Per-tenant caps | Prevents surprise bills |
| **Compression (zstd)** | 60% network bytes | $5-$50/mo network |
| **Queue Persistence** | 0% data loss | Prevents re-send costs |
| **DaemonSet mode** | 100% cross-AZ traffic eliminated | $100-$2,000/mo (see below) |
| **trafficDistribution** | Prefer same-zone routing | $50-$500/mo cross-AZ savings |

### Cross-AZ / Network Traffic Savings

A hidden cost most teams overlook. Cloud providers charge for cross-AZ data transfer (AWS: $0.01/GB, GCP: $0.01/GB, Azure: $0.01/GB within region). In a multi-AZ Kubernetes cluster, when pods send metrics to a metrics-governor Deployment in a different AZ, every byte crosses AZ boundaries and incurs charges.

| Throughput | Data Volume (uncompressed) | Cross-AZ % (3-AZ cluster) | Monthly Cross-AZ Cost |
|------------|--------------------------|---------------------------|----------------------|
| 1M dps/min | ~50 GB/mo | ~67% | ~$0.33/mo |
| 10M dps/min | ~500 GB/mo | ~67% | ~$3.35/mo |
| 100M dps/min | ~5 TB/mo | ~67% | ~$33.50/mo |
| 1B dps/min | ~50 TB/mo | ~67% | ~$335/mo |

**Three strategies to eliminate cross-AZ costs:**

1. **DaemonSet mode** (`kind: daemonset`) — metrics-governor runs on every node. All metric data stays on the same node (node-local traffic is free). Cross-AZ cost drops to **$0**. Best for high-throughput clusters where every node generates metrics.

2. **`service.trafficDistribution: PreferClose`** (K8s 1.31+) — Kubernetes routes traffic to the closest endpoint (same zone). Reduces cross-AZ from ~67% to ~5-10% (overflow only). Available via Helm: `service.trafficDistribution: PreferClose`.

3. **`service.internalTrafficPolicy: Local`** (K8s 1.22+) — strict node-local only; traffic fails if no local pod exists. Use with DaemonSet mode for guaranteed node-local delivery.

| Strategy | Cross-AZ Reduction | Trade-off |
|----------|-------------------|-----------|
| Default (Deployment) | 0% (baseline ~67% cross-AZ) | Simple, but pays cross-AZ |
| `trafficDistribution: PreferClose` | ~85-95% reduction | Slight imbalance possible |
| `internalTrafficPolicy: Local` + DaemonSet | **100% elimination** | Must run on every node |
| DaemonSet mode only | **100% elimination** | Higher total resource usage |

### Worked ROI Examples

| Scenario | Provider Cost | Reduction | Monthly Savings | Infra Cost | **ROI** |
|----------|--------------|-----------|-----------------|------------|---------|
| 500K series on Grafana Cloud | $4,000/mo | 45% | $1,800/mo | $30/mo | **60:1** |
| 2M series on Datadog | $100,000/mo | 45% | $45,000/mo | $130/mo | **346:1** |
| 10M series self-hosted Mimir | $5,000/mo | 50% | $2,500/mo | $520/mo | **4.8:1** |
| 200K series on New Relic | $600/mo | 40% | $240/mo | $30/mo | **8:1** |

> Even on self-hosted backends where per-series costs are low, metrics-governor reduces TSDB resource consumption (CPU, memory, disk IOPS), extending cluster capacity before scaling.

---

## Auto-Derivation Engine

When a config value is left at zero (or unset), the auto-derivation engine fills it from detected system resources. This runs after profile + YAML + CLI are merged, so **explicit user values are never overwritten**.

### Detected Resources

| Resource | Source | Fallback |
|----------|--------|----------|
| CPU cores | `runtime.NumCPU()` (respects cgroup CPU quota) | 1 |
| Memory | cgroup v2 `memory.max` (K8s/Docker) | System total RAM |
| Disk | `statfs()` on queue path | Profile default |

### CPU-Based Derivations

| Field | Formula | Example (8 CPU) |
|-------|---------|-----------------|
| QueueWorkers | NumCPU × 2 | 16 |
| ExportConcurrency | NumCPU × 4 | 32 |
| PreparerCount | NumCPU | 8 |
| SenderCount | NumCPU × 2 | 16 |
| GlobalSendLimit | NumCPU × 8 | 64 |

### Memory-Based Derivations

| Field | Formula | Example (2 GB) |
|-------|---------|----------------|
| QueueMaxBytes | Memory × QueueMemoryPercent | 200 MB (10%) |
| MaxBatchBytes | min(QueueMaxBytes/4, 8 MB) | 8 MB |

All derivations are logged at startup:

```
INFO auto-derived config value  field=queue-workers value=16 formula="NumCPU × 2"
INFO auto-derived config value  field=export-concurrency value=32 formula="NumCPU × 4"
INFO auto-derived config value  field=queue-max-bytes value=200Mi formula="MemoryLimit × 10%"
```

**Why this matters for HPA:** When Kubernetes scales pods via HPA, each new pod detects its cgroup CPU quota and derives worker counts automatically. No config changes needed — the auto-derivation engine adapts to whatever resources the scheduler provides.

---

## Memory & CPU Tuning

### GOMEMLIMIT and Cgroups Auto-detection

metrics-governor reads the container memory limit from cgroups v2 (`/sys/fs/cgroup/memory.max`) and automatically sets `GOMEMLIMIT` to `limit * memory.limit_ratio`. This prevents OOM kills by keeping Go's GC aware of the true memory budget.

```yaml
memory:
  limit_ratio: 0.9    # Default: 90% of container limit for GOMEMLIMIT
```

For containers > 4GB, reduce to 0.85 to leave more headroom for non-heap allocations (goroutine stacks, CGo, mmap'd files):

```yaml
memory:
  limit_ratio: 0.85
```

You can also set `GOMEMLIMIT` explicitly via environment variable, which takes precedence over auto-detection:

```yaml
env:
  - name: GOMEMLIMIT
    value: "3400MiB"
```

### CPU Sizing Formula

metrics-governor is I/O-bound for most workloads. CPU scales primarily with:
- **Export concurrency** — each goroutine uses ~0.1 CPU during batch serialization
- **String interning** — minimal CPU, saves memory allocations
- **Limits evaluation** — regex matching scales with rule count

**Rule of thumb**: 1 CPU core handles ~5M dps/min with default settings. Scale linearly.

---

## I/O Optimization

### Disk I/O (Persistent Queue)

The persistent queue uses buffered I/O with write coalescing to minimize syscalls:

| Setting | Default | Production Recommendation |
|---------|---------|--------------------------|
| `queue.inmemory_blocks` | 256 | 4096 (high throughput) |
| `queue.chunk_size` | 512MB | 512MB |
| `queue.meta_sync_interval` | 1s | 1s (max data loss window) |
| `queue.stale_flush_interval` | 5s | 30s (less frequent disk writes) |

**Storage class recommendations**:
- **AWS**: `gp3` (baseline 3000 IOPS, 125 MB/s) — sufficient for most workloads
- **GCP**: `pd-ssd` for high throughput, `pd-balanced` for cost-effective
- **Azure**: `managed-premium` for production

### Network I/O (Compression)

Compression reduces network bytes between metrics-governor and your backend:

| Algorithm | Ratio | CPU Cost | Best For |
|-----------|-------|----------|----------|
| **none** | 1.0x | 0 | Local/same-node backends |
| **gzip** | 3-5x | Medium | General purpose |
| **snappy** | 2-3x | Low | Latency-sensitive |
| **zstd** | 4-6x | Medium-Low | Best ratio with moderate CPU |
| **zlib** | 3-5x | Medium | Legacy compatibility |
| **deflate** | 3-5x | Medium | Legacy compatibility |

```yaml
exporter:
  compression:
    type: "zstd"
    level: 3    # 1-19, higher = better ratio, more CPU
```

---

## Limits Enforcer Tuning

### Stats Threshold

The per-group stats reporting on `/metrics` scales linearly with tracked groups. In high-cardinality environments (10K+ groups), this dominates scrape time.

**Benchmark results** (1,000 groups, ~50% filtered):

| Metric | No threshold | threshold=5 | Reduction |
|--------|:-----------:|:-----------:|:---------:|
| Latency | 345 us/op | 192 us/op | **44%** |
| Memory | 312 KB/op | 159 KB/op | **49%** |
| Allocations | 2,756/op | 1,403/op | **49%** |

Savings scale linearly — with 10K groups and threshold filtering 90%, expect ~90% reduction in scrape output.

```yaml
limits:
  stats_threshold: 100  # Only report groups with >= 100 datapoints or cardinality
```

See [limits.md](limits.md#per-group-stats-threshold) for full configuration.

### Rule Cache Sizing

The rule matching LRU cache avoids regex evaluation for previously seen metric identities:

```yaml
# High-cardinality workloads (100K+ unique metric names)
# Each entry ~100 bytes, so 100K entries ~ 10 MB
rule_cache_max_size: 100000
```

Monitor cache effectiveness:
```promql
rate(metrics_governor_rule_cache_hits_total[5m]) /
(rate(metrics_governor_rule_cache_hits_total[5m]) + rate(metrics_governor_rule_cache_misses_total[5m]))
```

Target: > 95% hit ratio.

### Cardinality Tracker Mode

Each profile sets a default cardinality tracking mode calibrated to its resource envelope:

| Profile | Mode | Why |
|---------|------|-----|
| `minimal` | `exact` | Accurate at small scale, low series count in dev |
| `balanced` | `bloom` | 98% memory savings vs exact at 100k+ series |
| `performance` | `hybrid` | Auto-switches Bloom to HLL for cardinality explosions |

For production with unknown or variable cardinality, use **hybrid mode**:

```yaml
cardinality:
  mode: hybrid
  expected_items: 100000
  fp_rate: 0.01
  hll_threshold: 50000
```

**Memory planning by mode:**

| Cardinality | Bloom (1% FPR) | HLL (p=14) | Exact (map) |
|-------------|:--------------:|:----------:|:-----------:|
| 10K series | 12 KB | 12 KB | 750 KB |
| 100K series | 120 KB | 12 KB | 7.5 MB |
| 1M series | 1.2 MB | 12 KB | 75 MB |
| 10M series | 12 MB | 12 KB | 750 MB |

### Hybrid Mode — Memory Savings & Auto-Switching

Hybrid mode gives the best of both worlds: **accurate membership testing** (Bloom phase) at low-to-moderate cardinality, and **constant ~12 KB memory** (HLL phase) when cardinality explodes.

**How it works:**

1. **Bloom phase** — Both a Bloom filter and an HLL sketch receive all inserts in parallel. Membership testing works. Memory grows linearly with cardinality.
2. **Threshold check** — When `count >= hll_threshold`, the mode switches.
3. **HLL phase** — Bloom filter is released. Only the HLL sketch remains (~12 KB constant). Membership testing is no longer available.
4. **One-way switch** — Once switched to HLL, stays there until the next `Reset()` cycle.

**Threshold tuning:**

```yaml
# Conservative — keep Bloom for more accurate enforcement
cardinality:
  mode: hybrid
  hll_threshold: 100000      # switch at 100k series (Bloom uses ~120 KB)

# Aggressive — save memory early
cardinality:
  mode: hybrid
  hll_threshold: 1000        # switch at 1k series (Bloom uses ~1.2 KB)

# Default (performance profile)
cardinality:
  mode: hybrid
  hll_threshold: 10000       # switch at 10k series (Bloom uses ~12 KB)
```

**What happens at the switch:**

- Limits enforcement that relies on membership testing (`TestOnly`) falls back to count-based enforcement only.
- Cardinality counts remain accurate (HLL estimates within 1-2%).
- A metric `metrics_governor_cardinality_mode{mode="hll"}` is emitted so you can alert on unexpected mode switches.

**Bloom persistence with hybrid mode:** When `bloom-persistence-enabled=true`, Bloom filter state is persisted during the Bloom phase. After a restart, the tracker restores from persisted state and may switch to HLL again if cardinality re-exceeds the threshold. This avoids the "re-learning" penalty on restart.

See [Cardinality Tracking](./cardinality-tracking.md) for full configuration reference and PromQL monitoring queries.

---

## Export Pipeline Tuning

| Setting | Small (<1M dps/min) | Medium (1-10M dps/min) | Large (10M+ dps/min) |
|---------|:-------------------:|:----------------------:|:--------------------:|
| `batch_size` | 500 | 1000 | 2000 |
| `max_batch_bytes` | 4MB | 8MB | 8MB |
| `queue_workers` | 0 (auto) | 0 (auto) | 0 (auto) |
| `buffer_size` | 100 | 500 | 1000 |
| `string_interning` | true | true | true |
| `intern_max_value_length` | 64 | 64 | 128 |
| `compression.type` | none | gzip | zstd |

### Batch Size Guidance

- **Too small**: More HTTP/gRPC round-trips, higher per-request overhead
- **Too large**: Higher memory usage per batch, risk of exceeding backend limits
- **max_batch_bytes**: Prevents oversized requests. Batches exceeding this are automatically split (recursive binary split).

### Worker Pool

Controls the number of pull-based worker goroutines that drain the queue. Workers are I/O-bound, so the default (2×NumCPU) provides good parallelism:

```yaml
queue:
  workers: 0   # 0 = 2×NumCPU (auto)
```

---

## Kubernetes Configuration

### Resources by Tier

```yaml
# Medium tier (1-5M dps/min)
resources:
  requests:
    cpu: 500m
    memory: 1Gi
  limits:
    cpu: "1"
    memory: 1Gi
```

> Set memory request = limit to avoid eviction under memory pressure (QoS class Guaranteed).

### Health Probes

```yaml
livenessProbe:
  enabled: true
  httpGet:
    path: /live
    port: stats
  initialDelaySeconds: 10
  periodSeconds: 10

readinessProbe:
  enabled: true
  httpGet:
    path: /ready
    port: stats
  initialDelaySeconds: 5
  periodSeconds: 5
```

- `/live` returns 200 if the process is running, 503 during shutdown
- `/ready` checks all pipeline components (receivers, exporters); returns 503 if any are down

### HPA & VPA — Autoscaling Best Practices

metrics-governor supports three autoscaling patterns. Choose based on your traffic predictability and resource optimization goals.

#### Pattern 1: HPA Only — CPU-Based Horizontal Scaling

Best for: steady traffic with periodic spikes, most production deployments.

```yaml
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 60
      policies:
        - type: Pods
          value: 2
          periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
        - type: Percent
          value: 25
          periodSeconds: 120
```

**Why 70% CPU target?** metrics-governor's adaptive batch tuning and worker scaling use spare CPU headroom for self-optimization. Setting the target above 80% starves the adaptive systems.

**Custom metrics** (requires Prometheus adapter or KEDA):

```yaml
autoscaling:
  customMetrics:
    - type: Pods
      pods:
        metric:
          name: metrics_governor_queue_depth
        target:
          type: AverageValue
          averageValue: "1000"
```

Queue depth is a better scaling signal than CPU for bursty workloads — it reacts faster to incoming spikes.

#### Pattern 2: VPA Only — Vertical Right-Sizing

Best for: single-replica deployments, stateful workloads, or initial capacity discovery.

VPA is not included in the Helm chart — apply separately:

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: metrics-governor-vpa
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment       # or StatefulSet
    name: metrics-governor
  updatePolicy:
    updateMode: Auto       # or "Off" for recommendations only
  resourcePolicy:
    containerPolicies:
      - containerName: metrics-governor
        minAllowed:
          cpu: 250m
          memory: 256Mi
        maxAllowed:
          cpu: 4
          memory: 8Gi
        controlledResources: ["cpu", "memory"]
```

> Use `updateMode: Off` in production initially — review VPA recommendations before enabling `Auto` mode, which restarts pods to apply new resource sizes.

#### Pattern 3: Combined HPA + VPA

Best for: large deployments that need both horizontal scaling and per-pod right-sizing.

**Critical constraint:** HPA and VPA must NOT both control the same resource dimension.

```yaml
# HPA: scales on CPU only
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  # Do NOT set targetMemoryUtilizationPercentage when using VPA for memory
```

```yaml
# VPA: controls memory only
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: metrics-governor-vpa
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: metrics-governor
  updatePolicy:
    updateMode: Auto
  resourcePolicy:
    containerPolicies:
      - containerName: metrics-governor
        controlledResources: ["memory"]  # memory only — CPU managed by HPA
        minAllowed:
          memory: 256Mi
        maxAllowed:
          memory: 8Gi
```

#### Autoscaling Conflict Matrix

| Resource | HPA Controls | VPA Controls | Result |
|----------|-------------|-------------|--------|
| CPU | Yes | No | HPA scales pods by CPU |
| Memory | No | Yes | VPA adjusts memory per pod |
| CPU | Yes | Yes | **Conflict — thrashing** |
| Memory | Yes | Yes | **Conflict — thrashing** |

#### Profile-Specific HPA Guidance

| Profile | Recommended HPA | Min Replicas | CPU Target | Notes |
|---------|----------------|--------------|------------|-------|
| `minimal` | Off | 1 | — | Single pod, no scaling |
| `balanced` | On | 2 | 70% | Standard production scaling |
| `performance` | On | 3 | 65% | Lower target — more headroom for adaptive systems |

> **Adaptive systems interaction:** When HPA scales to more pods, each pod's adaptive worker scaler and batch tuner operate independently. They self-optimize to the pod's local throughput, so scaling in and out is seamless.

### PodDisruptionBudget

```yaml
podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

### ConfigMap Reload

For hot-reloading limits, relabeling, and processing configs without pod restart:

```yaml
configReload:
  enabled: true
  watchInterval: 10
```

This deploys a lightweight sidecar that watches for ConfigMap changes and sends SIGHUP to metrics-governor. Requires `shareProcessNamespace: true` (set automatically).

> **Important**: Volume mounts with `subPath` do NOT auto-update from ConfigMap changes. The sidecar uses directory mounts, which are updated by the kubelet.

### Storage Classes

For StatefulSet with persistent queue:

```yaml
kind: statefulset
persistence:
  enabled: true
  storageClassName: gp3     # AWS
  size: 10Gi
queue:
  enabled: true
  path: /data/queue
```

### Deployment vs StatefulSet

The Helm chart supports both via `kind: deployment` (default) or `kind: statefulset`.

| Factor | Deployment | StatefulSet |
|--------|-----------|-------------|
| **Persistent queue** | Shared PVC (all pods write to same volume) | Per-pod PVCs (each pod owns its queue) |
| **Pod identity** | Random names, interchangeable | Stable names (`-0`, `-1`, …), ordered startup |
| **Scaling** | Fast scale-up/down, pods are fungible | Ordered scale — last pod removed first |
| **HPA compatible** | Yes | Yes |
| **Rolling updates** | `maxSurge` + `maxUnavailable` | One-at-a-time (ordered) or `Parallel` |
| **Queue data on scale-down** | Shared — nothing lost | Per-pod PVC orphaned until pod returns |
| **Recommended for** | Most deployments, memory queue | Disk queue, data durability requirements |

**When to use StatefulSet:**
- Persistent disk queue (`queue.type: disk`) where each pod must own its queue data
- You need the queue to survive pod restarts without data loss
- Typically paired with the `performance` profile

**When to use Deployment:**
- Memory queue (`queue.type: memory`) or no queue
- You want fast, flexible scaling
- Typically paired with `minimal` or `balanced` profiles

```yaml
# StatefulSet with per-pod persistent queue
kind: statefulset

statefulSet:
  podManagementPolicy: OrderedReady  # or Parallel for faster rollouts
  updateStrategy:
    type: RollingUpdate

persistence:
  enabled: true
  storageClassName: gp3       # AWS EBS gp3, or your StorageClass
  size: 10Gi
  accessModes: [ReadWriteOnce]
```

> **Migration path:** To migrate from Deployment to StatefulSet, set `kind: statefulset` and `persistence.enabled: true`. Helm will create the StatefulSet and per-pod PVCs. The old Deployment will be removed on the next `helm upgrade`. Queue data in the old shared PVC is not automatically migrated — drain the queue before switching.

### Traffic Distribution & Topology-Aware Routing

Cross-AZ network traffic is a significant cost driver in cloud environments. metrics-governor supports several routing strategies to minimize it.

#### Deployment with Zone-Aware Routing (K8s 1.31+)

Minimal cross-AZ traffic with standard Deployment:

```yaml
# values.yaml
kind: deployment
replicaCount: 3   # at least 1 per AZ

service:
  trafficDistribution: PreferClose
```

Kubernetes routes to the closest pod (same zone), with overflow to other zones only when needed. This is the simplest approach and works well for most deployments.

> **Note:** `trafficDistribution: PreferClose` requires K8s 1.31+ with the `ServiceTrafficDistribution` feature gate enabled. On older clusters, use topology-aware hints or DaemonSet mode instead.

#### Topology-Aware Hints (K8s 1.23+)

For older clusters that don't support `trafficDistribution`:

```yaml
service:
  annotations:
    service.kubernetes.io/topology-mode: Auto
```

The EndpointSlice controller allocates endpoints proportionally across zones. Works best when replicas are evenly distributed across zones.

#### Routing Strategy Decision

| Cluster Size | Traffic Pattern | Recommended |
|-------------|----------------|-------------|
| < 10 nodes, 1-2 AZs | Low to moderate | Deployment + `PreferClose` |
| 10-50 nodes, 2-3 AZs | Moderate, bursty | Deployment + `PreferClose` + HPA |
| 50+ nodes, 3+ AZs | High volume, steady | **DaemonSet** (zero cross-AZ) |
| Any size, latency-sensitive | APM / tracing | **DaemonSet** (guaranteed node-local) |

### DaemonSet Mode

DaemonSet places one metrics-governor pod on every node, guaranteeing node-local traffic with zero cross-AZ cost.

#### When to Use DaemonSet

| Factor | Deployment | DaemonSet |
|--------|-----------|-----------|
| **Node count < 10** | Preferred (fewer total pods) | Overhead per node |
| **Node count > 50** | Cross-AZ costs add up | Preferred (zero cross-AZ) |
| **Throughput > 10M dps/min** | Scale replicas + zone hints | Preferred (local traffic) |
| **Resource budget tight** | 2-4 pods total | 1 pod per node (more total) |
| **Latency-sensitive** | Zone-aware routing helps | Guaranteed node-local |

#### Configuration

```yaml
# values.yaml
kind: daemonset

service:
  externalTrafficPolicy: Local   # ensures node-local routing

daemonSet:
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1          # one node at a time during upgrades
```

Applications on each node send metrics to their local metrics-governor pod. No data crosses AZ boundaries.

#### DaemonSet Resource Sizing

Each DaemonSet pod handles only its node's traffic, so per-pod resources can be smaller:

| Node Workload | Profile | CPU Request | Memory Request |
|--------------|---------|-------------|----------------|
| Light (< 50 pods) | `minimal` | 100m | 128Mi |
| Medium (50-200 pods) | `balanced` | 250m | 512Mi |
| Heavy (200+ pods, high cardinality) | `balanced` | 500m | 1Gi |

#### DaemonSet with Persistent Queue

DaemonSet pods can use host-path or local PVs for disk queue persistence:

```yaml
kind: daemonset

persistence:
  enabled: true
  storageClassName: local-path   # or hostpath provisioner
  size: 5Gi

config:
  rawConfig: |
    queue:
      enabled: true
      path: /data/queue
```

> **Caveat:** DaemonSet with PVCs requires a storage provisioner that supports `ReadWriteOnce` with node affinity (e.g., `local-path-provisioner`, Rancher local-path, or OpenEBS). Standard cloud PVCs (EBS, PD) may not work well because they bind to a specific AZ, not a specific node.

#### DaemonSet Limitations

- **No HPA:** DaemonSet pod count is driven by node count, not load. Use VPA for vertical right-sizing instead.
- **Uneven load:** Nodes with more application pods generate more metrics. Some DaemonSet pods may be idle while others are busy.
- **Total resource usage:** With 100 nodes, you run 100 governor pods — more total resources than a 5-replica Deployment.

### Complete Helm Values Example (Medium Tier)

```yaml
kind: deployment
replicaCount: 2

resources:
  requests:
    cpu: 500m
    memory: 1Gi
  limits:
    cpu: "1"
    memory: 1Gi

config:
  rawConfig: |
    receiver:
      grpc:
        address: ":4317"
      http:
        address: ":4318"
    exporter:
      endpoint: "otel-collector.monitoring:4317"
      protocol: grpc
      insecure: false
      timeout: 60s
      compression:
        type: zstd
        level: 3
    buffer:
      size: 500
      batch_size: 1000
      flush_interval: 10s
    stats:
      address: ":9090"
    limits:
      dry_run: false
      stats_threshold: 100
    performance:
      queue_workers: 16
      string_interning: true
    memory:
      limit_ratio: 0.9
    queue:
      enabled: true
      path: /data/queue
      max_bytes: 5368709120

limits:
  enabled: true

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 6
  targetCPUUtilizationPercentage: 70

podDisruptionBudget:
  enabled: true
  minAvailable: 1

serviceMonitor:
  enabled: true
  interval: 30s
```

---

## Bare Metal / VM Deployment

When running outside Kubernetes, metrics-governor works as a standard Linux service. The auto-derivation engine detects system resources via `/proc` and `statfs()` instead of cgroup limits.

### systemd Service

```ini
# /etc/systemd/system/metrics-governor.service
[Unit]
Description=Metrics Governor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=metrics-governor
ExecStart=/usr/local/bin/metrics-governor \
  --profile=balanced \
  --exporter-endpoint=otel-collector:4317 \
  --queue-path=/var/lib/metrics-governor/queue \
  --config=/etc/metrics-governor/config.yaml
Restart=always
RestartSec=5
LimitNOFILE=65536

# Memory limit (cgroup v2 — metrics-governor auto-detects this)
MemoryMax=1G
MemoryHigh=768M

# CPU limit
CPUQuota=200%

[Install]
WantedBy=multi-user.target
```

### Disk Sizing for Queue

The auto-derivation engine detects available disk via `statfs()` and sizes the queue to 80% of available space. If the queue path doesn't exist at startup, it falls back to the profile default.

**Recommended disk layout:**
- Dedicated partition or mount for `/var/lib/metrics-governor/queue/`
- Separate from OS disk to avoid queue I/O impacting system
- SSD recommended for `performance` profile

**Sizing formula:**

```
Required disk = throughput_bytes/sec × outage_seconds × compression_ratio × 1.25

Example: 100k dps × 200 bytes/dp × 0.3 (snappy) × 3600s (1hr) × 1.25 = ~2.7 GB
```

The 1.25 safety factor accounts for compaction temporary space, metadata files, and filesystem overhead.

### Config Reload on Bare Metal

Without Kubernetes ConfigMap + sidecar, use file-based reload with SIGHUP:

```bash
# Edit config
vim /etc/metrics-governor/config.yaml

# Trigger reload (limits, relabeling, processing rules only)
kill -HUP $(pidof metrics-governor)
```

Or automate with a cron job or systemd path unit:

```ini
# /etc/systemd/system/metrics-governor-reload.path
[Path]
PathModified=/etc/metrics-governor/config.yaml

[Install]
WantedBy=multi-user.target

# /etc/systemd/system/metrics-governor-reload.service
[Service]
Type=oneshot
ExecStart=/bin/kill -HUP $MAINPID
```

### Security Hardening

```bash
# Create dedicated user
useradd --system --no-create-home --shell /usr/sbin/nologin metrics-governor

# Create directories with proper permissions
mkdir -p /var/lib/metrics-governor/queue
chown metrics-governor:metrics-governor /var/lib/metrics-governor/queue
chmod 750 /var/lib/metrics-governor/queue
```

Add systemd hardening directives to the `[Service]` section:

```ini
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/metrics-governor
NoNewPrivileges=true
PrivateTmp=true
```

### Differences from Kubernetes

| Aspect | Kubernetes | VM / Bare Metal |
|--------|-----------|----------------|
| Memory detection | cgroup v2 `memory.max` | System total RAM (or systemd `MemoryMax`) |
| CPU detection | cgroup v2 CPU quota | `runtime.NumCPU()` (all cores visible) |
| Disk detection | PVC mount at queue path | `statfs()` on queue directory |
| Disk provisioning | PVC via StorageClass | Manual partition/mount |
| Memory enforcement | OOM kill by kubelet | OOM kill by systemd `MemoryMax` |
| Scaling | HPA horizontal | Manual or external autoscaler |
| Config reload | ConfigMap + sidecar SIGHUP | File edit + `kill -HUP` |
| Service discovery | K8s Service + DNS | Load balancer or DNS round-robin |

> **Important:** On bare metal without cgroup limits, `GOMEMLIMIT` should be set explicitly via environment variable or `memory.limit_ratio` in config. Without it, the Go runtime cannot detect memory pressure and the adaptive memory sizing will use system total RAM, which may be shared with other processes.

---

## Resilience Tuning & Auto-Sensing

metrics-governor includes three adaptive mechanisms that self-tune based on real-time conditions. When enabled (default in `balanced` and `performance` profiles), these systems work together to optimize throughput while protecting against downstream failures.

For detailed resilience configuration and failure mode analysis, see [Resilience Guide](resilience.md).

### Adaptive Batch Tuning (AIMD)

The batch tuner uses an **Additive Increase / Multiplicative Decrease** algorithm to find the optimal batch size for your backend. It starts at a conservative size and grows on success, shrinking aggressively on failure.

**How it works:**
1. After `success_streak` consecutive successful exports, batch size grows by `grow_factor` (25%)
2. On any failure, batch size immediately shrinks by `shrink_factor` (50%)
3. On HTTP 413 (Request Too Large), a hard ceiling is set at 80% of the current size

```yaml
buffer:
  batch_auto_tune:
    enabled: true          # default: true (balanced/performance profiles)
    min_bytes: 512         # floor — never go below this
    max_bytes: 16777216    # ceiling — 16 MB
    success_streak: 10     # consecutive successes before growing
    grow_factor: 1.25      # 25% growth per step
    shrink_factor: 0.5     # 50% shrink on failure
```

**Monitoring:**

| Metric | Description |
|--------|-------------|
| `metrics_governor_batch_current_max_bytes` | Current tuned batch size |
| `metrics_governor_batch_hard_ceiling_bytes` | HTTP 413 hard ceiling (0 = none) |
| `metrics_governor_batch_tuning_adjustments_total{direction}` | Adjustments count (up/down) |
| `metrics_governor_batch_success_streak` | Current consecutive success count |

**Production tuning:**
- If batch size oscillates rapidly, increase `success_streak` (e.g., 20) to require more stability before growing
- If your backend has a known max request size, set `max_bytes` to 80% of that limit
- The 413-detection feature automatically discovers backend limits — no manual ceiling needed for most OTLP backends

### Adaptive Worker Scaling (AIMD)

The worker scaler dynamically adjusts the number of queue export workers based on queue depth and export latency.

**How it works:**
1. Every `scale_interval`, check queue depth and latency EWMA
2. If queue depth > `high_water_mark` AND latency < `max_latency` → add 1 worker (additive increase)
3. If queue depth < `low_water_mark` for `sustained_idle_secs` → halve worker count (multiplicative decrease)
4. Latency check prevents scaling up when the backend is already overloaded

```yaml
exporter:
  queue:
    adaptive_workers:
      enabled: true          # default: true (balanced/performance profiles)
      min_workers: 1         # floor
      max_workers: 0         # 0 = NumCPU × 4 (auto-derived)
      scale_interval: 5s     # decision interval
      high_water_mark: 100   # queue depth for scale-up trigger
      low_water_mark: 10     # queue depth for idle detection
      max_latency: 500ms     # suppress scale-up when latency exceeds this
      sustained_idle_secs: 30 # idle duration before scale-down
```

**Monitoring:**

| Metric | Description |
|--------|-------------|
| `queue_workers_desired` | Current target worker count |
| `queue_export_latency_ewma_seconds` | EWMA latency (alpha=0.3) |
| `queue_scaler_adjustments_total{direction}` | Scale events (up/down) |

**Production tuning:**
- Lower `high_water_mark` for faster reaction to spikes (but more scaling noise)
- Increase `sustained_idle_secs` to prevent premature scale-down during intermittent traffic
- Set `max_latency` based on your SLO — if exports take > 500ms, adding more workers won't help (the bottleneck is downstream)

### Circuit Breaker with Auto-Reset

The circuit breaker protects the downstream backend from being overwhelmed during failures. It follows the standard Closed → Open → Half-Open state machine.

**States:**
1. **Closed** — normal operation, all exports proceed
2. **Open** — after `threshold` consecutive failures, all exports are blocked (queued for retry)
3. **Half-Open** — after `reset_timeout`, one probe request is allowed through
   - If the probe succeeds → Closed (resume normal operation)
   - If the probe fails → Open (reset timer)

```yaml
exporter:
  queue:
    circuit_breaker:
      enabled: true        # default: true (balanced/performance profiles)
      threshold: 5         # failures before opening
      reset_timeout: 30s   # wait before half-open probe
```

**Monitoring:**

| Metric | Description |
|--------|-------------|
| `queue_circuit_state{state}` | Current state (closed/open/half_open) |
| `queue_circuit_open_total` | Total times circuit opened |

**Production tuning:**
- Lower `threshold` (e.g., 3) for aggressive protection — the `performance` profile uses this
- Shorter `reset_timeout` (e.g., 15s) for faster recovery — but only if your backend recovers quickly
- The circuit breaker works with backoff — when open, data accumulates in the queue and is drained on recovery

### Resilience Level Presets

Instead of tuning individual resilience parameters, use the `resilience_level` consolidated parameter:

```yaml
resilience_level: medium   # low, medium, or high
```

| Setting | `low` | `medium` (balanced) | `high` (performance) |
|---------|-------|---------------------|---------------------|
| Backoff multiplier | 1.5 | 2.0 | 3.0 |
| Circuit breaker | off | on (5 failures) | on (3 failures) |
| CB reset timeout | 60s | 30s | 15s |
| Batch drain size | 5 | 10 | 25 |
| Burst drain size | 50 | 100 | 250 |

**Choosing a level:**
- **`low`** — backend is reliable, you want minimal overhead. No circuit breaker, gentle backoff.
- **`medium`** — standard production. Circuit breaker catches cascading failures, moderate backoff.
- **`high`** — backend is unreliable or shared. Aggressive circuit breaker, fast backoff, large drain batches for quick recovery.

> Individual parameters can still override the preset. For example, `resilience_level: high` with an explicit `circuit_breaker.threshold: 10` uses all `high` defaults except the threshold.

### Failover Queue

Enable the persistent queue to catch all export failures:

```yaml
queue:
  enabled: true
  path: /data/queue
  max_bytes: 10737418240  # 10 GB
  full_behavior: drop_oldest
```

**Queue sizing formula:**
```
Required disk = (datapoints/min × avg_bytes_per_dp × desired_minutes) / compression_ratio

Example: 10M dps/min × 50 bytes × 30 min / 3 = 5 GB
```

### How the Adaptive Systems Work Together

In a typical failure scenario:

1. Backend starts returning errors
2. **Circuit breaker** opens after `threshold` failures — stops hammering the backend
3. **Batch tuner** shrinks batch size by 50% — prepares for smaller recovery batches
4. **Worker scaler** detects queue backing up but suppresses scale-up (latency too high)
5. Data accumulates in the **failover queue** (memory or disk)
6. After `reset_timeout`, circuit breaker enters half-open — sends one probe
7. If probe succeeds: circuit closes, workers scale up, batch tuner begins growing again
8. Queue drains at the rate the backend can handle, automatically paced by the adaptive systems

This feedback loop means metrics-governor self-recovers from outages without operator intervention. The queue acts as a buffer, the circuit breaker prevents cascading failures, and the adaptive tuners optimize the recovery rate.

---

## Prometheus Scrape Optimization

When metrics-governor itself produces high-cardinality output (many rules x many groups), optimize the scrape:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: metrics-governor
    scrape_interval: 30s     # Don't scrape too frequently
    scrape_timeout: 15s      # Allow time for large responses
    metrics_path: /metrics
```

Combine stats threshold with `metric_relabel_configs` to further reduce stored series:

```yaml
    metric_relabel_configs:
      # Drop per-group metrics if you only need rule-level aggregates
      - source_labels: [__name__]
        regex: 'metrics_governor_rule_group_(datapoints|cardinality)'
        action: drop
```

For recording rules that pre-aggregate common queries:

```yaml
# Recording rules to reduce query-time load
groups:
  - name: metrics-governor
    interval: 1m
    rules:
      - record: metrics_governor:datapoints_rate:5m
        expr: sum(rate(metrics_governor_datapoints_total[5m]))
      - record: metrics_governor:export_errors_rate:5m
        expr: sum(rate(metrics_governor_export_errors_total[5m]))
```

---

## Alerting Essentials

Five critical alerts every production deployment should have. For a complete alerting reference, see [Alerting Guide](alerting.md).

### 1. Export Failures Sustained

```yaml
- alert: MetricsGovernorExportFailures
  expr: rate(metrics_governor_export_errors_total[5m]) > 0.1
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "metrics-governor export failures"
    description: "Export error rate {{ $value }}/s for 5 minutes"
```

### 2. Queue Near Full

```yaml
- alert: MetricsGovernorQueueNearFull
  expr: metrics_governor_queue_size_bytes / metrics_governor_queue_max_bytes > 0.8
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "metrics-governor queue > 80% full"
```

### 3. High Memory Usage

```yaml
- alert: MetricsGovernorHighMemory
  expr: go_memstats_alloc_bytes / metrics_governor_memory_limit_bytes > 0.9
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "metrics-governor memory > 90% of limit"
```

### 4. Readiness Probe Failing

```yaml
- alert: MetricsGovernorNotReady
  expr: up{job="metrics-governor"} == 0
  for: 2m
  labels:
    severity: critical
  annotations:
    summary: "metrics-governor not responding"
```

### 5. Cardinality Limit Breached

```yaml
- alert: MetricsGovernorCardinalityBreach
  expr: metrics_governor_cardinality_current > metrics_governor_cardinality_limit * 0.9
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "metrics-governor cardinality approaching limit"
```

---

## Real-World Configuration Examples

### Example 1: Small SaaS

**Profile**: 1M dps/min, 50K series, single pod

**Monthly costs**:
- Grafana Cloud: ~$400/mo (50K active series)
- Datadog: ~$2,500/mo (50K custom metrics)
- metrics-governor infra: ~$30/mo

**Expected savings**: $120-$750/mo (30-45% reduction depending on workload)

```yaml
# config.yaml
receiver:
  grpc:
    address: ":4317"
  http:
    address: ":4318"

exporter:
  endpoint: "grafana-cloud-otlp.example.com:443"
  protocol: grpc
  insecure: false
  timeout: 30s
  compression:
    type: gzip

buffer:
  size: 200
  batch_size: 500
  flush_interval: 10s

stats:
  address: ":9090"
  labels:
    - service
    - env

limits:
  dry_run: false
  stats_threshold: 50

memory:
  limit_ratio: 0.9

performance:
  queue_workers: 4
  string_interning: true

queue:
  enabled: true
  path: /data/queue
  max_bytes: 1073741824  # 1 GB
```

### Example 2: Mid-Size Platform

**Profile**: 10M dps/min, 200K series, 2 pods

**Monthly costs**:
- Grafana Cloud: ~$1,600/mo
- Datadog: ~$10,000/mo
- metrics-governor infra: ~$130/mo

**Expected savings**: $640-$4,000/mo

```yaml
# config.yaml
receiver:
  grpc:
    address: ":4317"
  http:
    address: ":4318"
    server:
      max_request_body_size: 10485760  # 10MB
      read_header_timeout: 30s
      write_timeout: 1m
  tls:
    enabled: true
    cert_file: /etc/tls/receiver/tls.crt
    key_file: /etc/tls/receiver/tls.key

exporter:
  endpoint: "otel-collector.monitoring:4317"
  protocol: grpc
  insecure: false
  timeout: 60s
  compression:
    type: zstd
    level: 3
  http_client:
    max_idle_conns: 100
    max_idle_conns_per_host: 100

buffer:
  size: 500
  batch_size: 1000
  flush_interval: 10s

stats:
  address: ":9090"
  labels:
    - service
    - env
    - cluster

limits:
  dry_run: false
  stats_threshold: 100

cardinality:
  mode: hybrid
  expected_items: 100000
  fp_rate: 0.01
  hll_threshold: 50000

memory:
  limit_ratio: 0.9

performance:
  queue_workers: 16
  string_interning: true
  intern_max_value_length: 64

queue:
  enabled: true
  path: /data/queue
  max_bytes: 5368709120  # 5 GB
  inmemory_blocks: 1024
  retry_interval: 5s
  max_retry_delay: 5m
```

### Example 3: Large Enterprise

**Profile**: 100M dps/min, 2M series, DaemonSet on 50 nodes, VPA + PDB

Uses DaemonSet mode + `internalTrafficPolicy: Local` for zero cross-AZ costs.

**Monthly costs**:
- Grafana Cloud: ~$16,000/mo
- Datadog: ~$100,000/mo
- metrics-governor infra: ~$520/mo (across 50 nodes)

**Expected savings**: $7,200-$45,000/mo

**Cross-AZ savings vs Deployment**: ~$33/mo additional (at this scale, ingestion savings dominate, but cross-AZ elimination removes a variable cost entirely).

```yaml
# config.yaml
receiver:
  grpc:
    address: ":4317"
  http:
    address: ":4318"
    server:
      max_request_body_size: 20971520  # 20MB
      read_header_timeout: 30s
      write_timeout: 2m
      idle_timeout: 2m
  tls:
    enabled: true
    cert_file: /etc/tls/receiver/tls.crt
    key_file: /etc/tls/receiver/tls.key

exporter:
  endpoint: "mimir.monitoring:443"
  protocol: http
  insecure: false
  timeout: 120s
  compression:
    type: zstd
    level: 6
  tls:
    enabled: true
    ca_file: /etc/tls/ca.crt
  http_client:
    max_idle_conns: 200
    max_idle_conns_per_host: 200
    max_conns_per_host: 100

buffer:
  size: 1000
  batch_size: 2000
  flush_interval: 5s

stats:
  address: ":9090"
  labels:
    - service
    - env
    - cluster
    - namespace

limits:
  dry_run: false
  stats_threshold: 500

cardinality:
  mode: hybrid
  expected_items: 500000
  fp_rate: 0.01
  hll_threshold: 100000

memory:
  limit_ratio: 0.85

performance:
  queue_workers: 64
  string_interning: true
  intern_max_value_length: 128

queue:
  enabled: true
  path: /data/queue
  max_bytes: 53687091200  # 50 GB
  inmemory_blocks: 4096
  chunk_size: 536870912   # 512 MB
  meta_sync_interval: 1s
  stale_flush_interval: 30s
  retry_interval: 5s
  max_retry_delay: 5m
```

**Helm values (Enterprise DaemonSet)**:

```yaml
kind: daemonset

resources:
  requests:
    cpu: "2"
    memory: 4Gi
  limits:
    cpu: "4"
    memory: 4Gi

config:
  rawConfig: |
    # ... full config from above ...

service:
  externalTrafficPolicy: Local

autoscaling:
  vpa:
    enabled: true
    updatePolicy:
      updateMode: Auto
    minAllowed:
      cpu: "1"
      memory: 2Gi
    maxAllowed:
      cpu: "4"
      memory: 8Gi

podDisruptionBudget:
  enabled: true
  maxUnavailable: "10%"

serviceMonitor:
  enabled: true
  interval: 30s
```

---

## Complete CLI Reference

All CLI flags can also be set via the YAML config file. The config file is the recommended approach for production deployments. Flags are useful for quick testing and overrides.

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `""` | Path to YAML configuration file |
| `-grpc-listen` | `:4317` | gRPC receiver listen address |
| `-http-listen` | `:4318` | HTTP receiver listen address |
| `-http-receiver-path` | `/v1/metrics` | HTTP receiver path for OTLP metrics |
| `-exporter-endpoint` | `localhost:4317` | OTLP exporter endpoint |
| `-exporter-protocol` | `grpc` | Exporter protocol: `grpc` or `http` |
| `-exporter-insecure` | `true` | Use insecure connection (no TLS) |
| `-exporter-timeout` | `30s` | Exporter request timeout |
| `-batch-size` | `1000` | Maximum batch size for export |
| `-max-batch-bytes` | `8388608` | Maximum batch size in bytes (8MB) |
| `-buffer-size` | `10000` | Maximum number of metrics to buffer |
| `-flush-interval` | `5s` | Buffer flush interval |
| `-queue-workers` | `0` | Worker pool size (0 = 2×NumCPU) |
| `-queue-always-queue` | `true` | Always route data through queue |
| `-buffer-full-policy` | `reject` | Buffer full policy: reject, drop_oldest, block |
| `-buffer-memory-percent` | `0.15` | Buffer capacity as % of detected memory |
| `-queue-memory-percent` | `0.15` | Queue in-memory as % of detected memory |
| `-string-interning` | `true` | Enable string interning |
| `-intern-max-value-length` | `64` | Max value length for interning |
| `-limits-config` | `""` | Path to limits configuration file |
| `-limits-dry-run` | `true` | Dry run mode for limits |
| `-limits-stats-threshold` | `0` | Min datapoints/cardinality for group reporting |
| `-rule-cache-max-size` | `10000` | LRU cache size for rule matching |
| `-relabel-config` | `""` | Path to relabeling configuration file |
| `-processing-config` | `""` | Path to processing rules configuration file |
| `-cardinality-mode` | `exact` | Cardinality mode: `exact`, `bloom`, `hll`, `hybrid` |
| `-cardinality-expected-items` | `100000` | Expected items for bloom filter |
| `-cardinality-fp-rate` | `0.01` | False positive rate for bloom filter |
| `-cardinality-hll-threshold` | `50000` | Threshold for hybrid mode switch |
| `-queue-enabled` | `false` | Enable persistent queue |
| `-queue-path` | `/data/queue` | Queue storage directory |
| `-queue-max-bytes` | `1073741824` | Maximum queue size in bytes |
| `-memory-limit-ratio` | `0.9` | Memory limit ratio for GOMEMLIMIT |
| `-stats-addr` | `:9090` | Stats/metrics HTTP endpoint |
| `-stats-labels` | `""` | Comma-separated labels to track |
| `-exporter-compression` | `none` | Compression type: none/gzip/zstd/snappy |
| `-log-format` | `text` | Log format: `text` or `json` |
| `-log-level` | `info` | Log level: debug/info/warn/error |

> All byte-size flags accept human-readable notation: `1Gi`, `512Mi`, `256Ki`, etc.

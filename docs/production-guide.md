# Production Guide

This guide consolidates all production tuning, sizing, cost analysis, and operational best practices for metrics-governor into a single reference. It covers everything from initial sizing to advanced pipeline tuning.

> **Other references**: [Resilience](resilience.md) (circuit breaker, backoff, failover queue), [Performance](performance.md) (internals, benchmarks), [Limits](limits.md) (rule syntax, adaptive limiting), [Configuration](configuration.md) (full YAML and CLI reference).

---

## Table of Contents

1. [Production Readiness Checklist](#production-readiness-checklist)
2. [Deployment Sizing](#deployment-sizing)
3. [Cost Analysis](#cost-analysis)
4. [Memory & CPU Tuning](#memory--cpu-tuning)
5. [I/O Optimization](#io-optimization)
6. [Limits Enforcer Tuning](#limits-enforcer-tuning)
7. [Export Pipeline Tuning](#export-pipeline-tuning)
8. [Kubernetes Configuration](#kubernetes-configuration)
9. [Resilience Tuning](#resilience-tuning)
10. [Prometheus Scrape Optimization](#prometheus-scrape-optimization)
11. [Alerting Essentials](#alerting-essentials)
12. [Real-World Configuration Examples](#real-world-configuration-examples)
13. [Complete CLI Reference](#complete-cli-reference)

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
| **Sampling** | 50-90% for sampled metrics | $200-$800/mo |
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

### HPA + VPA

**HPA** for horizontal scaling based on CPU or custom metrics:

```yaml
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 300
```

**VPA** for vertical right-sizing (requires VPA CRD installed):

```yaml
autoscaling:
  vpa:
    enabled: true
    updatePolicy:
      updateMode: Auto
    minAllowed:
      cpu: 250m
      memory: 512Mi
    maxAllowed:
      cpu: 4
      memory: 8Gi
```

> Do not use HPA and VPA simultaneously on the same resource (CPU or memory). Use VPA for memory right-sizing and HPA for CPU-based scaling, or use VPA in `Off` mode for recommendations only.

### PodDisruptionBudget

```yaml
podDisruptionBudget:
  enabled: true
  minAvailable: 1
```

### ConfigMap Reload

For hot-reloading limits, relabeling, and sampling configs without pod restart:

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

### Traffic Distribution & DaemonSet Mode

#### When to Use DaemonSet vs Deployment

| Factor | Deployment | DaemonSet |
|--------|-----------|-----------|
| **Node count < 10** | Preferred (fewer total pods) | Overhead per node |
| **Node count > 50** | Cross-AZ costs add up | Preferred (zero cross-AZ) |
| **Throughput > 10M dps/min** | Scale replicas + zone hints | Preferred (local traffic) |
| **Resource budget tight** | 2-4 pods total | 1 pod per node (more total) |
| **Latency-sensitive** | Zone-aware routing helps | Guaranteed node-local |

#### DaemonSet with Node-Local Traffic

Zero cross-AZ cost configuration — all traffic stays on-node:

```yaml
# values.yaml
kind: daemonset

service:
  externalTrafficPolicy: Local
```

Applications on each node send metrics to their local metrics-governor pod. No data crosses AZ boundaries.

#### Deployment with Zone-Aware Routing

Minimal cross-AZ with standard Deployment (K8s 1.31+):

```yaml
# values.yaml
kind: deployment
replicaCount: 3

service:
  trafficDistribution: PreferClose
```

Kubernetes routes to the closest pod (same zone), with overflow to other zones only when needed.

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

## Resilience Tuning

For detailed resilience configuration, see [Resilience Guide](resilience.md).

Key production settings:

### Circuit Breaker

The exporter circuit breaker prevents cascading failures when the backend is down:

```yaml
exporter:
  circuit_breaker:
    failure_threshold: 5
    reset_timeout: 30s
```

### Exponential Backoff

Failed exports use exponential backoff with jitter:

```yaml
queue:
  retry_interval: 5s
  max_retry_delay: 5m
```

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
Required disk = (datapoints/min * avg_bytes_per_dp * desired_minutes) / compression_ratio

Example: 10M dps/min * 50 bytes * 30 min / 3 = 5 GB
```

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
| `-sampling-config` | `""` | Path to sampling configuration file |
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

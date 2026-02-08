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
- [Auto-Derivation Engine](#auto-derivation-engine)
- [Cardinality Tracker Modes](#cardinality-tracker-modes)
  - [Hybrid Mode — Memory Savings & Auto-Switching](#hybrid-mode--memory-savings--auto-switching)
- [Resilience Tuning & Auto-Sensing](#resilience-tuning--auto-sensing)
  - [Adaptive Batch Tuning (AIMD)](#adaptive-batch-tuning-aimd)
  - [Adaptive Worker Scaling (AIMD)](#adaptive-worker-scaling-aimd)
  - [Circuit Breaker with Auto-Reset](#circuit-breaker-with-auto-reset)
  - [Resilience Level Presets](#resilience-level-presets)
- [Kubernetes Deployment](#kubernetes-deployment)
  - [HPA & VPA — Autoscaling Best Practices](#hpa--vpa--autoscaling-best-practices)
  - [Deployment vs StatefulSet](#deployment-vs-statefulset)
  - [Traffic Distribution & Topology-Aware Routing](#traffic-distribution--topology-aware-routing)
  - [DaemonSet Mode](#daemonset-mode)
- [Bare Metal / VM Deployment](#bare-metal--vm-deployment)
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

## Auto-Derivation Engine

When a config value is left at zero (or unset), the auto-derivation engine fills it from detected system resources. This runs after profile + YAML + CLI are merged, so explicit user values are never overwritten.

**Detected resources:**

| Resource | Source | Fallback |
|----------|--------|----------|
| CPU cores | `runtime.NumCPU()` (respects cgroup CPU quota) | 1 |
| Memory | cgroup v2 `memory.max` (K8s/Docker) | System total RAM |
| Disk | `statfs()` on queue path | Profile default |

**CPU-based derivations:**

| Field | Formula | Example (8 CPU) |
|-------|---------|-----------------|
| QueueWorkers | NumCPU × 2 | 16 |
| ExportConcurrency | NumCPU × 4 | 32 |
| PreparerCount | NumCPU | 8 |
| SenderCount | NumCPU × 2 | 16 |
| GlobalSendLimit | NumCPU × 8 | 64 |

**Memory-based derivations:**

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

## Cardinality Tracker Modes

Each profile sets a default cardinality tracking mode calibrated to its resource envelope:

| Profile | Mode | Why |
|---------|------|-----|
| `minimal` | `exact` | Accurate at small scale, low series count in dev |
| `balanced` | `bloom` | 98% memory savings vs exact at 100k+ series |
| `performance` | `hybrid` | Auto-switches Bloom to HLL for cardinality explosions |

### Hybrid Mode — Memory Savings & Auto-Switching

Hybrid mode gives the best of both worlds: **accurate membership testing** (Bloom phase) at low-to-moderate cardinality, and **constant ~12 KB memory** (HLL phase) when cardinality explodes.

**How it works:**

1. **Bloom phase** — Both a Bloom filter and an HLL sketch receive all inserts in parallel. Membership testing works. Memory grows linearly with cardinality.
2. **Threshold check** — When `count >= hll_threshold`, the mode switches.
3. **HLL phase** — Bloom filter is released. Only the HLL sketch remains (~12 KB constant). Membership testing is no longer available.
4. **One-way switch** — Once switched to HLL, stays there until the next `Reset()` cycle.

**Memory savings compared to exact mode:**

| Unique Series | Exact (map) | Bloom (1% FPR) | HLL | Hybrid Savings |
|---------------|-------------|-----------------|-----|----------------|
| 1,000 | 75 KB | 1.2 KB | 12 KB | 98% (Bloom phase) |
| 10,000 | 750 KB | 12 KB | 12 KB | 98% (at switch threshold) |
| 100,000 | 7.5 MB | 120 KB | 12 KB | 99.8% (HLL phase) |
| 1,000,000 | 75 MB | 1.2 MB | 12 KB | 99.98% (HLL phase) |
| 10,000,000 | 750 MB | 12 MB | 12 KB | 99.998% (HLL phase) |

**Threshold tuning — choosing the switch point:**

The `hll_threshold` controls when Bloom stops and HLL takes over. Lower = save memory earlier but lose membership testing sooner. Higher = keep accurate membership testing longer.

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

- Limits enforcement that relies on membership testing (`TestOnly`) will stop working — the limiter falls back to count-based enforcement only.
- Cardinality counts remain accurate (HLL estimates are typically within 1-2%).
- A metric `metrics_governor_cardinality_mode{mode="hll"}` is emitted so you can alert on unexpected mode switches.

**Bloom persistence with hybrid mode:** When `bloom-persistence-enabled=true`, Bloom filter state is persisted during the Bloom phase. After a restart, the tracker restores from persisted state and may switch to HLL again if cardinality re-exceeds the threshold. This avoids the "re-learning" penalty on restart.

See [Cardinality Tracking](./cardinality-tracking.md) for full configuration reference and PromQL monitoring queries.

## Resilience Tuning & Auto-Sensing

metrics-governor includes three adaptive systems that automatically tune export performance based on backend behavior. These are enabled by the `balanced` and `performance` profiles.

### Adaptive Batch Tuning (AIMD)

The batch auto-tuner uses an **Additive Increase, Multiplicative Decrease (AIMD)** algorithm — the same principle behind TCP congestion control — to find the optimal batch size for your backend.

**How it works:**

1. **Start** at the profile's default batch size (e.g., 500 for `balanced`)
2. **On success**: increase batch size by a fixed increment (`batch_size += increase_step`)
3. **On failure** (timeout, 5xx, connection error): halve the batch size (`batch_size /= 2`)
4. **Bounds**: never go below `min_batch_size` or above `max_batch_size`

**Why AIMD:** Linear increase probes capacity gently. Multiplicative decrease backs off aggressively when the backend is struggling. Over time, the batch size oscillates around the optimal point — the largest size the backend can handle without errors.

```yaml
buffer:
  batch_auto_tune:
    enabled: true               # default in balanced/performance profiles
    min_batch_size: 100         # floor
    max_batch_size: 5000        # ceiling
    increase_step: 50           # additive increase per success
    decrease_ratio: 0.5         # multiplicative decrease on failure
    eval_interval: 10s          # re-evaluate every 10s
```

**Metrics:**
- `metrics_governor_batch_tuner_current_size` — current auto-tuned batch size
- `metrics_governor_batch_tuner_adjustments_total{direction}` — increase/decrease count

### Adaptive Worker Scaling (AIMD)

The worker scaler dynamically adjusts the number of export worker goroutines based on queue depth and export latency.

**How it works:**

1. **Monitor** queue depth and export success/failure rates
2. **Scale up**: when queue depth exceeds threshold, add workers (additive increase)
3. **Scale down**: when queue is draining and workers are idle, remove workers (multiplicative decrease)
4. **Bounds**: respects `min_workers` and `max_workers` derived from `--parallelism` or NumCPU

```yaml
exporter:
  queue:
    adaptive_workers:
      enabled: true             # default in balanced/performance profiles
      min_workers: 2            # floor (auto-derived from NumCPU)
      max_workers: 64           # ceiling (auto-derived from NumCPU × 8)
      scale_up_threshold: 100   # queue depth to trigger scale-up
      scale_down_threshold: 10  # queue depth to trigger scale-down
      eval_interval: 5s         # re-evaluate interval
```

**Metrics:**
- `metrics_governor_workers_active` — current active workers
- `metrics_governor_workers_target` — target worker count from scaler
- `metrics_governor_scaler_adjustments_total{direction}` — scale up/down events

### Circuit Breaker with Auto-Reset

The circuit breaker protects downstream backends from being overwhelmed during failures. It follows the standard pattern:

```
CLOSED → (failures >= threshold) → OPEN → (reset_timeout elapsed) → HALF-OPEN → (success) → CLOSED
                                                                   → (failure) → OPEN
```

**Auto-reset behavior:** After `reset_timeout` elapses, the circuit breaker enters HALF-OPEN state and allows a single probe request through. If the probe succeeds, the circuit closes and normal traffic resumes. If it fails, the circuit re-opens for another `reset_timeout` period.

**Combined with adaptive scaling:** When the circuit breaker opens, the worker scaler automatically scales down to `min_workers`. When the circuit closes, workers scale back up based on queue depth. This prevents resource waste during backend outages.

### Resilience Level Presets

The `--resilience-level` flag bundles all resilience settings into a single knob:

| Setting | `low` | `medium` (balanced) | `high` (performance) |
|---------|-------|---------------------|----------------------|
| Backoff multiplier | 1.5× | 2.0× | 3.0× |
| Circuit breaker | off | on (5 failures) | on (3 failures) |
| CB reset timeout | 60s | 30s | 15s |
| Batch drain size | 5 | 10 | 25 |
| Burst drain size | 50 | 100 | 250 |
| Adaptive workers | on | on | on |
| Batch auto-tune | on | on | on |

**When to use each level:**

- **`low`**: Backend is reliable but you want basic retry logic. Suitable for same-datacenter backends with low latency.
- **`medium`**: Production default. Protects against transient failures and backend restarts.
- **`high`**: For unreliable networks (cross-region, internet-facing) or backends with known capacity limits.

## Kubernetes Deployment

```yaml
# Helm values.yaml — minimal config with balanced profile
metricsGovernor:
  config:
    profile: balanced
    exporter:
      endpoint: "otel-collector:4317"
  resources:
    requests:
      cpu: "500m"
      memory: "256Mi"
    limits:
      cpu: "2"
      memory: "1Gi"
```

### HPA & VPA — Autoscaling Best Practices

**Do NOT use HPA and VPA simultaneously on the same resource (CPU or memory).** This is a Kubernetes-level conflict — both controllers will fight to set the resource requests, causing oscillation, thrashing, and unpredictable behavior.

**Recommended patterns:**

#### Pattern 1: HPA for CPU + VPA for Memory (recommended)

Use HPA to scale horizontally based on CPU utilization, and VPA to right-size memory requests. Configure VPA in `Auto` mode for memory only, and disable VPA's CPU recommendations.

```yaml
# HPA — scales pods based on CPU
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: metrics-governor
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: metrics-governor
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 75
  behavior:
    scaleDown:
      stabilizationWindowSeconds: 300    # 5 min cooldown before scale-down
      policies:
        - type: Pods
          value: 1
          periodSeconds: 60              # remove max 1 pod per minute
    scaleUp:
      stabilizationWindowSeconds: 30
      policies:
        - type: Pods
          value: 2
          periodSeconds: 60              # add max 2 pods per minute
---
# VPA — right-sizes memory only
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: metrics-governor
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: metrics-governor
  updatePolicy:
    updateMode: "Auto"
  resourcePolicy:
    containerPolicies:
      - containerName: metrics-governor
        controlledResources: ["memory"]   # VPA manages memory only
        minAllowed:
          memory: "256Mi"
        maxAllowed:
          memory: "4Gi"
```

#### Pattern 2: VPA in Recommendation-Only Mode

Use VPA in `Off` mode to get sizing recommendations without any automatic changes. Review recommendations and update resource requests manually or in your CI pipeline.

```yaml
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
    updateMode: "Off"                     # recommendations only, no changes
  resourcePolicy:
    containerPolicies:
      - containerName: metrics-governor
        minAllowed:
          cpu: "100m"
          memory: "128Mi"
        maxAllowed:
          cpu: "4"
          memory: "8Gi"
```

Check recommendations:

```bash
kubectl get vpa metrics-governor-vpa -o jsonpath='{.status.recommendation}'
```

Example output:

```json
{
  "containerRecommendations": [{
    "containerName": "metrics-governor",
    "lowerBound":  {"cpu": "250m", "memory": "512Mi"},
    "target":      {"cpu": "1",    "memory": "1Gi"},
    "upperBound":  {"cpu": "2",    "memory": "2Gi"}
  }]
}
```

#### Pattern 3: HPA Only (simplest)

When VPA is not installed or you want maximum simplicity:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: metrics-governor
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: metrics-governor
  minReplicas: 2
  maxReplicas: 8
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```

**Why metrics-governor works well with HPA:** The auto-derivation engine detects cgroup CPU quota changes after a pod reschedule. When HPA scales pods up, each new pod detects its CPU allocation and derives worker counts automatically. No config changes needed.

**Conflict summary:**

| Scenario | Works? | Notes |
|----------|--------|-------|
| HPA (CPU) + VPA (Memory only, `Auto`) | Yes | Recommended. No conflict. |
| HPA (CPU) + VPA (`Off` / recommendations) | Yes | Safe. VPA only reports. |
| HPA (CPU) + VPA (CPU, `Auto`) | **No** | Both fight over CPU requests. Oscillation. |
| HPA + VPA (`Auto`, both resources) | **No** | Both fight over requests. Unpredictable. |
| VPA only (`Auto`, both resources) | Yes | No HPA conflict, but no horizontal scaling. |

### Deployment vs StatefulSet

**Use a Deployment (default)** for most scenarios. metrics-governor is stateless when using a memory queue — each pod is interchangeable.

**Use a StatefulSet** when:

- Using the **disk queue** (`performance` profile) — each pod needs its own PersistentVolumeClaim
- Using **bloom persistence** — each pod persists its own Bloom filter state to disk
- You need **stable network identities** for monitoring (predictable pod names like `metrics-governor-0`, `metrics-governor-1`)

```yaml
# StatefulSet for performance profile with disk queue
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: metrics-governor
spec:
  serviceName: metrics-governor
  replicas: 3
  selector:
    matchLabels:
      app: metrics-governor
  template:
    metadata:
      labels:
        app: metrics-governor
    spec:
      containers:
        - name: metrics-governor
          image: metrics-governor:latest
          args:
            - "--profile=performance"
            - "--exporter-endpoint=otel-collector:4317"
            - "--queue-path=/data/queue"
          volumeMounts:
            - name: queue-data
              mountPath: /data/queue
  volumeClaimTemplates:
    - metadata:
        name: queue-data
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: gp3-ssd
        resources:
          requests:
            storage: 10Gi
```

### Traffic Distribution & Topology-Aware Routing

Kubernetes 1.27+ supports `trafficDistribution` on Services to prefer same-zone routing, reducing cross-zone data transfer costs and latency.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: metrics-governor
  annotations:
    # For older K8s versions (<1.27), use the annotation:
    # service.kubernetes.io/topology-mode: Auto
spec:
  trafficDistribution: PreferClose    # K8s 1.31+ (GA)
  selector:
    app: metrics-governor
  ports:
    - name: grpc
      port: 4317
      targetPort: 4317
    - name: http
      port: 4318
      targetPort: 4318
```

**Why this matters for metrics:** Metrics pipelines generate significant cross-zone traffic. A 100k dps workload at ~200 bytes/datapoint generates ~20 MB/s. With 3 availability zones, ~66% of traffic is cross-zone without topology awareness. Enabling `PreferClose` can cut cross-zone costs by 60-80%.

**Topology spread constraints for even distribution:**

```yaml
spec:
  topologySpreadConstraints:
    - maxSkew: 1
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: ScheduleAnyway
      labelSelector:
        matchLabels:
          app: metrics-governor
```

### DaemonSet Mode

For node-level metrics collection (similar to a sidecar per node), deploy metrics-governor as a DaemonSet. Each node gets one pod that collects and forwards metrics from all workloads on that node.

**Best for:**

- Node-level metric aggregation (kubelet, cAdvisor, node-exporter)
- Reducing exporter fan-out (100 pods on a node → 1 governor per node → 1 connection to backend)
- Edge/IoT deployments where each node runs autonomously

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: metrics-governor
spec:
  selector:
    matchLabels:
      app: metrics-governor
  template:
    metadata:
      labels:
        app: metrics-governor
    spec:
      containers:
        - name: metrics-governor
          image: metrics-governor:latest
          args:
            - "--profile=minimal"
            - "--exporter-endpoint=central-collector:4317"
          resources:
            requests:
              cpu: "100m"
              memory: "128Mi"
            limits:
              cpu: "500m"
              memory: "256Mi"
          ports:
            - containerPort: 4317
              hostPort: 4317          # expose on node for local collectors
            - containerPort: 4318
              hostPort: 4318
      tolerations:
        - operator: Exists            # schedule on all nodes including masters
```

**DaemonSet sizing:** Use the `minimal` profile for DaemonSet deployments. Each pod handles only its node's traffic (~1-5k dps typically), so the minimal resource envelope (0.25 CPU, 128 MB) is sufficient. If nodes have high-throughput workloads, switch to `balanced`.

**DaemonSet vs Deployment trade-offs:**

| Aspect | Deployment (+ HPA) | DaemonSet |
|--------|--------------------|-----------|
| Scaling | Horizontal (pod count) | Fixed (1 per node) |
| Resource efficiency | Shared pool | Dedicated per node |
| Failure blast radius | Spreads across pods | Limited to 1 node |
| Network hops | Extra hop to governor pod | Local (same node) |
| Config complexity | Central | Per-node (unless uniform) |
| Best for | Centralized collection | Node-local aggregation |

## Bare Metal / VM Deployment

```ini
# systemd unit: /etc/systemd/system/metrics-governor.service
[Unit]
Description=Metrics Governor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=metrics-governor
ExecStart=/usr/local/bin/metrics-governor \
  --profile=balanced \
  --exporter-endpoint=otel-collector:4317
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

The auto-derivation engine detects cgroup memory limits and `runtime.NumCPU()` to size internal components automatically.

**Differences from Kubernetes:**

| Aspect | Kubernetes | VM / Bare Metal |
|--------|-----------|----------------|
| Memory detection | cgroup v2 limit (`memory.max`) | System total RAM or systemd `MemoryMax` |
| CPU detection | cgroup v2 CPU quota | `runtime.NumCPU()` |
| Disk detection | PVC mount at queue path | `statfs()` on queue directory |
| Memory limit enforcement | OOM kill by kubelet | OOM kill by systemd `MemoryMax` |
| Scaling | HPA (horizontal) | Manual or external autoscaler |
| Config reload | ConfigMap + sidecar SIGHUP | File edit + `kill -HUP <pid>` |

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

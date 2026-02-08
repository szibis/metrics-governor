# metrics-governor Alert Rules & Runbooks

## Table of Contents

- [Quick Start](#quick-start)
- [Alert Design](#alert-design)
- [Alert Coverage Map](#alert-coverage-map)
- [Profile-Specific Thresholds](#profile-specific-thresholds)
- [Helm Chart Integration](#helm-chart-integration)
  - [Enabling Alerts](#enabling-alerts)
  - [Threshold Reference — What Changes What](#threshold-reference--what-changes-what)
  - [Disabling Alerts by Profile](#disabling-alerts-by-profile)
  - [Adding Custom Alerts](#adding-custom-alerts)
- [Playground — Interactive Threshold Tuning](#playground--interactive-threshold-tuning)
- [Runbooks](#runbooks)
  - [MetricsGovernorDown](#metricsgovernordown)
  - [MetricsGovernorDataLoss](#metricsgovernordataloss)
  - [MetricsGovernorExportDegraded](#metricsgovernorexportdegraded)
  - [MetricsGovernorQueueSaturated](#metricsgovernorqueuesaturated)
  - [MetricsGovernorCircuitOpen](#metricsgovernorcircuitopen)
  - [MetricsGovernorOOMRisk](#metricsgovernoroomrisk)
  - [MetricsGovernorBackpressure](#metricsgovernorbackpressure)
  - [MetricsGovernorWorkersSaturated](#metricsgovernorworkerssaturated)
  - [MetricsGovernorCardinalityExplosion](#metricsgovernorcardinalityexplosion)
  - [MetricsGovernorConfigStale](#metricsgovernorconfigstale)

---

## Quick Start

**Prometheus / VictoriaMetrics / Thanos:**

```bash
cp alerts/prometheus-rules.yaml /etc/prometheus/rules/metrics-governor.yaml
# Reload: kill -HUP $(pidof prometheus)
```

**Kubernetes (Prometheus Operator):**

```bash
kubectl apply -f alerts/prometheusrule.yaml
```

**Helm chart:**

```yaml
# values.yaml
alerting:
  enabled: true
  # Override any threshold — see Profile-Specific Thresholds below
```

**Playground** — generate profile-tuned rules with the alerts tab in `tools/playground/index.html`.

---

## Alert Design

### Principles

1. **10 alerts, zero overlap** — each alert fires for a distinct failure mode. During a backend outage, you get `ExportDegraded` (error rate), then `CircuitOpen` (CB trips), then `QueueSaturated` (queue fills) — a clear escalation chain, never the same signal twice.

2. **Signal chain, not symptom soup** — alerts are ordered by severity and timing:

```
Leading indicators (warn early)     Trailing indicators (act now)
─────────────────────────────────   ─────────────────────────────
ExportDegraded ──→ CircuitOpen ──→ QueueSaturated ──→ DataLoss
Backpressure ──→ WorkersSaturated
CardinalityExplosion
                                    OOMRisk ──→ Down
```

3. **Every alert is actionable** — each runbook has: what fired, what it means, exact commands to investigate, concrete fix actions, and prevention steps.

4. **Profile-aware** — thresholds vary by profile. The `minimal` profile doesn't have a queue, so `QueueSaturated` and `CircuitOpen` are N/A.

### What Each Alert Detects

| # | Alert | Severity | Detects | Fire Condition |
|---|-------|----------|---------|----------------|
| 1 | `Down` | critical | Process unavailable | `up == 0` for 2m |
| 2 | `DataLoss` | critical | Permanent data loss | `data_loss_total` increasing |
| 3 | `ExportDegraded` | warning | Backend errors | Error rate > 10% for 5m |
| 4 | `QueueSaturated` | warning | Queue filling up | Queue > 85% for 5m |
| 5 | `CircuitOpen` | warning | Backend protection active | CB open for 2m |
| 6 | `OOMRisk` | critical | Memory pressure | Heap > 90% limit for 5m |
| 7 | `Backpressure` | warning | Pipeline bottleneck | Buffer rejecting > 1/s for 5m |
| 8 | `WorkersSaturated` | warning | CPU/export ceiling | Workers > 90% for 10m |
| 9 | `CardinalityExplosion` | warning | Governance breach | Cardinality violations for 10m |
| 10 | `ConfigStale` | info | Operational drift | No reload in 24h |

---

## Alert Coverage Map

Each alert maps to one reliability dimension. No dimension is covered by more than 2 alerts, and no 2 alerts fire for the same root cause.

| Dimension | Alerts | Why Not Fewer |
|-----------|--------|---------------|
| **Availability** | `Down` | Single signal — process is either up or down |
| **Reliability** | `DataLoss`, `ExportDegraded` | `ExportDegraded` is a leading indicator (recoverable), `DataLoss` is trailing (permanent). Different severity, different action. |
| **Resilience** | `QueueSaturated`, `CircuitOpen` | Queue capacity vs circuit state — independent signals. CB can be open with queue empty (recent failure), queue can fill without CB (CB disabled or not yet tripped). |
| **Performance** | `Backpressure`, `WorkersSaturated`, `CardinalityExplosion` | Buffer stage vs export stage vs governance — three independent bottleneck points. |
| **Resource** | `OOMRisk` | Single signal — memory is the binding constraint (CPU issues surface as WorkersSaturated). |
| **Operational** | `ConfigStale` | Single signal — config freshness. |

---

## Profile-Specific Thresholds

Different profiles have different operating envelopes. Adjust thresholds accordingly:

| Alert | `minimal` | `balanced` | `performance` |
|-------|-----------|-----------|---------------|
| `Down` for | 2m | 2m | 2m |
| `DataLoss` rate | > 0 | > 0 | > 0 |
| `ExportDegraded` error % | > 10% | > 10% | > 5% |
| `QueueSaturated` % | N/A (no queue) | > 85% | > 90% |
| `CircuitOpen` for | N/A (CB off) | 2m | 1m |
| `OOMRisk` heap % | > 85% | > 90% | > 85% |
| `Backpressure` rate/s | > 0.5 | > 1 | > 5 |
| `WorkersSaturated` % | N/A (1 worker) | > 90% | > 95% |
| `CardinalityExplosion` for | 5m | 10m | 15m |
| `ConfigStale` hours | N/A (dev) | 24h | 12h |

**Why thresholds differ:**
- `minimal` has no queue or circuit breaker — those alerts are disabled
- `performance` has larger buffers and more workers — tighter thresholds catch issues before they cascade
- `performance` queue at 90% still has 200 MB headroom (vs 38 MB at 85% for balanced's 256 MB queue)

---

## Helm Chart Integration

### Enabling Alerts

Add to your `values.yaml`:

```yaml
alerting:
  enabled: true
  # Must match your Prometheus Operator's ruleSelector
  additionalLabels:
    prometheus: kube-prometheus
```

This creates a `PrometheusRule` CRD in your namespace with all 10 alerts using sensible defaults. The Prometheus Operator auto-discovers it.

### Threshold Reference — What Changes What

Each `alerting.thresholds.*` value in the Helm chart maps to exactly one alert. This table shows what to change, how it affects the alert expression, and which runbook to read when the alert fires.

| Helm Value | Type | Default | Alert It Changes | Effect on Expression | Runbook |
|-----------|------|---------|-----------------|---------------------|---------|
| `alerting.thresholds.exportErrorRate` | float (0-1) | `0.1` | **ExportDegraded** | Fire when `error_rate > value` (e.g., 0.1 = 10%) | [ExportDegraded](#metricsgovernorexportdegraded) |
| `alerting.thresholds.queueUtilization` | float (0-1) | `0.85` | **QueueSaturated** | Fire when `queue_bytes/max_bytes > value` | [QueueSaturated](#metricsgovernorqueuesaturated) |
| `alerting.thresholds.memoryUsage` | float (0-1) | `0.90` | **OOMRisk** | Fire when `heap_alloc/memory_limit > value` | [OOMRisk](#metricsgovernoroomrisk) |
| `alerting.thresholds.backpressureRate` | float (events/s) | `1` | **Backpressure** | Fire when `reject_rate + evict_rate > value` | [Backpressure](#metricsgovernorbackpressure) |
| `alerting.thresholds.workerUtilization` | float (0-1) | `0.90` | **WorkersSaturated** | Fire when `active_workers/total_workers > value` | [WorkersSaturated](#metricsgovernorworkerssaturated) |
| `alerting.thresholds.configStaleness` | int (seconds) | `86400` | **ConfigStale** | Fire when `time() - last_reload > value` | [ConfigStale](#metricsgovernorconfigstale) |

**Alerts without configurable thresholds** (fixed expressions):

| Alert | Expression | Why Not Configurable | Runbook |
|-------|-----------|---------------------|---------|
| **Down** | `up == 0 for 2m` | Binary — process is either up or down | [Down](#metricsgovernordown) |
| **DataLoss** | `data_loss_total > 0` | Any data loss is critical — no safe threshold | [DataLoss](#metricsgovernordataloss) |
| **CircuitOpen** | `state == open for 2m` | Binary — CB is either open or closed | [CircuitOpen](#metricsgovernorcircuitopen) |
| **CardinalityExplosion** | `violations > 0 for 10m` | Any sustained violation is a governance issue | [CardinalityExplosion](#metricsgovernorcardinalityexplosion) |

### Example: Tuned for Performance Profile

```yaml
alerting:
  enabled: true
  additionalLabels:
    prometheus: kube-prometheus
  thresholds:
    exportErrorRate: 0.05      # Tighter: 5% (default 10%)
    queueUtilization: 0.90     # Looser: 90% (queue is large, 2 GB)
    memoryUsage: 0.85          # Tighter: 85% (more memory headroom)
    backpressureRate: 5        # Looser: 5/s (high throughput is expected)
    workerUtilization: 0.95    # Looser: 95% (many workers, near-full is OK)
    configStaleness: 43200     # Tighter: 12h (production ops check frequently)
```

### Example: Tuned for Minimal Profile

```yaml
alerting:
  enabled: true
  thresholds:
    memoryUsage: 0.85          # Tighter: small memory budget
    backpressureRate: 0.5      # Tighter: any rejection is concerning at low volume
  # Disable alerts that don't apply to minimal profile (no queue, no CB, 1 worker)
  disabledAlerts:
    - MetricsGovernorQueueSaturated
    - MetricsGovernorCircuitOpen
    - MetricsGovernorWorkersSaturated
    - MetricsGovernorConfigStale
```

### Disabling Alerts by Profile

Use `alerting.disabledAlerts` to skip alerts that don't apply:

| Profile | Recommended `disabledAlerts` | Why |
|---------|------------------------------|-----|
| `minimal` | `QueueSaturated`, `CircuitOpen`, `WorkersSaturated`, `ConfigStale` | No queue, no CB, single worker, dev environment |
| `balanced` | (none — all apply) | Full feature set enabled |
| `performance` | (none — all apply) | Full feature set enabled |

### Adding Custom Alerts

Append custom rules via `alerting.additionalRules`:

```yaml
alerting:
  enabled: true
  additionalRules:
    - alert: MetricsGovernorDiskQueueFull
      expr: |
        metrics_governor_queue_disk_available_bytes < 1073741824
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "metrics-governor disk queue < 1 GB free"
```

---

## Playground — Interactive Threshold Tuning

The **Alert rules** tab in the playground (`tools/playground/index.html`) lets you:

1. **Adjust thresholds** — sliders for each configurable parameter, live preview of the YAML
2. **Toggle format** — switch between Prometheus rules YAML and Kubernetes PrometheusRule CRD
3. **See the coverage map** — table showing which alert each threshold affects
4. **Copy or download** — ready to import into Prometheus or `kubectl apply`

**How to use:**
1. Open `tools/playground/index.html` in your browser
2. Click the **"Alert rules"** tab in section 4 (Config Preview & Export)
3. Expand **"Threshold Tuning"** to adjust values
4. The YAML preview updates live
5. Toggle **"Kubernetes PrometheusRule CRD format"** for K8s deployment
6. Click **"Copy to Clipboard"** or **"Download"**

The playground generates the same alert rules as the Helm chart template, with your custom thresholds embedded directly in the expressions.

---

## Runbooks

### MetricsGovernorDown

**Severity:** Critical
**Fires when:** `up{job=~".*metrics-governor.*"} == 0` for 2 minutes.

**What it means:** The metrics-governor process is not responding to Prometheus scrapes. All metrics ingestion is stopped.

**Impact:** Complete metrics pipeline outage. No data flows to the backend. If other services buffer locally, data may be recoverable after restart. Otherwise, data is lost for the duration.

**Investigation:**

```bash
# 1. Check pod status (Kubernetes)
kubectl get pods -l app.kubernetes.io/name=metrics-governor
kubectl describe pod <pod-name>

# 2. Check for OOM kills
kubectl get events --field-selector reason=OOMKilled --sort-by=.lastTimestamp

# 3. Check logs for crash reason
kubectl logs <pod-name> --previous --tail=100

# 4. Check node status (is the node healthy?)
kubectl get node <node-name> -o wide

# Bare metal:
systemctl status metrics-governor
journalctl -u metrics-governor --since "5 minutes ago" --no-pager
dmesg | grep -i "oom\|killed" | tail -5
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| OOM killed | Increase memory limit or switch to a higher profile |
| CrashLoopBackOff | Check logs for startup errors (bad config, port conflict, missing PVC) |
| Node pressure | Check node resources, consider PDB to prevent eviction |
| Failed readiness probe | Check `/ready` endpoint, may be downstream dependency issue |
| Bad deployment | Roll back: `kubectl rollout undo deployment/metrics-governor` |

**Prevention:**
- Set `memory.limit_ratio: 0.85` to trigger GC before OOM
- Enable PDB with `minAvailable: 1`
- Use the `OOMRisk` alert to catch memory pressure before crash

---

### MetricsGovernorDataLoss

**Severity:** Critical
**Fires when:** `rate(metrics_governor_export_data_loss_total[5m]) > 0` for 1 minute.

**What it means:** Batches failed to export AND failed to push to the queue. Data is permanently lost — it cannot be recovered.

**Impact:** Gaps in metrics data. Dashboards will show missing intervals. SLO calculations may be affected.

**Investigation:**

```bash
# 1. Check export errors (why is the backend rejecting?)
curl -s http://<governor>:9090/metrics | grep 'otlp_export_errors_total'

# 2. Check queue status (why can't data be queued?)
curl -s http://<governor>:9090/metrics | grep -E 'queue_bytes|queue_max_bytes|queue_dropped'

# 3. Check disk space (for disk queue)
kubectl exec <pod> -- df -h /data/queue

# 4. Check if queue is disabled
curl -s http://<governor>:9090/metrics | grep 'queue_max_bytes'
# If 0, queue is disabled — switch to balanced or performance profile

# 5. Check backend health
curl -s http://<backend>:4317/health
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Queue disabled (minimal profile) | Switch to `balanced` or `performance` profile to enable queue buffering |
| Queue full + backend down | Increase `queue.max_bytes` or add disk. Fix backend. |
| Disk full (disk queue) | Expand PVC: `kubectl edit pvc data-metrics-governor-0` |
| Both export and queue failing | Check network connectivity to backend |
| Memory queue OOM | Switch to disk queue (`queue.type: disk`) |

**Prevention:**
- Enable persistent queue (at minimum, `balanced` profile)
- Size queue for your outage buffer target: `throughput × outage_duration / compression_ratio`
- Monitor `QueueSaturated` alert as leading indicator

---

### MetricsGovernorExportDegraded

**Severity:** Warning
**Fires when:** Export error rate > 10% sustained for 5 minutes.

**What it means:** The backend is returning errors for a significant portion of export requests. The queue is absorbing the failed batches, so no data loss yet — but if this continues, the queue will fill up.

**Impact:** Delayed metric delivery. Data is safe in queue (if enabled) but not visible in dashboards until the backend recovers.

**Investigation:**

```bash
# 1. Identify error types
curl -s http://<governor>:9090/metrics | grep 'otlp_export_errors_total'
# Look at error_type label: network, timeout, server_error, client_error, auth, rate_limit

# 2. Check export latency
curl -s http://<governor>:9090/metrics | grep 'queue_export_latency_ewma'

# 3. Check backend logs
kubectl logs -l app=otel-collector --tail=50

# 4. Check network
kubectl exec <pod> -- curl -s -o /dev/null -w "%{http_code}" http://<backend>:4317/
```

**Resolution:**

| Error Type | Fix |
|-----------|-----|
| `network` | Check DNS, Service, NetworkPolicy. Is the backend reachable? |
| `timeout` | Increase `exporter.timeout` or use `--export-timeout` consolidated param |
| `server_error` (5xx) | Backend is overloaded. Scale backend or reduce throughput. |
| `client_error` (4xx) | Check payload format. HTTP 413 = batch too large (batch tuner will auto-adjust). |
| `auth` (401/403) | Check TLS certs, API keys, mTLS configuration. |
| `rate_limit` (429) | Backend is throttling. Enable backoff. Reduce send concurrency. |

**Prevention:**
- Use `balanced` or `performance` profile (enables adaptive batch tuning and circuit breaker)
- Configure backoff: `queue.backoff.enabled: true`
- Monitor batch tuner metrics: if `batch_hard_ceiling_bytes` is set, the backend has a size limit

---

### MetricsGovernorQueueSaturated

**Severity:** Warning
**Fires when:** Queue utilization > 85% for 5 minutes.

**What it means:** The queue is filling faster than it can drain. If the backend doesn't recover before the queue reaches 100%, data will be dropped (policy: `drop_oldest`) or rejected (policy: `reject`).

**Impact:** Imminent data loss if trend continues. Time to act depends on fill rate.

**Investigation:**

```bash
# 1. Check fill rate (how fast is it growing?)
curl -s http://<governor>:9090/metrics | grep -E 'queue_bytes|queue_max_bytes|queue_utilization'

# 2. Estimate time to full
# queue_fill_rate = rate(queue_bytes[5m])
# time_to_full = (queue_max_bytes - queue_bytes) / queue_fill_rate

# 3. Check why queue isn't draining (export errors?)
curl -s http://<governor>:9090/metrics | grep 'export_errors_total'

# 4. Check circuit breaker state
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_state'

# 5. Check disk space (for disk queue)
kubectl exec <pod> -- df -h /data/queue
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Backend down | Fix backend first. Queue will auto-drain on recovery. |
| Queue too small | Increase `queue.max_bytes`. For disk queue, expand PVC. |
| Fill rate > drain rate | Scale backend, reduce throughput, or add more export workers. |
| Disk full | Expand storage. Consider switching `full_behavior: drop_oldest` to discard old data. |

**Prevention:**
- Size queue for at minimum 30 minutes of outage buffer
- Use `performance` profile with disk queue for critical workloads
- Monitor export error rate — fix backend issues before queue fills

---

### MetricsGovernorCircuitOpen

**Severity:** Warning
**Fires when:** Circuit breaker in `open` state for 2 minutes.

**What it means:** The circuit breaker tripped after detecting `threshold` consecutive export failures. All exports are blocked to protect the backend from being overwhelmed. Data is accumulating in the queue.

**Impact:** No data reaching backend. Queue is absorbing everything. The circuit breaker will auto-probe after `reset_timeout` — if the probe succeeds, normal operation resumes.

**Investigation:**

```bash
# 1. How long has circuit been open?
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_state'

# 2. How many times has it opened?
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_open_total'

# 3. What's the backoff delay?
curl -s http://<governor>:9090/metrics | grep 'queue_backoff_seconds'

# 4. Check backend health directly
kubectl exec <pod> -- curl -v http://<backend>:4317/

# 5. Check queue capacity (how much time do we have?)
curl -s http://<governor>:9090/metrics | grep -E 'queue_bytes|queue_max_bytes'
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Backend maintenance | Wait for maintenance to complete. Circuit will auto-close. |
| Backend crash | Restart backend. Circuit probes every `reset_timeout`. |
| Network partition | Fix connectivity. Check DNS, Service, firewall rules. |
| Persistent backend overload | Scale backend, reduce throughput, or switch to `drop_oldest` buffer policy. |

**If circuit won't close (keeps re-opening):**
- Temporarily increase `circuit_breaker.threshold` (e.g., to 20)
- Or lower `resilience_level` to `low` (disables CB)
- Fix root cause, then restore original settings

**Prevention:**
- Use `resilience_level: high` for aggressive protection with fast recovery
- Size queue to handle expected outage duration

---

### MetricsGovernorOOMRisk

**Severity:** Critical
**Fires when:** Heap memory > 90% of memory limit for 5 minutes.

**What it means:** The Go runtime is under severe memory pressure. If heap grows further, the container will be OOM-killed by the kubelet (K8s) or systemd (bare metal).

**Impact:** Imminent process crash. On crash, in-flight data and memory queue contents are lost. Disk queue data survives.

**Investigation:**

```bash
# 1. Check current memory
curl -s http://<governor>:9090/metrics | grep -E 'heap_alloc_bytes|memory_limit_bytes|memory_sys_bytes'

# 2. Check what's consuming memory
curl -s http://<governor>:9090/metrics | grep -E 'intern_pool_estimated|cardinality_memory|compression_pool_estimated|queue_bytes|buffer_bytes'

# 3. Check GC pressure
curl -s http://<governor>:9090/metrics | grep -E 'gc_cycles_total|gc_pause_total|gc_cpu_percent'

# 4. Check cardinality (high cardinality = high memory)
curl -s http://<governor>:9090/metrics | grep 'rule_group_cardinality'

# 5. Check for memory leaks (steadily growing over hours)
# Plot go_memstats_heap_alloc_bytes over 24h — should be sawtooth, not linear
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| High cardinality | Tighten limits rules. Switch to `bloom` or `hybrid` cardinality mode. |
| String interning table | Set `performance.string_interning_max_size` to cap table size |
| Large buffer | Reduce `buffer.size` or `buffer.memory_percent` |
| Memory queue too large | Reduce `queue.memory_percent` or switch to disk queue |
| Container limit too low | Increase memory limit, use VPA for right-sizing |
| Memory leak (rare) | Report a bug with heap profile: `go tool pprof http://<governor>:9090/debug/pprof/heap` |

**Prevention:**
- Set `memory.limit_ratio: 0.85` (balanced) or `0.80` (performance)
- Use `--memory-budget-percent` to control buffer + queue memory split
- Enable bloom cardinality mode for high-cardinality workloads

---

### MetricsGovernorBackpressure

**Severity:** Warning
**Fires when:** Buffer rejecting or evicting > 1 batch/sec for 5 minutes.

**What it means:** Incoming data rate exceeds the buffer's processing capacity. Depending on the `buffer_full_policy`:
- `reject` — sender gets an error, data is not accepted (back-pressure to upstream)
- `drop_oldest` — oldest buffered data is evicted to make room (silent data loss)

**Impact:** Data loss (drop_oldest) or upstream errors (reject). The bottleneck is between receiver and export pipeline.

**Investigation:**

```bash
# 1. Check buffer utilization
curl -s http://<governor>:9090/metrics | grep -E 'buffer_bytes|buffer_max_bytes|buffer_rejected|buffer_evictions'

# 2. Check incoming rate vs processing rate
curl -s http://<governor>:9090/metrics | grep -E 'receiver_datapoints_total|otlp_export_datapoints_total'

# 3. Check if workers are keeping up
curl -s http://<governor>:9090/metrics | grep -E 'workers_active|workers_total'

# 4. Check batch tuner (is batch size too large, causing slow processing?)
curl -s http://<governor>:9090/metrics | grep 'batch_current_max_bytes'
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Throughput exceeds capacity | Switch to higher profile, add more workers (`--parallelism`), or scale out (HPA) |
| Buffer too small | Increase `buffer.size` or `buffer.memory_percent` |
| Slow exports blocking buffer | Enable pipeline split to decouple compression from sending |
| Burst traffic | Enable queue (`balanced` profile) to absorb spikes |
| Single worker bottleneck | Switch from `minimal` to `balanced` (enables adaptive workers) |

**Prevention:**
- Use `balanced` or `performance` profile (adaptive workers self-tune)
- Size buffer for at least 2x expected burst size
- Enable HPA to scale out during sustained high throughput

---

### MetricsGovernorWorkersSaturated

**Severity:** Warning
**Fires when:** Worker utilization > 90% for 10 minutes.

**What it means:** Nearly all export workers are busy. The adaptive worker scaler has either hit its `max_workers` ceiling or is disabled. The export pipeline is at capacity.

**Impact:** Queue may start growing because workers can't drain fast enough. This is a capacity ceiling — not an error, but a signal that you need more resources.

**Investigation:**

```bash
# 1. Check worker counts
curl -s http://<governor>:9090/metrics | grep -E 'workers_active|workers_total|workers_desired'

# 2. Is adaptive scaling enabled?
curl -s http://<governor>:9090/metrics | grep 'scaler_adjustments_total'
# If no results, adaptive workers are disabled

# 3. Check if we hit max_workers
curl -s http://<governor>:9090/metrics | grep 'workers_desired'
# If desired == total, scaler has hit ceiling

# 4. Check export latency (are workers slow or just numerous?)
curl -s http://<governor>:9090/metrics | grep 'export_latency_ewma'

# 5. Check pipeline split (is compression the bottleneck?)
curl -s http://<governor>:9090/metrics | grep -E 'preparers_active|senders_active|prepared_channel_length'
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Max workers reached | Increase `adaptive_workers.max_workers` or `--parallelism` |
| Adaptive scaling disabled | Switch to `balanced` profile (enables adaptive workers) |
| Slow exports (high latency) | Fix backend latency. Workers are blocked on I/O. |
| Compression bottleneck | Enable pipeline split (`performance` profile) to separate CPU from I/O |
| Need more CPU | Scale vertically (more CPU) or horizontally (HPA) |

**Prevention:**
- Use `performance` profile for high-throughput workloads (pipeline split separates CPU/IO)
- Set HPA with CPU target at 70% to scale before saturation
- Monitor `queue_export_latency_ewma_seconds` — high latency means workers are waiting, not working

---

### MetricsGovernorCardinalityExplosion

**Severity:** Warning
**Fires when:** Cardinality limit violations sustained for 10 minutes.

**What it means:** One or more upstream services are generating metrics with unbounded label values (e.g., request IDs, timestamps, session tokens as labels). The limits enforcer is detecting and handling violations.

**Impact:** Depends on `limits.action`:
- `log` — violations are logged but data passes through (may cause backend memory issues)
- `adaptive` — excess data is dropped per fair-share algorithm (worst offender dropped first)

**Investigation:**

```bash
# 1. Which rules are violated?
curl -s http://<governor>:9090/metrics | grep 'cardinality_exceeded_total'

# 2. Which groups (services) are the worst offenders?
curl -s http://<governor>:9090/metrics | grep 'rule_group_cardinality' | sort -t' ' -k2 -rn | head -10

# 3. Check cardinality tracker memory
curl -s http://<governor>:9090/metrics | grep 'cardinality_memory_bytes'

# 4. Is hybrid mode switching? (performance profile)
curl -s http://<governor>:9090/metrics | grep 'cardinality_mode'

# 5. What are the problematic label values?
# Enable debug logging temporarily to see specific metrics being dropped
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Unbounded labels (request IDs, etc.) | Add relabeling rule to drop or aggregate the label |
| New service with high cardinality | Add a limits rule for the service with appropriate threshold |
| Limit too low for workload | Increase `default_max_cardinality` or per-rule limits |
| Bloom filter false positives | Switch to `hybrid` mode (auto-switches to HLL at high cardinality) |

**Prevention:**
- Define limits rules for all known services/namespaces
- Use relabeling to drop high-cardinality labels at ingestion
- Set `limits.action: adaptive` to auto-protect against cardinality bombs
- Monitor per-group cardinality dashboards to catch trends early

---

### MetricsGovernorConfigStale

**Severity:** Info
**Fires when:** Config not successfully reloaded in over 24 hours.

**What it means:** The limits/relabeling/sampling configuration has not been updated. This may indicate a problem with the config reload mechanism (sidecar, file watcher) or simply that no changes were made.

**Impact:** Low — if config hasn't changed, this is a false positive. If config should have changed (e.g., new limits rules deployed), the old rules are still in effect.

**Investigation:**

```bash
# 1. Check last reload time and count
curl -s http://<governor>:9090/metrics | grep -E 'config_reload_total|reload_last_success'

# 2. Check sidecar status (Kubernetes)
kubectl logs <pod> -c config-reloader --tail=20

# 3. Check if ConfigMap was updated
kubectl get configmap <config-name> -o jsonpath='{.metadata.resourceVersion}'

# 4. Check file modification time (bare metal)
stat /etc/metrics-governor/config.yaml

# 5. Try manual reload
kubectl exec <pod> -- kill -HUP 1
```

**Resolution:**

| Root Cause | Fix |
|-----------|-----|
| Sidecar not running | Check `config-reloader` container in pod spec |
| Volume mount with `subPath` | Switch to directory mount (subPath doesn't auto-update) |
| No config changes needed | Suppress alert or increase threshold |
| Permission error | Check file permissions on config directory |

**Prevention:**
- Use directory mounts for ConfigMaps (not subPath)
- Enable `configReload.enabled: true` in Helm values
- This alert is informational — consider disabling in dev environments

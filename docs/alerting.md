# Alerting

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Alert Rules — 10 Alerts, Zero Overlap](#alert-rules--10-alerts-zero-overlap)
  - [Signal Chain — How Alerts Escalate](#signal-chain--how-alerts-escalate)
  - [Complete Alert Reference](#complete-alert-reference)
- [Importing Rules](#importing-rules)
  - [Prometheus / VictoriaMetrics / Thanos](#prometheus--victoriametrics--thanos)
  - [Kubernetes (Prometheus Operator)](#kubernetes-prometheus-operator)
  - [Helm Chart](#helm-chart)
  - [VMAlert](#vmalert)
  - [Grafana Unified Alerting](#grafana-unified-alerting)
- [Threshold Tuning](#threshold-tuning)
  - [Helm Chart Thresholds — What Changes What](#helm-chart-thresholds--what-changes-what)
  - [Profile-Specific Thresholds](#profile-specific-thresholds)
  - [Playground — Interactive Tuning](#playground--interactive-tuning)
- [Disabling Alerts](#disabling-alerts)
- [Notification Channels](#notification-channels)
  - [Alertmanager — Slack](#alertmanager--slack)
  - [Alertmanager — PagerDuty](#alertmanager--pagerduty)
  - [Alertmanager — Email](#alertmanager--email)
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
- [See Also](#see-also)

---

## Overview

metrics-governor ships with **13 production-ready alert rules** designed around three principles:

1. **Zero overlap** — each alert fires for a distinct failure mode. No two alerts fire for the same root cause.
2. **Signal chain** — alerts are ordered by timing: leading indicators warn early, trailing indicators demand action.
3. **Every alert is actionable** — each has a runbook with exact commands to investigate and resolve.

All rules are in the `alerts/` folder, ready to import into any Prometheus-compatible system.

---

## Quick Start

**Fastest path to production alerting:**

```yaml
# Helm values.yaml — one line enables all 13 alerts
alerting:
  enabled: true
```

Or import manually:

```bash
# Prometheus / VictoriaMetrics
cp alerts/prometheus-rules.yaml /etc/prometheus/rules/metrics-governor.yaml
kill -HUP $(pidof prometheus)

# Kubernetes (Prometheus Operator)
kubectl apply -f alerts/prometheusrule.yaml
```

---

## Alert Rules — 13 Alerts, Zero Overlap

### Signal Chain — How Alerts Escalate

Alerts are not independent — they form escalation chains. During a backend outage, you'll see them fire in sequence:

```
Leading indicators (warn early)         Trailing indicators (act now)
────────────────────────────────        ────────────────────────────
ExportDegraded ──> CircuitOpen ──> QueueSaturated ──> DataLoss
Backpressure ──> WorkersSaturated
CardinalityExplosion
SpilloverCascade ──> QueueSaturated ──> DataLoss
LoadSheddingActive                       (parallel to Backpressure)
StatsDegraded                            (parallel to OOMRisk)
                                         OOMRisk ──> Down
```

This means:
- If you see `ExportDegraded`, fix it before `CircuitOpen` fires
- If you see `QueueSaturated`, the backend has been down long enough to fill the queue — `DataLoss` is next
- If you see `SpilloverCascade`, the queue is in a feedback loop — fix before it exhausts CPU
- If you see `LoadSheddingActive`, upstream clients are being rejected — check pipeline health
- If you see `StatsDegraded`, memory pressure caused stats to downgrade — cardinality dashboards may be stale
- If you see `OOMRisk`, the process will crash (`Down`) unless you act

### Complete Alert Reference

| # | Alert | Severity | What It Detects | Fire Condition | Runbook |
|---|-------|----------|----------------|----------------|---------|
| 1 | **Down** | critical | Process unavailable | `up == 0` for 2m | [Runbook](#metricsgovernordown) |
| 2 | **DataLoss** | critical | Permanent data loss | `data_loss_total` increasing | [Runbook](#metricsgovernordataloss) |
| 3 | **ExportDegraded** | warning | Backend errors | Error rate > 10% for 5m | [Runbook](#metricsgovernorexportdegraded) |
| 4 | **QueueSaturated** | warning | Queue filling up | Queue > 85% for 5m | [Runbook](#metricsgovernorqueuesaturated) |
| 5 | **CircuitOpen** | warning | Backend protection active | CB open for 2m | [Runbook](#metricsgovernorcircuitopen) |
| 6 | **OOMRisk** | critical | Memory pressure | Heap > 90% limit for 5m | [Runbook](#metricsgovernoroomrisk) |
| 7 | **Backpressure** | warning | Pipeline bottleneck | Buffer rejecting > 1/s for 5m | [Runbook](#metricsgovernorbackpressure) |
| 8 | **WorkersSaturated** | warning | CPU/export ceiling | Workers > 90% for 10m | [Runbook](#metricsgovernorworkerssaturated) |
| 9 | **CardinalityExplosion** | warning | Governance breach | Cardinality violations for 10m | [Runbook](#metricsgovernorcardinalityexplosion) |
| 10 | **ConfigStale** | info | Operational drift | No reload in 24h | [Runbook](#metricsgovernorconfigstale) |
| 11 | **SpilloverCascade** | warning | Queue cascade feedback loop | `spillover_active == 1` for 2m | [Runbook](#metricsgovernorspillovercascade) |
| 12 | **LoadSheddingActive** | warning | Pipeline rejecting requests | `load_shedding_total` rate > 0 for 2m | [Runbook](#metricsgovernorloadsheddingactive) |
| 13 | **StatsDegraded** | warning | Stats auto-downgraded | `stats_level_current` < configured for 5m | [Runbook](#metricsgovernorstatsdegraded) |

**Coverage by dimension** (no dimension has duplicate coverage):

| Dimension | Alerts | Why These |
|-----------|--------|-----------|
| **Availability** | `Down` | Binary — up or down |
| **Reliability** | `DataLoss`, `ExportDegraded` | Leading (recoverable) vs trailing (permanent) |
| **Resilience** | `QueueSaturated`, `CircuitOpen` | Independent signals — capacity vs circuit state |
| **Performance** | `Backpressure`, `WorkersSaturated`, `CardinalityExplosion` | Three independent bottleneck points |
| **Resource** | `OOMRisk` | Memory is the binding constraint |
| **Operational** | `ConfigStale` | Config freshness |
| **Stability** | `SpilloverCascade`, `LoadSheddingActive`, `StatsDegraded` | Queue feedback loop, admission control, graceful degradation |

---

## Importing Rules

### Prometheus / VictoriaMetrics / Thanos

```bash
cp alerts/prometheus-rules.yaml /etc/prometheus/rules/metrics-governor.yaml
# Reload Prometheus
kill -HUP $(pidof prometheus)
```

The file is a standard Prometheus rules group. Works with Prometheus, VictoriaMetrics, Thanos Ruler, and Cortex Ruler.

### Kubernetes (Prometheus Operator)

```bash
kubectl apply -f alerts/prometheusrule.yaml
```

Edit `alerts/prometheusrule.yaml` to match your Prometheus Operator's `ruleSelector` labels (default: `prometheus: kube-prometheus`).

### Helm Chart

```yaml
# values.yaml
alerting:
  enabled: true
  additionalLabels:
    prometheus: kube-prometheus   # must match your Prometheus Operator's ruleSelector
```

This creates a `PrometheusRule` CRD with all 10 alerts. See [Threshold Tuning](#threshold-tuning) for customization.

### VMAlert

```yaml
# vmalert config
rule:
  - /etc/vmalert/rules/metrics-governor.yaml
datasource:
  url: http://victoriametrics:8428
notifier:
  url: http://alertmanager:9093
```

```bash
# Copy rules
cp alerts/prometheus-rules.yaml /etc/vmalert/rules/metrics-governor.yaml

# Or run directly
vmalert \
  -rule=alerts/prometheus-rules.yaml \
  -datasource.url=http://victoriametrics:8428 \
  -notifier.url=http://alertmanager:9093
```

### Grafana Unified Alerting

The operations dashboard panels can be converted to Grafana alerts:

1. Open a panel (e.g., "Circuit Breaker State") in the dashboard
2. Click Edit, then the Alert tab
3. Set condition matching the alert expression from `alerts/prometheus-rules.yaml`
4. Configure notification channel and evaluation interval

---

## Threshold Tuning

### Helm Chart Thresholds — What Changes What

Each `alerting.thresholds.*` value maps to exactly one alert. This table shows what to change, how it modifies the alert, and which runbook to consult when it fires.

| Helm Value | Type | Default | Alert | What Changes | Runbook |
|-----------|------|---------|-------|-------------|---------|
| `thresholds.exportErrorRate` | float (0-1) | `0.1` | **ExportDegraded** | Fire at this error rate (0.1 = 10%) | [Runbook](#metricsgovernorexportdegraded) |
| `thresholds.queueUtilization` | float (0-1) | `0.85` | **QueueSaturated** | Fire at this queue fill level | [Runbook](#metricsgovernorqueuesaturated) |
| `thresholds.memoryUsage` | float (0-1) | `0.90` | **OOMRisk** | Fire at this heap % of limit | [Runbook](#metricsgovernoroomrisk) |
| `thresholds.backpressureRate` | float (events/s) | `1` | **Backpressure** | Fire at this reject+evict rate | [Runbook](#metricsgovernorbackpressure) |
| `thresholds.workerUtilization` | float (0-1) | `0.90` | **WorkersSaturated** | Fire at this worker usage level | [Runbook](#metricsgovernorworkerssaturated) |
| `thresholds.configStaleness` | int (seconds) | `86400` | **ConfigStale** | Fire after this many seconds without reload | [Runbook](#metricsgovernorconfigstale) |

**Alerts with fixed thresholds** (not tunable — any occurrence is significant):

| Alert | Why Fixed |
|-------|-----------|
| **Down** | Binary — process is either up or down |
| **DataLoss** | Any data loss is critical |
| **CircuitOpen** | Binary — CB is either open or closed |
| **CardinalityExplosion** | Any sustained violation needs investigation |

### Profile-Specific Thresholds

Different profiles have different operating envelopes:

| Alert | `minimal` | `balanced` | `performance` |
|-------|-----------|-----------|---------------|
| `ExportDegraded` | > 10% | > 10% | > 5% |
| `QueueSaturated` | N/A (no queue) | > 85% | > 90% |
| `CircuitOpen` | N/A (CB off) | 2m | 1m |
| `OOMRisk` | > 85% | > 90% | > 85% |
| `Backpressure` | > 0.5/s | > 1/s | > 5/s |
| `WorkersSaturated` | N/A (1 worker) | > 90% | > 95% |
| `ConfigStale` | N/A (dev) | 24h | 12h |

**Example Helm values for performance profile:**

```yaml
alerting:
  enabled: true
  thresholds:
    exportErrorRate: 0.05
    queueUtilization: 0.90
    memoryUsage: 0.85
    backpressureRate: 5
    workerUtilization: 0.95
    configStaleness: 43200
```

**Example for minimal profile (disable inapplicable alerts):**

```yaml
alerting:
  enabled: true
  thresholds:
    memoryUsage: 0.85
    backpressureRate: 0.5
  disabledAlerts:
    - MetricsGovernorQueueSaturated
    - MetricsGovernorCircuitOpen
    - MetricsGovernorWorkersSaturated
    - MetricsGovernorConfigStale
```

### Playground — Interactive Tuning

The **Alert rules** tab in `tools/playground/index.html` lets you:

1. Adjust each threshold with inputs — live preview of the YAML
2. Toggle between Prometheus rules and Kubernetes PrometheusRule CRD format
3. View the coverage map showing which alert each threshold affects
4. Copy or download the generated rules

---

## Disabling Alerts

Use `alerting.disabledAlerts` in Helm values to skip alerts that don't apply to your profile:

| Profile | Recommended `disabledAlerts` | Why |
|---------|------------------------------|-----|
| `minimal` | `QueueSaturated`, `CircuitOpen`, `WorkersSaturated`, `ConfigStale` | No queue, no CB, single worker, dev |
| `balanced` | (none) | All features enabled |
| `performance` | (none) | All features enabled |

---

## Notification Channels

### Alertmanager — Slack

```yaml
# alertmanager.yml
route:
  group_by: ['alertname', 'severity']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  receiver: 'slack-notifications'
  routes:
    - match:
        severity: critical
      receiver: 'slack-critical'
      repeat_interval: 30m

receivers:
  - name: 'slack-notifications'
    slack_configs:
      - api_url: 'https://hooks.slack.com/services/YOUR/WEBHOOK/URL'
        channel: '#observability-alerts'
        title: '{{ .CommonAnnotations.summary }}'
        text: '{{ .CommonAnnotations.description }}\nRunbook: {{ .CommonAnnotations.runbook_url }}'

  - name: 'slack-critical'
    slack_configs:
      - api_url: 'https://hooks.slack.com/services/YOUR/WEBHOOK/URL'
        channel: '#on-call'
        title: 'CRITICAL: {{ .CommonAnnotations.summary }}'
        text: '{{ .CommonAnnotations.description }}\nRunbook: {{ .CommonAnnotations.runbook_url }}'
```

### Alertmanager — PagerDuty

```yaml
receivers:
  - name: 'pagerduty-critical'
    pagerduty_configs:
      - service_key: 'YOUR-PAGERDUTY-SERVICE-KEY'
        severity: '{{ .CommonLabels.severity }}'
        description: '{{ .CommonAnnotations.summary }}'
        details:
          description: '{{ .CommonAnnotations.description }}'
          runbook: '{{ .CommonAnnotations.runbook_url }}'
```

### Alertmanager — Email

```yaml
receivers:
  - name: 'email-team'
    email_configs:
      - to: 'observability-team@example.com'
        from: 'alertmanager@example.com'
        smarthost: 'smtp.example.com:587'
        auth_username: 'alertmanager@example.com'
        auth_password: 'password'
```

---

## Runbooks

Each runbook follows the same structure: **what fired** → **what it means** → **impact** → **investigate** → **resolve** → **prevent**.

### MetricsGovernorDown

**Severity:** Critical | **Fires when:** `up == 0` for 2 minutes

**What it means:** The metrics-governor process is not responding to scrapes. All metrics ingestion is stopped.

**Impact:** Complete metrics pipeline outage.

**Investigate:**
```bash
kubectl get pods -l app.kubernetes.io/name=metrics-governor
kubectl describe pod <pod-name>
kubectl get events --field-selector reason=OOMKilled --sort-by=.lastTimestamp
kubectl logs <pod-name> --previous --tail=100
# Bare metal:
systemctl status metrics-governor
journalctl -u metrics-governor --since "5 minutes ago"
dmesg | grep -i "oom\|killed" | tail -5
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| OOM killed | Increase memory limit or switch to higher profile |
| CrashLoopBackOff | Check logs: bad config, port conflict, missing PVC |
| Node pressure | Check node resources, ensure PDB is set |
| Bad deployment | Roll back: `kubectl rollout undo deployment/metrics-governor` |

**Prevent:** Set `memory.limit_ratio: 0.85`, enable PDB, monitor `OOMRisk` alert.

---

### MetricsGovernorDataLoss

**Severity:** Critical | **Fires when:** `data_loss_total` increasing for 1 minute

**What it means:** Batches failed export AND failed to push to queue. Data is permanently lost.

**Impact:** Gaps in metrics data. Dashboard holes. SLO calculations affected.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep 'export_data_loss_total'
curl -s http://<governor>:9090/metrics | grep -E 'queue_bytes|queue_max_bytes|queue_dropped'
kubectl exec <pod> -- df -h /data/queue
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Queue disabled (minimal profile) | Switch to `balanced` or `performance` profile |
| Queue full + backend down | Increase `queue.max_bytes`, expand PVC, fix backend |
| Disk full | Expand PVC or reduce retention |
| Network failure | Check connectivity to backend |

**Prevent:** Enable persistent queue (`balanced` profile minimum). Size queue for outage buffer target.

---

### MetricsGovernorExportDegraded

**Severity:** Warning | **Fires when:** Error rate > 10% for 5 minutes

**What it means:** Backend is returning errors. Queue is absorbing failed batches — no data loss yet, but queue will eventually fill.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep 'otlp_export_errors_total'
# Look at error_type label: network, timeout, server_error, client_error, auth, rate_limit
curl -s http://<governor>:9090/metrics | grep 'queue_export_latency_ewma'
kubectl logs -l app=otel-collector --tail=50
```

**Resolve:**

| Error Type | Fix |
|-----------|-----|
| `network` | Check DNS, Service, NetworkPolicy |
| `timeout` | Increase `exporter.timeout` |
| `server_error` (5xx) | Scale backend or reduce throughput |
| `client_error` (413) | Batch tuner auto-adjusts; or reduce `buffer.batch_size` |
| `rate_limit` (429) | Enable backoff, reduce concurrency |

**Prevent:** Use `balanced` profile (enables adaptive batch tuning and circuit breaker).

---

### MetricsGovernorQueueSaturated

**Severity:** Warning | **Fires when:** Queue > 85% full for 5 minutes

**What it means:** Queue filling faster than draining. Data loss imminent.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep -E 'queue_bytes|queue_max_bytes'
curl -s http://<governor>:9090/metrics | grep 'export_errors_total'
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_state'
kubectl exec <pod> -- df -h /data/queue
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Backend down | Fix backend. Queue auto-drains on recovery. |
| Queue too small | Increase `queue.max_bytes`, expand PVC |
| Disk full | Expand storage or set `full_behavior: drop_oldest` |

**Prevent:** Size queue for 30+ minutes of outage buffer. Use `performance` profile with disk queue for critical workloads.

---

### MetricsGovernorCircuitOpen

**Severity:** Warning | **Fires when:** Circuit breaker open for 2 minutes

**What it means:** The CB tripped after consecutive export failures. All exports are blocked. Data accumulates in queue.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_state'
curl -s http://<governor>:9090/metrics | grep 'circuit_breaker_open_total'
curl -s http://<governor>:9090/metrics | grep 'queue_backoff_seconds'
kubectl exec <pod> -- curl -v http://<backend>:4317/
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Backend maintenance | Wait. Circuit auto-probes every `reset_timeout`. |
| Backend crash | Restart backend. |
| Network partition | Fix connectivity. |
| Won't close | Increase `circuit_breaker.threshold` temporarily, or set `resilience_level: low` |

**Prevent:** Use `resilience_level: high` for fast recovery. Size queue for outage duration.

---

### MetricsGovernorOOMRisk

**Severity:** Critical | **Fires when:** Heap > 90% of memory limit for 5 minutes

**What it means:** Go runtime under severe memory pressure. OOM kill imminent.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep -E 'heap_alloc_bytes|memory_limit_bytes'
curl -s http://<governor>:9090/metrics | grep -E 'intern_pool_estimated|cardinality_memory|queue_bytes|buffer_bytes'
curl -s http://<governor>:9090/metrics | grep -E 'gc_cycles_total|gc_cpu_percent'
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| High cardinality | Tighten limits, use `bloom` or `hybrid` mode |
| Large buffer | Reduce `buffer.size` or `buffer.memory_percent` |
| Memory queue | Reduce `queue.memory_percent` or switch to disk queue |
| Container limit too low | Increase limit, use VPA |

**Prevent:** Set `memory.limit_ratio: 0.85`, use `--memory-budget-percent`, enable bloom cardinality.

---

### MetricsGovernorBackpressure

**Severity:** Warning | **Fires when:** Buffer rejecting/evicting > 1/s for 5 minutes

**What it means:** Incoming rate exceeds buffer processing capacity. With `reject` policy: senders get errors. With `drop_oldest`: silent data loss.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep -E 'buffer_rejected|buffer_evictions'
curl -s http://<governor>:9090/metrics | grep -E 'receiver_datapoints_total|export_datapoints_total'
curl -s http://<governor>:9090/metrics | grep -E 'workers_active|workers_total'
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Throughput exceeds capacity | Switch to higher profile, increase `--parallelism`, or scale out (HPA) |
| Buffer too small | Increase `buffer.size` |
| Slow exports | Enable pipeline split (`performance` profile) |
| Burst traffic | Enable queue to absorb spikes |

**Prevent:** Use `balanced` profile (adaptive workers), size buffer for 2x expected burst.

---

### MetricsGovernorWorkersSaturated

**Severity:** Warning | **Fires when:** Workers > 90% utilized for 10 minutes

**What it means:** Export workers at capacity. Adaptive scaler hit ceiling or is disabled.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep -E 'workers_active|workers_total|workers_desired'
curl -s http://<governor>:9090/metrics | grep 'export_latency_ewma'
curl -s http://<governor>:9090/metrics | grep -E 'preparers_active|senders_active'
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Max workers reached | Increase `adaptive_workers.max_workers` or `--parallelism` |
| Scaling disabled | Switch to `balanced` profile |
| High export latency | Fix backend latency — workers are waiting, not working |
| Compression bottleneck | Enable pipeline split (`performance` profile) |

**Prevent:** Use `performance` profile, set HPA at 70% CPU.

---

### MetricsGovernorCardinalityExplosion

**Severity:** Warning | **Fires when:** Cardinality violations sustained for 10 minutes

**What it means:** Upstream services generating metrics with unbounded label values.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep 'cardinality_exceeded_total'
curl -s http://<governor>:9090/metrics | grep 'rule_group_cardinality' | sort -t' ' -k2 -rn | head -10
curl -s http://<governor>:9090/metrics | grep 'cardinality_memory_bytes'
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Unbounded labels | Add relabeling rule to drop or aggregate |
| New high-cardinality service | Add limits rule with appropriate threshold |
| Limit too low | Increase `default_max_cardinality` or per-rule limits |

**Prevent:** Define limits rules for all services. Use `limits.action: adaptive`. Monitor per-group cardinality.

---

### MetricsGovernorConfigStale

**Severity:** Info | **Fires when:** No config reload in 24 hours

**What it means:** Config reload mechanism may not be working. If no changes were expected, this is informational.

**Investigate:**
```bash
curl -s http://<governor>:9090/metrics | grep -E 'config_reload_total|reload_last_success'
kubectl logs <pod> -c config-reloader --tail=20
kubectl get configmap <config-name> -o jsonpath='{.metadata.resourceVersion}'
```

**Resolve:**

| Root Cause | Fix |
|-----------|-----|
| Sidecar not running | Check `config-reloader` container |
| Volume with `subPath` | Switch to directory mount |
| No changes needed | Suppress alert or increase threshold |

**Prevent:** Use directory mounts, enable `configReload.enabled: true`.

---

### MetricsGovernorSpilloverCascade

**Severity:** Warning | **Fires when:** `metrics_governor_spillover_active == 1` for 2 minutes

**What it means:** The hybrid queue entered a spillover cascade. Memory queue filled past the threshold, forcing batches to disk. Disk serialization adds CPU, which slows processing, which fills the queue faster.

**Impact:** Export latency spikes. CPU saturates. IOPS spike. If sustained, leads to QueueSaturated then DataLoss.

**Investigate:**

| Check | Command |
|-------|---------|
| Spillover state | `curl -s http://<governor>:9090/metrics \| grep spillover` |
| Input vs drain rate | `curl -s http://<governor>:9090/metrics \| grep -E 'receiver_datapoints\|export_datapoints'` |
| Export health | `curl -s http://<governor>:9090/metrics \| grep -E 'export_latency_ewma\|circuit_breaker_state'` |

| Root Cause | Fix |
|-----------|-----|
| Input exceeds drain | Reduce upstream traffic or scale out |
| Backend slow/down | Fix backend. See `ExportDegraded` / `CircuitOpen` runbooks |
| Threshold too low | Raise `spillover_threshold_pct` to 90% |
| Memory queue too small | Increase `queue.inmemory_blocks` |

**Prevent:** Size memory queue for 2x burst duration. Monitor `spillover_active` in Grafana. See [stability-guide.md](stability-guide.md).

---

### MetricsGovernorLoadSheddingActive

**Severity:** Warning | **Fires when:** `rate(metrics_governor_receiver_load_shedding_total[2m]) > 0`

**What it means:** Pipeline health score exceeded the load shedding threshold. Receivers are rejecting requests. gRPC clients receive `ResourceExhausted`, HTTP/PRW clients receive `429`.

**Impact:** Upstream senders see errors. Data is NOT lost at metrics-governor (rejected before acceptance), but senders must retry.

**Investigate:**

| Check | Command |
|-------|---------|
| Shedding rate | `curl -s http://<governor>:9090/metrics \| grep load_shedding_total` |
| Health score | `curl -s http://<governor>:9090/metrics \| grep pipeline_health` |
| Queue pressure | `curl -s http://<governor>:9090/metrics \| grep -E 'queue_bytes\|queue_max_bytes'` |

| Root Cause | Fix |
|-----------|-----|
| Queue full (backend down) | Fix backend. Shedding stops when health recovers |
| Traffic spike (temporary) | Wait. Shedding auto-resolves. Ensure senders retry with backoff |
| Sustained overload | Scale out or switch to higher-capacity profile |
| Threshold too aggressive | Raise `load_shedding_threshold` (e.g., 0.85 to 0.95) |

**Prevent:** Configure upstream senders with retry + exponential backoff. Size pipeline for 1.5x peak. See [stability-guide.md](stability-guide.md).

---

### MetricsGovernorStatsDegraded

**Severity:** Warning | **Fires when:** `metrics_governor_stats_level_current` < configured stats level for 5 minutes

**What it means:** Stats collector auto-downgraded to reduce memory pressure. Levels: `full` (2) -> `basic` (1) -> `none` (0). Core proxy function (receive, queue, export) is unaffected.

**Impact:** At `basic`: cardinality dashboards go stale. At `none`: all stats dashboards go blank. Limits enforcement continues from cached state.

**Investigate:**

| Check | Command |
|-------|---------|
| Current stats level | `curl -s http://<governor>:9090/metrics \| grep stats_level_current` |
| Memory pressure | `curl -s http://<governor>:9090/metrics \| grep -E 'heap_alloc_bytes\|memory_limit_bytes'` |
| Cardinality memory | `curl -s http://<governor>:9090/metrics \| grep cardinality_memory_bytes` |

| Root Cause | Fix |
|-----------|-----|
| High cardinality consuming memory | Tighten limits rules or use `bloom` tracker |
| Container memory too low | Increase memory limit. Full stats at 100k dps needs ~50 MB |
| GOMEMLIMIT too aggressive | Increase `memory.limit_ratio` from 0.85 to 0.90 |
| Stats `full` not needed | Switch to `balanced` profile (uses basic stats) |

**Prevent:** Size container for stats overhead: `full` needs 50-100 MB headroom. Monitor `heap_alloc_bytes / memory_limit_bytes` ratio. See [stability-guide.md](stability-guide.md).

---

## Dead Rule Alert Routing

Dead rule detection alerts include ownership labels from the rule's `labels:` map. This enables Alertmanager to route alerts to the responsible team automatically.

### How Ownership Labels Flow to Alerts

When you configure `labels:` on processing or limits rules, those labels become Prometheus dimensions on dead-rule metrics. Alert annotations can reference them:

```yaml
# In your alert rule:
annotations:
  summary: "Rule {{ $labels.rule }} stopped matching (team: {{ $labels.team }})"
  slack_channel: "{{ $labels.slack_channel }}"
  pagerduty_service: "{{ $labels.pagerduty_service }}"
  owner: "{{ $labels.owner_email }}"
```

### Alertmanager Routing Example

Route dead rule alerts to the owning team's channels:

```yaml
# alertmanager.yml
route:
  routes:
    - match_re:
        alertname: "MetricsGovernor(Dead|NeverMatched).*Rule"
      group_by: [team]
      routes:
        - match: { team: payments }
          receiver: payments-slack
        - match: { team: platform }
          receiver: platform-pagerduty

receivers:
  - name: payments-slack
    slack_configs:
      - channel: '#payments-alerts'
        title: 'Dead Rule: {{ .GroupLabels.rule }}'
        text: 'Owner: {{ .CommonAnnotations.owner }}'
  - name: platform-pagerduty
    pagerduty_configs:
      - service_key: '<pagerduty-key>'
        description: '{{ .CommonAnnotations.summary }}'
```

### Ensuring Routability with required_labels

Use `required_labels` in your processing config to guarantee every rule has the labels needed for alert routing:

```yaml
required_labels: [team, owner_email]
```

Config validation fails at load time if any rule is missing a required label, preventing unroutable dead-rule alerts.

### Alert Rule Files

| File | Format | Purpose |
|------|--------|---------|
| `alerts/dead-rules.yaml` | Prometheus | Standalone dead rule alert definitions |
| `alerts/dead-rules-prometheusrule.yaml` | K8s CRD | Same rules for prometheus-operator |

These alerts work **without** the in-governor scanner -- they use always-on `last_match_seconds` and `never_matched` metrics.

---

## SLO Burn-Rate Alerts

In addition to the 13 operational alerts above, metrics-governor provides **6 SLO burn-rate alerts** that measure error budget consumption over time. These two alert sets are complementary:

| Type | Purpose | Fires When | Response |
|------|---------|-----------|----------|
| **Operational** (13 alerts) | Symptom-based — what's broken NOW | A specific component is failing | Fix the immediate issue |
| **SLO** (6 alerts) | Budget-based — impact OVER TIME | Error budget is being consumed too fast | Assess cumulative impact, prioritize |

The SLO alerts use governor-computed `metrics_governor_sli_*_burn_rate` metrics and follow the multi-window, multi-burn-rate pattern from the Google SRE Workbook:

- **Critical (14.4x):** Budget exhausted in ~2 days — page immediately
- **High (6x):** Budget exhausted in ~5 days — page, investigate sustained degradation
- **Warning (1x):** At budget pace — ticket, investigate before breach

See [SLO documentation](slo.md) for full details, and [alerts/slo-alerts.yaml](../alerts/slo-alerts.yaml) for the alert definitions.

---

## See Also

- [SLOs](slo.md) — SLI definitions, error budgets, burn-rate alerts, health dashboard
- [Production Guide](production-guide.md) — Deployment sizing, resilience tuning, HPA/VPA
- [Resilience](resilience.md) — Circuit breaker, backoff, failure modes
- [Performance](performance.md) — Internals, benchmarks, pipeline tuning
- [Limits](limits.md) — Rule syntax, adaptive limiting, cardinality
- [Configuration Profiles](profiles.md) — Profile presets, parameter tables
- [Alert Rules](../alerts/) — Importable rule files
- [Alert Runbooks](../alerts/README.md) — Detailed runbooks for each alert
- [Processing Rules - Dead Rule Detection](processing-rules.md#dead-rule-detection) — Detection architecture and metrics

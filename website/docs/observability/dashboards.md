---
title: "Grafana Dashboards"
sidebar_position: 2
description: "Operations and development dashboards"
---

Pre-built Grafana dashboards for monitoring metrics-governor in production and development environments.

## Dashboard Overview

| Dashboard | File | Panels | Purpose |
|-----------|------|:------:|---------|
| **Operations** | `dashboards/operations.json` | ~154 | Production monitoring, troubleshooting, capacity planning |
| **Development** | `dashboards/development.json` | ~45 | Local dev/testing with docker-compose (generator + governor + verifier) |
| **E2E Testing** | `test/grafana/dashboards/e2e-testing.json` | ~75 | Automated e2e test validation |
| **Metrics Governor** | `test/grafana/dashboards/metrics-governor.json` | ~97 | Full self-monitoring dashboard |

## Operations Dashboard

The primary dashboard for production deployments. Covers the complete metrics pipeline.

### Sections

| Section | Key Panels | What to Watch |
|---------|-----------|---------------|
| **Overview** | Pass rate, datapoints/sec, unique metrics, cardinality, queue size, export errors | Overall health at a glance |
| **OTLP Throughput** | Datapoints rate (received/sent/dropped), batches & exports | OTLP pipeline flow |
| **PRW Throughput** | Datapoints/timeseries rate, batches & exports | PRW pipeline flow |
| **Limits Enforcement** | Violations by rule, dropped vs passed, cardinality tracking | Are limits firing? Which rules? |
| **Cardinality Analysis** | Top 10 metrics by cardinality, top 10 by datapoints rate | What's driving cardinality? |
| **Circuit Breaker & Backoff** | State indicator, current backoff, opens, rejections, timeline | Backend health |
| **Queue & Persistence** | Queue size, max size, push/pop/retry, workers active/total, workers utilization | Queue filling up? Workers saturated? |
| **Buffer** | Buffer size, memory usage, utilization gauge, rejected/s, evictions/s, flush rate, batch size | Buffer pressure and backpressure |
| **Sharding** | Active endpoints, datapoints by endpoint, DNS events | Shard distribution |
| **Network I/O** | Bytes sent/received, packets, errors | Network health (Linux only) |
| **Disk I/O** | Bytes read/written | Disk pressure (Linux only) |
| **Runtime** | Memory (heap/stack), goroutines, GC rate | Resource usage |
| **Process Health** | CPU user/system, open FDs, virtual/resident memory | Process health |
| **Config Reload & Health** | Uptime, reload count/rate, last reload time | Config freshness |
| **Bloom Persistence** | Saves, loads, disk usage, memory usage, trackers | Persistence health |
| **PSI Metrics** | CPU/Memory/IO pressure averages | System pressure (Linux only) |

### Key PromQL Queries

```promql
# Overall pass rate (should be > 95%)
1 - (rate(metrics_governor_limit_datapoints_dropped_total[5m])
  / rate(metrics_governor_datapoints_total[5m]))

# Datapoints throughput
rate(metrics_governor_datapoints_total[5m])

# Top cardinality offenders
topk(10, metrics_governor_metric_cardinality)

# Export error rate
rate(metrics_governor_otlp_export_errors_total[5m])

# Queue utilization
metrics_governor_queue_size / metrics_governor_queue_max_size
```

## Development Dashboard

Designed for local testing with docker-compose. Shows the full data pipeline from generator through governor to verifier.

### Sections

| Section | Key Panels | Purpose |
|---------|-----------|---------|
| **Test Pipeline Overview** | Generator rate, governor throughput, export errors, verifier pass rate | At-a-glance pipeline status |
| **Generator** | Throughput, batch latency, totals, errors, spike/mistake status | Is the generator healthy? |
| **Governor Pipeline** | Datapoints rate (received/sent/dropped), batches, buffer, bytes | Is the governor processing correctly? |
| **Config & Health** | Uptime, config reloads, goroutines, memory, GC | Governor resource health |
| **Limits Enforcement** | Dropped vs passed, violations, per-rule datapoints and cardinality | Are limits working as expected? |
| **Verifier** | Pass rate, ingestion rate, VM stats, check totals, e2e comparison | Is data arriving correctly? |

## Installation

### Import via Grafana UI

1. Open Grafana -> **Dashboards** -> **Import**
2. Upload the JSON file or paste its contents
3. Select your Prometheus datasource
4. Click **Import**

### Provisioning (Kubernetes/Docker)

```yaml
# grafana-provisioning/dashboards/dashboards.yaml
apiVersion: 1
providers:
  - name: 'metrics-governor'
    orgId: 1
    folder: 'Metrics Governor'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    options:
      path: /var/lib/grafana/dashboards/metrics-governor
```

### Helm Chart

The Helm chart can mount dashboards automatically. Add the JSON files to a ConfigMap or mount from a volume:

```yaml
# values.yaml
grafana:
  dashboards:
    enabled: true
    provider:
      folder: "Metrics Governor"
```

### Docker Compose

The test environment auto-imports dashboards via existing provisioning:

```yaml
grafana:
  image: grafana/grafana:latest
  volumes:
    - ./dashboards:/var/lib/grafana/dashboards/metrics-governor
    - ./test/grafana/provisioning:/etc/grafana/provisioning
```

## Customization

### Adding Panels

1. Edit the dashboard in Grafana UI
2. Add panel -> select metric from `metrics_governor_*` namespace
3. Save changes, then export JSON

### Common Customizations

- **Thresholds**: Add color-coded thresholds to stat panels (green < 80%, yellow < 95%, red >= 95%)
- **Variables**: Add template variables for filtering by rule, endpoint, or service
- **Alerts**: Convert panels to Grafana alerts (see [Alerting](/docs/observability/alerting))
- **Time ranges**: Adjust default time range for your monitoring cadence

### Dashboard Variables

All dashboards use a `datasource` template variable to select the Prometheus datasource.

## Requirements

- Grafana 9.x or later
- Prometheus datasource configured
- metrics-governor exposing metrics on `/metrics` endpoint (default `:9090`)

## See Also

- [Statistics](/docs/observability/statistics) — Complete list of all Prometheus metrics
- [Alerting](/docs/observability/alerting) — Alerting rules based on dashboard metrics
- [Testing](/docs/contributing/testing) — Test environment with auto-imported dashboards

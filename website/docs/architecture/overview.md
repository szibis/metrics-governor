---
title: "Two-Tier Architecture"
sidebar_position: 1
description: "Edge and aggregation tier architecture overview"
---

## Table of Contents

- [Overview](#overview)
- [Architecture Diagram](#architecture-diagram)
- [When to Use Two-Tier](#when-to-use-two-tier)
- [Tier 1: DaemonSet Edge Processing](#tier-1-daemonset-edge-processing)
  - [Purpose](#purpose)
  - [Configuration](#configuration)
  - [Typical Processing Actions](#typical-processing-actions)
  - [Helm Values](#helm-values)
- [Tier 2: StatefulSet Global Gateway](#tier-2-statefulset-global-gateway)
  - [Purpose](#purpose-1)
  - [Configuration](#configuration-1)
  - [Typical Processing Actions](#typical-processing-actions-1)
  - [Helm Values](#helm-values-1)
- [Sizing Guide](#sizing-guide)
- [Kubernetes Deployment](#kubernetes-deployment)
  - [Two Helm Releases](#two-helm-releases)
  - [Tier 2 Service](#tier-2-service)
  - [Full Deployment Commands](#full-deployment-commands)
- [Monitoring](#monitoring)
  - [Tier 1 Key Metrics](#tier-1-key-metrics)
  - [Tier 2 Key Metrics](#tier-2-key-metrics)
  - [Dashboard Sections](#dashboard-sections)
- [See Also](#see-also)

---

## Overview

The two-tier architecture splits metrics-governor into two cooperating layers:

- **Tier 1 (DaemonSet)** -- deployed on every node, performs edge pre-processing. Each instance handles only the metrics produced on its own node, applying local reduction (adaptive downsampling, label normalization, noisy metric dropping) before forwarding the reduced stream to Tier 2.

- **Tier 2 (StatefulSet)** -- a small set of gateway replicas that receive pre-processed data from all Tier 1 instances. Tier 2 performs cross-node aggregation and final cardinality reduction before exporting to long-term storage (VictoriaMetrics, Thanos, etc.).

**Goals:**

1. **Minimize cross-network traffic** -- Tier 1 reduces data volume at the source, cutting cross-AZ and cross-region bandwidth by 10-50x before data leaves the node.
2. **Enable global cross-node aggregation** -- Only Tier 2 sees data from all nodes, making it the natural place for cluster-wide rollups (`group_by: [cluster]`).
3. **Independent scaling** -- Tier 1 scales automatically with the cluster (one pod per node), while Tier 2 scales independently based on aggregate throughput.

> **Other references**: [Production Guide](/docs/operations/production-guide) (sizing, tuning), [Sharding](/docs/operations/sharding) (consistent hashing for Tier 2 backends), [Configuration](/docs/getting-started/configuration) (full YAML and CLI reference).

---

## Architecture Diagram

```
                          Node A                              Node B
                    ┌─────────────────┐                ┌─────────────────┐
                    │  node-exporter   │                │  node-exporter   │
                    │  kube-state-m.   │                │  kube-state-m.   │
                    │  app exporters   │                │  app exporters   │
                    └────────┬────────┘                └────────┬────────┘
                             │                                  │
                             v                                  v
                    ┌─────────────────┐                ┌─────────────────┐
                    │   Tier 1 (DS)   │                │   Tier 1 (DS)   │
                    │                 │                │                 │
                    │ - adaptive down │                │ - adaptive down │
                    │ - label strip   │                │ - label strip   │
                    │ - drop noisy    │                │ - drop noisy    │
                    │                 │                │                 │
                    │ processing-     │                │ processing-     │
                    │ tier1.yaml      │                │ tier1.yaml      │
                    └────────┬────────┘                └────────┬────────┘
                             │  10-50x reduction                │
                             │  OTLP gRPC                       │
                             └──────────┬───────────────────────┘
                                        │
                                        v
                    ┌───────────────────────────────────────────┐
                    │         Tier 2 (StatefulSet, 2-3 replicas)│
                    │                                           │
                    │  - cross-node aggregate (group_by)        │
                    │  - drop pod-level labels                  │
                    │  - final cardinality reduction             │
                    │                                           │
                    │  processing-tier2.yaml                    │
                    └─────────────────────┬─────────────────────┘
                                          │  5-20x additional reduction
                                          │  OTLP gRPC / HTTP
                                          v
                    ┌───────────────────────────────────────────┐
                    │    VictoriaMetrics / Thanos / Cortex      │
                    └───────────────────────────────────────────┘
```

---

## When to Use Two-Tier

Use the two-tier architecture when any of the following apply:

| Condition | Why Two-Tier Helps |
|-----------|--------------------|
| **Large clusters (50+ nodes)** | Per-node reduction prevents a single gateway from being overwhelmed |
| **Cross-AZ or cross-region traffic costs** | Tier 1 reduces data volume before it crosses network boundaries |
| **Need for global aggregation** | Tier 2 can compute cluster-wide sums/averages that no single node can |
| **High per-node metric volume** | DaemonSet metrics (node-exporter, cAdvisor) produce thousands of series per node |
| **Cost optimization** | Combined 50-1000x reduction significantly lowers storage and ingestion costs |

For small clusters (under 50 nodes) with low metric volume, a single-tier Deployment or StatefulSet is simpler and sufficient. See the [Production Guide](/docs/operations/production-guide) for single-tier sizing.

---

## Tier 1: DaemonSet Edge Processing

### Purpose

Tier 1 runs as a DaemonSet -- one pod per Kubernetes node. It receives metrics from local exporters (node-exporter, kube-state-metrics, application pods) and performs aggressive local reduction. Only the reduced stream is forwarded to Tier 2, cutting cross-network traffic by 10-50x.

### Configuration

Tier 1 processing rules are defined in [`examples/processing-tier1.yaml`](https://github.com/szibis/metrics-governor/blob/main/examples/processing-tier1.yaml). The key sections:

- **Label normalization** -- strip high-cardinality pod identifiers (`pod_ip`, `container_id`, `uid`), normalize instance labels to IP-only.
- **Adaptive downsampling** -- compress stable infrastructure metrics (CPU, memory, disk, network) while preserving spikes.
- **Drop noisy metrics** -- remove Go runtime (`go_*`), scrape metadata (`scrape_*`), and process metrics (`process_*`).

### Typical Processing Actions

```yaml
# From examples/processing-tier1.yaml

# Strip high-cardinality pod labels before aggregation
- name: strip-pod-identifiers
  input: "kube_pod_.*"
  action: transform
  operations:
    - remove: [pod_ip, container_id, uid]

# CPU metrics: compress stable idle periods, keep spikes
- name: cpu-adaptive
  input: "node_cpu_seconds_total"
  action: downsample
  method: adaptive
  interval: 30s
  min_rate: 0.1
  max_rate: 1.0
  variance_window: 30

# Drop internal Go runtime metrics
- name: drop-go-runtime
  input: "go_.*"
  action: drop
```

### Helm Values

Create a values file for the Tier 1 release (`values-tier1.yaml`):

```yaml
kind: daemonset

config:
  rawConfig: |
    receiver:
      grpc:
        address: ":4317"
      http:
        address: ":4318"

    exporter:
      endpoint: "tier2-metrics-governor:4317"
      protocol: "grpc"

    buffer:
      max_size_bytes: 52428800  # 50MB per node (smaller footprint)

    processing:
      enabled: true
      config: "/etc/metrics-governor/processing.yaml"

    stats:
      address: ":9090"

# Mount the processing rules file
extraVolumes:
  - name: processing-rules
    configMap:
      name: metrics-governor-tier1-processing

extraVolumeMounts:
  - name: processing-rules
    mountPath: /etc/metrics-governor/processing.yaml
    subPath: processing.yaml
    readOnly: true

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

---

## Tier 2: StatefulSet Global Gateway

### Purpose

Tier 2 runs as a StatefulSet with 2-3 replicas. It receives pre-processed data from all Tier 1 DaemonSet pods across the cluster. Its job is cross-node aggregation (computing cluster-wide sums, averages, percentiles) and final cardinality reduction before sending data to long-term storage.

### Configuration

Tier 2 processing rules are defined in [`examples/processing-tier2.yaml`](https://github.com/szibis/metrics-governor/blob/main/examples/processing-tier2.yaml). The key sections:

- **Cross-node aggregation** -- aggregate CPU, memory, and network metrics by cluster using `group_by`.
- **Application metric rollup** -- aggregate HTTP and gRPC request metrics by service, dropping pod-level detail.
- **Kubernetes churn reduction** -- strip pod-specific labels from kube-state-metrics, roll up to deployment level.
- **Final safety drops** -- remove any internal or debug metrics that slipped through Tier 1.

### Typical Processing Actions

```yaml
# From examples/processing-tier2.yaml

# Aggregate CPU across all nodes by cluster
- name: cluster-cpu
  input: "node_cpu_seconds_total"
  output: "cluster_cpu_seconds_total"
  action: aggregate
  interval: 1m
  group_by: [cluster, mode]
  functions: [sum, avg]
  keep_input: false

# HTTP requests: aggregate by service, dropping pod-level detail
- name: http-by-service
  input: "http_requests_total"
  output: "http_requests_by_service"
  action: aggregate
  interval: 30s
  group_by: [service, method, status_code, env]
  functions: [sum]
  keep_input: false

# Strip pod-specific labels from kube-state-metrics
- name: kube-strip-pods
  input: "kube_pod_.*"
  action: aggregate
  interval: 1m
  drop_labels: [pod, pod_ip, uid, container_id, instance]
  functions: [last]
  keep_input: false
```

### Helm Values

Create a values file for the Tier 2 release (`values-tier2.yaml`):

```yaml
kind: statefulset
replicaCount: 2

config:
  rawConfig: |
    receiver:
      grpc:
        address: ":4317"
      http:
        address: ":4318"

    exporter:
      endpoint: "victoriametrics:4317"
      protocol: "grpc"

    buffer:
      max_size_bytes: 209715200  # 200MB (handles aggregate of all nodes)

    processing:
      enabled: true
      config: "/etc/metrics-governor/processing.yaml"

    stats:
      address: ":9090"

    exporter:
      queue:
        enabled: true
        path: "/var/lib/metrics-governor/queue"
        max_size_bytes: 1073741824  # 1GB

# Mount the processing rules file
extraVolumes:
  - name: processing-rules
    configMap:
      name: metrics-governor-tier2-processing

extraVolumeMounts:
  - name: processing-rules
    mountPath: /etc/metrics-governor/processing.yaml
    subPath: processing.yaml
    readOnly: true

queue:
  enabled: true
  size: 2Gi
  storageClass: gp3

resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: 2000m
    memory: 2Gi
```

---

## Sizing Guide

The following table provides rough estimates based on typical infrastructure metric workloads. Actual requirements depend on metric cardinality, scrape frequency, and processing rule complexity.

| Cluster Size | Tier 1 CPU (per node) | Tier 1 Memory (per node) | Tier 2 Replicas | Tier 2 CPU (per replica) | Tier 2 Memory (per replica) | Expected Total Reduction |
|:------------:|:---------------------:|:------------------------:|:---------------:|:------------------------:|:---------------------------:|:------------------------:|
| **50 nodes** | 100m - 250m | 128Mi - 256Mi | 2 | 500m - 1000m | 512Mi - 1Gi | 50-100x |
| **200 nodes** | 100m - 500m | 128Mi - 512Mi | 2-3 | 1000m - 2000m | 1Gi - 2Gi | 100-500x |
| **1000 nodes** | 250m - 500m | 256Mi - 512Mi | 3-5 | 2000m - 4000m | 2Gi - 4Gi | 200-1000x |

**Notes:**

- Tier 1 resources are per node (DaemonSet). Total Tier 1 cluster cost = per-node cost * number of nodes.
- Tier 2 replicas can use [consistent sharding](/docs/operations/sharding) to distribute load across backends.
- Memory requirements increase with cardinality tracker usage. See [Cardinality Tracking](/docs/governance/cardinality-tracking) for tuning.
- Enable [persistent queues](/docs/architecture/queue) on Tier 2 for durability during backend outages.
- Start with the lower bound and scale up based on observed resource usage. Use the [Operations Dashboard](/docs/observability/dashboards) to monitor actual utilization.

---

## Kubernetes Deployment

### Two Helm Releases

Deploy the two tiers as separate Helm releases with different values files. This allows independent version upgrades, rollbacks, and scaling.

```bash
# Install Tier 2 first (so the service endpoint exists for Tier 1)
helm install metrics-governor-tier2 ./helm/metrics-governor \
  -f values-tier2.yaml \
  -n monitoring

# Install Tier 1 (DaemonSet, targets Tier 2 service)
helm install metrics-governor-tier1 ./helm/metrics-governor \
  -f values-tier1.yaml \
  -n monitoring
```

### Tier 2 Service

Tier 1 DaemonSet pods need to reach Tier 2. The Helm chart creates a service automatically. By default, the Tier 2 service name follows the release name:

```
tier2-metrics-governor.monitoring.svc.cluster.local:4317
```

Ensure the `exporter.endpoint` in `values-tier1.yaml` matches the Tier 2 service name and namespace.

For StatefulSet deployments, the chart also creates a headless service. If you want Tier 1 to shard across Tier 2 replicas, configure sharding in `values-tier1.yaml`:

```yaml
config:
  rawConfig: |
    exporter:
      endpoint: "tier2-metrics-governor:4317"
      protocol: "grpc"
      sharding:
        enabled: true
        headless_service: "tier2-metrics-governor-headless.monitoring.svc.cluster.local:4317"
```

See [Sharding](/docs/operations/sharding) for full configuration details.

### Full Deployment Commands

Create the processing rule ConfigMaps, then deploy both tiers:

```bash
# Create processing rule ConfigMaps
kubectl create configmap metrics-governor-tier1-processing \
  --from-file=processing.yaml=examples/processing-tier1.yaml \
  -n monitoring

kubectl create configmap metrics-governor-tier2-processing \
  --from-file=processing.yaml=examples/processing-tier2.yaml \
  -n monitoring

# Deploy Tier 2 (gateway)
helm install metrics-governor-tier2 ./helm/metrics-governor \
  -f values-tier2.yaml \
  -n monitoring

# Verify Tier 2 is ready
kubectl rollout status statefulset/metrics-governor-tier2 -n monitoring

# Deploy Tier 1 (edge)
helm install metrics-governor-tier1 ./helm/metrics-governor \
  -f values-tier1.yaml \
  -n monitoring

# Verify Tier 1 is running on all nodes
kubectl rollout status daemonset/metrics-governor-tier1 -n monitoring
```

---

## Monitoring

### Tier 1 Key Metrics

Monitor these metrics on each Tier 1 DaemonSet pod:

| Metric | What to Watch |
|--------|---------------|
| `metrics_governor_receiver_datapoints_total` | Inbound volume per node -- spikes indicate noisy exporters |
| `metrics_governor_datapoints_total` | Outbound volume after processing -- compare with inbound to verify reduction ratio |
| `metrics_governor_buffer_size` | Buffer utilization -- sustained high values mean Tier 2 is slow |
| `metrics_governor_buffer_rejected_total` | Backpressure events -- Tier 1 cannot keep up or Tier 2 is unavailable |
| `metrics_governor_receiver_errors_total` | Receiver errors -- local exporter connection issues |

**Key ratio to track:**

```promql
# Tier 1 reduction ratio (should be 10-50x)
rate(metrics_governor_receiver_datapoints_total{tier="1"}[5m])
  / rate(metrics_governor_datapoints_total{tier="1"}[5m])
```

### Tier 2 Key Metrics

Monitor these metrics on each Tier 2 StatefulSet pod:

| Metric | What to Watch |
|--------|---------------|
| `metrics_governor_receiver_datapoints_total` | Aggregate inbound from all Tier 1 nodes |
| `metrics_governor_datapoints_total` | Final outbound to storage -- should be significantly less than inbound |
| `metrics_governor_limit_datapoints_dropped_total` | Limit enforcement activity |
| `metrics_governor_metric_cardinality` | Active time series count -- the number you are paying for |
| `metrics_governor_buffer_size` | Buffer pressure -- indicates backend write speed |
| `metrics_governor_sharding_endpoints_total` | Active backend endpoints (if sharding enabled) |

**Key ratio to track:**

```promql
# End-to-end reduction (Tier 1 inbound vs Tier 2 outbound)
sum(rate(metrics_governor_receiver_datapoints_total{tier="1"}[5m]))
  / sum(rate(metrics_governor_datapoints_total{tier="2"}[5m]))
```

### Dashboard Sections

The [Operations Dashboard](/docs/observability/dashboards) covers all relevant panels. When running two tiers, filter by the `tier` label (set via Helm values or exporter labels) to separate Tier 1 and Tier 2 views. Key dashboard sections:

- **Overview** -- pass rate and throughput per tier
- **OTLP Throughput** -- datapoints/sec at each tier boundary
- **Buffer** -- buffer pressure on both tiers (Tier 1 filling = Tier 2 bottleneck)
- **Queue & Persistence** -- Tier 2 queue depth (early warning for backend issues)
- **Cardinality Analysis** -- top metrics by cardinality (use to tune processing rules)

---

## See Also

- [`examples/processing-tier1.yaml`](https://github.com/szibis/metrics-governor/blob/main/examples/processing-tier1.yaml) -- Tier 1 DaemonSet processing rules
- [`examples/processing-tier2.yaml`](https://github.com/szibis/metrics-governor/blob/main/examples/processing-tier2.yaml) -- Tier 2 StatefulSet processing rules
- [Production Guide](/docs/operations/production-guide) -- sizing, tuning, and cost analysis
- [Sharding](/docs/operations/sharding) -- consistent hashing for Tier 2 backend distribution
- [Dashboards](/docs/observability/dashboards) -- Grafana dashboards for monitoring both tiers
- [Configuration](/docs/getting-started/configuration) -- full YAML and CLI reference
- [Queue](/docs/architecture/queue) -- persistent queue configuration for Tier 2 durability

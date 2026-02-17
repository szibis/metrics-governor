<p align="center">
  <img src="docs/images/logo.svg" alt="metrics-governor logo" width="120">
</p>

<h1 align="center">metrics-governor</h1>

<p align="center">

[![Release](https://img.shields.io/github/v/release/szibis/metrics-governor?style=for-the-badge&logo=github&label=Release&color=2ea44f)](https://github.com/szibis/metrics-governor/releases/latest)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue?style=for-the-badge&logo=apache)](LICENSE)

[![Build](https://img.shields.io/github/actions/workflow/status/szibis/metrics-governor/build.yml?style=for-the-badge&logo=githubactions&logoColor=white&label=Build)](https://github.com/szibis/metrics-governor/actions/workflows/build.yml)
[![Security](https://img.shields.io/github/actions/workflow/status/szibis/metrics-governor/security-scan.yml?style=for-the-badge&logo=shieldsdotio&logoColor=white&label=Security)](https://github.com/szibis/metrics-governor/actions/workflows/security-scan.yml)
[![CodeQL](https://img.shields.io/github/actions/workflow/status/szibis/metrics-governor/codeql.yml?style=for-the-badge&logo=github&logoColor=white&label=CodeQL)](https://github.com/szibis/metrics-governor/actions/workflows/codeql.yml)

[![Tests](https://img.shields.io/badge/Tests-3100+-success?style=for-the-badge&logo=testinglibrary&logoColor=white)](docs/testing.md#test-coverage-by-component)
[![Coverage](https://img.shields.io/badge/Coverage-90%25-brightgreen?style=for-the-badge&logo=codecov&logoColor=white)](https://github.com/szibis/metrics-governor/actions/workflows/build.yml)
[![Race Detector](https://img.shields.io/badge/Race_Detector-passing-success?style=for-the-badge&logo=go&logoColor=white)](docs/testing.md)
[![Go Lines](https://img.shields.io/badge/Go_Code-169k_lines-informational?style=for-the-badge&logo=go&logoColor=white)](.)
[![Docs](https://img.shields.io/badge/Docs-31_guides-8A2BE2?style=for-the-badge&logo=readthedocs&logoColor=white)](docs/)
[![Benchmarks](https://img.shields.io/badge/Benchmarks-5_Matrix_Tests-success?style=for-the-badge&logo=speedtest&logoColor=white)](https://github.com/szibis/metrics-governor/actions/workflows/benchmark.yml)

[![OTLP](https://img.shields.io/badge/OTLP-gRPC_%7C_HTTP-4a90d9?style=for-the-badge&logo=opentelemetry&logoColor=white)](docs/receiving.md)
[![PRW](https://img.shields.io/badge/PRW-1.0_%7C_2.0-e8833a?style=for-the-badge&logo=prometheus&logoColor=white)](docs/receiving.md#prometheus-remote-write-receiver)
[![vtprotobuf](https://img.shields.io/badge/vtprotobuf-Zero_Alloc-00ADD8?style=for-the-badge&logo=go&logoColor=white)](docs/performance.md)
[![Alerts](https://img.shields.io/badge/Alerts-13_Rules_%2B_Runbooks-dc3545?style=for-the-badge&logo=prometheus&logoColor=white)](docs/alerting.md)
[![SLOs](https://img.shields.io/badge/SLOs-Error_Budgets_%2B_Burn_Rate-8B5CF6?style=for-the-badge&logo=prometheus&logoColor=white)](docs/slo.md)
[![Helm Chart](https://img.shields.io/badge/Helm-Chart_Included-0F1689?style=for-the-badge&logo=helm&logoColor=white)](helm/metrics-governor/)
[![Grafana](https://img.shields.io/badge/Grafana-Dashboards-F46800?style=for-the-badge&logo=grafana&logoColor=white)](dashboards/)
[![Playground](https://img.shields.io/badge/Playground-Config_Tool-20c997?style=for-the-badge&logo=googlechrome&logoColor=white)](https://szibis.github.io/metrics-governor/)

</p>

---

**metrics-governor** is a high-performance metrics governance proxy for **OTLP** and **Prometheus Remote Write**. Drop it between your apps and your backend to control cardinality, transform metrics in-flight, and scale horizontally — with zero data loss.

**Any pipeline. Any backend. On-prem or cloud.** Whether you're shipping metrics to Prometheus, Grafana Cloud, Datadog, Splunk, VictoriaMetrics, or any OTLP-compatible backend — metrics-governor sits in front and gives you governance powers that no collector, agent, or vendor provides out of the box.

> **Two native pipelines. Zero conversion. Zero allocation.** OTLP stays OTLP. PRW stays PRW. Each protocol runs its own receive-process-export path with full feature parity, no conversion overhead, and zero-allocation serialization via [vtprotobuf](https://github.com/planetscale/vtprotobuf).

### What's New

- **v1.0.1 — Memory optimization** — GOGC tuning (200→100) + Green Tea GC + reduced buffer/queue allocation. Memory at 50k dps dropped **48%** (37.5%→19.5%) with only +0.19pp CPU. Memory budget metrics added for operational visibility. [Details](docs/performance.md)
- **v1.0 stable release** — All 15 deprecated CLI flags, legacy sampling metrics, and backward-compatibility shims removed. Clean, unified API surface.
- **vtprotobuf integration** (v0.44) — Zero-allocation protobuf marshal/unmarshal via [PlanetScale vtprotobuf](https://github.com/planetscale/vtprotobuf) with `sync.Pool` message reuse. Measured **<1% CPU** at 100k dps.
- **Pipeline performance** (v1.0.1) — Lock-free atomic counters, single-shot zstd, pooled compression. Stats full-mode now viable for production.
- **3,100+ tests** — Comprehensive coverage including race detector, vtprotobuf integration, and parity tests across all packages.

> Migrating from v0.x? All deprecated flags have replacements — see [DEPRECATIONS.md](DEPRECATIONS.md) for the full migration table.

### The Cardinality Problem — And Why It's Still Unsolved

Metric cardinality is the silent budget killer in observability. Every distinct combination of metric name and label values creates a separate time series. One unbounded label — a user ID, a request path, an ephemeral container name — can turn a single counter into millions of series, crushing your storage backend and exploding your costs.

**What's missing across the industry is governance *in transit*** — intelligence between your apps and your backend that knows who the offenders are, protects everyone else, and escalates gradually instead of cutting blindly. That's what metrics-governor does.

### Comparison: Open-Source Collectors & Agents

How metrics-governor compares against the most common open-source metrics collectors and agents:

| Feature | [metrics-governor](https://github.com/szibis/metrics-governor) | [OTel Collector](https://opentelemetry.io/docs/collector/) | [Grafana Alloy](https://grafana.com/oss/alloy-opentelemetry-collector/) | [vmagent](https://docs.victoriametrics.com/vmagent/) | [Vector](https://vector.dev/) | [Prometheus](https://prometheus.io/) | [Cribl Stream](https://cribl.io/stream/) |
|---------|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **Cardinality Governance** | | | | | | | |
| Adaptive limiting (drop only top offenders) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Tiered escalation (log→sample→strip→drop) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Per-group / per-tenant quotas | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ |
| Dry-run mode for limits | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ |
| Dead rule detection | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Rule ownership labels (team routing) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Processing** | | | | | | | |
| Static filter / drop | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Label transform (rename, regex, add/remove) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Downsample (per-series temporal compression) | ✅ | ❌ | ❌ | ⚠️ | ❌ | ❌ | ❌ |
| Cross-series aggregation (avg, sum, p95) | ✅ | ⚠️ | ⚠️ | ✅ | ⚠️ | ⚠️ | ⚠️ |
| Classify (derive ownership labels) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ |
| **Pipeline** | | | | | | | |
| OTLP native (gRPC + HTTP) | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ |
| PRW native (no conversion) | ✅ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ | ✅ |
| Persistent queue / zero data loss | ✅ | ⚠️ | ⚠️ | ✅ | ✅ | ⚠️ | ✅ |
| Consistent hash sharding | ✅ | ❌ | ⚠️ | ⚠️ | ❌ | ❌ | ⚠️ |
| Circuit breaker / backpressure | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ⚠️ | ✅ |

<details>
<summary>Legend and notes</summary>

- ✅ Fully supported — ⚠️ Partial or limited — ❌ Not available
- **vmagent** OTLP: experimental ingestion since v1.93+, primarily PRW-focused
- **vmagent** downsample: stream aggregation provides time-based aggregation, not per-series compression algorithms (LTTB, SDT, CV-based)
- **vmagent** sharding: requires external hashmod relabeling across multiple instances
- **OTel Collector** PRW: available via contrib receiver/exporter, involves internal conversion
- **OTel Collector** aggregation: `groupbyattrsprocessor` provides basic grouping, not full statistical aggregation
- **OTel Collector** persistent queue: `file_storage` extension, limited compared to dedicated disk queue
- **Grafana Alloy** sharding: clustering mode with hash ring distribution
- **Vector** OTLP: source and sink available, later addition to the platform
- **Vector** aggregation: `aggregate` transform provides interval-based reduction, limited cross-series operations
- **Prometheus** OTLP: receiver available since v2.47+, recording rules provide aggregation (not in forwarding path)
- **Prometheus** persistent queue: WAL-based remote write queue, limited durability guarantees
- **Cribl Stream** quotas: routing by source/destination, not per-metric-group adaptive enforcement
- **Cribl Stream** classify: data classification available, not metrics-ownership-specific

</details>

### Comparison: Vendor Cardinality Management

How metrics-governor's in-transit governance compares against vendor-side cardinality management solutions:

| Feature | [metrics-governor](https://github.com/szibis/metrics-governor) | [Datadog MwL](https://docs.datadoghq.com/metrics/metrics-without-limits/) | [Grafana Adaptive Metrics](https://grafana.com/docs/grafana-cloud/cost-management-and-billing/reduce-costs/metrics-costs/control-metrics-usage-via-adaptive-metrics/) | [Splunk MPM](https://docs.splunk.com/observability/en/infrastructure/metrics-pipeline/metrics-pipeline.html) | [Chronosphere](https://chronosphere.io/) | [New Relic](https://newrelic.com/) |
|---------|:---:|:---:|:---:|:---:|:---:|:---:|
| **Where it runs** | **In transit** (your infra) | Backend (SaaS) | Backend (SaaS) | Backend (SaaS) | Backend (SaaS) | Backend (SaaS) |
| **Open source** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Reduces volume before shipping** | ✅ | ❌ | ❌ | ❌ | ⚠️ | ❌ |
| Adaptive limiting (top offenders only) | ✅ | ❌ | ⚠️ | ❌ | ⚠️ | ❌ |
| Tiered escalation | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Tag allowlist / blocklist | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ |
| Per-group / per-tenant quotas | ✅ | ❌ | ❌ | ❌ | ✅ | ❌ |
| Unused dimension detection | ⚠️ | ✅ | ✅ | ✅ | ✅ | ⚠️ |
| ML-based recommendations | ❌ | ❌ | ✅ | ❌ | ⚠️ | ❌ |
| Downsample / aggregate in-transit | ✅ | ❌ | ❌ | ⚠️ | ❌ | ❌ |
| Dead rule detection | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Works with any backend | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| No vendor lock-in | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Self-hosted / on-prem | ✅ | ❌ | ❌ | ❌ | ⚠️ | ❌ |

<details>
<summary>Legend and notes</summary>

- ✅ Fully supported — ⚠️ Partial or limited — ❌ Not available
- **Datadog Metrics without Limits**: Decouples ingestion from indexing — all data is ingested (and billed), you choose which tags to keep queryable. Does not reduce data in transit.
- **Grafana Adaptive Metrics**: ML-based recommendations for tag aggregation in Grafana Cloud. Suggestions only — requires manual approval. Cloud-only, not available on-prem.
- **Splunk MPM**: Dimension utilization ranking (R0-R5), aggregation rules. Available in Splunk Observability Cloud only. Aggregation reduces stored MTS but doesn't reduce ingest volume.
- **Chronosphere**: Control plane with aggregation rules and quotas. Available as SaaS and on-prem (limited). Reduces stored data but relies on Chronosphere's storage.
- **New Relic**: Drop rules and data management. Limited cardinality-specific controls compared to dedicated governance tools.
- **metrics-governor** unused dimension detection: Dead rule detection tracks stale rules; per-metric stats in `full` mode tracks cardinality per metric. Not ML-based discovery.

</details>

### Universal Governance for Mixed Environments

Whether you're running **legacy Prometheus Remote Write**, migrating to **modern OpenTelemetry**, or operating both in parallel — metrics-governor provides a single governance layer across all your metrics traffic.

- **Bridge old and new** — adopt OTel incrementally while maintaining full control over existing Prometheus infrastructure
- **Same rules, same protection** — cardinality limits, processing rules, and alerting work identically across both protocols
- **Single pane of governance** — one proxy, one config, one set of dashboards for your entire metrics pipeline regardless of protocol mix

## Why metrics-governor?

| Challenge | How metrics-governor Solves It |
|-----------|-------------------------------|
| **Cardinality explosions** crush your backend | [Adaptive limiting](docs/limits.md) identifies and drops only the **top offenders** — well-behaved services keep flowing |
| **All-or-nothing** enforcement kills good data | [Tiered escalation](docs/limits.md) with graduated responses: log → sample → strip labels → drop |
| **Raw volume** too high for storage budget | [Processing rules](docs/processing-rules.md) sample, downsample, aggregate, classify, transform, or drop metrics **before they leave the proxy** |
| **Storage explosion** from a noisy tenant | [Multi-tenancy](docs/tenant.md) with per-tenant quotas and [adaptive limits](docs/limits.md) — detect tenants, enforce budgets, protect storage without blanket-dropping |
| **No team accountability** for metric costs | [Rule ownership labels](docs/processing-rules.md) attach `team`, `slack_channel`, `pagerduty_service` to any rule for Alertmanager routing |
| **Data loss** during backend outages | [Always-queue architecture](docs/resilience.md) with circuit breaker, persistent disk queue, and exponential backoff — zero data loss by default |
| **Single backend** can't keep up | [Consistent sharding](docs/sharding.md) fans out to N backends via K8s DNS discovery with stable hash routing |
| **No visibility** into the metrics pipeline | [Real-time stats](docs/statistics.md), [13 production alerts](docs/alerting.md), [Grafana dashboards](dashboards/), and [dead rule detection](docs/processing-rules.md#dead-rule-detection) |
| **Unpredictable costs** from runaway services | [Per-group tracking](docs/limits.md) with configurable limits, dry-run mode, and ownership labels for team routing |
| **Need team/severity labels** derived from business values | [Transform rules](docs/processing-rules.md) — build `severity`, `team`, `env` from metric names and label values |
| **Stale rules** pile up unnoticed | [Dead rule detection](docs/processing-rules.md#dead-rule-detection) tracks last-match time for every rule, with alerts for stale cleanup |
| **Complex deployment** planning | [Interactive Playground](https://szibis.github.io/metrics-governor/) generates Helm, app, and limits YAML from your throughput inputs |

---

## Architecture

<p align="center">
  <img src="docs/images/architecture.svg" alt="metrics-governor architecture" width="100%">
</p>

<details>
<summary>View as text diagram (Mermaid)</summary>

```mermaid
flowchart LR
    subgraph Sources["&nbsp; Sources &nbsp;"]
        S1["OTLP gRPC / HTTP\nApps · Agents · SDKs"]:::source
        S2["PRW 1.0 / 2.0\nPrometheus · Grafana Agent"]:::source
    end

    subgraph MG["&nbsp; ⚡ metrics-governor &nbsp;"]
        direction TB
        subgraph OTLP["&thinsp; OTLP Pipeline &thinsp;"]
            direction LR
            O1(["Receive"]):::rx --> O2(["Process"]):::proc --> O3(["Limit"]):::limit
            O3 --> O4(["Queue"]):::queue --> O5(["Prepare"]):::prep --> O6(["Send"]):::send
            O6 -. "retry" .-> O4
        end
        subgraph PRW["&thinsp; PRW Pipeline &thinsp;"]
            direction LR
            P1(["Receive"]):::rx --> P2(["Process"]):::proc --> P3(["Limit"]):::limit
            P3 --> P4(["Queue"]):::queue --> P5(["Prepare"]):::prep --> P6(["Send"]):::send
            P6 -. "retry" .-> P4
        end
    end

    subgraph Backends["&nbsp; Backends &nbsp;"]
        B1["Collector · Mimir\nVictoriaMetrics · Grafana Cloud"]:::backend
        B2["Prometheus · Thanos\nVictoriaMetrics · Cortex"]:::backend
    end

    S1 -->|"gRPC :4317\nHTTP :4318"| O1
    S2 -->|"HTTP :9091"| P1
    O6 --> B1
    P6 --> B2

    classDef source fill:#3498db,stroke:#1a5276,color:#fff,stroke-width:2px
    classDef rx fill:#1abc9c,stroke:#0e6655,color:#fff,stroke-width:2px
    classDef proc fill:#9b59b6,stroke:#6c3483,color:#fff,stroke-width:2px
    classDef limit fill:#e74c3c,stroke:#922b21,color:#fff,stroke-width:2px
    classDef queue fill:#f39c12,stroke:#b7770a,color:#fff,stroke-width:2px
    classDef prep fill:#3498db,stroke:#1a5276,color:#fff,stroke-width:2px
    classDef send fill:#2ecc71,stroke:#1a8c4e,color:#fff,stroke-width:2px
    classDef backend fill:#2ecc71,stroke:#1a8c4e,color:#fff,stroke-width:2px

    style Sources fill:#eaf2f8,stroke:#2980b9,stroke-width:2px,color:#1a5276
    style MG fill:#f9f3e3,stroke:#d4a017,stroke-width:3px,color:#7d6608
    style OTLP fill:#e8f6f3,stroke:#1abc9c,stroke-width:1px,color:#0e6655
    style PRW fill:#fef5e7,stroke:#f39c12,stroke-width:1px,color:#b7770a
    style Backends fill:#eafaf1,stroke:#2ecc71,stroke-width:2px,color:#1a8c4e
```

</details>

Each pipeline runs independently: **Receive** → **Process** → **Limit** → **Queue** → **Prepare** → **Send** → **Backend**. Failed exports retry through the queue with circuit breaker protection.

---

## Features

### Receive — Dual Native Protocols

| Protocol | Ports | Capabilities |
|----------|-------|-------------|
| **OTLP gRPC** | `:4317` | Full `ExportMetricsService`, TLS/mTLS, bearer token, gzip/zstd, vtprotobuf zero-alloc unmarshal |
| **OTLP HTTP** | `:4318` | Protobuf + JSON, gzip/zstd/snappy decompression, content negotiation, vtprotobuf pool reuse |
| **PRW 1.0/2.0** | `:9091` | Auto-detect version, native histograms, VictoriaMetrics mode, exemplars |

Backpressure built in: capacity-bounded buffers return `429` / `ResourceExhausted` when full. [Docs](docs/receiving.md)

**Supported backends:**

| Protocol | Backends |
|----------|----------|
| **OTLP** | OpenTelemetry Collector, Grafana Mimir, Cortex, VictoriaMetrics, ClickHouse, Grafana Cloud |
| **PRW** | Prometheus, VictoriaMetrics, Grafana Mimir, Cortex, Thanos Receive, Amazon Managed Prometheus, GCP Managed Prometheus, Grafana Cloud |

### Process — Unified Rules Engine

Six actions in a single ordered pipeline — first match wins:

| Action | What It Does | Terminal? |
|--------|-------------|:---------:|
| **[Sample](docs/processing-rules.md)** | Stochastic reduction (probabilistic or head-N) | Yes |
| **[Downsample](docs/processing-rules.md)** | Per-series compression — 10 methods incl. adaptive CV-based, LTTB, SDT | Yes |
| **[Aggregate](docs/processing-rules.md)** | Cross-series reduction with `group_by` — avg, sum, p95, stddev, and more | Yes |
| **[Transform](docs/processing-rules.md)** | 12 label operations — rename, regex replace, add, remove, keep, drop | No (chains) |
| **[Classify](docs/processing-rules.md)** | Derive ownership labels (team, severity, priority) from metric metadata | No (chains) |
| **[Drop](docs/processing-rules.md)** | Unconditional removal | Yes |

**Transform → Classify chaining**: non-terminal actions chain — classify metrics into categories, then transform labels to match your storage schema in a single pass. Plus **dead rule detection**: always-on metrics track when rules stop matching, with optional scanner and alert rules for stale rule cleanup. [Docs](docs/processing-rules.md)

### Control — Intelligent Cardinality Governance

- **[Adaptive Limiting](docs/limits.md)** — Drops only the top offenders, not everything. Per-group tracking by service, namespace, or any label combination. Tiered escalation: log → sample → strip labels → drop. Dry-run mode for safe rollouts
- **[Cardinality Tracking](docs/cardinality-tracking.md)** — Three modes: **Bloom filter** (98% less memory — 1.2 MB vs 75 MB @ 1M series), **HyperLogLog** (constant 12 KB), **Hybrid** (auto-switches at threshold)
- **[Bloom Persistence](docs/bloom-persistence.md)** — Save/restore filter state across restarts, eliminating cold-start re-learning
- **[Rule Ownership Labels](docs/processing-rules.md)** — Attach `team`, `slack_channel`, `pagerduty_service` to any rule for Alertmanager routing

### Export — High-Throughput Pipeline

| Optimization | Impact | How |
|-------------|--------|-----|
| **[vtprotobuf](docs/performance.md)** | **Zero-allocation marshal/unmarshal** | PlanetScale vtprotobuf with `sync.Pool` message reuse — near-zero GC pressure |
| **[Pipeline Split](docs/exporting.md)** | **+60-76% throughput** | CPU-bound preparers (NumCPU) compress, I/O-bound senders (NumCPU x 2) send HTTP |
| **[AIMD Batch Tuning](docs/exporting.md)** | Auto-discovers optimal batch size | +25% after 10 successes, -50% on failure, HTTP 413 ceiling discovery |
| **[Adaptive Worker Scaling](docs/exporting.md)** | 1 to NumCPU x 4 workers | EWMA latency tracking, scale up on queue depth, halve on 30s idle |
| **[Async Send](docs/exporting.md)** | Max network utilization | Semaphore-bounded concurrency: 4/sender, NumCPU x 8 global |
| **[Connection Pre-warming](docs/exporting.md)** | Zero cold-start latency | HEAD requests at startup establish connection pools |
| **[String Interning](docs/performance.md)** | **76% fewer allocations** | Label deduplication across the hot path |
| **[Compression Pooling](docs/compression.md)** | 80% fewer allocs | Reusable gzip/zstd/snappy encoder pools |

### Protect — Zero Data Loss Architecture

- **[Always-Queue](docs/resilience.md)** — All data flows through the queue (VMAgent/OTel-inspired), eliminating flush-time blocking
- **[Persistent Queue](docs/resilience.md)** — FastQueue disk-backed with snappy compression, 256 KB buffered I/O, write coalescing — **128x fewer IOPS, 70% less disk I/O**
- **[Circuit Breaker](docs/resilience.md)** — Three-state (closed/open/half-open) with CAS transitions, prevents cascading failures
- **[Split-on-Error](docs/resilience.md)** — Oversized batches auto-split on HTTP 413 from Mimir, Thanos, VictoriaMetrics, Cortex
- **[Backpressure](docs/resilience.md)** — Buffer returns 429/ResourceExhausted; percentage-based memory sizing (15% buffer, 15% queue)
- **[Graceful Shutdown](docs/resilience.md)** — Drains in-flight exports and persists queue state before termination

### Scale — Horizontal and Hierarchical

- **[Consistent Sharding](docs/sharding.md)** — Hash ring with 150 virtual nodes per endpoint, K8s DNS discovery with automatic failover. Same series always routes to same backend (OTLP and PRW)
- **[Two-Tier Architecture](docs/two-tier-architecture.md)** — DaemonSet edge (Tier 1) processes per-node, StatefulSet gateway (Tier 2) aggregates globally — **10-50x traffic reduction** between nodes
- **[Percentage-Based Memory](docs/performance.md)** — Buffer and queue sizes auto-scale with container resources via cgroup detection
- **[Three Queue Modes](docs/queue.md)** — `memory` (fastest), `disk` (durable), `hybrid` (best of both)

### Monitor — Full Observability

- **[Real-Time Statistics](docs/statistics.md)** — Per-metric cardinality, datapoints, and limit violations with three stats levels (none/basic/full)
- **[13 Production Alerts](docs/alerting.md)** — Zero-overlap design: DataLoss, ExportDegraded, QueueSaturated, CircuitOpen, OOMRisk, CardinalityExplosion, and more — each with runbooks
- **[Dead Rule Detection](docs/processing-rules.md)** — Always-on last-match tracking for processing and limits rules, with alert rules for stale rule cleanup
- **[Grafana Dashboards](dashboards/)** — Operations and development dashboards included, auto-imported via provisioning
- **[Health Endpoints](docs/health.md)** — `/live` and `/ready` probes with per-component JSON status for Kubernetes

### Deploy — Production Ready from Day One

- **[Helm Chart](helm/metrics-governor/)** — Full production chart with probes, ConfigMap sidecar, HPA-ready, alert rules integrated
- **[Profiles](docs/profiles.md)** — 6 presets (`minimal`, `balanced`, `safety`, `observable`, `resilient`, `performance`) — one flag to set 30+ parameters, tuned from measured vtprotobuf benchmarks
- **[Hot Reload](docs/reload.md)** — SIGHUP reloads limits and processing rules without restart; ConfigMap sidecar for Kubernetes
- **[Interactive Playground](https://szibis.github.io/metrics-governor/)** — Browser tool estimates resources, generates Helm/YAML/limits configs, recommends cloud storage classes
- **[TLS/mTLS + Auth](docs/tls.md)** — Full TLS, mutual TLS, bearer token, basic auth, custom headers
- **Zero-Config Start** — Works out of the box with sensible defaults; add limits and sharding when needed

---

## Performance at a Glance

**Measured comparison** — governor vs OTel Collector vs vmagent (4-core, 1 GB, OTLP gRPC → HTTP):

| Load | Tool | CPU avg | Memory avg | Ingestion |
|------|------|---------|-----------|-----------|
| **50k dps** | **metrics-governor** (balanced) | **4.51%** | **19.5%** | 99.25% |
| | OTel Collector | 4.51% | 15.3% | 99.83% |
| | vmagent | 2.94% | 7.3% | 99.90% |
| **100k dps** | **metrics-governor** (balanced) | **6.47%** | **18.4%** | 99.53% |
| | OTel Collector | 6.58% | 9.3% | 99.83% |
| | vmagent | 16.70% | 3.2% | 99.83% |

Governor scales **sublinearly**: 1.43x CPU for 2x load (50k→100k). At 100k dps, governor uses **less CPU than OTel Collector** while providing full governance features neither tool offers.

| Optimization | Impact |
|-------------|--------|
| vtprotobuf marshal/unmarshal | **Zero allocations** — `sync.Pool` message reuse, near-zero GC pressure |
| Pipeline split | **+60-76% throughput** — CPU-bound preparers + I/O-bound senders |
| Green Tea GC + GOGC=100 | **48% memory reduction** vs default GC tuning |
| Cardinality memory (Bloom) | **1.2 MB** per 1M series (98% less than maps) |
| String interning | **76%** fewer allocations on the hot path |
| Disk I/O (buffered + coalesced) | **128x fewer IOPS**, 70% less throughput |
| Queue compression (snappy) | **2.5-3x** storage capacity |
| Two-tier traffic reduction | **10-50x** between DaemonSet and StatefulSet tiers |

See [Performance Guide](docs/performance.md) and [Benchmarks](https://github.com/szibis/metrics-governor/actions/workflows/benchmark.yml) for methodology and full results.

---

## Flexible Operating Modes

One binary, six profiles — choose durability, observability, cost efficiency, or raw throughput:

| Priority | Queue Mode | Stats Level | Profile | Cost Efficiency | Trade-off |
|----------|-----------|-------------|---------|----------------|-----------|
| **Maximum Safety** | `disk` | `full` | `safety` | High | Full crash recovery + per-metric cost tracking |
| **Durable + Observable** | `hybrid` | `full` | `observable` | High | Disk spillover + full per-metric stats for cost visibility |
| **Resilient** | `hybrid` | `basic` | `resilient` | Medium | Memory-speed normally, disk spillover for spikes |
| **High Throughput** | `hybrid` | `basic` | `performance` | Low | Pipeline split + max throughput + adaptive tuning |
| **Balanced** (default) | `memory` | `basic` | `balanced` | Medium | Best performance with essential metrics |
| **Minimal Footprint** | `memory` | `none` | `minimal` | — | Smallest resource usage, pure proxy |

> Higher proxy resources (disk, CPU) can save 10–100x in backend SaaS costs by identifying and reducing expensive metrics before they reach your storage. See [Cost Efficiency](docs/profiles.md#cost-efficiency).

See [Profiles](docs/profiles.md) and [Performance Tuning](docs/performance.md#performance-tuning-knobs) for details.

---

## Quick Start

```bash
# Start metrics-governor with adaptive limits
metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -limits-config limits.yaml \
  -limits-dry-run=false \
  -stats-labels service,env

# Point your apps at metrics-governor instead of the collector
# export OTEL_EXPORTER_OTLP_ENDPOINT=http://metrics-governor:4317
```

```yaml
# limits.yaml — adaptive limiting by service
rules:
  - name: "per-service-limits"
    match:
      labels:
        service: "*"
    max_cardinality: 10000
    max_datapoints_rate: 100000
    action: adaptive
    group_by: ["service"]
```

When cardinality exceeds 10,000, metrics-governor identifies which service is the top contributor and drops only that service's excess metrics — everyone else keeps flowing.

---

## Playground

Plan your deployment in seconds. The **interactive Playground** estimates CPU, memory, disk I/O, and K8s pod sizing from your throughput inputs, and generates ready-to-use Helm, app config, and limits YAML — all in a single zero-dependency HTML page.

**[Open Playground](https://szibis.github.io/metrics-governor/)** | [Source](tools/playground/)

<table>
<tr>
<td width="50%" align="center">
<a href="docs/images/config-helper-inputs.svg"><img src="docs/images/config-helper-inputs.svg" alt="Throughput inputs" width="100%"></a>
<br><sub><b>Throughput Inputs</b> — Simple &amp; Advanced modes</sub>
</td>
<td width="50%" align="center">
<a href="docs/images/config-helper-estimation.svg"><img src="docs/images/config-helper-estimation.svg" alt="Resource estimation" width="100%"></a>
<br><sub><b>Resource Estimation</b> — CPU, memory, disk, fit check</sub>
</td>
</tr>
<tr>
<td width="50%" align="center">
<a href="docs/images/config-helper-preview.svg"><img src="docs/images/config-helper-preview.svg" alt="YAML preview" width="100%"></a>
<br><sub><b>Editable YAML</b> — Bidirectional sync with inputs</sub>
</td>
<td width="50%" align="center">
<a href="docs/images/config-helper-fitcheck.svg"><img src="docs/images/config-helper-fitcheck.svg" alt="Fit check" width="100%"></a>
<br><sub><b>Fit Check</b> — Pod override &amp; resource validation</sub>
</td>
</tr>
</table>

---

## Documentation

| | Guide | Description |
|:---:|-------|-------------|
| 🚀 | [**Installation**](docs/installation.md) | Source, Docker, or Helm chart |
| ⚙️ | [**Configuration**](docs/configuration.md) | YAML config and CLI flags reference |
| 📋 | [**Profiles**](docs/profiles.md) | 6 presets: `minimal`, `balanced`, `safety`, `observable`, `resilient`, `performance` |
| 📡 | [**Receiving**](docs/receiving.md) | OTLP gRPC/HTTP, PRW 1.0/2.0, backpressure |
| 📡 | [**PRW Protocol**](docs/prw.md) | PRW 1.0/2.0, native histograms, VictoriaMetrics mode |
| 🔄 | [**Processing Rules**](docs/processing-rules.md) | Sample, downsample, aggregate, transform, classify, drop, dead rule detection |
| 🏗️ | [**Two-Tier Architecture**](docs/two-tier-architecture.md) | DaemonSet edge + StatefulSet gateway pattern |
| 🎯 | [**Limits**](docs/limits.md) | Adaptive limiting, tiered escalation, per-label limits, rule ownership |
| 👥 | [**Multi-Tenancy**](docs/tenant.md) | Tenant detection (header/label/attribute), per-tenant quotas, priority-based enforcement |
| 🔀 | [**Sharding**](docs/sharding.md) | Consistent hashing, K8s DNS discovery |
| 📊 | [**Statistics**](docs/statistics.md) | Per-metric tracking, three stats levels |
| ⚡ | [**Export Pipeline**](docs/exporting.md) | Pipeline split, batch tuning, adaptive scaling |
| ⚡ | [**Performance**](docs/performance.md) | Bloom filters, string interning, I/O optimization |
| 🛡️ | [**Resilience**](docs/resilience.md) | Circuit breaker, persistent queue, backoff |
| 📦 | [**Queue**](docs/queue.md) | Memory, disk, hybrid queue modes |
| 🔢 | [**Cardinality Tracking**](docs/cardinality-tracking.md) | Bloom, HyperLogLog, Hybrid mode |
| 💾 | [**Bloom Persistence**](docs/bloom-persistence.md) | Save/restore filter state across restarts |
| 🚨 | [**Alerting**](docs/alerting.md) | 13 alerts with runbooks, dead rule detection |
| 🎯 | [**SLOs**](docs/slo.md) | SLI definitions, error budgets, burn-rate alerts, health dashboard |
| 📊 | [**Dashboards**](docs/dashboards.md) | Grafana operations and development dashboards |
| 🏭 | [**Production Guide**](docs/production-guide.md) | Sizing, HPA/VPA, DaemonSet, bare metal |
| 🔧 | [**Stability Tuning**](docs/stability-guide.md) | Graduated spillover, load shedding, drain ordering, backpressure tuning |
| 🏥 | [**Health**](docs/health.md) | Kubernetes liveness and readiness probes |
| 🔄 | [**Dynamic Reload**](docs/reload.md) | Hot-reload via SIGHUP with ConfigMap sidecar |
| 🔐 | [**TLS**](docs/tls.md) | Server/client TLS, mTLS |
| 🔑 | [**Auth**](docs/authentication.md) | Bearer token, basic auth, custom headers |
| 📦 | [**Compression**](docs/compression.md) | gzip, zstd, snappy |
| 🌐 | [**HTTP Settings**](docs/http-settings.md) | Connection pools, timeouts, HTTP/2 |
| 📝 | [**Logging**](docs/logging.md) | JSON structured logging |
| 🖥️ | [**Playground**](docs/playground.md) | Interactive deployment planner |
| 🧪 | [**Testing**](docs/testing.md) | Test environment, Docker Compose |
| 🛠️ | [**Development**](docs/development.md) | Building, contributing |
| 📜 | [**Changelog**](CHANGELOG.md) | Release history with breaking changes |
| ⚠️ | [**Deprecations**](DEPRECATIONS.md) | Deprecation lifecycle, migration table |

---

## Contributing

Contributions welcome! See [Development Guide](docs/development.md) for details.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Support

- 📖 [Documentation](docs/)
- 🐛 [Issue Tracker](https://github.com/szibis/metrics-governor/issues)
- 💬 [Discussions](https://github.com/szibis/metrics-governor/discussions)

---

<p align="center">
  <sub>Built with ❤️ for the observability community</sub>
</p>

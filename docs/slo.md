# SLI/SLO Framework

## Table of Contents

- [Overview](#overview)
- [SLI Definitions](#sli-definitions)
  - [Delivery Ratio](#delivery-ratio)
  - [Export Success](#export-success)
  - [Intentional Drops](#intentional-drops)
- [SLO Targets](#slo-targets)
- [Error Budget](#error-budget)
- [Governor Metrics](#governor-metrics)
- [Burn-Rate Alerts](#burn-rate-alerts)
- [Dashboard Guide](#dashboard-guide)
- [Recording Rules (Alternative)](#recording-rules-alternative)
- [Configuration](#configuration)
  - [CLI Flags](#cli-flags)
  - [YAML Config](#yaml-config)
- [See Also](#see-also)

---

## Overview

metrics-governor includes a **governor-computed SLI/SLO framework** that answers the operational question: *"Are we meeting our service level objectives?"*

The framework computes SLI ratios, burn rates, and error budgets **directly inside the governor process** — no external recording rules, no Prometheus-side computation, no dependency on specific monitoring infrastructure. The metrics are exposed alongside existing `/metrics` output and can be scraped by any Prometheus-compatible system.

**Key concepts:**

- **SLI (Service Level Indicator):** A quantitative measure of service quality (e.g., "99.95% of datapoints delivered")
- **SLO (Service Level Objective):** The target value for an SLI (e.g., "99.9% delivery over 30 days")
- **Error Budget:** The allowed failure margin — if the SLO is 99.9%, the error budget is 0.1%
- **Burn Rate:** How fast the error budget is being consumed (1.0x = sustainable, 14.4x = budget gone in ~2 days)

---

## SLI Definitions

### Delivery Ratio

Measures end-to-end datapoint delivery across both OTLP and PRW pipelines.

```
delivery_ratio = good_events / eligible_events

good_events     = datapointsSent (OTLP) + datapointsSent (PRW)
eligible_events = datapointsReceived (OTLP + PRW) − intentional_drops
```

**Why subtract intentional drops?** Datapoints dropped by the limits enforcer are **policy decisions**, not failures. A tenant exceeding their quota triggers an intentional drop — this should not count against the delivery SLO.

| Metric | Source |
|--------|--------|
| `metrics_governor_sli_delivery_ratio{window="5m\|30m\|1h\|6h"}` | Governor-computed |
| `metrics_governor_sli_delivery_burn_rate{window="..."}` | Governor-computed |
| `metrics_governor_sli_delivery_budget_remaining` | Governor-computed |

### Export Success

Measures batch-level export reliability.

```
export_success = good_batches / total_batches

good_batches  = batchesSent (OTLP) + batchesSent (PRW)
total_batches = good_batches + exportErrors (OTLP) + exportErrors (PRW)
```

| Metric | Source |
|--------|--------|
| `metrics_governor_sli_export_success_ratio{window="5m\|30m\|1h\|6h"}` | Governor-computed |
| `metrics_governor_sli_export_burn_rate{window="..."}` | Governor-computed |
| `metrics_governor_sli_export_budget_remaining` | Governor-computed |

### Intentional Drops

The limits enforcer tracks `datapointsDropped` per rule. The SLI tracker calls `TotalDropped()` to sum all rule-level drops, then subtracts this from the delivery denominator:

```
eligible = received − intentional_drops
```

If no limits enforcer is configured, intentional drops are always 0.

---

## SLO Targets

| SLI | Default Target | Error Budget (30d) | Equivalent Downtime |
|-----|---------------|-------------------|-------------------|
| Delivery ratio | 99.9% | 0.1% | ~43 minutes |
| Export success | 99.5% | 0.5% | ~3.6 hours |

Targets are configurable via CLI flags or YAML. See [Configuration](#configuration).

---

## Error Budget

The error budget represents how much failure is "allowed" before breaching the SLO.

```
budget_remaining = 1 − (actual_error_rate / allowed_error_rate)
```

- **1.0** = No errors consumed, full budget available
- **0.5** = Half the budget consumed
- **0.0** = Budget exhausted — SLO breached
- **< 0** = Budget exceeded (clamped to 0.0 in metrics)

The governor computes error budget from the first-ever snapshot (startup) to the latest, projecting consumption rate over the configured budget window (default 30 days).

**Budget management policies:**

| Budget Remaining | Action |
|-----------------|--------|
| > 75% | Normal operations — deploy freely |
| 50-75% | Caution — investigate any active alerts |
| 25-50% | Restrict deployments, prioritize reliability |
| < 25% | Freeze changes, focus on budget recovery |

---

## Governor Metrics

All metrics are exposed at the `/metrics` endpoint alongside existing stats.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `metrics_governor_sli_delivery_ratio` | gauge | `window` | Delivery SLI ratio (0-1) |
| `metrics_governor_sli_export_success_ratio` | gauge | `window` | Export success SLI ratio (0-1) |
| `metrics_governor_sli_delivery_burn_rate` | gauge | `window` | Delivery burn rate (1.0 = at SLO pace) |
| `metrics_governor_sli_export_burn_rate` | gauge | `window` | Export burn rate |
| `metrics_governor_sli_delivery_budget_remaining` | gauge | — | Delivery error budget remaining (0-1) |
| `metrics_governor_sli_export_budget_remaining` | gauge | — | Export error budget remaining (0-1) |
| `metrics_governor_slo_target` | gauge | `sli` | Configured SLO target value |
| `metrics_governor_sli_uptime_seconds` | gauge | — | Seconds since SLI tracking started |
| `metrics_governor_sli_snapshots_total` | counter | — | Total snapshots recorded |

**Window labels:** `5m`, `30m`, `1h`, `6h`

---

## Burn-Rate Alerts

Six multi-window, multi-burn-rate alerts follow the [Google SRE Workbook](https://sre.google/workbook/alerting-on-slos/) pattern. They use governor-emitted metrics directly.

| Alert | Burn Rate | Windows | Budget Time | Severity |
|-------|-----------|---------|-------------|----------|
| `MetricsGovernorDeliveryBudgetCritical` | 14.4x | 1h AND 5m | ~2 days | critical |
| `MetricsGovernorDeliveryBudgetHigh` | 6x | 6h AND 30m | ~5 days | critical |
| `MetricsGovernorDeliveryBudgetSlow` | 1x | 6h AND 1h | 30 days | warning |
| `MetricsGovernorExportBudgetCritical` | 14.4x | 1h AND 5m | ~2 days | critical |
| `MetricsGovernorExportBudgetHigh` | 6x | 6h AND 30m | ~5 days | critical |
| `MetricsGovernorExportBudgetSlow` | 1x | 6h AND 1h | 30 days | warning |

**Multi-window logic:** Both the long and short window must exceed the threshold. This prevents alerting on brief spikes (short window only) or stale signals (long window only).

**How SLO alerts complement operational alerts:**

| Type | Purpose | Example | Response |
|------|---------|---------|----------|
| **Operational** (13 alerts) | Symptom-based — what's broken NOW | `ExportDegraded`, `QueueSaturated` | Fix the immediate issue |
| **SLO** (6 alerts) | Budget-based — impact OVER TIME | `DeliveryBudgetHigh` | Assess budget impact, prioritize |

See [alerts/slo-alerts.yaml](../alerts/slo-alerts.yaml) for the full alert definitions.

---

## Dashboard Guide

The **Metrics Governor - SLO** dashboard (`dashboards/slo.json`) provides an executive-level view of SLI/SLO health. It auto-provisions alongside existing dashboards.

### Rows

| Row | Content | What to Look For |
|-----|---------|-----------------|
| **Status at a Glance** | UP/DOWN, delivery ratio, export success, memory, rates | Any red stat = investigate immediately |
| **SLO Compliance** | Gauges + time series vs target lines | Ratio below target line = SLO breach |
| **Error Budget** | Budget remaining gauges + trend | Below 25% = freeze changes |
| **Burn Rate** | 5m/1h/6h burn rates with threshold lines | Above 14.4x line = page, above 1x = ticket |
| **Pipeline Health** | Throughput, errors, queue, circuit breaker | Supporting data for root cause analysis |
| **Resource Health** | Memory, GC pressure | Capacity-related context |

---

## Recording Rules (Alternative)

If you prefer Prometheus-side computation over governor-computed metrics, supplementary recording rules are available at `alerts/recording-rules.yaml`. These produce equivalent ratios and burn rates using `rate()` over raw counters.

**Limitation:** Recording rules cannot subtract intentional drops from the delivery denominator (they don't have access to the limits enforcer state). For policy-aware delivery ratios, use the governor-computed `metrics_governor_sli_delivery_ratio` metric.

Import:
```bash
cp alerts/recording-rules.yaml /etc/prometheus/rules/
kill -HUP $(pidof prometheus)
```

---

## Configuration

### CLI Flags

```
--sli-enabled          bool      Enable SLI metrics (default: true)
--sli-delivery-target  float64   Delivery SLO target, 0.0-1.0 (default: 0.999)
--sli-export-target    float64   Export success SLO target, 0.0-1.0 (default: 0.995)
--sli-budget-window    duration  Error budget window (default: 720h = 30 days)
```

### YAML Config

```yaml
stats:
  sli:
    enabled: true
    delivery_target: 0.999
    export_target: 0.995
    budget_window: 720h
```

### Environment Override

All flags can be overridden via CLI arguments, which take precedence over YAML config.

### Disabling SLI Metrics

To disable governor-computed SLI metrics entirely:

```bash
metrics-governor --sli-enabled=false
```

Or in YAML:
```yaml
stats:
  sli:
    enabled: false
```

---

## See Also

- [Alerting](alerting.md) — 13 operational alerts with runbooks
- [Health Endpoints](health.md) — Liveness and readiness probes
- [Statistics](statistics.md) — Per-metric cardinality and datapoint tracking
- [Performance](performance.md) — Memory optimization and benchmarks
- [Alert Rules README](../alerts/README.md) — Alert runbooks including SLO alerts

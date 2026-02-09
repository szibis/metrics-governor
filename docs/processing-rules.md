# Processing Rules

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Six Processing Actions](#six-processing-actions)
- [Configuration Reference](#configuration-reference)
  - [Common Fields](#common-fields)
  - [Sample](#sample)
  - [Downsample](#downsample)
  - [Aggregate](#aggregate)
  - [Transform](#transform)
  - [Classify](#classify)
  - [Drop](#drop)
- [Rule Ownership Labels](#rule-ownership-labels)
  - [Labels Field](#labels-field)
  - [Required Labels Enforcement](#required-labels-enforcement)
- [Dead Rule Detection](#dead-rule-detection)
  - [Always-On Metrics](#always-on-metrics)
  - [Optional Scanner](#optional-scanner)
  - [Alert Rules](#alert-rules)
- [Unmatched Metrics](#unmatched-metrics)
- [Execution Model](#execution-model)
- [Adaptive Downsampling Deep Dive](#adaptive-downsampling-deep-dive)
  - [How CV-Based Rate Adjustment Works](#how-cv-based-rate-adjustment-works)
  - [Adaptive vs Fixed Downsampling](#adaptive-vs-fixed-downsampling)
  - [Tuning Guide](#tuning-guide)
- [Processing Rules vs Prometheus Recording Rules](#processing-rules-vs-prometheus-recording-rules)
  - [Comparison](#comparison)
  - [When to Use Each](#when-to-use-each)
- [Real-Life Problems Solved](#real-life-problems-solved)
  - [Kubernetes Pod Churn](#kubernetes-pod-churn)
  - [High-Cardinality Labels](#high-cardinality-labels)
  - [Cross-Network Traffic Reduction](#cross-network-traffic-reduction)
  - [Monitoring Bill Reduction](#monitoring-bill-reduction)
  - [Label Inconsistency](#label-inconsistency)
  - [Privacy and PII Removal](#privacy-and-pii-removal)
- [Pipeline Position](#pipeline-position)
- [Observability](#observability)
  - [Per-Rule Metrics](#per-rule-metrics)
  - [Overall Pipeline Metrics](#overall-pipeline-metrics)
  - [Aggregate Engine Metrics](#aggregate-engine-metrics)
  - [Transform Engine Metrics](#transform-engine-metrics)
- [Hot Reload](#hot-reload)
- [Backward Compatibility](#backward-compatibility)
- [Performance Tuning](#performance-tuning)
- [See Also](#see-also)

Processing rules are configured via a YAML file specified with `--processing-config`. See [examples/processing.yaml](../examples/processing.yaml) for a complete example.

---

## Overview

The Processing Rules engine is the unified metrics processing layer in metrics-governor. It replaces the older sampling system with a single, declarative configuration file that handles six distinct actions:

- **Sample** -- stochastic datapoint reduction
- **Downsample** -- per-series time-windowed compression with fixed or adaptive rates
- **Aggregate** -- cross-series reduction with configurable functions
- **Transform** -- label manipulation (non-terminal, chains with other rules)
- **Classify** -- derive team ownership, severity, and priority labels (non-terminal)
- **Drop** -- unconditional 100% removal

Rules are evaluated in order. Each incoming metric walks the rule list until it matches a terminal action (sample, downsample, aggregate, or drop) or passes through all rules unchanged. Transform and classify rules are non-terminal: they modify labels and continue evaluation.

This design consolidates what previously required separate sampling configs, relabeling configs, and external recording rules into a single pipeline stage.

---

## Quick Start

Create a file called `processing.yaml`:

```yaml
staleness_interval: 10m

rules:
  # Keep 10% of debug metrics
  - name: reduce-debug
    input: "debug_.*"
    action: sample
    rate: 0.1
    method: probabilistic

  # Compress CPU metrics to 1-minute averages
  - name: cpu-downsample
    input: "process_cpu_.*"
    action: downsample
    method: avg
    interval: 1m

  # Drop all internal metrics
  - name: drop-internal
    input: "internal_.*"
    action: drop
```

Start metrics-governor with the processing config:

```bash
metrics-governor --processing-config=processing.yaml
```

That is all that is needed. Unmatched metrics pass through unchanged.

---

## Six Processing Actions

| Action | Purpose | Stateful? | Status |
|--------|---------|:---------:|--------|
| **sample** | Stochastic reduction -- keep a percentage of datapoints | No | Existing (renamed from sampling) |
| **downsample** | Per-series time-windowed compression (fixed or adaptive rate) | Yes | New |
| **aggregate** | Cross-series reduction with math functions | Yes | New |
| **transform** | Label manipulation (rename, set, hash, etc.) | No | New |
| **drop** | Unconditional 100% removal of matched metrics | No | Existing (renamed from sampling drop) |

---

## Configuration Reference

### Common Fields

Every rule shares these fields:

```yaml
rules:
  - name: "rule-name"          # Required. Unique identifier, used in metrics and logs.
    input: "metric_name_regex"  # Required. Regex matched against the metric name.
    input_labels:               # Optional. Additional label matchers (all must match).
      env: "production"         #   Exact match.
      service: "api-.*"         #   Regex match.
    action: sample              # Required. One of: sample, downsample, aggregate, transform, drop.
```

- **name** -- Must be unique across all rules. Used as the `rule` label in all processing metrics.
- **input** -- A regular expression anchored to the full metric name. `".*"` matches everything.
- **input_labels** -- A map of label name to value pattern. All specified labels must match for the rule to apply. Supports exact strings and regex patterns.
- **action** -- Determines which action-specific fields are expected.

---

### Sample

Stochastic datapoint reduction. Each datapoint is independently kept or dropped based on a configured probability.

```yaml
- name: reduce-debug
  input: "debug_.*"
  action: sample
  rate: 0.01            # Keep 1% of datapoints (0.0 to 1.0)
  method: probabilistic  # or "head"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rate` | float | 1.0 | Probability of keeping each datapoint. `1.0` = keep all, `0.0` = drop all. |
| `method` | string | `head` | `head`: deterministic, keeps first N per window. `probabilistic`: random per-datapoint coin flip. |

**When to use:** Quick, stateless reduction when you do not need time-series-aware compression. Good for debug/test metrics or as a safety valve.

---

### Downsample

Per-series time-windowed compression. Tracks each unique series by its label set and emits reduced output at the configured interval.

```yaml
- name: cpu-avg
  input: "process_cpu_.*"
  action: downsample
  method: avg
  interval: 1m
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `method` | string | required | Downsampling algorithm (see table below) |
| `interval` | duration | required | Time window for aggregation/emission |
| `resolution` | int | -- | Target number of output points per window (LTTB only) |
| `deviation` | float | -- | Deadband tolerance (SDT only) |
| `min_rate` | float | 0.05 | Minimum keep rate during stable periods (adaptive only) |
| `max_rate` | float | 1.0 | Maximum keep rate during volatile periods (adaptive only) |
| `variance_window` | int | 30 | Number of samples in the sliding CV window (adaptive only) |

**Available methods (10):**

| Method | Type | Description |
|--------|------|-------------|
| `avg` | Fixed | Arithmetic mean of values in the window |
| `min` | Fixed | Minimum value in the window |
| `max` | Fixed | Maximum value in the window |
| `sum` | Fixed | Sum of values in the window |
| `last` | Fixed | Last value seen in the window |
| `first` | Fixed | First value seen in the window |
| `count` | Fixed | Number of datapoints in the window |
| `lttb` | Fixed | Largest Triangle Three Buckets -- preserves visual shape of the series |
| `sdt` | Fixed | Swinging Door Trending -- deadband compression for near-constant signals |
| `adaptive` | Dynamic | CV-based rate adjustment -- keeps more data when the signal is volatile |

**Example -- adaptive downsampling:**

```yaml
- name: network-adaptive
  input: "node_network_.*"
  action: downsample
  method: adaptive
  interval: 30s
  min_rate: 0.1
  max_rate: 1.0
  variance_window: 30
```

---

### Aggregate

Cross-series reduction. Groups incoming series by a label subset and periodically flushes aggregated output. This is where the largest cardinality reductions happen.

```yaml
- name: http-by-service
  input: "http_requests_total"
  output: "http_requests_by_service"
  action: aggregate
  interval: 1m
  group_by: [service, env, method]
  functions: [sum, count]
  keep_input: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `output` | string | same as input | Name for the emitted aggregated metric. If omitted, uses the input name. |
| `interval` | duration | required | Flush interval for aggregated results |
| `group_by` | list | -- | Labels to keep as grouping dimensions. All other labels are dropped. |
| `drop_labels` | list | -- | Labels to remove. All other labels are kept as grouping dimensions. |
| `functions` | list | required | Aggregation functions to apply (see table below) |
| `keep_input` | bool | false | If `true`, pass through the original (unaggregated) datapoints as well |

**Important:** `group_by` and `drop_labels` are mutually exclusive. Use `group_by` when you know which labels you want to keep. Use `drop_labels` when you know which labels you want to remove but want to preserve everything else.

**Available functions:**

| Function | Output Suffix | Description |
|----------|---------------|-------------|
| `sum` | `_sum` | Sum of values across grouped series |
| `avg` | `_avg` | Arithmetic mean across grouped series |
| `min` | `_min` | Minimum value across grouped series |
| `max` | `_max` | Maximum value across grouped series |
| `count` | `_count` | Number of contributing series |
| `last` | `_last` | Most recently seen value |
| `increase` | `_increase` | Monotonic increase over the interval (counter-aware) |
| `rate` | `_rate` | Per-second rate of increase over the interval |
| `stddev` | `_stddev` | Standard deviation across grouped series |
| `stdvar` | `_stdvar` | Variance across grouped series |
| `quantiles(p1, p2, ...)` | `_p50`, `_p99`, etc. | Quantile estimates at specified percentiles |

**Example -- quantile aggregation:**

```yaml
- name: http-latency-percentiles
  input: "http_request_duration_seconds"
  action: aggregate
  interval: 1m
  group_by: [service, method]
  functions: [avg, quantiles(0.5, 0.9, 0.95, 0.99)]
  keep_input: false
```

**Example -- drop_labels instead of group_by:**

```yaml
- name: strip-pod-labels
  input: "kube_pod_.*"
  action: aggregate
  interval: 30s
  drop_labels: [pod, pod_ip, container_id, instance]
  functions: [last]
```

---

### Transform

Label manipulation. Transform rules are **non-terminal** -- they modify labels on matched metrics and then continue evaluation against subsequent rules. This allows chaining: normalize labels first, then aggregate or drop.

```yaml
- name: normalize-env
  input: ".*"
  action: transform
  when:
    - label: environment
      matches: ".*"
  operations:
    - rename:
        source: environment
        target: env
    - map:
        source: env
        target: env
        values:
          "production": "prod"
          "staging": "stg"
          "development": "dev"
        default: ""
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `when` | list | -- | Optional conditions that must all be true for the transform to apply |
| `operations` | list | required | Ordered list of label operations to execute |

**When conditions:**

```yaml
when:
  - label: env               # Label name to check
    matches: "prod.*"         # Regex pattern the label value must match
```

All `when` conditions must match (AND logic). If `when` is omitted, the transform applies to every metric matched by `input` and `input_labels`.

**Available operations (12):**

| Operation | Description | Example |
|-----------|-------------|---------|
| `remove` | Remove one or more labels | `remove: [pod_ip, container_id]` |
| `set` | Set a label to a fixed or interpolated value | `set: {label: env, value: "prod"}` |
| `rename` | Rename a label (preserves value) | `rename: {source: old, target: new}` |
| `copy` | Copy a label value to a new label | `copy: {source: service, target: app}` |
| `replace` | Regex replace within a label value | `replace: {label: instance, pattern: ":\\d+$", replacement: ""}` |
| `extract` | Extract a capture group into a new label | `extract: {source: service, target: base, pattern: "(.+)-v\\d+$", group: 1}` |
| `hash_mod` | Hash a label value and take modulus | `hash_mod: {source: user_id, target: user_hash, modulus: 65536}` |
| `lower` | Lowercase a label value | `lower: {label: env}` |
| `upper` | Uppercase a label value | `upper: {label: region}` |
| `concat` | Concatenate multiple labels into one | `concat: {sources: [service, method], target: key, separator: ":"}` |
| `map` | Map label values via a lookup table | `map: {source: env, target: env, values: {"production": "prod"}, default: ""}` |
| `math` | Integer arithmetic on a numeric label value | `math: {source: replica_index, target: group, operation: div, operand: 3}` |

**Interpolation:** The `set` operation supports `${label_name}` interpolation for constructing values from existing labels:

```yaml
operations:
  - set:
      label: fqdn
      value: "${service}.${namespace}.svc.cluster.local"
```

**Example -- PII removal with hash_mod:**

```yaml
- name: hash-user-ids
  input: ".*"
  action: transform
  when:
    - label: user_id
      matches: ".*"
  operations:
    - hash_mod:
        source: user_id
        target: user_hash
        modulus: 65536
    - remove: [user_id]
```

---

### Classify

Derives classification labels (team ownership, severity, priority) from existing metric labels. Like transform, classify is **non-terminal** -- it modifies labels and continues to the next rule.

Classify has two phases:

1. **Chains** -- ordered list, first match wins, sets multiple labels at once
2. **Mappings** -- all applied in order after chains, lookup-table style

```yaml
- name: classify-ownership
  input: ".*"
  action: classify
  labels:
    team: "platform"
    owner_email: "platform@company.com"
  classify:
    chains:
      - when:
          - label: product
            matches: "^checkout$"
          - label: component
            matches: "^(payment-processor|fraud-detection)$"
        set:
          team: "payments-core"
          oncall: "payments-core-oncall"

      - when:
          - label: product
            matches: "^(checkout|billing|subscriptions)$"
        set:
          team: "payments"
          oncall: "payments-oncall"

    mappings:
      - source: stage
        target: severity
        values:
          "^(ga|stable|production)$": "critical"
          "^(beta|rc|canary)$": "warning"
        default: "info"

      - sources: [team, severity]
        separator: ":"
        target: priority
        values:
          "^payments.*:critical$": "P0"
          "^.*:critical$": "P1"
        default: "P3"

    remove_after: [component]
```

**Chain conditions** reuse the same syntax as transform `when` conditions (`equals`, `matches`, `contains`, `not_matches`). **Mapping values** support regex keys (same as the transform `map` operation). **Set values** support `${interpolation}` for referencing other labels. **Multi-source mappings** concatenate multiple label values with a separator before matching.

| Field | Description |
|-------|-------------|
| `classify.chains[].when` | AND conditions (all must match) |
| `classify.chains[].set` | Labels to set on match (supports `${interpolation}`) |
| `classify.mappings[].source` | Single source label |
| `classify.mappings[].sources` | Multiple source labels (alternative to `source`) |
| `classify.mappings[].separator` | Separator for multi-source concatenation (default: `:`) |
| `classify.mappings[].target` | Target label name |
| `classify.mappings[].values` | Regex key -> value lookup table |
| `classify.mappings[].default` | Default value when no regex matches |
| `classify.remove_after` | Labels to remove after all classification completes |

See [examples/processing-classification.yaml](../examples/processing-classification.yaml) for a complete working example.

### Drop

Unconditional 100% removal. No additional fields beyond the common fields.

```yaml
- name: drop-internal
  input: "internal_.*"
  action: drop
```

All matching datapoints are discarded. Use this for metrics that should never reach the backend -- Go runtime metrics, scrape metadata, known-bad metric families, etc.

---

## Rule Ownership Labels

Every rule (processing and limits) supports an optional `labels:` map for ownership metadata. These labels are exposed as Prometheus dimensions on dead-rule detection metrics, enabling Alertmanager routing to the responsible team.

### Labels Field

```yaml
rules:
  - name: classify-team
    input: ".*"
    action: classify
    labels:
      team: "payments"
      product: "checkout"
      slack_channel: "#payments-alerts"
      pagerduty_service: "payments-oncall"
      owner_email: "payments@company.com"
```

Labels are free-form key-value pairs. Reserved names (`rule`, `action`, `__name__`) are rejected at config validation time. These labels appear on dead-rule metrics:

```
metrics_governor_processing_rule_last_match_seconds{rule="classify-team", action="classify", team="payments", product="checkout"} 3600
```

### Required Labels Enforcement

The top-level `required_labels` field specifies which labels must be present on every rule. Config validation fails at load time if any rule is missing a required label:

```yaml
required_labels: [team, owner_email]

rules:
  - name: drop-old
    input: "old_.*"
    action: drop
    labels:
      team: "platform"
      # MISSING owner_email -> validation error:
      # "rule 'drop-old' is missing required label 'owner_email'"
```

This works the same for both processing config and limits config.

---

## Dead Rule Detection

Processing and limits rules can become stale when products are decommissioned, label values change, or regex patterns no longer match. Dead rule detection provides two independent layers:

### Always-On Metrics

These gauges are **always exposed** regardless of config. They enable PromQL-based alerting even when the in-governor scanner is disabled.

| Metric | Type | Description |
|--------|------|-------------|
| `processing_rule_last_match_seconds` | Gauge | Seconds since last match (Inf if never) |
| `processing_rule_never_matched` | Gauge | 1 if never matched since load, 0 otherwise |
| `processing_rule_loaded_seconds` | Gauge | Seconds since rule was loaded |
| `limits_rule_last_match_seconds` | Gauge | Same for limits rules |
| `limits_rule_never_matched` | Gauge | Same for limits rules |
| `limits_rule_loaded_seconds` | Gauge | Same for limits rules |

All metrics include `rule` and `action` labels, plus any custom labels from the rule's `labels:` map.

### Optional Scanner

The scanner is a periodic goroutine controlled by `dead_rule_interval` in config:

```yaml
dead_rule_interval: "30m"   # 0 or omitted = scanner disabled
```

When enabled, it evaluates rules every `interval/2` and:
- Sets `processing_rule_dead{rule, action}` gauge (1=dead, 0=alive)
- Sets `processing_rules_dead_total` gauge (total dead count)
- Logs state transitions: alive->dead (WARN), dead->alive (INFO)

### Alert Rules

Separate alert rule files are provided in `alerts/dead-rules.yaml` and `alerts/dead-rules-prometheusrule.yaml`. These work **without the scanner** using always-on metrics:

```yaml
# Stopped matching (was working before)
MetricsGovernorDeadProcessingRule:
  processing_rule_last_match_seconds > 1800 and processing_rule_never_matched == 0

# Never matched at all (possible config error)
MetricsGovernorNeverMatchedRule:
  processing_rule_never_matched == 1 and processing_rule_loaded_seconds > 3600

# Declining rate (gradual drift)
MetricsGovernorDecliningRuleRate:
  rate(processing_rule_input_total[6h]) < 0.01 and ... offset 7d > 1
```

See [Alerting - Dead Rule Alert Routing](alerting.md#dead-rule-alert-routing) for Alertmanager routing setup.

---

## Unmatched Metrics

Any metric that does not match any rule passes through unchanged. There is no implicit drop. If your rule list is empty, 100% of metrics are forwarded unmodified.

This is an important safety property: adding new processing rules can only reduce traffic, never accidentally block metrics that were previously flowing.

---

## Execution Model

Processing rules use a **multi-touch** evaluation model with two categories of rules:

**Terminal actions** -- `sample`, `downsample`, `aggregate`, `drop`:
- First match wins. Once a metric matches a terminal rule, it is processed by that rule and evaluation stops. No subsequent rules are considered for that metric.

**Non-terminal actions** -- `transform`:
- Apply and continue. Transform rules modify labels on the metric and then pass it to the next rule in the list. A metric can match multiple transform rules in sequence before hitting a terminal rule (or passing through unmatched).

**Evaluation walk:**

```
For each incoming datapoint:
  1. Walk rules top-to-bottom
  2. If rule input + input_labels match:
     a. If action = transform:
        - Apply label operations
        - Continue to next rule (do NOT stop)
     b. If action = sample | downsample | aggregate | drop:
        - Execute the action
        - STOP (first terminal match wins)
  3. If no terminal rule matched:
     - Pass through unchanged (100% kept)
```

**Practical implication:** Place transform rules before the terminal rules they should feed into. For example, normalize environment labels first, then aggregate by the normalized label:

```yaml
rules:
  # Step 1: normalize (non-terminal, continues)
  - name: normalize-env
    input: "http_.*"
    action: transform
    operations:
      - map:
          source: env
          target: env
          values:
            "production": "prod"
            "staging": "stg"

  # Step 2: aggregate using the normalized label (terminal, stops)
  - name: http-by-service
    input: "http_requests_total"
    action: aggregate
    interval: 1m
    group_by: [service, env, method]
    functions: [sum]
```

---

## Adaptive Downsampling Deep Dive

Adaptive downsampling is the most sophisticated processing action. Instead of a fixed compression ratio, it dynamically adjusts the keep rate based on signal volatility: stable signals are aggressively compressed, while volatile signals retain more data.

### How CV-Based Rate Adjustment Works

The algorithm computes the **Coefficient of Variation (CV)** over a sliding window of recent values for each tracked series:

```
CV = standard_deviation / mean
```

The CV is a dimensionless measure of relative variability. A CV near 0 indicates a stable signal; a CV above 0.5-1.0 indicates high volatility.

The keep rate is then linearly interpolated between `min_rate` and `max_rate` based on the CV:

```
if CV <= low_threshold:
    rate = min_rate                    # stable -- compress aggressively
elif CV >= high_threshold:
    rate = max_rate                    # volatile -- keep everything
else:
    rate = min_rate + (max_rate - min_rate) * (CV - low) / (high - low)
```

Where `low_threshold` and `high_threshold` are derived from the variance window size.

**Concrete example -- server room temperature:**

```
Normal day (24.0C +/- 0.1C):
  CV = 0.1 / 24.0 = 0.004 (very stable)
  rate = min_rate = 0.05  -->  1440 daily points become 72

AC failure event (24C -> 35C in 10 minutes):
  CV = 3.2 / 29.5 = 0.108 (high volatility)
  rate = max_rate = 1.0   -->  all points preserved during the incident
```

### Adaptive vs Fixed Downsampling

| Characteristic | Fixed (avg, min, max, ...) | Adaptive |
|---------------|---------------------------|----------|
| Compression ratio | Constant, determined by interval | Variable, driven by signal behavior |
| Anomaly preservation | May smooth out spikes within the window | Automatically retains detail during anomalies |
| Configuration | Simple: method + interval | Requires tuning: min_rate, max_rate, variance_window |
| Memory per series | ~150 bytes | ~150 bytes + variance window buffer |
| Best for | Predictable signals, dashboards with fixed granularity | Infrastructure metrics, sensors, any signal with idle/burst patterns |

**Use adaptive when:**
- The signal alternates between long stable periods and short bursts of activity
- You want automatic anomaly preservation without manual alerting rules
- You are running edge/DaemonSet processing and want maximum compression without data loss during incidents

**Use fixed when:**
- The signal has consistent variability (e.g., request latency is always noisy)
- You need a guaranteed output resolution for downstream dashboards
- Simplicity is more important than optimal compression

### Tuning Guide

| Parameter | Low Value | High Value | Guidance |
|-----------|-----------|------------|----------|
| `min_rate` | 0.01 (99% drop) | 0.5 (50% drop) | Lower = more aggressive during stable periods. Start at 0.1 for infrastructure metrics. |
| `max_rate` | 0.5 | 1.0 (keep all) | Typically 1.0 unless you want some reduction even during anomalies. |
| `variance_window` | 10 | 100 | Larger windows smooth out noise and avoid false positives. 30 is a good default. 50+ for very stable signals (temperature, disk usage). |
| `interval` | 10s | 5m | Controls the emission cadence. Shorter = lower latency. Longer = more compression. |

**Tuning workflow:**

1. Start with `min_rate: 0.1`, `max_rate: 1.0`, `variance_window: 30`
2. Monitor `processing_rule_output_total` / `processing_rule_input_total` to see actual compression
3. If too aggressive (missing data during events), increase `min_rate` or decrease `variance_window`
4. If not aggressive enough (still too much data during idle), decrease `min_rate` or increase `variance_window`

---

## Processing Rules vs Prometheus Recording Rules

Both processing rules and Prometheus recording rules perform metric reduction. They operate at different layers and complement each other.

### Comparison

| Dimension | Processing Rules | Prometheus Recording Rules |
|-----------|-----------------|---------------------------|
| **Where** | At the proxy, before storage | At the Prometheus server, after storage |
| **Storage impact** | Reduces data before it reaches storage | Requires raw data to already be stored |
| **Cardinality reduction** | Reduces series count before ingest | Creates new series alongside existing ones |
| **Cost** | Reduces storage/ingest cost directly | Increases storage cost (new series on top of raw) |
| **Scale** | Handles any volume (proxy-level) | Limited by Prometheus query capacity |
| **Multi-cluster** | Naturally works across clusters (centralized proxy) | Requires federation or Thanos/Cortex for cross-cluster |
| **Complex PromQL** | Limited to built-in functions (sum, avg, quantiles, etc.) | Full PromQL expressiveness |
| **Label transforms** | 12 built-in operations | Not supported (relabeling is separate) |
| **Retroactive** | Cannot reprocess historical data | Can be added and applied to stored data |

### When to Use Each

**Use Processing Rules when:**
- Your primary goal is cost reduction (fewer series stored and transmitted)
- You need to eliminate high-cardinality labels before they reach storage
- You want label normalization and PII removal in the data path
- You are operating at scale where Prometheus cannot handle the raw volume

**Use Prometheus Recording Rules when:**
- You need complex PromQL expressions (histogram_quantile, predict_linear, etc.)
- You want pre-computed aggregates for dashboard performance
- You need to experiment with aggregation logic without changing the pipeline
- The raw data must also be available for ad-hoc queries

**Recommended approach:** Use both. Processing rules handle the heavy lifting of cardinality reduction and label normalization in the data path. Prometheus recording rules handle complex pre-computations on the already-reduced data.

---

## Real-Life Problems Solved

### Kubernetes Pod Churn

**Problem:** 200 services x 50 pods x 100 metrics = 1,000,000 series. Pods restart frequently, creating ephemeral series that inflate cardinality without adding value.

**Solution:** Aggregate away pod-level labels, keeping only service-level dimensions.

```yaml
rules:
  - name: strip-pod-labels
    input: "kube_pod_.*"
    action: aggregate
    interval: 1m
    drop_labels: [pod, pod_ip, uid, container_id, instance]
    functions: [last]
    keep_input: false
```

**Result:** 200 services x 100 metrics = 20,000 series. **98% reduction** in cardinality. Pod restarts no longer create new series.

---

### High-Cardinality Labels

**Problem:** A `user_id` label on request metrics produces one series per user. 1 million active users = 1 million series for a single metric.

**Solution:** Aggregate by service and endpoint, dropping the user dimension.

```yaml
rules:
  - name: aggregate-by-service
    input: "http_requests_total"
    output: "http_requests_by_service"
    action: aggregate
    interval: 30s
    group_by: [service, method, status_code, env]
    functions: [sum, count]
    keep_input: false
```

**Result:** ~1,000 series (service x method x status_code x env combinations). The per-user dimension is collapsed into counts and sums.

---

### Cross-Network Traffic Reduction

**Problem:** 500 nodes sending metrics over a WAN link. 10,000 metrics/node x 500 nodes x 1 sample/sec = 5,000,000 datapoints/second crossing the network.

**Solution:** Deploy metrics-governor as a DaemonSet on each node with adaptive downsampling. Stable infrastructure metrics are compressed 10-50x; volatile metrics retain full resolution.

```yaml
rules:
  - name: node-adaptive
    input: "node_(cpu|memory|disk|network)_.*"
    action: downsample
    method: adaptive
    interval: 30s
    min_rate: 0.1
    max_rate: 1.0
    variance_window: 30

  - name: drop-go-runtime
    input: "go_.*"
    action: drop
```

**Result:** ~40,000 datapoints/second across the WAN. **125x reduction** in cross-network traffic. Anomalies are still preserved at full resolution.

---

### Monitoring Bill Reduction

**Problem:** Cloud monitoring vendor charges per active series. Current bill covers 500,000 series; budget allows 50,000.

**Solution:** Tiered processing -- aggregate application metrics by service, downsample infrastructure metrics, drop unnecessary metric families.

```yaml
rules:
  - name: http-aggregate
    input: "http_.*"
    action: aggregate
    interval: 1m
    group_by: [service, method, status_code]
    functions: [sum, avg]
    keep_input: false

  - name: infra-downsample
    input: "node_.*"
    action: downsample
    method: avg
    interval: 5m

  - name: drop-debug
    input: "debug_.*"
    action: drop

  - name: drop-go
    input: "go_.*"
    action: drop
```

**Result:** ~6,000 active series. **80-99% cost reduction** depending on the original cardinality distribution.

---

### Label Inconsistency

**Problem:** Different teams use different label names and values for the same concept. Team A uses `env=prod`, Team B uses `environment=production`, Team C uses `ENV=Production`. Dashboards and alerts break.

**Solution:** Chain transform rules to normalize before any aggregation.

```yaml
rules:
  - name: normalize-env-label
    input: ".*"
    action: transform
    when:
      - label: environment
        matches: ".*"
    operations:
      - lower:
          label: environment
      - rename:
          source: environment
          target: env
      - map:
          source: env
          target: env
          values:
            "production": "prod"
            "staging": "stg"
            "development": "dev"
          default: ""

  - name: normalize-env-uppercase
    input: ".*"
    action: transform
    when:
      - label: ENV
        matches: ".*"
    operations:
      - lower:
          label: ENV
      - rename:
          source: ENV
          target: env
```

**Result:** All metrics arrive at the backend with a consistent `env` label using the values `prod`, `stg`, or `dev`.

---

### Privacy and PII Removal

**Problem:** Application metrics include a `user_id` label for debugging. Compliance requires that raw user identifiers never reach long-term storage.

**Solution:** Hash the user ID into a fixed-range bucket and remove the original label. The hash preserves cardinality analysis (you can still count distinct users) without storing identifiable data.

```yaml
rules:
  - name: hash-user-ids
    input: ".*"
    action: transform
    when:
      - label: user_id
        matches: ".*"
    operations:
      - hash_mod:
          source: user_id
          target: user_hash
          modulus: 65536
      - remove: [user_id]

  - name: remove-email
    input: ".*"
    action: transform
    when:
      - label: user_email
        matches: ".*"
    operations:
      - remove: [user_email]
```

**Result:** No PII reaches the backend. `user_hash` retains enough information for cardinality estimation and per-user-bucket dashboards.

---

## Pipeline Position

Processing rules execute after receiver stats and before tenant routing and limits enforcement. This positioning allows processing to reduce data volume before limits are evaluated, which means limits see the already-processed stream.

```
Receiver (gRPC/HTTP/PRW)
    |
    v
Stats Collection
    |
    v
PROCESSING RULES  <-- you are here
    |
    v
Tenant Routing
    |
    v
LIMITS ENFORCER
    |
    v
[Buffer]
    |
    v
RELABEL (Prometheus-style)
    |
    v
Export Pipeline
```

Key implications:
- Processing rules see raw, unmodified metrics as received from instrumentation.
- Labels added or modified by transforms are visible to all downstream stages (limits, relabeling, export).
- Aggregate and downsample outputs flow through limits and relabeling like any other metric.
- Limit budgets should be set based on post-processing volumes, not raw input.

---

## Observability

When processing rules are enabled, the following Prometheus metrics are exposed. All metrics use the `metrics_governor_` prefix.

### Per-Rule Metrics

These metrics carry a `rule` label matching the rule's `name` field.

| Metric | Type | Description |
|--------|------|-------------|
| `processing_rule_evaluations_total` | Counter | Number of times the rule was evaluated against an incoming metric |
| `processing_rule_input_total` | Counter | Datapoints that matched and entered the rule |
| `processing_rule_output_total` | Counter | Datapoints emitted by the rule |
| `processing_rule_dropped_total` | Counter | Datapoints dropped by the rule |
| `processing_rule_duration_seconds` | Histogram | Time spent processing per rule evaluation |

### Overall Pipeline Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `processing_input_datapoints_total` | Counter | Total datapoints entering the processing stage |
| `processing_output_datapoints_total` | Counter | Total datapoints leaving the processing stage |
| `processing_dropped_datapoints_total` | Counter | Total datapoints removed by processing |
| `processing_duration_seconds` | Histogram | Total time spent in the processing stage per batch |
| `processing_config_reloads_total` | Counter | Number of processing config reloads (success + failure) |
| `processing_rules_active` | Gauge | Number of currently active processing rules |
| `processing_memory_bytes` | Gauge | Memory used by stateful processing engines (aggregate groups, downsample buffers) |

### Aggregate Engine Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `processing_aggregate_groups_active` | Gauge | Number of currently tracked aggregation groups |
| `processing_aggregate_groups_created_total` | Counter | Total aggregation groups created over time |
| `processing_aggregate_stale_cleaned_total` | Counter | Stale groups evicted by the staleness cleaner |
| `processing_aggregate_flush_duration_seconds` | Histogram | Time spent flushing aggregated results |

### Transform Engine Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `processing_transform_labels_added_total` | Counter | Labels added by transform operations |
| `processing_transform_labels_removed_total` | Counter | Labels removed by transform operations |
| `processing_transform_labels_modified_total` | Counter | Labels modified (renamed, replaced, etc.) by transform operations |
| `processing_transform_operations_total` | Counter | Total transform operations executed |

**Useful PromQL queries:**

```promql
# Overall processing compression ratio
rate(processing_output_datapoints_total[5m])
/ rate(processing_input_datapoints_total[5m])

# Per-rule drop ratio
rate(metrics_governor_processing_rule_dropped_total{rule="my-rule"}[5m])
/ rate(metrics_governor_processing_rule_input_total{rule="my-rule"}[5m])

# Aggregate memory growth rate
deriv(metrics_governor_processing_memory_bytes[15m])

# Stale group cleanup rate
rate(metrics_governor_processing_aggregate_stale_cleaned_total[5m])
```

---

## Hot Reload

Processing configuration can be hot-reloaded at runtime via `SIGHUP` -- no restart needed.

```bash
# Local / VM
kill -HUP $(pidof metrics-governor)

# Kubernetes (with configmap-reload sidecar -- automatic)
# Just update the ConfigMap; the sidecar sends SIGHUP for you.
```

On SIGHUP, the processing YAML is re-read and validated. If valid, the new rule set is atomically swapped in:

- **Stateless rules** (sample, transform, drop) take effect immediately.
- **Stateful rules** (downsample, aggregate) restart their engines. In-flight aggregate groups are flushed before the swap. New groups start accumulating with the new configuration.
- If the new file is invalid, the reload is rejected and the current config stays active. The failure is logged and counted in `processing_config_reloads_total`.

**Kubernetes sidecar approach:** In Kubernetes, ConfigMap updates do not trigger filesystem events that Go's `fsnotify` can reliably detect (due to symlink-based atomic swaps). Instead, use the `configmap-reload` sidecar, which watches the ConfigMap mount and sends SIGHUP to the metrics-governor process. This requires `shareProcessNamespace: true` and `SYS_PTRACE` capability in the pod spec.

For full details on the reload mechanism, ConfigMap sidecar setup, and monitoring, see **[Dynamic Configuration Reload](reload.md)**.

---

## Backward Compatibility

The Processing Rules engine fully replaces the older sampling system. Existing configurations continue to work:

- **`--sampling-config`** -- The old CLI flag still works and is treated as an alias for `--processing-config`. A WARN-level log is emitted on startup: `"--sampling-config is deprecated, use --processing-config"`.
- **Old YAML format** -- The old sampling configuration format (using `default_rate`, `strategy`, and `metrics` blocks) is auto-detected and internally converted to the equivalent processing rules. No manual migration is required.
- **Helm `sampling.enabled`** -- The Helm chart value `sampling.enabled` still works and maps to the new processing config. A deprecation notice is logged.

**Migration path (optional but recommended):**

1. Replace `--sampling-config` with `--processing-config` in your deployment
2. Convert the old YAML to the new rule-based format
3. Remove the deprecated fields from Helm values

The old format will continue to work indefinitely but will not support the new actions (downsample, aggregate, transform).

---

## Performance Tuning

**Aggregate groups:** Each active aggregation group consumes approximately **200 bytes** of memory. A rule with 10,000 distinct label combinations produces ~2 MB of overhead. Monitor `processing_aggregate_groups_active` and `processing_memory_bytes` to track growth.

**Downsample series:** Each tracked series for downsampling consumes approximately **150 bytes** plus the variance window buffer (for adaptive mode). A variance window of 30 samples adds ~240 bytes per series.

**Staleness interval:** The `staleness_interval` setting (top-level in the processing YAML) controls how long stateful engines (aggregate, downsample) keep tracking a series that has stopped receiving data. Lower values free memory sooner but risk dropping series that report infrequently. The default of `10m` is appropriate for most Prometheus-style scraping at 15-30s intervals. Increase to `30m` or more for metrics that report on longer cycles (e.g., hourly batch jobs).

**Rule ordering:** Place more selective rules (specific metric names, label matchers) before broad rules (wildcard `input: ".*"`). This reduces the number of regex evaluations per metric since terminal rules stop evaluation on first match.

**Transform chains:** Each transform rule in a chain adds a small per-datapoint cost. Combine related operations into a single transform rule where possible rather than spreading them across multiple rules.

---

## See Also

- **[Export Pipeline](exporting.md)** -- How processed metrics are batched, compressed, and sent to backends
- **[Limits Configuration](limits.md)** -- Cardinality and datapoints-rate limiting (downstream of processing)
- **[Configuration Profiles](profiles.md)** -- Pre-built configuration bundles for common deployment scenarios
- **[Processing Examples](../examples/processing.yaml)** -- Comprehensive example with all 5 actions
- **[Adaptive Downsampling Examples](../examples/processing-adaptive.yaml)** -- Detailed adaptive and LTTB/SDT examples
- **[Transform Examples](../examples/processing-transforms.yaml)** -- All 12 transform operations demonstrated
- **[Tier 1 Edge Processing](../examples/processing-tier1.yaml)** -- DaemonSet edge pre-processing config
- **[Tier 2 Gateway Aggregation](../examples/processing-tier2.yaml)** -- StatefulSet global aggregation config
- **[Dynamic Configuration Reload](reload.md)** -- SIGHUP, sidecar setup, and monitoring

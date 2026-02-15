# Multi-Tenancy

metrics-governor supports multi-tenant deployments where metrics from different organisations, teams, or environments flow through a shared proxy. Tenancy provides two capabilities:

1. **Tenant detection** — identify which tenant owns each metric batch
2. **Per-tenant quotas** — enforce datapoint rate and cardinality limits per tenant

Both are optional and independent: you can detect tenants without enforcing quotas (for visibility), or use quotas to protect your backend from noisy neighbours.

## Quick Start

```bash
# Enable tenancy with header-based detection (Mimir/Cortex compatible)
metrics-governor \
  --tenancy-enabled \
  --tenancy-mode=header \
  --tenancy-header-name=X-Scope-OrgID \
  --tenancy-inject-label=true \
  --tenancy-config=/etc/metrics-governor/tenant-quotas.yaml
```

## How It Works

### Pipeline Position

Tenant detection runs **before** limits enforcement in the processing pipeline:

```
Receive → Stats → Processing Rules → Tenant Detection → Limits Enforcement → Buffer → Export
                                      ^^^^^^^^^^^^^^^^    ^^^^^^^^^^^^^^^^^^
                                      Annotates metrics   Can use tenant labels
                                      with __tenant__     in matching rules
```

Both stages are fused into a single timed step (`fused_tenant_limits`) for efficiency — one pass through the data instead of two.

### Detection Modes

| Mode | Flag | Source | Use Case |
|------|------|--------|----------|
| `header` | `-tenancy-header-name` | HTTP/gRPC request header | Mimir, Cortex, Thanos multi-tenant ingestion |
| `label` | `-tenancy-label-name` | Metric label value | Metrics pre-labelled by source (e.g., `tenant=acme`) |
| `attribute` | `-tenancy-attribute-key` | OTLP resource attribute | Cloud-native deployments using `service.namespace` |

**Header mode** (default): The receiver extracts the tenant from the configured HTTP header (e.g., `X-Scope-OrgID`) and passes it to the pipeline. This is the standard approach for Mimir and Cortex multi-tenant setups.

**Label mode**: The detector scans metric labels for a matching key (e.g., `tenant`). The first non-empty value found across all datapoints in a `ResourceMetrics` is used. Optionally strip the source label after detection with `-tenancy-strip-source=true`.

**Attribute mode**: The detector reads an OTLP resource attribute (e.g., `service.namespace`). This works well with Kubernetes deployments where namespace or service identity is already in the resource attributes.

### Tenant Label Injection

When `-tenancy-inject-label=true` (default), the detected tenant ID is injected as a label on every datapoint:

```
# Before injection
http_requests_total{service="api", method="GET"} 42

# After injection (tenant detected as "acme" from header)
http_requests_total{service="api", method="GET", __tenant__="acme"} 42
```

The label name defaults to `__tenant__` (configurable via `-tenancy-inject-label-name`). The double-underscore prefix follows the Prometheus convention for internal labels.

This injected label enables:
- **Limits rules** to match by tenant (e.g., `match: { labels: { __tenant__: "acme" } }`)
- **Processing rules** to transform or drop by tenant
- **Statistics** to track per-tenant metrics
- **Downstream backends** to route by tenant

### Fallback Behaviour

When detection fails (header missing, label not found, attribute absent), the **default tenant** is used (configurable via `-tenancy-default-tenant`, defaults to `"default"`). A counter tracks fallback events:

```
metrics_governor_tenant_fallback_total
```

## Per-Tenant Quotas

Quotas enforce datapoint rate and cardinality limits per tenant. They require a separate configuration file (`-tenancy-config`).

### Quota Configuration

```yaml
# tenant-quotas.yaml

# Global limits — protect the backend regardless of individual tenants
global:
  max_datapoints: 1000000   # per window
  max_cardinality: 500000   # unique series
  action: adaptive          # drop low-priority tenants first
  window: 1m

# Default quota — applies to any tenant without an explicit override
default:
  max_datapoints: 100000
  max_cardinality: 50000
  action: adaptive
  window: 1m

# Per-tenant overrides
tenants:
  enterprise-acme:
    max_datapoints: 500000
    max_cardinality: 100000
    action: log             # premium tenant — log only, never drop
    priority: 10            # high priority — survives global adaptive filtering
    window: 1m

  free-startup:
    max_datapoints: 10000
    max_cardinality: 5000
    action: drop            # hard cap
    priority: 0
    window: 1m
```

### Quota Actions

| Action | Behaviour |
|--------|-----------|
| `drop` | Hard drop all metrics when limit exceeded |
| `log` | Log the violation but pass all metrics through (for premium tenants) |
| `adaptive` | Allow partial data up to the limit, then stop accepting new `ResourceMetrics` |

### Hierarchical Enforcement

Quotas are evaluated in two levels:

1. **Global** — system-wide limits to protect the backend. When exceeded with `adaptive` action, low-priority tenants (priority <= 0) are dropped first while high-priority tenants pass through.
2. **Per-tenant** — individual tenant limits. Matched by tenant ID: explicit override first, then default.

### Hot Reload

Tenant quotas reload on SIGHUP alongside limits and processing configs:

```bash
kill -HUP $(pidof metrics-governor)
```

Or via Kubernetes ConfigMap sidecar (see [Dynamic Reload](reload.md)).

## Tenancy vs Limits — Two Independent Systems

metrics-governor has two rate-limiting mechanisms that serve different purposes:

| Feature | Tenancy Quotas | Limits Enforcer |
|---------|:---:|:---:|
| **Config flag** | `-tenancy-config` | `-limits-config` |
| **Matching** | By detected tenant ID | By metric name/label regex |
| **Scope** | Per-tenant budgets | Per-metric-pattern rules |
| **Rate windows** | Configurable per tenant | Per minute |
| **Actions** | drop, log, adaptive | log, drop, adaptive, sample, tiered |
| **Pipeline order** | Runs first (annotates) | Runs second (uses annotations) |
| **Use case** | Multi-org SaaS, team budgets | Cardinality protection, cost control |

**They work together**: Tenant detection annotates metrics with `__tenant__`, then limits rules can use that label for per-tenant matching:

```yaml
# limits.yaml — uses tenant label injected by the tenant detector
rules:
  - name: "per-tenant-cardinality"
    match:
      labels:
        __tenant__: "free-.*"   # regex: all free-tier tenants
    max_cardinality: 5000
    action: adaptive
    group_by: [__tenant__]      # track each tenant independently
```

**Alternative approach**: For simpler setups, you can achieve per-tenant limiting using _only_ the limits enforcer with `group_by`, without enabling full tenancy:

```yaml
# limits.yaml — tenant-like limiting via group_by (no tenancy feature required)
rules:
  - name: "per-service-quota"
    match:
      labels:
        service: "*"
    max_datapoints_rate: 100000
    action: adaptive
    group_by: [service]         # each service gets its own quota bucket
```

See [`examples/limits-tenant-quotas.yaml`](../examples/limits-tenant-quotas.yaml) for a full tiered example using the limits enforcer.

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-tenancy-enabled` | `false` | Enable multi-tenancy support |
| `-tenancy-mode` | `header` | Detection mode: `header`, `label`, `attribute` |
| `-tenancy-header-name` | `X-Scope-OrgID` | HTTP header for tenant ID (mode=header) |
| `-tenancy-label-name` | `tenant` | Metric label for tenant ID (mode=label) |
| `-tenancy-attribute-key` | `service.namespace` | Resource attribute for tenant ID (mode=attribute) |
| `-tenancy-default-tenant` | `default` | Fallback tenant when detection fails |
| `-tenancy-inject-label` | `true` | Inject tenant ID as a label on all datapoints |
| `-tenancy-inject-label-name` | `__tenant__` | Label name for injected tenant ID |
| `-tenancy-strip-source` | `false` | Remove source label after detection (mode=label only) |
| `-tenancy-config` | (none) | Path to tenant quotas YAML file |

## Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `metrics_governor_tenant_detected_total` | counter | `tenant`, `source` | ResourceMetrics processed per tenant |
| `metrics_governor_tenant_fallback_total` | counter | — | Times default tenant was used as fallback |
| `metrics_governor_tenant_datapoints_total` | counter | `tenant` | Total datapoints per tenant (when quotas enabled) |
| `metrics_governor_tenant_cardinality` | gauge | `tenant` | Current cardinality per tenant |
| `metrics_governor_tenant_limit_exceeded_total` | counter | `tenant`, `level` | Times a limit was exceeded (level: `global`, `tenant`) |
| `metrics_governor_global_datapoints_total` | counter | — | Total datapoints across all tenants |
| `metrics_governor_global_cardinality` | gauge | — | Current global cardinality estimate |
| `metrics_governor_tenant_quotas_reload_total` | counter | — | Total quota config reloads |

## Examples

### Mimir/Cortex Multi-Tenant Setup

```bash
metrics-governor \
  --profile=balanced \
  --tenancy-enabled \
  --tenancy-mode=header \
  --tenancy-header-name=X-Scope-OrgID \
  --tenancy-config=/etc/metrics-governor/tenant-quotas.yaml \
  --limits-config=/etc/metrics-governor/limits.yaml \
  -exporter-endpoint=http://mimir:8080/api/v1/push
```

### Kubernetes Namespace-Based Tenancy

```bash
metrics-governor \
  --profile=observable \
  --tenancy-enabled \
  --tenancy-mode=attribute \
  --tenancy-attribute-key=k8s.namespace.name \
  --tenancy-inject-label-name=__namespace__ \
  --tenancy-config=/etc/metrics-governor/namespace-quotas.yaml
```

### Label-Based Tenancy with Source Stripping

```bash
metrics-governor \
  --profile=balanced \
  --tenancy-enabled \
  --tenancy-mode=label \
  --tenancy-label-name=org_id \
  --tenancy-strip-source=true \
  --tenancy-inject-label-name=__tenant__
```

## See Also

- [Limits](limits.md) — rule-based adaptive limiting with `group_by`
- [Processing Rules](processing-rules.md) — sample, transform, drop by tenant
- [Dynamic Reload](reload.md) — hot-reload tenant quotas via SIGHUP
- [Alerting](alerting.md) — production alerts including tenant limit violations
- [Production Guide](production-guide.md) — sizing for multi-tenant deployments

# LLM/GenAI Metric Governance

metrics-governor provides first-class governance for LLM observability metrics flowing through OpenTelemetry GenAI semantic conventions. It is the only Prometheus-native metrics proxy that governs the telemetry *about* LLM usage (unlike LiteLLM, Portkey, or Helicone which govern the LLM API traffic itself).

## The Problem

LLM metrics create unique governance challenges:

1. **Model version cardinality explosion** — date-stamped versions (`gpt-4-0125-preview`, `gpt-4-turbo-2024-04-09`) create unbounded series growth. A single `gen_ai.client.token.usage` metric can produce 50+ time series per model version.

2. **Unbounded per-user dimensions** — attributes like `gen_ai.user`, `gen_ai.session.id`, and `gen_ai.request.id` create series per user/session, rapidly exceeding Prometheus ingestion limits.

3. **No token budget visibility** — teams have no way to see token consumption rates, project daily burn, or detect runaway AI agents burning through budgets before the cloud invoice arrives.

## Two Approaches

metrics-governor offers two complementary approaches:

### Option A: Limits Rules (Config-Only)

Uses the existing limits engine to govern `gen_ai.*` metrics. No new code required — just configuration. Best for:
- Model version collapse (regex matching)
- Per-user cardinality caps (adaptive limiting with `group_by`)
- Rate limiting high-frequency metrics (embeddings, operation duration)

### Option B: Token Budget Tracking (New Feature)

A dedicated tracker that observes `gen_ai.client.token.usage` metrics as they flow through the pipeline and computes:
- Per-model/provider token consumption rates
- Budget burn rates (are we on pace to exceed daily allocation?)
- Budget remaining ratios
- Active model counts

Best for:
- Token budget enforcement visibility
- Multi-team cost allocation
- Runaway AI agent detection
- Provider/model comparison

## Option A: Limits Rules

### Example Configuration

```yaml
# limits-llm.yaml
rules:
  # Collapse date-stamped model versions
  - name: "llm-model-version-collapse"
    match:
      metric_name: "gen_ai\\..*"
      labels:
        gen_ai.request.model: "gpt-4-*"
    max_cardinality: 50000
    action: adaptive
    group_by: ["gen_ai.request.model"]

  # Cap per-user cardinality
  - name: "llm-per-user-cardinality-cap"
    match:
      metric_name: "gen_ai\\.client\\.token\\.usage"
    max_cardinality: 10000
    action: adaptive
    group_by: ["gen_ai.system", "gen_ai.request.model"]

  # Rate limit operation duration histograms
  - name: "llm-operation-duration-rate"
    match:
      metric_name: "gen_ai\\.client\\.operation\\.duration"
    max_datapoints_rate: 100000
    max_cardinality: 20000
    action: adaptive
    group_by: ["gen_ai.system", "gen_ai.operation.name"]
```

Usage:
```bash
metrics-governor --limits-config=limits-llm.yaml
```

See [`examples/limits-llm.yaml`](../examples/limits-llm.yaml) for a comprehensive example.

## Option B: Token Budget Tracking

### Configuration

**CLI flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--llm-enabled` | `false` | Enable LLM token budget tracking |
| `--llm-token-metric` | `gen_ai.client.token.usage` | Token usage metric name prefix to match |
| `--llm-budget-window` | `24h` | Token budget window |

**YAML config:**

```yaml
stats:
  llm:
    enabled: true
    token_metric: "gen_ai.client.token.usage"
    budget_window: 24h
    budgets:
      - provider: "openai"
        model: "gpt-4*"
        daily_tokens: 1000000
      - provider: "anthropic"
        model: "claude-*"
        daily_tokens: 500000
      - provider: "*"
        model: "*"
        daily_tokens: 2000000  # catch-all
```

Budget rules use glob matching (`*` and `?` wildcards). Rules are evaluated in order; the first match wins. Set `daily_tokens: 0` for observe-only mode (tracks consumption without budget computation).

### How It Works

1. The tracker hooks into `stats.Collector.Process()` — the same path all incoming metrics flow through
2. It scans for metrics matching the configured prefix (`gen_ai.client.token.usage` by default)
3. For matching metrics, it extracts `gen_ai.system`/`gen_ai.provider.name`, `gen_ai.request.model`, and `gen_ai.token.type` attributes
4. Token counts are accumulated per model in a lock-protected map
5. Every 30 seconds, a snapshot is recorded to a ring buffer (720 slots = 6 hours)
6. On `/metrics` scrape, rates and budget metrics are computed from the ring buffer

### Performance Impact

| Scenario | Cost |
|----------|------|
| **Disabled** (default) | ~1ns per batch (nil pointer check) |
| **Enabled, no gen_ai metrics** | <100ns per batch (string prefix scan) |
| **Enabled, with gen_ai metrics** | <600ns per batch (attribute extraction + accumulation) |
| **Memory** | ~70KB total (ring buffer + model accumulators) |

Zero allocations per operation. Independent mutex (`accMu`) — no contention with the main processing pipeline.

### Metrics Reference

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `metrics_governor_llm_tokens_total` | counter | `provider`, `model`, `token_type` | Cumulative token count |
| `metrics_governor_llm_token_rate` | gauge | `token_type`, `window` | Token consumption rate (tokens/sec) |
| `metrics_governor_llm_budget_remaining_ratio` | gauge | `provider`, `model` | Fraction of daily budget remaining (0-1) |
| `metrics_governor_llm_budget_burn_rate` | gauge | `provider`, `model` | Budget burn rate (1.0 = on pace) |
| `metrics_governor_llm_models_active` | gauge | — | Number of active LLM models |
| `metrics_governor_llm_tracker_enabled` | gauge | — | Whether tracker is enabled (1=yes) |
| `metrics_governor_llm_uptime_seconds` | gauge | — | Seconds since tracker started |
| `metrics_governor_llm_snapshots_total` | counter | — | Total snapshots recorded |

### Token Rate Windows

Rates are computed over 4 standard windows (same as SLI tracker):

| Window | Ring Slots | Duration |
|--------|-----------|----------|
| `5m` | 10 | 5 minutes |
| `30m` | 60 | 30 minutes |
| `1h` | 120 | 1 hour |
| `6h` | 720 | 6 hours |

### Budget Burn Rate

The burn rate indicates whether token consumption is on pace to exhaust the daily budget:

| Burn Rate | Meaning |
|-----------|---------|
| 0.5 | Consuming at half the budgeted pace |
| 1.0 | Exactly on pace — will exhaust budget at window end |
| 2.0 | Consuming at 2x pace — will exhaust in 12 hours |
| 5.0 | Consuming at 5x pace — will exhaust in ~5 hours |

## Use Cases

### Multi-Team Token Allocation

```yaml
stats:
  llm:
    enabled: true
    budgets:
      - provider: "openai"
        model: "gpt-4*"
        daily_tokens: 500000    # Team A budget
      - provider: "openai"
        model: "gpt-3.5*"
        daily_tokens: 2000000   # Team B budget
      - provider: "anthropic"
        model: "*"
        daily_tokens: 1000000   # Shared pool
```

### Runaway AI Agent Detection

Alert when burn rate exceeds 3x (will exhaust budget in 8 hours):

```yaml
# Prometheus alerting rule
groups:
  - name: llm-budget
    rules:
      - alert: LLMBudgetBurnHigh
        expr: metrics_governor_llm_budget_burn_rate > 3
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "LLM token budget burning at {{ $value }}x pace"
          description: "Model {{ $labels.model }} on {{ $labels.provider }} is consuming tokens at {{ $value }}x the daily budget rate"

      - alert: LLMBudgetExhausted
        expr: metrics_governor_llm_budget_remaining_ratio < 0.1
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "LLM token budget nearly exhausted ({{ $value | humanizePercentage }} remaining)"
```

### Model Migration Tracking

Monitor token distribution across model versions during migration:

```yaml
stats:
  llm:
    enabled: true
    budgets:
      - provider: "openai"
        model: "gpt-4"
        daily_tokens: 0          # observe only — old model
      - provider: "openai"
        model: "gpt-4o*"
        daily_tokens: 1000000    # budget for new model
```

Track the ratio: `metrics_governor_llm_tokens_total{model="gpt-4o-mini"} / metrics_governor_llm_tokens_total{model="gpt-4"}`

## OTel GenAI Semantic Conventions

The tracker follows the [OpenTelemetry GenAI Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/):

| Attribute | Used For | Example Values |
|-----------|----------|---------------|
| `gen_ai.system` | Provider identification | `openai`, `anthropic`, `azure` |
| `gen_ai.provider.name` | Provider identification (newer) | `openai`, `anthropic` |
| `gen_ai.request.model` | Model identification | `gpt-4`, `claude-3.5-sonnet` |
| `gen_ai.token.type` | Input/output classification | `input`, `output`, `prompt`, `completion` |

Both `gen_ai.system` and `gen_ai.provider.name` are supported (newer convention).
Both `input`/`output` and `prompt`/`completion` token type values are recognized.

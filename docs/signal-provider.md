# External Signal Provider Architecture

The External Signal Provider is a decoupled component that runs expensive queries (cloud APIs, AI/LLM reasoning, cost analysis) outside the governor's hot path.

## Why Decouple?

Governor's autotune loop runs every 30-60 seconds and must complete quickly (<50ms). Expensive operations like:

- **Grafana Cloud Adaptive Metrics** recommendations (batched, 10+ second latency)
- **AI/LLM reasoning** (1-30 second API calls depending on model)
- **Cloud platform API queries** (Datadog usage API, New Relic NerdGraph, etc.)
- **Cost analysis** (billing API aggregation)

...would block or slow the autotune cycle. The signal provider runs these queries independently at their own pace, caches results, and exposes them via a lightweight HTTP API.

## Architecture

```
Governor Pod (hot path, <50ms/cycle)
  └── polls GET /api/v1/signals (lightweight, cached)
        │
External Signal Provider (separate process/pod)
  ├── queries Grafana Cloud Adaptive Metrics (every 10m)
  ├── queries Datadog usage API (every 15m)
  ├── queries AI/LLM endpoint (every 5m)
  └── caches results with TTL
```

## Signal Provider API

### GET /api/v1/signals

Returns the latest aggregated signals with TTL information.

**Response:**
```json
{
  "recommendations": [
    {
      "rule_name": "api_requests",
      "action": "decrease",
      "new_value": 5000,
      "reason": "Adaptive Metrics suggests aggregation"
    }
  ],
  "anomaly_scores": {
    "http_requests_total": 0.85,
    "process_cpu_seconds": 0.12
  },
  "cost_data": {
    "active_series_cost_per_month": 142.50,
    "projected_savings": 23.80
  },
  "metadata": {
    "sources_queried": ["grafana_cloud", "llm"],
    "last_llm_call": "2026-02-18T10:30:00Z"
  },
  "timestamp": "2026-02-18T10:35:00Z",
  "ttl": "5m"
}
```

## Deployment Options

### Sidecar (simplest)

Runs alongside the governor pod, shares network namespace.

```yaml
containers:
  - name: governor
    args: ["--autotune-external-url=http://localhost:8080"]
  - name: signal-provider
    image: metrics-governor-signal-provider:latest
    ports:
      - containerPort: 8080
```

### Standalone deployment

Separate pod/service. Scales independently. Can serve multiple governor clusters.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: signal-provider
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: signal-provider
          image: metrics-governor-signal-provider:latest
          env:
            - name: GRAFANA_CLOUD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: signal-provider-secrets
                  key: grafana-token
```

### Central hub component

Runs in the central governance cluster (Strategy D), serves recommendations to all islands.

## Governor Configuration

```yaml
autotune:
  external_url: "http://signal-provider:8080"
  external_poll_interval: 5m
```

Or via CLI flags:
```
--autotune-external-url=http://signal-provider:8080
--autotune-external-poll-interval=5m
```

## Authentication

The governor supports multiple auth methods for communicating with the signal provider:

| Method | Config | Header Sent |
|--------|--------|-------------|
| None | `auth.type: none` | (none) |
| Basic | `auth.type: basic` | `Authorization: Basic base64(user:pass)` |
| Bearer | `auth.type: bearer` | `Authorization: Bearer <token>` |
| Custom header | `auth.type: header` | `<header_name>: <header_value>` |

## Cloud Platform Adapters (Roadmap)

Each cloud platform becomes a plugin in the signal provider:

| Platform | Signal Type | Status |
|----------|------------|--------|
| Grafana Cloud Adaptive Metrics | Aggregation recommendations | Planned |
| Datadog Metrics without Limits | Tag configuration suggestions | Planned |
| New Relic NerdGraph | Cardinality analysis via NRQL | Planned |
| Elastic ML | Anomaly scores (0-100) | Planned |
| Splunk/SignalFx | MTS cardinality insights | Planned |
| Dynatrace Davis AI | Problem correlation | Planned |

## TTL and Caching

The signal provider includes a TTL field in every response. The governor's `ExternalClient` respects this TTL — if a cached response is still fresh, no HTTP request is made. This means:

- Signal provider can set longer TTLs for expensive queries (e.g., 15m for billing data)
- Governor polls frequently but only transfers data when the cache expires
- Network-efficient: typical steady state is 1 actual HTTP request per TTL period

---
title: "Health Endpoints"
sidebar_position: 2
description: "Liveness and readiness probes"
---

metrics-governor exposes dedicated health endpoints for Kubernetes liveness and readiness probes on the stats HTTP server (default `:9090`).

## Endpoints

### `/live` — Liveness Probe

Returns **200 OK** if the process is running and not in shutdown. Returns **503 Service Unavailable** once a shutdown signal (SIGINT/SIGTERM) is received.

**When it fails**: Only during shutdown. If this probe fails repeatedly, Kubernetes will restart the pod — this is correct behavior for a hung process.

```json
// Healthy
{"status":"up","timestamp":"2026-02-06T12:00:00Z"}

// Shutting down
{"status":"down","components":{"process":{"status":"down","message":"shutting down"}},"timestamp":"2026-02-06T12:00:00Z"}
```

### `/ready` — Readiness Probe

Returns **200 OK** if all pipeline components are healthy. Returns **503 Service Unavailable** if any component check fails or if the instance is shutting down.

Registered checks:
- **grpc_receiver** — gRPC listener is running and accepting connections
- **http_receiver** — OTLP HTTP port is reachable
- **prw_receiver** — PRW HTTP port is reachable (only if PRW pipeline is enabled)

```json
// All healthy
{
  "status": "up",
  "components": {
    "grpc_receiver": {"status": "up"},
    "http_receiver": {"status": "up"},
    "prw_receiver": {"status": "up"}
  },
  "timestamp": "2026-02-06T12:00:00Z"
}

// Exporter down
{
  "status": "down",
  "components": {
    "grpc_receiver": {"status": "up"},
    "http_receiver": {"status": "up"},
    "prw_receiver": {"status": "down", "message": "PRW receiver not reachable on :9091: dial tcp :9091: connect: connection refused"}
  },
  "timestamp": "2026-02-06T12:00:00Z"
}
```

## Kubernetes Configuration

### Helm Chart (Recommended)

The Helm chart configures probes automatically. In `values.yaml`:

```yaml
livenessProbe:
  enabled: true
  httpGet:
    path: /live
    port: stats
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3

readinessProbe:
  enabled: true
  httpGet:
    path: /ready
    port: stats
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  failureThreshold: 3

# Optional: startup probe for slow-starting pods
startupProbe:
  enabled: true
  httpGet:
    path: /live
    port: stats
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 30
```

### Manual Deployment

If deploying without Helm, add probes to your Deployment spec:

```yaml
containers:
  - name: metrics-governor
    ports:
      - name: stats
        containerPort: 9090
    livenessProbe:
      httpGet:
        path: /live
        port: 9090
      initialDelaySeconds: 10
      periodSeconds: 10
    readinessProbe:
      httpGet:
        path: /ready
        port: 9090
      initialDelaySeconds: 5
      periodSeconds: 5
```

## Graceful Shutdown Sequence

1. Kubernetes sends SIGTERM to the pod
2. metrics-governor marks health as "shutting down" — `/live` and `/ready` immediately return 503
3. Kubernetes removes the pod from Service endpoints (no new traffic)
4. Receivers stop accepting new data
5. Buffers drain in-flight exports (up to `--shutdown-timeout`, default 30s)
6. Exporters close connections
7. Process exits

**Important**: Set `terminationGracePeriodSeconds` in your pod spec to be greater than `--shutdown-timeout` (default: 30s) to allow complete buffer drain:

```yaml
spec:
  terminationGracePeriodSeconds: 45  # > shutdown-timeout (30s)
```

## Response Format

All health endpoints return JSON with the following structure:

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | `"up"` or `"down"` |
| `components` | object | Per-component health status (readiness only) |
| `timestamp` | string | ISO 8601 UTC timestamp |

HTTP status codes:
- **200** — Healthy
- **503** — Unhealthy or shutting down

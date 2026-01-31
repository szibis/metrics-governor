# Installation

## From Source

```bash
go install ./cmd/metrics-governor
```

## Build from Source

```bash
git clone <repository-url>
cd metrics-governor
make build
```

## Multi-platform Builds

```bash
make all  # Builds darwin-arm64, linux-arm64, linux-amd64
```

Binaries are output to `bin/` directory.

## Docker

```bash
make docker
# or
docker build -t metrics-governor .
```

### Running with Docker

```bash
docker run -p 4317:4317 -p 4318:4318 -p 9090:9090 metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -stats-labels service,env,cluster
```

## Helm Chart

```bash
# Install from local chart
helm install metrics-governor ./helm/metrics-governor

# Install with custom values
helm install metrics-governor ./helm/metrics-governor \
  --set config.exporterEndpoint=otel-collector:4317 \
  --set limits.enabled=true \
  --set serviceMonitor.enabled=true

# Install as StatefulSet with persistence
helm install metrics-governor ./helm/metrics-governor \
  --set kind=statefulset \
  --set persistence.enabled=true \
  --set persistence.size=10Gi

# Install as DaemonSet
helm install metrics-governor ./helm/metrics-governor \
  --set kind=daemonset \
  --set hostNetwork=true
```

See [helm/metrics-governor/values.yaml](../helm/metrics-governor/values.yaml) for all available options.

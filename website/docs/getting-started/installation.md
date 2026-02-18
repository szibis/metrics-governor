---
title: "Installation"
sidebar_position: 1
description: "Install metrics-governor from source, Docker, or Helm"
---

> **Dual Pipeline Support**: metrics-governor supports both OTLP and PRW (Prometheus Remote Write) pipelines. They work identically but are completely separate - you can run either or both.

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

### Running with Docker (OTLP)

```bash
docker run -p 4317:4317 -p 4318:4318 -p 9091:9091 metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -stats-labels service,env,cluster
```

### Running with Docker (PRW)

```bash
docker run -p 9090:9090 -p 9091:9091 metrics-governor \
  -prw-listen :9090 \
  -prw-exporter-endpoint http://victoriametrics:8428/api/v1/write \
  -prw-exporter-vm-mode
```

### Running with Docker (Dual Pipeline - OTLP + PRW)

```bash
docker run -p 4317:4317 -p 4318:4318 -p 9090:9090 -p 9091:9091 metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -prw-listen :9090 \
  -prw-exporter-endpoint http://victoriametrics:8428/api/v1/write \
  -stats-labels service,env,cluster
```

## Helm Chart

### OTLP Pipeline

```bash
# Install from local chart
helm install metrics-governor ./helm/metrics-governor

# Install with custom values
helm install metrics-governor ./helm/metrics-governor \
  --set config.exporterEndpoint=otel-collector:4317 \
  --set limits.enabled=true \
  --set serviceMonitor.enabled=true
```

### PRW Pipeline

```bash
# Install for Prometheus Remote Write to VictoriaMetrics
helm install metrics-governor ./helm/metrics-governor \
  --set config.prwListenAddr=":9090" \
  --set config.prwExporterEndpoint="http://vminsert:8480/insert/0/prometheus/api/v1/write" \
  --set config.prwExporterVMMode=true
```

### Dual Pipeline (OTLP + PRW)

```bash
# Install with both OTLP and PRW pipelines
helm install metrics-governor ./helm/metrics-governor \
  --set config.exporterEndpoint=otel-collector:4317 \
  --set config.prwListenAddr=":9090" \
  --set config.prwExporterEndpoint="http://victoriametrics:8428/api/v1/write" \
  --set limits.enabled=true
```

### Advanced Options

```bash
# Install as StatefulSet with persistence (for queues)
helm install metrics-governor ./helm/metrics-governor \
  --set kind=statefulset \
  --set persistence.enabled=true \
  --set persistence.size=10Gi

# Install as DaemonSet
helm install metrics-governor ./helm/metrics-governor \
  --set kind=daemonset \
  --set hostNetwork=true
```

See [helm/metrics-governor/values.yaml](https://github.com/szibis/metrics-governor/blob/main/helm/metrics-governor/values.yaml) for all available options.

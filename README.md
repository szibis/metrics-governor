# metrics-governor

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Build](https://github.com/szibis/metrics-governor/actions/workflows/build.yml/badge.svg)](https://github.com/szibis/metrics-governor/actions/workflows/build.yml)
[![Tests](https://img.shields.io/badge/Tests-855+-success?style=flat&logo=go)](docs/testing.md#test-coverage-by-component)
[![Coverage](https://img.shields.io/badge/Coverage-85%25-brightgreen.svg)](https://github.com/szibis/metrics-governor/actions/workflows/build.yml)

---

**metrics-governor** is a high-performance metrics proxy supporting both **OTLP** and **Prometheus Remote Write (PRW)** protocols. It sits between your applications and your metrics backend, providing intelligent cardinality control, horizontal scaling via consistent sharding, and full observability for your metrics pipeline.

> **Two Independent Pipelines**: OTLP→OTLP and PRW→PRW. No cross-protocol conversion - each protocol stays native for zero overhead and full feature support.

## Why metrics-governor?

| Challenge | Solution |
|-----------|----------|
| **Cardinality explosions** crushing your backend | **Adaptive limiting** drops only the worst offenders, preserving well-behaved services |
| **Single backend bottleneck** limiting throughput | **Consistent sharding** distributes load across multiple endpoints via K8s DNS discovery |
| **Data loss during outages** | **High-performance persistent queue** with automatic retry and exponential backoff |
| **No visibility** into metrics pipeline | **Real-time statistics** with per-metric cardinality, datapoints, and Prometheus metrics |
| **Unpredictable costs** from runaway metrics | **Per-group tracking** with configurable limits and dry-run mode for safe testing |

## Key Features

- **Dual Protocol Support** - Native OTLP (gRPC/HTTP) and Prometheus Remote Write (PRW 1.0/2.0) pipelines, each running independently with zero conversion overhead
- **Intelligent Limiting** - Unlike simple rate limiters that drop everything, metrics-governor identifies and drops only the top offenders while preserving data from well-behaved services
- **Consistent Sharding** - Automatic endpoint discovery from Kubernetes headless services with consistent hashing ensures the same time-series always route to the same backend (works for both OTLP and PRW)
- **Production-Ready** - FastQueue durable persistence, TLS/mTLS, authentication, compression (gzip/zstd/snappy/lz4), and Helm chart included
- **High-Performance Optimizations** - String interning reduces allocations by 76%, concurrency limiting prevents goroutine explosion, Bloom filters reduce cardinality tracking memory by 98% (techniques inspired by [VictoriaMetrics articles](https://valyala.medium.com/))
- **Zero Configuration Start** - Works out of the box with sensible defaults; add limits and sharding when needed

## Architecture

```mermaid
flowchart LR
    subgraph Clients["Metrics Sources"]
        App1["App 1<br/>(OTel SDK)"]
        App2["App 2<br/>(OTel SDK)"]
        Prom["Prometheus<br/>(remote_write)"]
    end

    subgraph MG["metrics-governor"]
        subgraph Receivers["Receivers"]
            GRPC["gRPC Receiver<br/>:4317"]
            HTTP["HTTP Receiver<br/>:4318"]
            PRW["PRW Receiver<br/>:9091"]
        end

        subgraph Pipelines["Independent Pipelines"]
            subgraph OTLP_Pipeline["OTLP Pipeline"]
                OBuf["Buffer"]
                OStats["Stats"]
                OLimits["Limits"]
                OExp["Exporter"]
            end
            subgraph PRW_Pipeline["PRW Pipeline"]
                PBuf["Buffer"]
                PStats["Stats"]
                PLimits["Limits"]
                PExp["Exporter"]
            end
        end

        subgraph Queue["Persistent Queues"]
            OQueue["OTLP Queue<br/>(FastQueue)"]
            PQueue["PRW Queue<br/>(FastQueue)"]
        end
    end

    subgraph Backends["Backends"]
        OTel["OTel Collector"]
        VM["VictoriaMetrics<br/>Prometheus<br/>Thanos"]
    end

    App1 -->|"OTLP/gRPC"| GRPC
    App2 -->|"OTLP/HTTP"| HTTP
    Prom -->|"PRW"| PRW

    GRPC --> OBuf --> OStats --> OLimits --> OExp
    HTTP --> OBuf
    PRW --> PBuf --> PStats --> PLimits --> PExp

    OExp -->|"Success"| OTel
    OExp -.->|"Failure"| OQueue -.->|"Retry"| OExp
    PExp -->|"Success"| VM
    PExp -.->|"Failure"| PQueue -.->|"Retry"| PExp
```

## Quick Start

```bash
# Start metrics-governor with adaptive limits
metrics-governor \
  -exporter-endpoint otel-collector:4317 \
  -limits-config limits.yaml \
  -limits-dry-run=false \
  -stats-labels service,env

# Your apps send metrics to metrics-governor instead of directly to collector
# App: export OTEL_EXPORTER_OTLP_ENDPOINT=http://metrics-governor:4317
```

```yaml
# limits.yaml - Adaptive limiting by service
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

When cardinality exceeds 10,000, metrics-governor identifies which service is the top contributor and drops only that service's metrics, preserving data from well-behaved services.

---

## 📚 Documentation

| | Guide | Description |
|:---:|-------|-------------|
| 🚀 | [**Installation**](docs/installation.md) | Install from source, Docker, or Helm chart |
| ⚙️ | [**Configuration**](docs/configuration.md) | YAML config and CLI flags reference |
| 📡 | [**Prometheus Remote Write**](docs/prw.md) | PRW 1.0/2.0 protocol, VictoriaMetrics mode |
| 🎯 | [**Limits**](docs/limits.md) | Adaptive limiting, cardinality control, dry-run mode |
| 🔀 | [**Sharding**](docs/sharding.md) | Consistent hashing, K8s DNS discovery, horizontal scaling |
| 📊 | [**Statistics**](docs/statistics.md) | Prometheus metrics, per-metric tracking, observability |
| 🔐 | [**TLS**](docs/tls.md) | Server and client TLS, mTLS configuration |
| 🔑 | [**Authentication**](docs/authentication.md) | Bearer token, basic auth, custom headers |
| 📦 | [**Compression**](docs/compression.md) | gzip, zstd, snappy, lz4 compression support |
| 🌐 | [**HTTP Settings**](docs/http-settings.md) | Connection pools, timeouts, HTTP/2 |
| 📝 | [**Logging**](docs/logging.md) | JSON structured logging, log aggregation |
| 🧪 | [**Testing**](docs/testing.md) | Test environment, Docker Compose, verification |
| 🛠️ | [**Development**](docs/development.md) | Building, project structure, contributing |

---

## Capabilities Overview

| Capability | Description |
|------------|-------------|
| **OTLP Protocol** | Full gRPC and HTTP receiver/exporter with TLS and authentication |
| **PRW Protocol** | Prometheus Remote Write 1.0/2.0 with native histograms, VictoriaMetrics mode |
| **Intelligent Buffering** | Configurable buffer with batching for optimal throughput (both OTLP and PRW) |
| **Adaptive Limits** | Per-group tracking with smart dropping of top offenders only |
| **Real-time Statistics** | Per-metric cardinality, datapoints, and limit violation tracking |
| **Prometheus Integration** | Native `/metrics` endpoint for monitoring the proxy itself |
| **Consistent Sharding** | Distribute metrics across multiple backends via DNS discovery (OTLP and PRW) |
| **Persistent Queue** | FastQueue durable persistence with automatic retry (OTLP and PRW) |
| **Memory Optimized** | Bloom filter cardinality tracking uses 98% less memory (1.2MB vs 75MB per 1M series) |
| **Performance Optimized** | String interning and concurrency limiting for high-throughput workloads |
| **Production Ready** | Helm chart, multi-arch Docker images, graceful shutdown |

---

## Performance Optimizations

metrics-governor includes several high-performance optimizations for production workloads. **All optimizations apply to both OTLP and PRW pipelines.**

> **Note**: These optimizations are protocol-agnostic and work identically for both pipelines. The same memory savings, allocation reductions, and concurrency controls apply whether you're processing OTLP or Prometheus Remote Write metrics.

### Bloom Filter Cardinality Tracking

Cardinality tracking uses Bloom filters instead of maps for 98% memory reduction:

| Unique Series | map[string]struct{} | Bloom Filter (1% FPR) | Memory Savings |
|---------------|---------------------|------------------------|----------------|
| 10,000        | 750 KB              | 12 KB                  | **98%**        |
| 100,000       | 7.5 MB              | 120 KB                 | **98%**        |
| 1,000,000     | 75 MB               | 1.2 MB                 | **98%**        |
| 10,000,000    | 750 MB              | 12 MB                  | **98%**        |

**Applies to:** OTLP limits enforcer, OTLP stats collector, PRW limits enforcer, PRW stats collector

Configure via CLI flags:
```bash
# Use Bloom filter mode (default, memory-efficient)
metrics-governor -cardinality-mode bloom -cardinality-expected-items 100000 -cardinality-fp-rate 0.01

# Use exact mode (100% accurate, higher memory)
metrics-governor -cardinality-mode exact
```

**Observability metrics:**
- `metrics_governor_cardinality_mode{mode}` - Active tracking mode
- `metrics_governor_cardinality_memory_bytes` - Total memory used by trackers
- `metrics_governor_cardinality_trackers_total` - Number of active trackers
- `metrics_governor_rule_cardinality_memory_bytes{rule}` - Memory per limits rule

### String Interning

Label string deduplication reduces allocations by 76%:
- Pre-populated pool for common Prometheus labels (`__name__`, `job`, `instance`, etc.)
- Zero-allocation cache hits using `sync.Map`
- Configurable max value length to balance memory vs deduplication

**Applies to:** OTLP shard key building, PRW label parsing, PRW shard key building

Configure via CLI flags:
```bash
# Enable string interning (default: true)
metrics-governor -string-interning=true -intern-max-value-length=64
```

### Concurrency Limiting

Semaphore-based limiting prevents goroutine explosion:
- Bounded at `NumCPU * 4` by default
- 88% reduction in concurrent goroutines under load
- Prevents memory exhaustion during traffic spikes

**Applies to:** OTLP sharded exporter, PRW sharded exporter

Configure via CLI flags:
```bash
# Limit concurrent exports (default: NumCPU * 4)
metrics-governor -export-concurrency=32
```

### Summary

| Optimization | OTLP | PRW | Memory Impact | CPU Impact |
|--------------|:----:|:---:|---------------|------------|
| Bloom Filters | ✓ | ✓ | -98% for cardinality tracking | Minimal |
| String Interning | ✓ | ✓ | -76% allocations | -12% CPU |
| Concurrency Limiting | ✓ | ✓ | Bounded goroutines | Controlled parallelism |

---

## Contributing

Contributions are welcome! Please see [Development Guide](docs/development.md) for details.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Support

- 📖 [Documentation](docs/)
- 🐛 [Issue Tracker](https://github.com/szibis/metrics-governor/issues)
- 💬 [Discussions](https://github.com/szibis/metrics-governor/discussions)

---

<p align="center">
  <sub>Built with ❤️ for the observability community</sub>
</p>

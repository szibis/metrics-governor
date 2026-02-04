# Testing

A comprehensive test suite with **880+ tests** across unit, functional, e2e, and performance testing ensures reliability and correctness.

[![Build Status](https://github.com/szibis/metrics-governor/actions/workflows/build.yml/badge.svg)](https://github.com/szibis/metrics-governor/actions/workflows/build.yml)
[![Benchmarks](https://github.com/szibis/metrics-governor/actions/workflows/benchmark.yml/badge.svg)](https://github.com/szibis/metrics-governor/actions/workflows/benchmark.yml)

## Live Test Status

View the latest test results and coverage reports:

| Status | Link |
|--------|------|
| **Build & Tests** | [GitHub Actions - Build](https://github.com/szibis/metrics-governor/actions/workflows/build.yml) |
| **Benchmarks** | [GitHub Actions - Benchmarks](https://github.com/szibis/metrics-governor/actions/workflows/benchmark.yml) |
| **Coverage Report** | [PR Coverage Comments](https://github.com/szibis/metrics-governor/pulls?q=is%3Apr+label%3Asize%2F) |

## Test Coverage by Component

| Component | Unit Tests | Functional | E2E | Benchmarks | Coverage |
|-----------|:----------:|:----------:|:---:|:----------:|:--------:|
| [**Auth**](../internal/auth/) | 27 | - | ✓ | 10 | ~88% |
| [**Buffer**](../internal/buffer/) | 28 | 6 | ✓ | 6 | ~95% |
| [**Compression**](../internal/compression/) | 19 | - | ✓ | 13 | ~90% |
| [**Config**](../internal/config/) | 50 | - | - | - | ~85% |
| [**Exporter**](../internal/exporter/) | 118 | 5 | ✓ | 14 | ~92% |
| [**Limits**](../internal/limits/) | 77 | 10 | ✓ | 9 | ~92% |
| [**Logging**](../internal/logging/) | 24 | - | - | - | ~80% |
| [**PRW**](../internal/prw/) | 82 | 8 | ✓ | 6 | ~89% |
| [**Queue**](../internal/queue/) | 78 | 8 | ✓ | 7 | ~88% |
| [**Receiver**](../internal/receiver/) | 45 | 9 | ✓ | 9 | ~90% |
| [**Sharding**](../internal/sharding/) | 98 | 8 | ✓ | 10 | ~95% |
| [**Stats**](../internal/stats/) | 65 | 12 | ✓ | 6 | ~90% |
| [**TLS**](../internal/tls/) | 12 | - | ✓ | - | ~85% |
| **Functional** | - | 73 | - | - | - |
| **E2E** | - | - | 8 | - | - |
| **Test Utils** | - | - | 72 | - | - |
| **Total** | **727** | **73** | **80** | **90** | **~86%** |

## Test Categories Detail

### Unit Tests (`internal/*/`)

Component-level tests with mocks. Each package tests its core functionality in isolation.

| Package | Tests | Key Test Areas |
|---------|:-----:|----------------|
| `auth` | 27 | Bearer token, basic auth, HTTP middleware, gRPC interceptors |
| `buffer` | 28 | Add/flush operations, batching, concurrent access, graceful shutdown, failover drain |
| `compression` | 19 | gzip/zstd/snappy/lz4 compress/decompress, round-trips |
| `config` | 50 | CLI parsing, YAML loading, validation, defaults |
| `exporter` | 118 | gRPC/HTTP export, retries, sharded export, queued export, split-on-error, pipeline parity |
| `limits` | 77 | Rule matching, cardinality tracking, adaptive limiting, dry-run |
| `logging` | 24 | JSON output, log levels, field formatting |
| `prw` | 82 | PRW 1.0/2.0 encoding, buffer, limits, sharding, metadata bounds |
| `queue` | 187 | FastQueue operations, push/pop, persistence, recovery |
| `receiver` | 45 | gRPC/HTTP receivers, TLS, authentication |
| `sharding` | 98 | Hash ring, consistent hashing, DNS discovery, splitter |
| `stats` | 65 | Metrics collection, cardinality tracking, Prometheus output |
| `tls` | 12 | Certificate loading, mTLS, client/server config |

### Functional Tests (`functional/`)

Integration tests with real components working together.

| File | Tests | Description |
|------|:-----:|-------------|
| `buffer_test.go` | 6 | Buffer with real stats collector |
| `exporter_test.go` | 5 | Export to mock backends |
| `limits_test.go` | 10 | Limits enforcement with real metrics |
| `prw_test.go` | 8 | PRW pipeline end-to-end |
| `queue_test.go` | 8 | Queue persistence and recovery |
| `receiver_test.go` | 9 | Receiver with compression and auth |
| `sharding_test.go` | 8 | Sharding with multiple endpoints |
| `stats_test.go` | 12 | Stats with high cardinality |
| `verifier_test.go` | 7 | Verification functions |

### E2E Tests (`e2e/`)

Full system tests with complete pipeline.

| Test | Description |
|------|-------------|
| `TestE2E_GRPCFullPipeline` | Complete gRPC flow through all components |
| `TestE2E_HTTPFullPipeline` | Complete HTTP flow through all components |
| `TestE2E_BufferFlush` | Graceful shutdown and buffer flush |
| `TestE2E_ConcurrentClients` | Multiple concurrent clients |
| `TestE2E_HighCardinality` | High cardinality metric handling |
| `TestE2E_ManyDatapoints` | Large batch processing |
| `TestE2E_BurstTraffic` | Traffic burst handling |
| `TestE2E_EdgeCaseValues` | Extreme float values |

### Benchmarks

Performance tests measuring throughput, latency, and memory usage.

| Package | Benchmarks | Key Metrics |
|---------|:----------:|-------------|
| `auth` | 10 | Auth middleware overhead |
| `buffer` | 6 | Add throughput, flush latency |
| `compression` | 13 | Compression ratios and speeds |
| `exporter` | 14 | Export throughput per protocol |
| `limits` | 9 | Rule matching performance |
| `prw` | 6 | PRW encoding/decoding speed |
| `queue` | 7 | FastQueue push/pop performance |
| `receiver` | 9 | Request handling throughput |
| `sharding` | 10 | Hash ring operations |
| `stats` | 6 | Stats collection overhead |

Run benchmarks:
```bash
# All benchmarks
make bench

# Specific package
go test -bench=. -benchmem ./internal/buffer/...

# Compare against baseline
make bench-compare
```

## Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run benchmarks
go test -bench=. -benchmem ./...

# Run functional tests only
go test ./functional/...

# Run e2e tests (requires Docker)
go test ./e2e/...
```

## Test Environment

A comprehensive test environment is provided using Docker Compose with full observability stack.

### Architecture

```mermaid
flowchart LR
    subgraph Sources["Metrics Sources"]
        Generator["Metrics Generator<br/>:9091/metrics"]
    end

    subgraph Ingestion["Ingestion Layer"]
        OTel["OpenTelemetry Collector<br/>:4317 gRPC receiver<br/>:4318 HTTP receiver"]
    end

    subgraph Proxy["Proxy Layer"]
        MG["metrics-governor<br/>:14317 gRPC<br/>:14318 HTTP<br/>:9090/metrics"]
    end

    subgraph Storage["Storage Layer"]
        VM["VictoriaMetrics<br/>:8428"]
    end

    Generator -->|"OTLP/gRPC"| OTel
    OTel -->|"OTLP/gRPC"| MG
    MG -->|"OTLP/HTTP"| VM
```

### Components

| Service | Ports | Description |
|---------|-------|-------------|
| **otel-collector** | 4317, 4318, 8888 | Receives metrics from generator |
| **metrics-governor** | 14317, 14318, 9090 | OTLP proxy with limits |
| **victoriametrics** | 8428 | Storage backend |
| **metrics-generator** | 9091 | Test traffic generator |
| **verifier** | 9092 | Automated verification |
| **grafana** | 3000 | Visualization |

### Quick Start

```bash
# Start the complete test environment
docker compose up --build -d

# Wait for services
sleep 30

# Open Grafana
open http://localhost:3000  # Login: admin/admin

# View metrics-governor stats
curl -s localhost:9090/metrics | grep metrics_governor

# Stop all services
docker compose down
```

### Test Configurations

| Config | Command | Datapoints/sec | Use Case |
|--------|---------|----------------|----------|
| **stable** | `docker compose -f docker-compose.yaml -f compose_overrides/stable.yaml up -d` | ~1,300 | Rate verification |
| **light** | `docker compose -f docker-compose.yaml -f compose_overrides/light.yaml up -d` | ~5,000-10,000 | CI/CD |
| **default** | `docker compose up -d` | ~10,000-20,000 | General testing |
| **perf** | `docker compose -f docker-compose.yaml -f compose_overrides/perf.yaml up -d` | ~100,000+ | Stress testing |
| **sharding** | `docker compose -f docker-compose.yaml -f compose_overrides/sharding.yaml up -d` | ~10,000 | Multi-endpoint sharding |

### Available Endpoints

| Service | Endpoint | Description |
|---------|----------|-------------|
| OTel Collector gRPC | `localhost:4317` | OTLP gRPC receiver |
| OTel Collector HTTP | `localhost:4318` | OTLP HTTP receiver |
| metrics-governor gRPC | `localhost:14317` | Proxy gRPC receiver |
| metrics-governor HTTP | `localhost:14318` | Proxy HTTP receiver |
| metrics-governor stats | `http://localhost:9090/metrics` | Prometheus metrics |
| VictoriaMetrics | `http://localhost:8428` | Query API |
| Grafana | `http://localhost:3000` | Dashboard (admin/admin) |

### Test Scenarios

The metrics generator creates various test scenarios:

| Scenario | Description |
|----------|-------------|
| **Normal traffic** | HTTP request metrics for services |
| **High cardinality** | Unique user/session/request IDs |
| **Burst traffic** | Periodic traffic spikes |
| **Edge cases** | Extreme values (0, ±inf, π, e) |
| **Many datapoints** | Histograms with 15 buckets |
| **Diverse metrics** | ~200 unique metric names |

### Generator Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OTLP_ENDPOINT` | `metrics-governor:4317` | Target endpoint |
| `METRICS_INTERVAL` | `100ms` | Generation interval |
| `SERVICES` | `payment-api,order-api,...` | Service names |
| `ENABLE_HIGH_CARDINALITY` | `true` | Generate high cardinality |
| `ENABLE_BURST_TRAFFIC` | `true` | Enable burst patterns |
| `TARGET_DATAPOINTS_PER_SEC` | `10000` | Target datapoints |

### Useful Commands

```bash
# View metrics-governor stats
curl -s localhost:9090/metrics | grep metrics_governor

# Check limit violations
curl -s localhost:9090/metrics | grep limit

# Query VictoriaMetrics for time series count
curl -s 'localhost:8428/api/v1/query?query=count({__name__=~".+"})'

# View verification results
docker compose logs -f verifier

# View metrics-governor logs
docker compose logs -f metrics-governor
```

### Verifier Output

```
========================================
  VERIFICATION RESULT - PASS
========================================
VICTORIAMETRICS:
  Total time series:      5000
  Unique metric names:    50

METRICS-GOVERNOR:
  Datapoints received:    100000
  Datapoints sent:        98000
  Export errors:          0

VERIFICATION:
  Ingestion rate:         98.00%
  Status:                 PASS
========================================
```

### Troubleshooting

| Issue | Solution |
|-------|----------|
| No metrics in VictoriaMetrics | Check otel-collector logs |
| High export errors | Check network connectivity |
| Verification failing | Check ingestion rate |
| Grafana no data | Wait 30s for metrics |

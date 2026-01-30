# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-01-30

### Added
- Add persistent WAL-based sending queue for exporter


## [0.3.0] - 2026-01-30

### Added

#### Persistent Sending Queue (WAL-based)

A new file-based persistent queue for the exporter that stores failed batches on disk and retries them with configurable backoff. This prevents data loss during network issues or backend unavailability.

**Core Features:**
- **Write-Ahead Log (WAL) storage** for durable batch persistence
  - CRC32 checksums for data integrity validation
  - Sequential write optimization for high throughput
  - Automatic recovery of queued batches on restart
  - Protobuf serialization for efficient storage
- **Configurable queue-full behavior**
  - `drop_oldest` - Drop oldest entries when queue is full (default)
  - `drop_newest` - Reject new entries when queue is full
  - `block` - Block until space is available (with configurable timeout)
- **Adaptive queue sizing** based on available disk space
  - Automatically adjusts limits to maintain target disk utilization
  - Monitors available disk space via syscall
  - Configurable target utilization (default: 85%)
- **WAL compaction** to reclaim space from consumed entries
  - Triggered when consumed entries exceed configurable threshold
  - Preserves pending entries during compaction
  - Atomic file operations for safety
- **Graceful disk-full handling** without getting stuck
  - Detects ENOSPC errors and applies queue-full behavior
  - Continues operation instead of blocking indefinitely
- **Exponential backoff retry** with configurable delays
  - Initial retry interval (default: 5s)
  - Maximum backoff delay (default: 5m)
  - Automatic retry on export failure

#### New CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-queue-enabled` | `false` | Enable persistent queue for export retries |
| `-queue-path` | `./queue` | Directory for WAL queue files |
| `-queue-max-size` | `10000` | Maximum number of batches in queue |
| `-queue-max-bytes` | `1073741824` | Maximum total queue size (1GB) |
| `-queue-retry-interval` | `5s` | Initial retry interval for failed exports |
| `-queue-max-retry-delay` | `5m` | Maximum backoff delay between retries |
| `-queue-full-behavior` | `drop_oldest` | Action when queue is full: `drop_oldest`, `drop_newest`, `block` |
| `-queue-target-utilization` | `0.85` | Target disk utilization for adaptive sizing (0.0-1.0) |
| `-queue-adaptive-enabled` | `true` | Enable adaptive queue sizing based on disk space |
| `-queue-compact-threshold` | `0.5` | Ratio of consumed entries before compaction (0.0-1.0) |

#### New YAML Configuration Options

```yaml
exporter:
  queue:
    enabled: true                    # Enable persistent queue
    path: "/var/lib/metrics-governor/queue"  # Storage directory
    max_size: 10000                  # Max batches in queue
    max_bytes: 1073741824            # Max total size (1GB)
    retry_interval: 5s               # Initial retry delay
    max_retry_delay: 5m              # Max backoff delay
    full_behavior: drop_oldest       # drop_oldest, drop_newest, block
    target_utilization: 0.85         # Target disk utilization
    adaptive_enabled: true           # Enable adaptive sizing
    compact_threshold: 0.5           # Compaction threshold
```

#### New Prometheus Metrics

**Queue Size Metrics:**
| Metric | Type | Description |
|--------|------|-------------|
| `metrics_governor_queue_size` | Gauge | Current number of batches in the send queue |
| `metrics_governor_queue_bytes` | Gauge | Current size of the send queue in bytes |
| `metrics_governor_queue_max_size` | Gauge | Configured maximum batches |
| `metrics_governor_queue_max_bytes` | Gauge | Configured maximum bytes |
| `metrics_governor_queue_effective_max_size` | Gauge | Current effective max batches (adaptive) |
| `metrics_governor_queue_effective_max_bytes` | Gauge | Current effective max bytes (adaptive) |
| `metrics_governor_queue_utilization_ratio` | Gauge | Current queue utilization (0.0-1.0) |
| `metrics_governor_queue_disk_available_bytes` | Gauge | Available disk space on queue partition |

**Queue Operation Metrics:**
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `metrics_governor_queue_push_total` | Counter | - | Total batches pushed to queue |
| `metrics_governor_queue_dropped_total` | Counter | `reason` | Batches dropped (reason: oldest, newest, error) |
| `metrics_governor_queue_retry_total` | Counter | - | Total retry attempts |
| `metrics_governor_queue_retry_success_total` | Counter | - | Successful retry exports |
| `metrics_governor_queue_wal_write_total` | Counter | - | WAL write operations |
| `metrics_governor_queue_wal_compact_total` | Counter | - | WAL compaction operations |
| `metrics_governor_queue_disk_full_total` | Counter | - | Disk full events encountered |

#### Helm Chart Updates

- New `queue` section in `values.yaml` with all queue configuration options
- Persistence volume support for StatefulSet deployments with queue enabled
- Queue-related CLI arguments generation in `_helpers.tpl`
- Volume mount for queue storage directory

### Changed

#### Architecture Diagrams

- Updated detailed architecture diagram with Persistent Queue component
- Added queue retry flow showing failure path from Exporter to SendQueue
- Updated simplified flow diagram with queue retry path
- Added queue metrics to Prometheus observability section

#### Documentation

- Updated README components list to include Persistent Queue
- Updated project structure to include `internal/queue/` package
- Added queue configuration examples

#### Test Coverage

- Improved overall test coverage from 77.7% to 80.6%
- Added comprehensive tests for auth package (98.7% coverage)
- Added tests for config package methods (71.0% coverage)
- Added queue package tests including DefaultConfig, metrics, compaction
- Added receiver package tests for HTTP and gRPC config

### New Files

- `internal/queue/wal.go` - Write-Ahead Log implementation
- `internal/queue/queue.go` - SendQueue wrapper with full behavior handling
- `internal/queue/metrics.go` - Prometheus metrics for queue observability
- `internal/queue/queue_test.go` - Comprehensive queue tests
- `internal/exporter/queued.go` - QueuedExporter wrapper with retry logic
- `internal/exporter/queued_test.go` - QueuedExporter tests

## [0.2.7] - 2026-01-29

### Changed

#### Documentation
- **Architecture diagrams** - Replaced ASCII art with Mermaid diagrams
  - Detailed architecture diagram converted to interactive Mermaid flowchart
  - Simplified flow diagram converted to Mermaid flowchart
  - Better visualization and rendering on GitHub

## [0.2.6] - 2026-01-29

### Added
- Add high-level overview and detailed architecture diagram to README

## [0.2.4] - 2026-01-29

### Added

#### Functional Tests (`functional/`)
- **Receiver tests** (`receiver_test.go`)
  - gRPC receiver basic flow with real buffer integration
  - HTTP receiver basic flow with protobuf payloads
  - HTTP receiver with gzip compression support
  - Multiple concurrent gRPC clients handling
  - HTTP method validation (rejects non-POST)
  - Invalid protobuf payload rejection
- **Exporter tests** (`exporter_test.go`)
  - gRPC protocol export to mock backend
  - HTTP protocol export with protobuf serialization
  - Timeout handling with slow backends
  - Large payload export (100 metrics × 100 datapoints)
  - Concurrent exports (10 goroutines × 50 exports)

#### End-to-End Tests (`e2e/`)
- **Full pipeline tests** (`e2e_test.go`)
  - Complete gRPC flow: client → receiver → buffer → exporter → mock backend
  - Complete HTTP flow: client → receiver → buffer → exporter → mock backend
  - Buffer flush on context cancellation (graceful shutdown)
  - Concurrent clients stress test (10 clients × 50 metrics each)
  - Metric content verification through entire pipeline

#### CI/CD Improvements
- Separate GitHub Actions jobs for unit, functional, and e2e tests
- All test types must pass before building release binaries
- Better test isolation and parallel execution

#### Makefile Targets
- `make test-unit` - Run unit tests only (`./internal/...`)
- `make test-functional` - Run functional tests only (`./functional/...`)
- `make test-e2e` - Run e2e tests only (`./e2e/...`)
- `make test-all` - Run all test suites sequentially

## [0.2.3] - 2026-01-29

### Fixed
- Exclude Helm templates from yamllint validation (Go templates are not valid YAML until rendered)
- Lint only `Chart.yaml` and `values.yaml` which are pure YAML files

## [0.2.2] - 2026-01-29

### Added
- Hadolint configuration file (`.hadolint.yaml`) to ignore DL3018 warning
  - Alpine packages update frequently, pinning versions causes maintenance overhead

## [0.2.1] - 2026-01-29

### Added

#### Linting Infrastructure
- **Dockerfile linting** with hadolint
  - Validates `Dockerfile` and `test/Dockerfile.generator`
  - Configurable failure threshold
- **YAML linting** with yamllint
  - Validates example configs (`examples/*.yaml`)
  - Validates Helm `values.yaml` and `Chart.yaml`
  - Custom `.yamllint.yml` configuration with relaxed rules
- **Helm chart linting** with `helm lint`
  - Validates chart structure and templates

#### Automatic Changelog Generation
- Changelog generated from git commits on release
- Commits categorized into Added/Fixed/Changed/Documentation sections
- `CHANGELOG.md` automatically updated and committed back to main
- Release notes include full changelog in GitHub Release

#### Makefile Targets
- `make lint-dockerfile` - Lint Dockerfiles with hadolint
- `make lint-yaml` - Lint YAML files with yamllint
- `make lint-helm` - Lint Helm chart
- `make lint-all` - Run all linters

## [0.2.0] - 2026-01-29

### Added
- GHCR (GitHub Container Registry) publishing alongside Docker Hub
- GitHub Actions release workflow with multi-arch Docker builds

### Changed
- Docker image names updated:
  - Docker Hub: `slaskoss/metrics-governor`
  - GHCR: `ghcr.io/szibis/metrics-governor`

## [0.1.0] - 2024-01-01

### Added
- Initial release of metrics-governor
- OTLP metrics receiver (gRPC and HTTP)
- Metrics buffering with configurable size and batch settings
- Statistics tracking for cardinality and datapoints
- Limits enforcement with dry-run mode
- TLS support for receiver and exporter
- Authentication support (bearer token, basic auth)
- Compression support (gzip, zstd, snappy, lz4)
- Helm chart for Kubernetes deployment
- Multi-platform binaries (darwin-arm64, linux-arm64, linux-amd64)
- Docker images (multi-arch: amd64, arm64)

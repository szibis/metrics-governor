# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

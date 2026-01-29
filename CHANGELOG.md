# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.3] - 2026-01-29

### Fixed
- Fix yamllint: exclude Helm templates (Go templates, not pure YAML)

### Changed
- Update CHANGELOG.md for v0.2.2


## [0.2.2] - 2026-01-29

### Added
- Add hadolint config to ignore DL3018 for Alpine packages

### Changed
- Update CHANGELOG.md for v0.2.1


## [0.2.1] - 2026-01-29

### Added
- Add linting and automatic changelog generation


## [0.1.0] - 2024-01-01

### Added
- Initial release
- OTLP metrics receiver (gRPC and HTTP)
- Buffering with configurable size and batch settings
- Statistics tracking for cardinality and datapoints
- Limits enforcement with dry-run mode
- TLS support for receiver and exporter
- Authentication support (bearer token, basic auth)
- Compression support (gzip, zstd, snappy, lz4)
- Helm chart for Kubernetes deployment

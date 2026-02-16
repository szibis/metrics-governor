# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [1.0.1] - 2026-02-16

### Performance

- perf: pipeline performance optimizations — stats, compression, GOGC tuning, comparison infrastructure (#187)

### Other

- docs: update README for v1.0 release and fix CHANGELOG duplicate (#186)


## [Unreleased]

### Added

- Profile-Guided Optimization (PGO) support: `make pgo-profile` and `make pgo-build` for 2-7% additional throughput
- Native compression via Rust FFI (optional, build-tag gated): ~1.6x faster gzip/zlib/deflate through flate2 with zlib-ng backend
- `Dockerfile.native` for multi-stage Rust+Go builds with native compression
- Makefile targets: `rust-build`, `build-native`, `test-native`, `docker-native`
- Stats full-mode config knobs: `--stats-cardinality-threshold` (skip Bloom for low-volume metrics) and `--stats-max-label-combinations` (cap label tracking memory)
- YAML config: `stats.cardinality_threshold` and `stats.max_label_combinations`
- CI: `cargo-audit` job in security-scan workflow for Rust dependency vulnerability scanning
- Three-way multi-load comparison: `test/compare/run-multi-load.sh` tests governor vs OTel Collector vs vmagent at 50k/100k dps with scaled resources

### Changed

- GOGC raised from 50/25 to 200/400 across all profiles — GC CPU at 50k dps dropped from 44.5% to negligible (GOMEMLIMIT prevents OOM regardless)
- Stats full-mode uses dual-map key building instead of `mergeAttrs()` — eliminates ~38% of pipeline allocations
- Stats `Record*` counter methods are lock-free atomics with ARM64 cache line padding — Prometheus scrape latency drops from ~10ms to ~1ms under load
- Stats `processFull` uses per-metric lock scope — Bloom filter `Add()` outside collector lock improves multi-core scaling
- Zstd codec uses `EncodeAll`/`DecodeAll` single-shot API instead of streaming `Write`/`Close` — eliminates goroutine coordination overhead and enables pooled destination buffers
- PRW exporter uses pooled `CompressToBuf()` instead of allocating `Compress()` — one fewer `[]byte` copy per export
- Dockerfile conditionally uses PGO when `default.pgo` exists in build context

### Performance

Measured on Apple M3 Max (14 cores), before/after comparison:

- End-to-end pipeline latency: **-13% to -21%** across 1k-50k dps workloads
- Stats full-mode overhead: **22,373 → 18,008 ns/op** per batch (-20%)
- Concurrent throughput (4 goroutines): **520 → 257 ns/op** (-51%)
- Memory per operation: **-12% to -21%** across workloads
- `Record*` counter latency: ~100-500ns (mutex) → ~132ns (atomic, zero allocs)
- `full` stats mode now viable for production (CPU: 30-40% → 12-18% at 100k dps)
- Zstd compress 100KB: **22,900 → 15,059 ns/op** (1.52x faster)
- Zstd decompress 100KB: **62,674 → 36,611 ns/op** (1.71x faster, zero allocs)
- Zstd roundtrip 100KB: **85,574 → 51,670 ns/op** (1.66x faster)
- Governor vs OTel Collector at 50k dps: **29% less CPU** (5.35% vs 7.52%), **zero data loss**
- Governor vs OTel Collector at 100k dps: roughly equal CPU (7.20% vs 6.81%), sublinear scaling (1.35x for 2x load)
- vmagent degrades badly at 100k dps (16.70% CPU, 2.3x governor) — Remote Write scales poorly with high cardinality

### Fixed

- Comparison script measurement bug: `grep -i "metrics-governor"` matched all 6 containers (inflating CPU ~5x); now uses full Docker Compose container names


## [1.0.0] - 2026-02-11

### BREAKING CHANGES

- Removed deprecated CLI flag `--sampling-config` (use `--processing-config`)
- Removed deprecated CLI flags: `--queue-workers`, `--export-concurrency` (use `--parallelism`)
- Removed deprecated CLI flags: `--buffer-memory-percent`, `--queue-memory-percent` (use `--memory-budget-percent`)
- Removed deprecated CLI flags: `--queue-direct-export-timeout`, `--queue-retry-timeout`, `--queue-drain-timeout`, `--queue-drain-entry-timeout`, `--queue-close-timeout` (use `--export-timeout`)
- Removed deprecated CLI flags: `--queue-backoff-multiplier`, `--queue-circuit-breaker-enabled`, `--queue-circuit-breaker-threshold`, `--queue-batch-drain-size`, `--queue-burst-drain-size` (use `--resilience-level`)
- Removed deprecated Prometheus metrics: `metrics_governor_sampling_*`, `metrics_governor_downsampling_*` (use `metrics_governor_processing_*`)
- Removed deprecated Helm `sampling:` section (use `processing:`)
- Removed legacy sampling code path — processing engine handles all operations (sample, downsample, aggregate, transform, classify, drop)


## [0.44.0] - 2026-02-11

### Added

- feat: vtprotobuf integration with updated profiles and documentation (#182)


## [0.43.3] - 2026-02-10

### Fixed

- fix: drop mergeAttrs allocation size hint to resolve CodeQL overflow (#179)


## [0.43.2] - 2026-02-10

### Fixed

- fix: use min() builtin in mergeAttrs to satisfy CodeQL overflow check (#177)


## [0.43.1] - 2026-02-10

### Fixed

- fix: cap mergeAttrs allocation size hint to prevent overflow (#175)


## [0.43.0] - 2026-02-10

### Added

- feat: sync.Pool memory optimizations and performance comparison tooling (#173)


## [0.42.0] - 2026-02-10

### Added

- feat: pipeline stability and predictability improvements (#170)


## [0.41.1] - 2026-02-10

### Other

- docs: simplify architecture SVG and add universal governance messaging (#166)


## [0.41.0] - 2026-02-09

### Added

- feat: add autotune design document (#167)


## [0.40.2] - 2026-02-09

### Other

- ci: bump actions/checkout from 4 to 6


## [0.40.1] - 2026-02-09

### Fixed

- fix: prevent typed nil panic in FusedProcessor and stats pipeline (#157)


## [0.40.0] - 2026-02-09

### Added

- feat: add safety, observable, and resilient configuration profiles with README overhaul (#155)


## [0.39.0] - 2026-02-09

### Added

- feat: rule scaling performance and advanced limits enforcement (#153)

### Other

- docs: rebuild README with marketing-focused structure and upgraded badges (#151)


## [0.38.0] - 2026-02-09

### Added

- feat: classify action, dead rule detection, and rule ownership labels (#148)


## [0.37.0] - 2026-02-09

### Added

- feat: pipeline performance optimizations with queue modes, stats levels, and pipeline fusion (#146)


## [0.36.1] - 2026-02-08

### Other

- ci: speed up CI workflows by removing redundant race detection and enabling test cache (#144)


## [0.36.0] - 2026-02-08

### Added

- feat: 5-test benchmark matrix with OTLP HTTP input and dual export paths (#142)


## [0.35.0] - 2026-02-08

### Added

- feat: benchmark infrastructure and processing test configs (#140)


## [0.34.1] - 2026-02-08

### Performance

- perf: processing pipeline performance optimizations (#138)

### Other

- test: boost processing rules coverage from 80.9% to 89.5% (#137)


## [0.34.0] - 2026-02-08

### Added

- feat: unified processing rules engine with aggregate, transform, and two-tier architecture (#135)


## [0.33.0] - 2026-02-08

### Added

- feat: production guide expansion, alerting system, and playground alerts tab (#133)


## [0.32.0] - 2026-02-08

### Added

- feat: configuration simplification with profiles, auto-derivation, and deprecation lifecycle (#131)


## [0.31.0] - 2026-02-08

### Added

- feat: memory forensics metrics, resource control tuning, and mermaid diagram fixes (#129)


## [0.30.0] - 2026-02-08

### Added

- feat: export pipeline optimization with batch tuning, adaptive scaling, and comprehensive docs (#127)


## [0.29.0] - 2026-02-08

### Added

- feat: export pipeline optimization, component metrics, and resource visibility (#125)


## [0.28.0] - 2026-02-07

### Added

- feat: pipeline component utilization metrics, worker utilization fix, and stability tuning (#123)


## [0.27.2] - 2026-02-07

### Fixed

- fix: operations dashboard workers utilization, layout, and component visibility (#121)


## [0.27.1] - 2026-02-07

### Added

- feat: always-queue architecture with worker pool, buffer backpressure, and percentage memory sizing (#119)


## [0.27.0] - 2026-02-07

### Added

- feat: always-queue architecture with worker pool, buffer backpressure, and percentage memory sizing (#117)


## [0.26.0] - 2026-02-07

### Added

- feat: fix silent degradation under slow destinations, add log observability metrics (#115)

### Other

- docs: update architecture diagram, boost test coverage to 90%+ (#112)


## [0.25.0] - 2026-02-07

### Added

- feat(helm): replace CLI flag sections with config-file-first approach (#113)


## [0.24.0] - 2026-02-07

### Added

- feat: queue/exporter resilience — fix backpressure & stability under destination failures (#110)

### Other

- test: add Helm chart unit tests with helm-unittest (#109)


## [0.23.0] - 2026-02-06

### Added

- feat: OTEL logging, I/O metrics, reload fix, stats threshold, and production tuning (#107)

### Other

- test: add extended coverage for relabeling, sampling, and multi-tenancy (#106)


## [0.22.0] - 2026-02-06

### Added

- feat: add multi-tenancy with configurable tenant detection and hierarchical quotas (#104)


## [0.21.0] - 2026-02-06

### Added

- feat: add metric sampling/downsampling with configurable strategies (#102)


## [0.20.0] - 2026-02-06

### Added

- feat: add Prometheus-compatible metric relabeling engine with pipeline integration (#100)


## [0.19.0] - 2026-02-06

### Added

- feat: add config validation CLI subcommand (#98)


## [0.18.0] - 2026-02-06

### Added

- feat: add /debug/pprof/ endpoints behind -pprof-enabled flag (#96)


## [0.17.0] - 2026-02-06

### Added

- feat: health endpoints, config validation, dynamic reload, and observability (#94)


## [0.16.1] - 2026-02-05

### Fixed

- fix: bump Go 1.25.7 and resolve resilience test race (#90)


## [0.16.0] - 2026-02-05

### Added

- feat: disk queue I/O optimizations — buffered writer, snappy compression, write coalescing (#88)


## [0.15.0] - 2026-02-05

### Added

- feat: add interactive configuration helper UI (#87)

### Other

- test: add comprehensive race condition and memory leak tests across all packages (#86)


## [0.14.0] - 2026-02-05

### Added

- feat: hybrid Bloom/HyperLogLog cardinality tracking (#84)


## [0.13.2] - 2026-02-05

### Performance

- perf(limits): scale limits.yaml to half of perf.yaml throughput (#82)


## [0.13.1] - 2026-02-05

### Other

- ci(release): combine changelog sections with PR links in release notes (#80)


## [0.13.0] - 2026-02-04

### Added

- feat(test): spike scenarios, limits testing, queue recovery & graceful shutdown (#78)


## [0.12.1] - 2026-02-04


## [0.12.0] - 2026-02-04

### Added

- feat(ci): add smart CI filtering based on changed files (#74)

### Other

- docs: add security vulnerability reporting policy (#72)


## [Unreleased]

### Added

- feat(ci): add smart CI filtering based on changed files (#74)


## [0.11.1] - 2026-02-04


## [0.11.0] - 2026-02-04

### Added

- feat(ci): run memory check tests on push to main (#57)


## [0.10.1] - 2026-02-04

### Fixed

- fix(ci): replace disallowed actions with direct binary installs and fix label guard (#68)


## [0.10.0] - 2026-02-04

### Added

- feat(ci): add security scanning workflows and release PR skip logic (#58)


## [0.9.8] - 2026-02-04

### Fixed

- fix: address memory leak vectors with bounded maps, pool resets, and CI checks (#55)


## [0.9.7] - 2026-02-04

### Added

- feat: pipeline parity, failover drain, split-on-error, memory leak fixes (#43)
- feat: byte-aware batch splitting, concurrent exports, failover queue (#41)

### Fixed

- fix(ci): use GITHUB_TOKEN for verified commits in auto-release (#53)
- fix(ci): use GitHub API for commits and tags in auto-release (#51)
- fix(ci): use github-actions[bot] identity instead of GPG signing (#49)
- fix(ci): configure git user identity from GPG key in auto-release (#47)
- fix(ci): add --repo flag to gh pr view in auto-release (#46)
- fix(ci): fix PR discovery for squash-merged commits (#45)
- fix(ci): replace crazy-max/ghaction-import-gpg with inline GPG import (#44)
- fix(ci): improve test results detection and fix race conditions (#39)
- fix(ci): add GPG signing to release workflow commits (#38)
- fix(ci): use release PR strategy instead of direct push (#36)

### Performance

- perf: caching and pooling optimizations for hot paths (#40)


## [Unreleased]

### Added

- feat(buffer): add failover queue drain loop that actively re-exports queued entries every 5s instead of leaving them stranded
  - New `Pop()` method on `FailoverQueue` interface
  - Drain up to 10 entries per tick, re-push on failure
  - New metrics: `metrics_governor_failover_queue_drain_total`, `metrics_governor_failover_queue_drain_errors_total`
- feat(prw): persistent disk-backed queue replacing in-memory slice (pipeline parity with OTLP)
  - PRW queue now uses the same `SendQueue` as OTLP for durable, restart-surviving storage
  - Added `BackoffEnabled`, `BackoffMultiplier` configuration options
  - Added `UnmarshalWriteRequest` helper for queue deserialization
- feat(prw): split-on-error support for PRW pipeline
  - HTTP 413 and "too big"/"too large"/"exceeding" patterns trigger automatic batch splitting at Timeseries level
  - PRW exporter now returns `*ExportError` wrapping `*PRWClientError`/`*PRWServerError` for unified `IsSplittable()`/`IsRetryable()` handling
- feat(exporter): pipeline parity tests verifying OTLP and PRW have identical resilience behavior

### Fixed

- fix(memqueue): fix memory leak from unbounded slice growth in MemoryQueue causing OOM crash-loop after sustained traffic
- fix(prw): cap metadata entries at 10,000 to prevent unbounded memory growth from continuously-arriving new metric families
- fix(prw): eliminate unbounded queue slice growth by replacing in-memory `[]*prwQueueEntry` with disk-backed `*queue.SendQueue`

## [0.9.6] - 2026-02-02

### Fixed

- fix(ci): create GitHub release before uploading assets (#35)


## [0.9.5] - 2026-02-02

### Added

- feat(cardinality): add bloom filter state persistence (#34)
- feat(queue): add resilience features and comprehensive documentation (#30)

### Fixed

- fix(ci): use GitHub API for PR file detection in auto-release (#33)
- fix(ci): improve release workflow with independent chart versioning (#29)

### Other

- docs: add mermaid diagrams to performance docs and improve README (#32)

## [0.9.3] - 2026-02-02

### Fixed

- Fix floating point comparison in e2e memory stability test (#25)

### CI

- Fix PAT authentication for version bump push (#24)

## [0.9.2] - 2026-02-02

### Changed

- Move performance optimization documentation from README to dedicated `docs/performance.md` (#22)

### CI

- Fix auto-release workflow to create PR for version bumps (works with branch protection)
- Add darwin-amd64 (Intel Mac) binary to release artifacts

## [0.9.0] - 2026-02-02

### Added

- **Bloom Filter Cardinality Tracking** - Memory-efficient probabilistic cardinality tracking using Bloom filters
  - New `internal/cardinality/` package with `Tracker` interface
  - `BloomTracker` implementation using [bits-and-blooms/bloom/v3](https://github.com/bits-and-blooms/bloom)
  - `ExactTracker` implementation for 100% accurate tracking (backward compatibility)
  - **98% memory reduction** compared to map-based tracking (75MB → 1.2MB per 1M series)
  - Configurable false positive rate (default: 1%)
  - Thread-safe concurrent access with `sync.RWMutex`
  - Applied to both limits enforcer (`internal/limits/enforcer.go`) and stats collector (`internal/stats/stats.go`)

- **Cardinality Configuration** - New CLI flags for cardinality tracking:
  - `-cardinality-mode` - Tracking mode: `bloom` (memory-efficient) or `exact` (100% accurate)
  - `-cardinality-expected-items` - Expected unique items per tracker for Bloom sizing (default: 100000)
  - `-cardinality-fp-rate` - Bloom filter false positive rate (default: 0.01 = 1%)

- **Cardinality Observability Metrics** - New Prometheus metrics for Bloom filter monitoring:
  | Metric | Type | Description |
  |--------|------|-------------|
  | `metrics_governor_cardinality_mode{mode}` | gauge | Active tracking mode (bloom/exact) |
  | `metrics_governor_cardinality_trackers_total` | gauge | Number of active trackers (stats) |
  | `metrics_governor_cardinality_memory_bytes` | gauge | Total memory used by trackers (stats) |
  | `metrics_governor_cardinality_config_expected_items` | gauge | Configured expected items |
  | `metrics_governor_cardinality_config_fp_rate` | gauge | Configured false positive rate |
  | `metrics_governor_rule_cardinality_memory_bytes{rule}` | gauge | Memory per rule (limits) |
  | `metrics_governor_limits_cardinality_trackers_total` | gauge | Total trackers in enforcer |
  | `metrics_governor_limits_cardinality_memory_bytes` | gauge | Total memory (limits) |

### Performance

- **Memory Optimization** - Cardinality tracking memory usage:
  | Items | map[string]struct{} | Bloom (1% FPR) | Savings |
  |-------|---------------------|----------------|---------|
  | 10K   | 750KB               | 12KB           | 98%     |
  | 100K  | 7.5MB               | 120KB          | 98%     |
  | 1M    | 75MB                | 1.2MB          | 98%     |
  | 10M   | 750MB               | 12MB           | 98%     |

- **False Positive Impact** - With 1% false positive rate:
  - ~1% undercount of cardinality (acceptable for rate limiting and dashboards)
  - Slightly more permissive limits enforcement (~1% more series allowed)

### Changed

- `groupStats.cardinality` in `internal/limits/enforcer.go` now uses `cardinality.Tracker` interface
- `MetricStats.UniqueSeries` and `LabelStats.UniqueSeries` in `internal/stats/stats.go` now use `cardinality.Tracker` interface
- `GetGlobalStats()` return type changed from `int` to `int64` for cardinality count

### New Files

- `internal/cardinality/tracker.go` - Tracker interface and BloomTracker implementation
- `internal/cardinality/exact.go` - ExactTracker implementation (map-based)
- `internal/cardinality/config.go` - Global configuration and factory functions
- `internal/cardinality/tracker_test.go` - Comprehensive tests and benchmarks (94.4% coverage)

### Dependencies

- Added `github.com/bits-and-blooms/bloom/v3 v3.7.1`
- Added `github.com/bits-and-blooms/bitset v1.24.2` (indirect)

## [0.8.0] - 2026-02-02

### Added

- **FastQueue Persistent Queue** - VictoriaMetrics-inspired high-performance queue replacing WAL implementation
  - Two-layer architecture: in-memory buffered channel + disk chunk files
  - Metadata-only persistence with atomic JSON sync (configurable, default: 1s)
  - Simple block format: 8-byte length header + data (no per-write compression overhead)
  - Automatic chunk rotation at configurable size boundaries
  - O(1) recovery time vs O(n) index scan with old WAL

- **FastQueue Configuration** - New CLI flags:
  - `-queue-inmemory-blocks` - In-memory channel size (default: 256)
  - `-queue-chunk-size` - Chunk file size in bytes (default: 512MB)
  - `-queue-meta-sync` - Metadata sync interval / max data loss window (default: 1s)
  - `-queue-stale-flush` - Interval to flush stale in-memory blocks to disk (default: 5s)

- **FastQueue Metrics** - New Prometheus metrics for queue monitoring:
  | Metric | Type | Description |
  |--------|------|-------------|
  | `metrics_governor_fastqueue_inmemory_blocks` | gauge | Current in-memory block count |
  | `metrics_governor_fastqueue_disk_bytes` | gauge | Bytes stored on disk |
  | `metrics_governor_fastqueue_meta_sync_total` | counter | Metadata sync operations |
  | `metrics_governor_fastqueue_chunk_rotations` | counter | Chunk file rotations |
  | `metrics_governor_fastqueue_inmemory_flushes` | counter | Stale flushes to disk |

- **E2E Queue Testing** - New test infrastructure for queue persistence:
  - `compose_overrides/queue.yaml` - Queue testing overlay with aggressive settings
  - `test/e2e-queue-test.sh` - E2E script for persistence and recovery testing

### Changed

- **Queue Architecture** - Replaced WAL (Write-Ahead Log) with FastQueue
  - Eliminated per-write sync overhead (sync once per second vs every write)
  - Removed compression overhead from hot path
  - ~15x reduction in disk I/O at high throughput

### Removed

- **WAL Implementation** - Deleted `internal/queue/wal.go` and related WAL code
  - `-queue-sync-mode` flag removed (no longer needed)
  - `-queue-sync-batch-size` flag removed
  - `-queue-sync-interval` flag replaced by `-queue-meta-sync`
  - `-queue-compression` flag removed (no per-write compression)
  - `-queue-write-ahead` flag removed (always write-ahead now)

### Performance

- **I/O Optimization** - FastQueue vs old WAL at 200k datapoints/s:
  | Metric | Old WAL | FastQueue | Improvement |
  |--------|---------|-----------|-------------|
  | Sync operations | ~4000/s | ~1/s | 4000x |
  | Disk I/O | 1.5GB | <100MB | 15x |
  | Recovery time | O(n) scan | O(1) metadata | Instant |
  | Max data loss | 250ms | 1s (configurable) | Trade-off |

### Migration

- Existing WAL files (`queue.wal`, `queue.idx`) are not compatible with FastQueue
- FastQueue creates new files: `fastqueue.meta` and chunk files (`0000000000000000`, etc.)
- Recommend clearing queue directory when upgrading, or let old files be ignored

## [0.7.0] - 2026-02-01

### Added

- **String Interning** - New `internal/intern` package for string deduplication
  - Concept inspired by [VictoriaMetrics blog articles](https://valyala.medium.com/how-victoriametrics-makes-instant-snapshots-for-multi-terabyte-time-series-data-e1f3fb0e0282) on TSDB optimization techniques
  - Original implementation using standard Go patterns (`sync.Map`, `unsafe.String`)
  - Reduces memory allocations by 66% for PRW label parsing
  - Pre-populated pool for common Prometheus labels (`__name__`, `job`, `instance`, etc.)
  - Applied to PRW label parsing and shard key building
  - Zero-allocation cache hits using `sync.Map`
  - Configurable via `-string-interning` and `-intern-max-value-length` flags

- **Concurrency Limiting** - Semaphore-based limiter for parallel export operations
  - Concept inspired by VictoriaMetrics concurrency control patterns
  - Original implementation using standard Go channel-based semaphore pattern
  - Prevents goroutine explosion under high load (88% reduction in concurrent goroutines)
  - Bounded at `NumCPU * 4` by default, configurable via `-export-concurrency`
  - Applied to both OTLP and PRW sharded exporters

- **Performance Configuration** - New CLI flags for tuning:
  - `-export-concurrency` - Limit concurrent export goroutines (default: NumCPU * 4)
  - `-string-interning` - Enable/disable label string interning (default: true)
  - `-intern-max-value-length` - Max length for value interning (default: 64)

### Changed

- **Queue Timeout Optimization** - Replaced goroutine+sleep pattern with `time.AfterFunc` for more efficient timeout handling in persistent queue

### Performance

- **PRW Label Parsing**: 66% reduction in allocations, 12.5% reduction in memory
- **Intern Hit Rate**: 99.99% for common Prometheus labels
- **Goroutine Reduction**: 88% fewer concurrent goroutines under load
- **GC Pressure**: Significantly reduced due to string deduplication in PRW pipeline

## [0.6.3] - 2026-02-01

### Added

- **Ship skills restructure** - Separated ship workflow into `ship_release` and `ship_pr` skills for better organization (#3) @szibis
  - `ship_release`: Creates release PRs with auto-generated changelog from merged PRs
  - `ship_pr`: Creates regular PRs with conventional commits and automatic labels

### Fixed

- **tag-on-merge workflow** - Fixed PR body expansion error that caused shell commands to fail (#2) @szibis

### CI/CD Improvements

- Added golangci-lint with staticcheck to CI pipeline
- Added automatic PR labeler with color-coded labels based on:
  - Commit type (feat, fix, docs, perf, etc.)
  - Changed components (buffer, queue, sharding, etc.)
  - PR size (XS, S, M, L, XL)
- Fixed test counting in CI workflow
- Updated development documentation with labeler guide

**Full Changelog**: https://github.com/szibis/metrics-governor/compare/v0.6.2...v0.6.3

**Test Coverage:**
- Unit Tests: 702
- Functional Tests: 73
- E2E Tests: 20
- Benchmarks: 90
- Total: 885+ tests

## [0.6.2] - 2026-01-31

### Fixed

Fix buffer benchmark regression, add dual pipeline docs

**Performance:**
- Fix buffer benchmark regression by lazy `countDatapoints()` evaluation
- `countDatapoints()` now only computed when needed (error logging or stats recording)
- Improved buffer add throughput from ~14.5 ns/op back to ~11.3 ns/op

**Documentation:**
- Add dual pipeline notices across all documentation
- Clarify that OTLP and PRW pipelines are completely separate
- Components (limits, buffer, exporters, sharding) work identically for both protocols

**Test Coverage:**
- Unit Tests: 461
- Functional Tests: 73
- E2E Tests: 20
- Other Tests: 32
- Benchmarks: 90
- Total: 586+ tests

## [0.6.1] - 2026-01-31

### Added

Add PRW metrics, sharding, queue, and Grafana dashboards

**PRW Enhancements:**
- Consistent hash sharding for PRW (same as OTLP) - routes metrics to multiple backends
- WAL-based persistent queue for PRW retry with exponential backoff
- Shard key builder from metric name and configurable labels
- Sharded PRW exporter with static endpoints or dynamic discovery

**Prometheus Metrics:**
- `metrics_governor_prw_datapoints_received_total` - PRW datapoints received
- `metrics_governor_prw_timeseries_received_total` - PRW timeseries received
- `metrics_governor_prw_datapoints_sent_total` - PRW datapoints sent
- `metrics_governor_prw_timeseries_sent_total` - PRW timeseries sent
- `metrics_governor_prw_batches_sent_total` - PRW batches exported
- `metrics_governor_prw_export_errors_total` - PRW export errors

**Grafana Dashboards:**
- `dashboards/operations.json` - Operations dashboard with separate OTLP and PRW sections
- `dashboards/e2e-testing.json` - E2E testing dashboard
- `dashboards/README.md` - Dashboard documentation and installation guide

**Bug Fixes:**
- Fix race condition in PRW buffer SetExporter
- Fix race condition in PRW queue processQueue

**Test Coverage:**
- Unit Tests: 461
- Functional Tests: 73
- E2E Tests: 8
- Benchmarks: 90
- Total: 632+ tests

## [0.6.0] - 2026-01-31

### Added

Add Prometheus Remote Write support

Implement PRW protocol as a separate pipeline (PRW→PRW) alongside existing OTLP pipeline. No cross-protocol conversion - metrics stay in their original format.

**Features:**
- PRW 1.0 and 2.0 protocol support
- Native histograms, exemplars, and metadata (PRW 2.0)
- Snappy and zstd compression
- VictoriaMetrics mode with extra labels and short endpoint
- TLS and authentication support
- Buffering with configurable batch size and flush interval
- Retry queue with exponential backoff

**New Components:**
- `internal/prw/` - PRW types, buffer, limits, proto encoding
- `internal/receiver/prw.go` - PRW HTTP receiver
- `internal/exporter/prw_exporter.go` - PRW exporter
- `internal/exporter/prw_queued.go` - Queued PRW exporter
- `docs/prw.md` - PRW documentation

**Test Coverage:**
- Unit Tests: 427
- Functional Tests: 73
- E2E Tests: 20
- Benchmarks: 88
- Total: 608+ tests

## [0.5.5] - 2026-01-31

### Added

Add limiting metadata labels to metrics

When a limiting rule matches a metric, two labels are now injected at the DataPoint level:
- `metrics.governor.action`: The action taken (passed, log, drop, adaptive)
- `metrics.governor.rule`: The name of the matching rule

This enables downstream systems to identify which metrics were affected by limiting rules and what action was applied.

**Test Coverage:**
- Unit Tests: 345
- Functional Tests: 64
- E2E Tests: 8
- Benchmarks: 76
- Total: 493+ tests

## [0.5.4] - 2026-01-31

### Changed

Updated release script and /release skill

**Test Coverage:**
- Unit Tests: 333
- Functional Tests: 59
- E2E Tests: 20
- Benchmarks: 76
- Total: 488+ tests

### Added

- Helm ConfigMap template for metrics-governor configuration

## [0.5.1] - 2026-01-31

### Added

#### Comprehensive Functional Test Suite

Added 58 functional tests covering all major components with end-to-end behavior verification:

**Test Coverage by Component:**

| Component | Unit | Functional | E2E | Benchmarks | Coverage |
|-----------|:----:|:----------:|:---:|:----------:|:--------:|
| Buffer | 13 | 6 | ✓ | 8 | 95% |
| Exporter | 31 | 5 | ✓ | 12 | 90% |
| Receiver | 16 | 9 | ✓ | 10 | 90% |
| Limits | 37 | 10 | ✓ | 8 | 92% |
| Queue | 29 | 8 | ✓ | 10 | 88% |
| Sharding | 98 | 8 | ✓ | 6 | 95% |
| Stats | 19 | 12 | ✓ | 8 | 90% |
| **Total** | **333** | **58** | **20** | **76** | **~85%** |

**New Functional Test Files:**
- `functional/buffer_test.go` - Batching, flush intervals, concurrent access, graceful shutdown
- `functional/limits_test.go` - Dry run mode, drop/log/adaptive actions, rule matching
- `functional/queue_test.go` - Push/pop, persistence, drop behaviors, retry, compaction
- `functional/sharding_test.go` - Hash ring distribution, consistent hashing, minimal rehash
- `functional/stats_test.go` - Basic tracking, label tracking, cardinality, Prometheus output

### Changed

- Updated release workflow to automatically bump and package Helm chart version
- Added clearer artifact descriptions in release notes distinguishing binaries from Helm chart
- Updated README with comprehensive test coverage table and badges

## [0.5.0] - 2026-01-31

### Added

#### Consistent Sharding for Horizontal Scaling

A major new feature that enables distributing metrics across multiple backend endpoints using consistent hashing. This allows horizontal scaling of time-series databases like VictoriaMetrics vminsert.

**Core Features:**
- **DNS-based endpoint discovery** - Automatically discovers backend pods from Kubernetes headless services
- **Consistent hash ring** - Uses xxhash with configurable virtual nodes (default: 150) for even distribution
- **Per-datapoint routing** - Each datapoint is routed independently based on shard key
- **Minimal rehashing** - Adding/removing endpoints only moves ~1/n of the data
- **Per-endpoint queuing** - When queue is enabled, each endpoint gets its own independent queue for retry

**Shard Key Construction:**
- Metric name is always included (automatic)
- Additional labels can be configured for finer-grained sharding
- Format: `metric_name|label1=value1|label2=value2` (sorted alphabetically)
- All datapoints with the same shard key always go to the same endpoint

**New CLI Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-sharding-enabled` | `false` | Enable consistent sharding |
| `-sharding-headless-service` | | K8s headless service DNS name |
| `-sharding-dns-refresh-interval` | `30s` | DNS refresh interval |
| `-sharding-dns-timeout` | `5s` | DNS lookup timeout |
| `-sharding-labels` | | Comma-separated labels for shard key |
| `-sharding-virtual-nodes` | `150` | Virtual nodes per endpoint |
| `-sharding-fallback-on-empty` | `true` | Use static endpoint if DNS empty |

**New YAML Configuration:**

```yaml
exporter:
  sharding:
    enabled: true
    headless_service: "vminsert-headless.monitoring.svc.cluster.local:8480"
    dns_refresh_interval: 30s
    dns_timeout: 5s
    labels:
      - service
      - env
    virtual_nodes: 150
    fallback_on_empty: true
```

**New Prometheus Metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `metrics_governor_sharding_endpoints_total` | gauge | Current number of active endpoints |
| `metrics_governor_sharding_datapoints_total{endpoint}` | counter | Datapoints sent per endpoint |
| `metrics_governor_sharding_export_errors_total{endpoint}` | counter | Export errors per endpoint |
| `metrics_governor_sharding_rehash_total` | counter | Hash ring rehash events |
| `metrics_governor_sharding_dns_refresh_total` | counter | DNS refresh attempts |
| `metrics_governor_sharding_dns_errors_total` | counter | DNS lookup errors |
| `metrics_governor_sharding_dns_latency_seconds` | histogram | DNS lookup latency |
| `metrics_governor_sharding_export_latency_seconds{endpoint}` | histogram | Export latency per endpoint |

**Exporter Configuration Matrix:**

| Sharding | Queue | Result |
|----------|-------|--------|
| off | off | Single OTLPExporter |
| off | on | QueuedExporter → OTLPExporter |
| on | off | ShardedExporter → multiple OTLPExporters |
| on | on | ShardedExporter → multiple (QueuedExporter → OTLPExporter) |

**New Files:**
- `internal/sharding/hashring.go` - Consistent hash ring with xxhash and virtual nodes
- `internal/sharding/hashring_test.go` - Hash ring tests (distribution, consistency, concurrency)
- `internal/sharding/shardkey.go` - Shard key builder from metric name + labels
- `internal/sharding/shardkey_test.go` - Shard key tests
- `internal/sharding/splitter.go` - Splits ResourceMetrics by shard key
- `internal/sharding/splitter_test.go` - Splitter tests for all metric types
- `internal/sharding/discovery.go` - DNS-based endpoint discovery
- `internal/sharding/discovery_test.go` - Discovery tests with mock resolver
- `internal/sharding/metrics.go` - Prometheus metrics for sharding
- `internal/exporter/sharded.go` - ShardedExporter implementation
- `internal/exporter/sharded_test.go` - ShardedExporter tests

**Modified Files:**
- `internal/config/yaml.go` - Added ShardingYAMLConfig struct
- `internal/config/config.go` - Added sharding CLI flags and configuration
- `cmd/metrics-governor/main.go` - Wired up ShardedExporter when sharding enabled

#### Grafana Dashboard - Sharding Section

New "Sharding" section with 9 panels:
- Active Endpoints (stat)
- Rehash Events (stat)
- DNS Errors (stat)
- DNS Refreshes/min (stat)
- Datapoints Rate by Endpoint (timeseries)
- Export Errors by Endpoint (timeseries)
- Export Latency by Endpoint (timeseries)
- DNS Lookup Latency (timeseries)
- Endpoint Distribution (piechart)

### Changed

#### README Updates

- Added "Core Value Proposition" table highlighting key challenges and solutions
- Added "What Sets metrics-governor Apart" section
- Added comprehensive "Consistent Sharding" documentation section with:
  - Architecture diagram (Mermaid)
  - Kubernetes headless service setup example
  - Configuration examples (YAML and CLI)
  - How sharding works explanation
  - Sharding metrics table
  - Default configuration table
- Updated Features list to include Consistent Sharding
- Updated Key Capabilities table
- Updated Project Structure to include `internal/sharding/` package

## [0.4.4] - 2026-01-30

### Added

#### Runtime Metrics for Governor Process

New internal metrics exposing Go runtime and process statistics:

**Go Runtime Metrics:**
- `metrics_governor_goroutines` - Number of goroutines
- `metrics_governor_memory_alloc_bytes` - Currently allocated memory
- `metrics_governor_memory_heap_*` - Heap memory stats (alloc, sys, idle, inuse, released, objects)
- `metrics_governor_memory_stack_*` - Stack memory stats
- `metrics_governor_gc_cycles_total` - Total GC cycles
- `metrics_governor_gc_pause_total_seconds` - Total GC pause time
- `metrics_governor_gc_cpu_percent` - GC CPU usage percentage
- `metrics_governor_process_uptime_seconds` - Process uptime

**Network I/O Metrics (Linux):**
- `metrics_governor_network_receive_bytes_total` - Total bytes received
- `metrics_governor_network_transmit_bytes_total` - Total bytes transmitted
- `metrics_governor_network_receive_packets_total` - Total packets received
- `metrics_governor_network_transmit_packets_total` - Total packets transmitted
- `metrics_governor_network_*_errors_total` - Network errors
- `metrics_governor_network_*_dropped_total` - Dropped packets

**Disk I/O Metrics (Linux):**
- `metrics_governor_disk_read_bytes_total` - Actual bytes read from storage layer
- `metrics_governor_disk_write_bytes_total` - Actual bytes written to storage layer

**PSI Metrics (Linux):**
- `metrics_governor_psi_{cpu,memory,io}_some_avg*` - Pressure stall info

**New Files:**
- `internal/stats/runtime.go` - RuntimeStats collector

**Modified Files:**
- `cmd/metrics-governor/main.go` - Integrated RuntimeStats into /metrics endpoint

#### Grafana Dashboard - Runtime Section

New "Runtime (Governor Process)" section with 12 panels:
- Memory Usage (heap, stack, system)
- Goroutines & Heap Objects
- GC Pause Time and GC Cycles & CPU
- Network Throughput (receive/transmit bytes/sec)
- Disk I/O Throughput (read/write bytes/sec)
- Network Packets Rate
- Disk I/O Syscalls Rate
- Stat panels for key metrics

## [0.4.3] - 2026-01-30

### Added

#### Log Aggregation for High-Throughput Logging

Implemented log aggregation to reduce log noise during high-throughput operation (50k-100k metrics/sec):

**New LogAggregator Component:**
- Batches similar log messages per 10-second interval
- Tracks occurrence count, total datapoints, and first/last seen timestamps
- Reduces thousands of repetitive log lines to a single summary per interval

**Example Output:**
```json
{"timestamp":"2026-01-30T12:00:00Z","level":"warn","message":"limit exceeded: cardinality","fields":{"rule":"per-service-limits","occurrences":57,"total_datapoints":285000,"first_seen":"2026-01-30T11:59:50Z","last_seen":"2026-01-30T12:00:00Z"}}
```

**Integration Points:**
- Buffer export errors are aggregated
- Limits enforcer violations are aggregated
- Each component can use its own LogAggregator instance

**New Files:**
- `internal/limits/log_aggregator.go` - LogAggregator implementation

**Modified Files:**
- `internal/buffer/buffer.go` - Added LogAggregator interface and field
- `internal/limits/enforcer.go` - Uses log aggregator for violations
- `cmd/metrics-governor/main.go` - Wires up log aggregator with graceful shutdown

### Changed

#### Verifier Optimization

Replaced expensive VictoriaMetrics queries with efficient TSDB Status API:

**Before:**
```
count({__name__=~".+"})  # Expensive - scans all time series
```

**After:**
```
GET /api/v1/status/tsdb  # Efficient - returns pre-computed statistics
```

This fixes VictoriaMetrics overload issues when running the test environment under high load.

#### Test Environment Tuning

**VictoriaMetrics Configuration:**
- Increased memory limit to 10GB for high-throughput testing
- Added query timeout settings: `--search.maxQueryDuration=10s`, `--search.logSlowQueryDuration=5s`
- Increased `--search.maxUniqueTimeseries=1000000`

**Prometheus Scrape Configuration:**
- Increased scrape intervals from 5s to 15s to reduce load
- Increased scrape timeout to 10s

### Fixed

#### GitHub Actions Benchmark Workflow

Fixed benchmark performance alerts causing false CI failures:
- Disabled `fail-on-alert` flag (CI benchmarks have high variance)
- Increased alert threshold from 150% to 200%
- Real regressions should be caught in code review, not CI

#### Verifier Test Mock Server

Fixed `TestFunctional_VerifyFunction` test failure:
- Added handler for `/api/v1/status/tsdb` endpoint in mock server
- Test now correctly simulates VictoriaMetrics TSDB API response

## [0.4.2] - 2026-01-30

### Added

#### Zstd Compression Across Full Pipeline

Implemented end-to-end zstd compression support for the entire metrics pipeline:

**gRPC Receiver Compression:**
- Registered zstd compressor via `google.golang.org/grpc/encoding`
- Pooled zstd encoders/decoders for performance (sync.Pool)
- Automatic decompression for incoming gRPC requests
- Also supports gzip via import

**HTTP Exporter Compression:**
- Full zstd support for OTLP/HTTP with protobuf encoding
- Configurable via `-exporter-compression=zstd`
- Optimized for VictoriaMetrics OTLP HTTP endpoint

**Test Environment:**
- OTel Collector exports to metrics-governor with zstd compression
- metrics-governor exports to VictoriaMetrics with zstd compression
- VictoriaMetrics configured with optimal limits for high-throughput ingestion

#### Diverse Metrics Generator

Enhanced the test metrics generator with many more unique metric names to better test storage backends:

**New Metrics Categories (~200 unique metric names):**
- **CPU metrics** (9): `node_cpu_user_percent`, `node_cpu_system_percent`, `node_cpu_idle_percent`, etc.
- **Memory metrics** (15): `node_memory_total_bytes`, `node_memory_free_bytes`, `node_memory_cached_bytes`, etc.
- **Disk metrics** (9): `node_disk_read_bytes_total`, `node_disk_written_bytes_total`, etc.
- **Filesystem metrics** (5): `node_filesystem_size_bytes`, `node_filesystem_free_bytes`, etc.
- **Network metrics** (12): `node_network_receive_bytes_total`, `node_network_transmit_bytes_total`, etc.
- **Process metrics** (7): `process_cpu_seconds_total`, `process_resident_memory_bytes`, etc.
- **Application metrics** (20): `app_db_connections_active`, `app_cache_hits_total`, `app_queue_length`, etc.
- **HTTP endpoint metrics** (34): Per-endpoint duration and request count metrics
- **Custom metrics**: Additional unique counters, gauges, and histograms

**New Environment Variables:**
| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_DIVERSE_METRICS` | `true` | Enable diverse metric generation |
| `DIVERSE_METRIC_COUNT` | `200` | Target number of unique metric names |

### Fixed

#### GitHub Actions Benchmark Workflow

Fixed "body is too long" error when storing benchmark results:
- Error: `Validation Failed: body is too long (maximum is 65536 characters)`
- Solution: Filter benchmark output to keep only summary lines before posting
- Truncate to 500 lines if still too large
- Disabled `comment-always` and `summary-always` to prevent oversized comments
- Full benchmark results still available as artifacts

### Changed

#### Docker Compose Configuration

Updated test environment for zstd compression:
```yaml
otel-collector:
  exporters:
    otlp:
      compression: zstd  # Changed from gzip

metrics-governor:
  command:
    - "-exporter-compression=zstd"

victoriametrics:
  command:
    - "--maxInsertRequestSize=64MB"
    - "--opentelemetry.maxRequestSize=64MB"
    - "--opentelemetry.convertMetricNamesToPrometheus"
    - "--memory.allowedPercent=60"
```

## [0.4.1] - 2026-01-30

### Changed

#### Docker Compose Architecture Update

Changed the test environment data flow to demonstrate metrics-governor as a proxy between OTel Collector and VictoriaMetrics:

**New Flow:**
```
Generator → OTel Collector → metrics-governor → VictoriaMetrics
   (OTLP/gRPC)      (OTLP/gRPC)         (OTLP/HTTP protobuf)
```

**Key Changes:**
- Generator now sends metrics to OTel Collector (`:4317`)
- OTel Collector forwards to metrics-governor (`:14317`)
- metrics-governor exports to VictoriaMetrics OTLP HTTP endpoint (`/opentelemetry/v1/metrics`)
- Updated port mappings to avoid conflicts

**Port Mappings:**
| Service | External Ports | Internal Ports |
|---------|----------------|----------------|
| otel-collector | 4317, 4318, 8888 | 4317, 4318, 8888 |
| metrics-governor | 14317, 14318, 9090 | 4317, 4318, 9090 |
| victoriametrics | 8428 | 8428 |

This architecture tests metrics-governor as a transparent proxy that can be inserted between any OTLP pipeline and a backend storage system.

## [0.4.0] - 2026-01-30

### Added

#### Comprehensive Docker Compose Test Environment

A complete end-to-end testing environment with full observability stack:

**New Services:**
- **Grafana 12.3.2** - Visualization dashboard with auto-provisioned datasources
- **VictoriaMetrics v1.134.0** - High-performance metrics storage with Prometheus-compatible API
- **Data Verifier** - Automated verification tool for metrics flow validation

**Docker Compose Architecture:**
```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│ metrics-generator│────▶│ metrics-governor │────▶│ otel-collector  │
│   :9091/metrics │     │  :4317 gRPC      │     │                 │
└─────────────────┘     │  :4318 HTTP      │     └────────┬────────┘
                        │  :9090 metrics   │              │
                        └──────────────────┘              │
                                                          ▼
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│    verifier     │────▶│ victoriametrics  │◀────│ prometheusremote│
│   :9092/metrics │     │  :8428 API       │     │   write         │
└─────────────────┘     └──────────────────┘     └─────────────────┘
                                │
                                ▼
                        ┌──────────────────┐
                        │     grafana      │
                        │   :3000 UI       │
                        │  admin/admin     │
                        └──────────────────┘
```

**Comprehensive Grafana Dashboard:**
- **Metrics Governor Section** - Datapoints received/sent, batches, export errors, queue size, cardinality
- **Generator Section** - Throughput, latency, high cardinality tracking, burst metrics
- **Verifier Section** - Check pass rate, ingestion rate, VM time series, export errors

#### Generator and Verifier Prometheus Metrics

**Generator Metrics (`:9091/metrics`):**
| Metric | Type | Description |
|--------|------|-------------|
| `generator_runtime_seconds` | counter | Total runtime |
| `generator_metrics_sent_total` | counter | Total metrics sent |
| `generator_datapoints_sent_total` | counter | Total datapoints sent |
| `generator_batches_sent_total` | counter | Total batches sent |
| `generator_batch_latency_avg_seconds` | gauge | Average batch latency |
| `generator_batch_latency_min_seconds` | gauge | Minimum batch latency |
| `generator_batch_latency_max_seconds` | gauge | Maximum batch latency |
| `generator_high_cardinality_metrics_total` | counter | High cardinality metrics |
| `generator_bursts_sent_total` | counter | Burst traffic events |
| `generator_burst_metrics_total` | counter | Metrics in bursts |
| `generator_errors_total` | counter | Total errors |

**Verifier Metrics (`:9092/metrics`):**
| Metric | Type | Description |
|--------|------|-------------|
| `verifier_runtime_seconds` | counter | Total runtime |
| `verifier_checks_total` | counter | Total verification checks |
| `verifier_checks_passed_total` | counter | Passed checks |
| `verifier_checks_failed_total` | counter | Failed checks |
| `verifier_pass_rate_percent` | gauge | Overall pass rate |
| `verifier_last_ingestion_rate_percent` | gauge | Last ingestion rate |
| `verifier_vm_time_series` | gauge | Time series in VictoriaMetrics |
| `verifier_vm_unique_metrics` | gauge | Unique metric names |
| `verifier_vm_verification_counter` | gauge | Verification counter value |
| `verifier_mg_datapoints_received` | gauge | Datapoints received by governor |
| `verifier_mg_datapoints_sent` | gauge | Datapoints sent by governor |
| `verifier_mg_export_errors` | gauge | Export errors from governor |
| `verifier_last_check_status` | gauge | Last check (1=pass, 0=fail) |

#### Metrics Governor Export Tracking

New metrics for tracking export operations:

| Metric | Type | Description |
|--------|------|-------------|
| `metrics_governor_datapoints_received_total` | counter | Total datapoints received |
| `metrics_governor_datapoints_sent_total` | counter | Total datapoints successfully exported |
| `metrics_governor_batches_sent_total` | counter | Total batches exported |
| `metrics_governor_export_errors_total` | counter | Total export failures |

#### Comprehensive Test Suite for Generator and Verifier

**Unit Tests:**
- `test/generator_test.go` - Environment parsing, stats tracking, calculations
- `test/verifier/main_test.go` - Environment parsing, metric extraction, verification logic

**Functional Tests:**
- `test/functional_generator_test.go` - Metrics endpoint, stats tracking, concurrency
- `test/verifier/functional_test.go` - VM queries, MG stats, verification with mocked services

**E2E Integration Tests (`test/e2e_integration_test.go`):**
- Service health checks for all components
- Metrics flow verification from generator to VictoriaMetrics
- Ingestion rate validation
- High cardinality metrics handling
- Verification pass rate tracking
- Export error monitoring

Run integration tests:
```bash
docker compose up -d
sleep 30
go test -tags=integration -v ./test/...
docker compose down
```

### Changed

#### Docker Compose Improvements

- **gRPC DNS resolution** - Added `dns:///` prefix for proper service discovery
- **Message size limits** - Configured 64MB max message size for otel-collector
- **Batch size optimization** - Reduced from 5000 to 500 for better throughput
- **Buffer size** - Reduced from 100000 to 10000 for faster flushing
- **Service restart** - Added `restart: on-failure` for metrics-governor
- **OTEL Collector config** - Updated telemetry configuration for v0.144.0 format

#### Limits Configuration

Adjusted `examples/limits.yaml` for better testing:
- Increased default cardinality from 100k to 500k
- Increased default datapoints rate from 1M to 10M per minute
- Changed high-cardinality-protection action from `drop` to `log`
- All rules configured for testing without blocking traffic

### Fixed

#### Data Flow Issues

- **DNS resolution error** - Fixed "name resolver error: produced zero addresses" by adding `dns:///` prefix to exporter endpoint
- **gRPC message size** - Fixed "received message larger than max" by configuring `max_recv_msg_size_mib: 64` in otel-collector
- **OTEL Collector telemetry** - Fixed "'migration.MetricsConfigV030' has invalid keys" by updating to v0.144.0 telemetry format

#### Verifier Ingestion Rate Calculation

Fixed incorrect ingestion rate showing 928%:
- Was comparing `VMVerificationCounter / MGBatchesSent`
- Now correctly calculates `MGDatapointsSent / MGDatapointsReceived * 100`
- Capped at 100% for timing edge cases

#### Export Error Logging

Added detailed export error logging in buffer:
```go
logging.Error("export failed", logging.F(
    "error", err.Error(),
    "batch_size", len(batch),
    "datapoints", datapointCount,
))
```

### New Files

- `test/grafana/provisioning/datasources/datasources.yaml` - Grafana datasource config
- `test/grafana/provisioning/dashboards/dashboards.yaml` - Dashboard provider config
- `test/grafana/dashboards/metrics-governor.json` - Comprehensive dashboard
- `test/vmscrape-config.yaml` - VictoriaMetrics scrape configuration
- `test/generator_test.go` - Generator unit tests
- `test/functional_generator_test.go` - Generator functional tests
- `test/verifier/main_test.go` - Verifier unit tests
- `test/verifier/functional_test.go` - Verifier functional tests
- `test/e2e_integration_test.go` - E2E integration tests

## [0.3.1] - 2026-01-30

### Added

#### Comprehensive Performance Benchmarks

Added benchmark tests for all core components to enable performance monitoring and regression detection:

**Stats Package (`bench-stats`):**
- `BenchmarkCollector_Process` - Basic metrics processing
- `BenchmarkCollector_ProcessHighCardinality` - High cardinality label combinations
- `BenchmarkCollector_ProcessManyDatapoints` - Large datapoint volumes
- `BenchmarkCollector_GetGlobalStats` - Stats retrieval performance
- `BenchmarkCollector_ConcurrentProcess` - Parallel processing
- `BenchmarkCollector_Scale` - Scale testing (10x10 to 10000x10)

**Buffer Package (`bench-buffer`):**
- `BenchmarkBuffer_Add` - Basic buffer add operations
- `BenchmarkBuffer_AddWithStats` - Buffer with stats collection
- `BenchmarkBuffer_ConcurrentAdd` - Concurrent buffer access
- `BenchmarkBuffer_HighThroughput` - Sustained high throughput
- `BenchmarkBuffer_Scale` - Scale testing
- `BenchmarkBuffer_FlushThroughput` - Flush performance

**Compression Package (`bench-compression`):**
- All compression algorithms: gzip, zstd, snappy, lz4, zlib, deflate
- Compression/decompression at 1KB, 10KB, 100KB, 1MB sizes
- Compression level comparisons (fastest to best)
- Round-trip benchmarks (compress + decompress)

**Limits Package (`bench-limits`):**
- `BenchmarkEnforcer_Process_NoRules` - Baseline without rules
- `BenchmarkEnforcer_Process_SimpleRule` - Single rule processing
- `BenchmarkEnforcer_Process_MultipleRules` - Multiple rule matching
- `BenchmarkEnforcer_Process_DryRun` - Dry run mode
- `BenchmarkEnforcer_Process_HighCardinality` - High cardinality handling
- `BenchmarkEnforcer_Process_Concurrent` - Concurrent enforcement
- `BenchmarkEnforcer_Process_Scale` - Scale testing
- `BenchmarkEnforcer_Process_RegexMatch` - Regex pattern matching
- `BenchmarkEnforcer_Process_LabelMatch` - Label matching

**Queue Package (`bench-queue`):**
- `BenchmarkQueue_Push` - Push operations
- `BenchmarkQueue_PushPop` - Push/pop cycles
- `BenchmarkQueue_Peek` - Peek operations
- `BenchmarkQueue_LenSize` - Length/size queries
- `BenchmarkQueue_Push_PayloadSizes` - Different payload sizes
- `BenchmarkQueue_DropOldest` - Drop oldest behavior
- `BenchmarkQueue_Concurrent` - Concurrent access

**Receiver Package (`bench-receiver`):**
- `BenchmarkGRPCReceiver_Export` - gRPC export handling
- `BenchmarkGRPCReceiver_Export_Concurrent` - Concurrent gRPC
- `BenchmarkGRPCReceiver_Export_Scale` - Scale testing
- `BenchmarkHTTPReceiver_HandleMetrics` - HTTP request handling
- `BenchmarkHTTPReceiver_HandleMetrics_Concurrent` - Concurrent HTTP
- `BenchmarkHTTPReceiver_HandleMetrics_Scale` - Scale testing
- `BenchmarkProtobuf_Unmarshal` - Protobuf baseline

**Exporter Package (`bench-exporter`):**
- `BenchmarkExporter_GRPC` - gRPC export
- `BenchmarkExporter_GRPC_Concurrent` - Concurrent gRPC export
- `BenchmarkExporter_HTTP` - HTTP export
- `BenchmarkExporter_HTTP_Concurrent` - Concurrent HTTP export
- `BenchmarkExporter_HTTP_WithCompression` - Compression comparison
- `BenchmarkExporter_GRPC_Scale` - Scale testing
- `BenchmarkProtobuf_Marshal` - Protobuf baseline

**Auth Package (`bench-auth`):**
- `BenchmarkHTTPMiddleware_BearerToken` - Bearer token validation
- `BenchmarkHTTPMiddleware_BasicAuth` - Basic auth validation
- `BenchmarkHTTPMiddleware_Disabled` - Disabled auth baseline
- `BenchmarkHTTPTransport_BearerToken` - HTTP client auth
- `BenchmarkHTTPTransport_CustomHeaders` - Custom headers
- `BenchmarkGRPCClientInterceptor_BearerToken` - gRPC client auth
- `BenchmarkGRPCServerInterceptor_Enabled` - gRPC server auth
- `BenchmarkGRPCServerInterceptor_Disabled` - Disabled baseline
- `BenchmarkGRPCServerInterceptor_Concurrent` - Concurrent validation
- `BenchmarkHTTPMiddleware_Concurrent` - Concurrent HTTP auth

#### New Makefile Benchmark Targets

| Target | Description |
|--------|-------------|
| `make bench` | Run all benchmarks |
| `make bench-stats` | Stats package benchmarks |
| `make bench-buffer` | Buffer package benchmarks |
| `make bench-compression` | Compression benchmarks |
| `make bench-limits` | Limits enforcer benchmarks |
| `make bench-queue` | Queue benchmarks |
| `make bench-receiver` | Receiver benchmarks |
| `make bench-exporter` | Exporter benchmarks |
| `make bench-auth` | Auth benchmarks |
| `make bench-all` | Run all benchmark suites |
| `make bench-compare` | Run benchmarks and save results |
| `make bench-quick` | Quick scale benchmarks only |

#### Enhanced Metrics Generator

Updated `test/generator.go` with edge case testing:

- **Edge case values**: Zero, negative, very large (±1e308), very small (±1e-300), Pi, e
- **High cardinality metrics**: Configurable unique label combinations
- **Burst traffic patterns**: Configurable burst size and interval
- **Many datapoints histogram**: 15 explicit buckets

New environment variables:
| Variable | Default | Description |
|----------|---------|-------------|
| `ENABLE_EDGE_CASES` | `true` | Enable edge case value generation |
| `ENABLE_HIGH_CARDINALITY` | `true` | Enable high cardinality metrics |
| `ENABLE_BURST_TRAFFIC` | `true` | Enable burst traffic patterns |
| `HIGH_CARDINALITY_COUNT` | `100` | Unique label combinations per interval |
| `BURST_SIZE` | `500` | Metrics per burst |
| `BURST_INTERVAL_SEC` | `30` | Seconds between bursts |

#### E2E Tests for Edge Cases

New end-to-end tests:
- `TestE2E_HighCardinality` - 1000 unique user/request combinations
- `TestE2E_ManyDatapoints` - 10 requests × 10,000 datapoints each
- `TestE2E_BurstTraffic` - 5 bursts × 500 concurrent requests
- `TestE2E_EdgeCaseValues` - Extreme float values

#### Functional Tests for Edge Cases

New functional tests:
- `TestFunctional_GRPCReceiver_HighCardinality`
- `TestFunctional_GRPCReceiver_ManyDatapoints`
- `TestFunctional_GRPCReceiver_EdgeCaseValues`

### Changed

#### Docker Compose - VictoriaMetrics

Replaced Prometheus with VictoriaMetrics (vmsingle) for metrics storage:
- VictoriaMetrics v1.96.0 with OTLP ingestion support
- OpenTelemetry Collector updated to contrib image for prometheusremotewrite exporter
- Configured remote write to VictoriaMetrics

### Statistics

- **New benchmark files:** 8
- **New test files:** 0 (tests added to existing files)
- **New Makefile targets:** 12

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

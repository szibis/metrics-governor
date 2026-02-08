# Deprecation Log

## Table of Contents

- [Active Deprecations](#active-deprecations)
  - [v0.34.0 — Processing Rules](#v0340--processing-rules)
    - [Sampling to Processing Migration](#sampling-to-processing-migration)
  - [v0.30.0 — Configuration Simplification](#v0300--configuration-simplification)
    - [Parallelism Consolidation](#parallelism-consolidation)
    - [Memory Budget Consolidation](#memory-budget-consolidation)
    - [Timeout Cascade Consolidation](#timeout-cascade-consolidation)
    - [Resilience Level Consolidation](#resilience-level-consolidation)
- [Removed (Historical)](#removed-historical)
- [Deprecation Lifecycle](#deprecation-lifecycle)
- [Policy](#policy)
- [See Also](#see-also)

---

This document tracks all parameter deprecations, their replacement parameters, lifecycle stages, and removal targets.

Use `--show-deprecations` to see the current status of all deprecations at runtime.
Use `--strict-deprecations` in CI to fail on removed parameters.

## Active Deprecations

### v0.34.0 — Processing Rules

The sampling system has been replaced by the unified Processing Rules engine.
All deprecated parameters continue to work identically — they are not removed.

#### Sampling to Processing Migration

| Parameter | Replacement | Stage | Removal Target |
|-----------|-------------|-------|---------------|
| `--sampling-config` | `--processing-config` | Announced | v1.0 |
| `sampling.enabled` (Helm) | `processing.enabled` | Announced | v1.0 |
| `metrics_governor_sampling_*` | `metrics_governor_processing_*` | Announced | v1.0 |
| `metrics_governor_downsampling_*` | `metrics_governor_processing_*` | Announced | v1.0 |

**Migration:** Replace `--sampling-config=sampling.yaml` with `--processing-config=processing.yaml`. The old YAML format (with `default_rate`, `strategy`, individual `rules`) is auto-detected and converted internally. New format uses unified `rules` with explicit `action` field. See [docs/processing-rules.md](docs/processing-rules.md) for the full reference.

In Helm, replace:
```yaml
sampling:
  enabled: true
  config: |
    default_rate: 1.0
    rules: [...]
```
with:
```yaml
processing:
  enabled: true
  config: |
    staleness_interval: 10m
    rules:
      - name: my-rule
        input: ".*"
        action: sample
        rate: 1.0
```

### v0.30.0 — Configuration Simplification

The following individual parameters are being consolidated into higher-level controls.
All deprecated parameters continue to work identically — they are not removed.

#### Parallelism Consolidation

| Parameter | Replacement | Stage | Removal Target |
|-----------|-------------|-------|---------------|
| `--queue-workers` | `--parallelism` | Announced | v1.0 |
| `--export-concurrency` | `--parallelism` | Announced | v1.0 |

**Migration:** Replace individual worker counts with `--parallelism=N`, which derives:
- `queue-workers = N * 2`
- `export-concurrency = N * 4`
- `preparer-count = N`
- `sender-count = N * 2`
- `max-concurrent-sends = max(2, N/2)`
- `global-send-limit = N * 8`

#### Memory Budget Consolidation

| Parameter | Replacement | Stage | Removal Target |
|-----------|-------------|-------|---------------|
| `--buffer-memory-percent` | `--memory-budget-percent` | Announced | v1.0 |
| `--queue-memory-percent` | `--memory-budget-percent` | Announced | v1.0 |

**Migration:** Replace individual memory percentages with `--memory-budget-percent=0.20`, which splits the budget 50/50 between buffer and queue.

#### Timeout Cascade Consolidation

| Parameter | Replacement | Stage | Removal Target |
|-----------|-------------|-------|---------------|
| `--queue-direct-export-timeout` | `--export-timeout` | Announced | v1.0 |
| `--queue-retry-timeout` | `--export-timeout` | Announced | v1.0 |
| `--queue-drain-timeout` | `--export-timeout` | Announced | v1.0 |
| `--queue-drain-entry-timeout` | `--export-timeout` | Announced | v1.0 |
| `--queue-close-timeout` | `--export-timeout` | Announced | v1.0 |

**Migration:** Replace individual timeouts with `--export-timeout=30s`, which derives:
- `exporter-timeout = 30s` (base)
- `queue-direct-export-timeout = 5s` (base/6)
- `queue-retry-timeout = 10s` (base/3)
- `queue-drain-entry-timeout = 5s` (base/6)
- `queue-drain-timeout = 30s` (base)
- `queue-close-timeout = 60s` (base*2)
- `flush-timeout = 30s` (base)

#### Resilience Level Consolidation

| Parameter | Replacement | Stage | Removal Target |
|-----------|-------------|-------|---------------|
| `--queue-backoff-multiplier` | `--resilience-level` | Announced | v1.0 |
| `--queue-circuit-breaker-enabled` | `--resilience-level` | Announced | v1.0 |
| `--queue-circuit-breaker-threshold` | `--resilience-level` | Announced | v1.0 |
| `--queue-batch-drain-size` | `--resilience-level` | Announced | v1.0 |
| `--queue-burst-drain-size` | `--resilience-level` | Announced | v1.0 |

**Migration:** Replace individual resilience settings with `--resilience-level=medium`:
- `low`: multiplier 1.5, CB off, batch drain 5, burst drain 50
- `medium`: multiplier 2.0, CB on (5 failures, 30s reset), batch drain 10, burst drain 100
- `high`: multiplier 3.0, CB on (3 failures, 15s reset), batch drain 25, burst drain 250

## Removed (Historical)

(none yet)

## Deprecation Lifecycle

Every deprecation follows a 4-stage lifecycle:

1. **ANNOUNCED** — Parameter works normally. INFO log at startup noting the upcoming change.
2. **DEPRECATED** — Parameter still works. WARN log at every startup.
3. **WARNING** — Parameter still works. WARN log + prominent banner. Within 2 minor versions of removal.
4. **REMOVED** — Parameter is ignored. ERROR log, or startup failure with `--strict-deprecations`.

## Policy

1. **Announce** deprecation at least 2 minor versions before deprecating
2. **Deprecate** with WARN logs — parameter still works identically
3. **Warning** banner when within 2 minor versions of removal
4. **Remove** only in major versions (v1.0, v2.0) or after 6+ months
5. All deprecations tracked in this file and in `--show-deprecations`
6. CI can enforce with `--strict-deprecations`

## See Also

- [Configuration Profiles](docs/profiles.md) — profile presets that replace manual tuning
- `--show-deprecations` — runtime deprecation status
- `--show-effective-config` — see the final merged configuration

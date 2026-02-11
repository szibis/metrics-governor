# Deprecation Log

## Table of Contents

- [Active Deprecations](#active-deprecations)
- [Removed (Historical)](#removed-historical)
  - [v1.0.0 — All v0.x Deprecations Removed](#v100--all-v0x-deprecations-removed)
- [Deprecation Lifecycle](#deprecation-lifecycle)
- [Policy](#policy)
- [See Also](#see-also)

---

This document tracks all parameter deprecations, their replacement parameters, lifecycle stages, and removal targets.

Use `--show-deprecations` to see the current status of all deprecations at runtime.
Use `--strict-deprecations` in CI to fail on removed parameters.

## Active Deprecations

(none)

## Removed (Historical)

### v1.0.0 — All v0.x Deprecations Removed

All deprecated parameters from v0.30.0 and v0.34.0 were removed in v1.0.0.

#### Sampling to Processing Migration (v0.34.0)

| Parameter | Replacement | Removed In |
|-----------|-------------|------------|
| `--sampling-config` | `--processing-config` | v1.0.0 |
| `sampling.enabled` (Helm) | `processing.enabled` | v1.0.0 |
| `metrics_governor_sampling_*` | `metrics_governor_processing_*` | v1.0.0 |
| `metrics_governor_downsampling_*` | `metrics_governor_processing_*` | v1.0.0 |

#### Parallelism Consolidation (v0.30.0)

| Parameter | Replacement | Removed In |
|-----------|-------------|------------|
| `--queue-workers` | `--parallelism` | v1.0.0 |
| `--export-concurrency` | `--parallelism` | v1.0.0 |

#### Memory Budget Consolidation (v0.30.0)

| Parameter | Replacement | Removed In |
|-----------|-------------|------------|
| `--buffer-memory-percent` | `--memory-budget-percent` | v1.0.0 |
| `--queue-memory-percent` | `--memory-budget-percent` | v1.0.0 |

#### Timeout Cascade Consolidation (v0.30.0)

| Parameter | Replacement | Removed In |
|-----------|-------------|------------|
| `--queue-direct-export-timeout` | `--export-timeout` | v1.0.0 |
| `--queue-retry-timeout` | `--export-timeout` | v1.0.0 |
| `--queue-drain-timeout` | `--export-timeout` | v1.0.0 |
| `--queue-drain-entry-timeout` | `--export-timeout` | v1.0.0 |
| `--queue-close-timeout` | `--export-timeout` | v1.0.0 |

#### Resilience Level Consolidation (v0.30.0)

| Parameter | Replacement | Removed In |
|-----------|-------------|------------|
| `--queue-backoff-multiplier` | `--resilience-level` | v1.0.0 |
| `--queue-circuit-breaker-enabled` | `--resilience-level` | v1.0.0 |
| `--queue-circuit-breaker-threshold` | `--resilience-level` | v1.0.0 |
| `--queue-batch-drain-size` | `--resilience-level` | v1.0.0 |
| `--queue-burst-drain-size` | `--resilience-level` | v1.0.0 |

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

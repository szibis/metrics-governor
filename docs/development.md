# Development

## Building

```bash
# Build for current platform
make build

# Build all platforms (darwin-arm64, linux-arm64, linux-amd64)
make all

# Build Docker image
make docker
```

Binaries are output to `bin/` directory.

## Running Tests

```bash
# Run all tests
make test

# Run tests with verbose output
make test-verbose

# Run tests with coverage report
make test-coverage
```

Coverage report is generated at `bin/coverage.html`.

### Test Commands

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

# Run specific test
go test -v -run TestAdaptiveLimiting ./internal/limits/...
```

## Project Structure

```
metrics-governor/
├── cmd/metrics-governor/    # Main application entry point
├── internal/
│   ├── auth/                # Authentication (bearer token, basic auth)
│   ├── buffer/              # Metrics buffering and batching
│   ├── compression/         # Compression support (gzip, zstd, etc.)
│   ├── config/              # Configuration management
│   ├── exporter/            # OTLP gRPC and HTTP exporters
│   ├── limits/              # Limits enforcement (adaptive, drop, log)
│   ├── logging/             # JSON structured logging
│   ├── queue/               # WAL-based persistent queue for retries
│   ├── receiver/            # gRPC and HTTP receivers
│   ├── sharding/            # Consistent hashing and DNS discovery
│   ├── stats/               # Statistics collection
│   └── tls/                 # TLS configuration utilities
├── functional/              # Functional tests
├── e2e/                     # End-to-end tests
├── helm/metrics-governor/   # Helm chart for Kubernetes
├── examples/                # Example configuration files
├── docs/                    # Documentation
├── test/                    # Integration test environment
├── bin/                     # Build output directory
├── Dockerfile
├── docker-compose.yaml
└── Makefile
```

## Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting
- Run `go vet` before committing
- Keep functions focused and small
- Write tests for new functionality

## Adding New Features

1. Create feature branch from `main`
2. Implement feature with tests
3. Ensure all tests pass: `make test`
4. Update documentation if needed
5. Create pull request

## Debugging

### Enable Verbose Logging

All logs are JSON formatted. Use `jq` for parsing:

```bash
metrics-governor 2>&1 | jq .
```

### Profile CPU/Memory

```bash
go test -cpuprofile=cpu.prof -memprofile=mem.prof -bench=. ./...
go tool pprof cpu.prof
```

### Debug with Delve

```bash
dlv debug ./cmd/metrics-governor -- -config config.yaml
```

## Release Process

Releases are automated via GitHub Actions. To create a release:

```bash
/release <version> <description>
# Example: /release 0.5.5 Add limiting metadata labels
```

This will:
1. Update test coverage in README
2. Update CHANGELOG
3. Create git tag
4. Push to GitHub
5. Trigger CI/CD to build binaries and Docker images

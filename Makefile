BINARY_NAME=metrics-governor
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-s -w -X github.com/slawomirskowron/metrics-governor/internal/config.version=$(VERSION)"

BUILD_DIR=bin

.PHONY: all build clean darwin-arm64 linux-arm64 linux-amd64 docker test test-coverage test-verbose test-unit test-functional test-e2e test-all bench bench-stats bench-buffer bench-compression bench-limits bench-queue bench-receiver bench-exporter bench-auth bench-all lint lint-dockerfile lint-yaml lint-helm lint-all validate-config-helper generate-config-meta ship ship-dry-run tag compose-up compose-down compose-light compose-stable compose-perf compose-queue compose-persistence compose-sharding compose-logs

all: darwin-arm64 linux-arm64 linux-amd64

build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/metrics-governor

darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/metrics-governor

linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/metrics-governor

linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/metrics-governor

docker:
	docker build -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .

docker-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 -t $(BINARY_NAME):$(VERSION) -t $(BINARY_NAME):latest .

test:
	go test ./...

test-verbose:
	go test -v ./...

test-unit:
	@echo "Running unit tests..."
	go test -v ./internal/...

test-functional:
	@echo "Running functional tests..."
	go test -v ./functional/...

test-e2e:
	@echo "Running e2e tests..."
	go test -v ./e2e/...

test-all: test-unit test-functional test-e2e
	@echo "All tests passed!"

bench:
	@echo "Running all benchmarks..."
	go test -bench=. -benchmem ./internal/...

bench-stats:
	@echo "Running stats benchmarks..."
	go test -bench=. -benchmem ./internal/stats/...

bench-buffer:
	@echo "Running buffer benchmarks..."
	go test -bench=. -benchmem ./internal/buffer/...

bench-compression:
	@echo "Running compression benchmarks..."
	go test -bench=. -benchmem ./internal/compression/...

bench-limits:
	@echo "Running limits benchmarks..."
	go test -bench=. -benchmem ./internal/limits/...

bench-queue:
	@echo "Running queue benchmarks..."
	go test -bench=. -benchmem ./internal/queue/...

bench-receiver:
	@echo "Running receiver benchmarks..."
	go test -bench=. -benchmem ./internal/receiver/...

bench-exporter:
	@echo "Running exporter benchmarks..."
	go test -bench=. -benchmem ./internal/exporter/...

bench-auth:
	@echo "Running auth benchmarks..."
	go test -bench=. -benchmem ./internal/auth/...

bench-all: bench-stats bench-buffer bench-compression bench-limits bench-queue bench-receiver bench-exporter bench-auth
	@echo "All benchmarks complete!"

bench-compare:
	@echo "Running benchmarks and saving baseline..."
	@mkdir -p $(BUILD_DIR)
	go test -bench=. -benchmem ./internal/... | tee $(BUILD_DIR)/bench-$(VERSION).txt
	@echo "Benchmark results saved to $(BUILD_DIR)/bench-$(VERSION).txt"

bench-quick:
	@echo "Running quick benchmarks (scale tests only)..."
	go test -bench=Scale -benchmem ./internal/...

test-coverage:
	@mkdir -p $(BUILD_DIR)
	go test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	go tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "Coverage report: $(BUILD_DIR)/coverage.html"
	@go tool cover -func=$(BUILD_DIR)/coverage.out | tail -1

clean:
	rm -rf $(BUILD_DIR)

lint:
	@echo "Running go vet..."
	go vet ./...

lint-dockerfile:
	@echo "Linting Dockerfiles..."
	@command -v hadolint >/dev/null 2>&1 || { echo "hadolint not installed. Install with: brew install hadolint"; exit 1; }
	hadolint Dockerfile
	hadolint test/Dockerfile.generator

lint-yaml:
	@echo "Linting YAML files..."
	@command -v yamllint >/dev/null 2>&1 || { echo "yamllint not installed. Install with: pip install yamllint"; exit 1; }
	yamllint -c .yamllint.yml examples/
	yamllint -c .yamllint.yml helm/metrics-governor/values.yaml
	yamllint -c .yamllint.yml helm/metrics-governor/Chart.yaml

lint-helm:
	@echo "Linting Helm chart..."
	@command -v helm >/dev/null 2>&1 || { echo "helm not installed. Install from: https://helm.sh/docs/intro/install/"; exit 1; }
	helm lint helm/metrics-governor

lint-all: lint lint-dockerfile lint-yaml lint-helm validate-config-helper
	@echo "All lints passed!"

generate-config-meta:
	@echo "Generating config-meta from Go defaults + storage-specs.json..."
	go run ./tools/config-helper/cmd/generate

validate-config-helper:
	@echo "Validating config helper against Go defaults..."
	go test -v ./tools/config-helper/...

# Ship targets - creates PR-based releases
# Usage: make ship VERSION=0.5.2 MESSAGE="Add new feature"
ship:
ifndef VERSION
	$(error VERSION is required. Usage: make ship VERSION=0.5.2 MESSAGE="Description")
endif
ifndef MESSAGE
	$(error MESSAGE is required. Usage: make ship VERSION=0.5.2 MESSAGE="Description")
endif
	@./scripts/release.sh $(VERSION) -m "$(MESSAGE)"

ship-dry-run:
ifndef VERSION
	$(error VERSION is required. Usage: make ship-dry-run VERSION=0.5.2 MESSAGE="Description")
endif
ifndef MESSAGE
	$(error MESSAGE is required. Usage: make ship-dry-run VERSION=0.5.2 MESSAGE="Description")
endif
	@./scripts/release.sh $(VERSION) -m "$(MESSAGE)" --dry-run

# Legacy tag target (deprecated, use ship instead)
tag:
ifndef VERSION
	$(error VERSION is required. Usage: make tag VERSION=v0.2.0)
endif
	@echo "Creating tag $(VERSION)"
	git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "Tag created. Push with: git push origin $(VERSION)"

build-release: test lint all
	@echo "Build complete. Use: make ship VERSION=X.Y.Z MESSAGE='Description'"

# Docker Compose test environment targets
compose-up:
	@echo "Starting test environment (moderate load)..."
	docker compose up -d --build
	@echo "Waiting for services to start..."
	@sleep 10
	@echo "Services started. Grafana: http://localhost:3000 (admin/admin)"

compose-down:
	@echo "Stopping test environment..."
	docker compose down -v
	@echo "Test environment stopped."

compose-light:
	@echo "Starting LIGHT test environment (low volume, quick validation)..."
	docker compose -f docker-compose.yaml -f compose_overrides/light.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 10
	@echo "Light test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-stable:
	@echo "Starting STABLE test environment (predictable metrics for verification)..."
	docker compose -f docker-compose.yaml -f compose_overrides/stable.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 10
	@echo "Stable test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-perf:
	@echo "Starting PERFORMANCE test environment (high volume stress testing)..."
	docker compose -f docker-compose.yaml -f compose_overrides/perf.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 15
	@echo "Performance test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-queue:
	@echo "Starting QUEUE test environment (in-memory queue testing)..."
	docker compose -f docker-compose.yaml -f compose_overrides/queue.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 10
	@echo "Queue test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-persistence:
	@echo "Starting PERSISTENCE test environment (bloom filter persistence testing)..."
	docker compose -f docker-compose.yaml -f compose_overrides/persistence.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 10
	@echo "Persistence test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-sharding:
	@echo "Starting SHARDING test environment (multi-endpoint consistent hashing)..."
	docker compose -f docker-compose.yaml -f compose_overrides/sharding.yaml up -d --build
	@echo "Waiting for services to start..."
	@sleep 15
	@echo "Sharding test environment started. Grafana: http://localhost:3000 (admin/admin)"

compose-logs:
	docker compose logs -f

compose-status:
	@echo "=== Container Status ==="
	docker compose ps
	@echo ""
	@echo "=== VictoriaMetrics Stats ==="
	@curl -s http://localhost:8428/api/v1/status/tsdb 2>/dev/null | jq -r '.data | "Total time series: \(.totalSeries)"' || echo "VictoriaMetrics not responding"
	@echo ""
	@echo "=== Metrics Governor Stats ==="
	@curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_(datapoints_received|datapoints_sent|export_errors)_total" || echo "Metrics Governor not responding"

help:
	@echo "Available targets:"
	@echo "  all              - Build all platforms (darwin-arm64, linux-arm64, linux-amd64)"
	@echo "  build            - Build for current platform (output: bin/metrics-governor)"
	@echo "  darwin-arm64     - Build for macOS ARM64"
	@echo "  linux-arm64      - Build for Linux ARM64"
	@echo "  linux-amd64      - Build for Linux AMD64"
	@echo "  docker           - Build Docker image"
	@echo "  docker-multiarch - Build multi-arch Docker image (requires buildx)"
	@echo "  test             - Run all tests"
	@echo "  test-verbose     - Run tests with verbose output"
	@echo "  test-unit        - Run unit tests only"
	@echo "  test-functional  - Run functional tests only"
	@echo "  test-e2e         - Run e2e tests only"
	@echo "  test-all         - Run unit, functional, and e2e tests"
	@echo "  test-coverage    - Run tests with coverage report"
	@echo "  bench            - Run all benchmarks"
	@echo "  bench-stats      - Run stats package benchmarks"
	@echo "  bench-buffer     - Run buffer package benchmarks"
	@echo "  bench-compression - Run compression benchmarks"
	@echo "  bench-limits     - Run limits enforcer benchmarks"
	@echo "  bench-queue      - Run queue benchmarks"
	@echo "  bench-receiver   - Run receiver benchmarks"
	@echo "  bench-exporter   - Run exporter benchmarks"
	@echo "  bench-auth       - Run auth benchmarks"
	@echo "  bench-all        - Run all benchmark suites"
	@echo "  bench-compare    - Run benchmarks and save results"
	@echo "  bench-quick      - Run quick scale benchmarks only"
	@echo "  lint             - Run go vet"
	@echo "  lint-dockerfile  - Lint Dockerfiles with hadolint"
	@echo "  lint-yaml        - Lint YAML files with yamllint"
	@echo "  lint-helm        - Lint Helm chart"
	@echo "  lint-all         - Run all linters"
	@echo "  generate-config-meta - Regenerate config-meta JSON in config helper HTML"
	@echo "  validate-config-helper - Validate config helper HTML against Go defaults"
	@echo "  build-release    - Run tests, lint, and build all platforms"
	@echo "  ship VERSION=X.Y.Z MESSAGE='...' - Create release PR with auto-merge"
	@echo "  ship-dry-run VERSION=X.Y.Z MESSAGE='...' - Preview release changes"
	@echo "  tag VERSION=vX.Y.Z - Create a git tag (deprecated)"
	@echo "  clean            - Remove build artifacts"
	@echo ""
	@echo "Docker Compose Test Environment:"
	@echo "  compose-up       - Start test environment (moderate load)"
	@echo "  compose-down     - Stop test environment and remove volumes"
	@echo "  compose-light    - Start LIGHT test (low volume, quick validation)"
	@echo "  compose-stable   - Start STABLE test (predictable metrics)"
	@echo "  compose-perf     - Start PERFORMANCE test (high volume stress)"
	@echo "  compose-queue    - Start QUEUE test (in-memory queue testing)"
	@echo "  compose-persistence - Start PERSISTENCE test (bloom filter)"
	@echo "  compose-sharding - Start SHARDING test (multi-endpoint)"
	@echo "  compose-logs     - Follow logs from all containers"
	@echo "  compose-status   - Show container and metrics status"
	@echo ""
	@echo "  help             - Show this help"

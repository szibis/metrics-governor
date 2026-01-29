BINARY_NAME=metrics-governor
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-s -w -X github.com/slawomirskowron/metrics-governor/internal/config.version=$(VERSION)"

BUILD_DIR=bin

.PHONY: all build clean darwin-arm64 linux-arm64 linux-amd64 docker test test-coverage test-verbose test-unit test-functional test-e2e test-all lint lint-dockerfile lint-yaml lint-helm lint-all release tag

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

lint-all: lint lint-dockerfile lint-yaml lint-helm
	@echo "All lints passed!"

# Release targets
# Usage: make tag VERSION=v0.2.0
tag:
ifndef VERSION
	$(error VERSION is required. Usage: make tag VERSION=v0.2.0)
endif
	@echo "Creating tag $(VERSION)"
	git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "Tag created. Push with: git push origin $(VERSION)"

release: test lint all
	@echo "Build complete. Create release with: make tag VERSION=vX.Y.Z"

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
	@echo "  lint             - Run go vet"
	@echo "  lint-dockerfile  - Lint Dockerfiles with hadolint"
	@echo "  lint-yaml        - Lint YAML files with yamllint"
	@echo "  lint-helm        - Lint Helm chart"
	@echo "  lint-all         - Run all linters"
	@echo "  release          - Run tests, lint, and build all platforms"
	@echo "  tag VERSION=vX.Y.Z - Create a git tag for release"
	@echo "  clean            - Remove build artifacts"
	@echo "  help             - Show this help"

BINARY_NAME=metrics-governor
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-s -w -X github.com/slawomirskowron/metrics-governor/internal/config.version=$(VERSION)"

BUILD_DIR=bin

.PHONY: all build clean darwin-arm64 linux-arm64 linux-amd64 docker test test-coverage test-verbose

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

test-coverage:
	@mkdir -p $(BUILD_DIR)
	go test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	go tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "Coverage report: $(BUILD_DIR)/coverage.html"
	@go tool cover -func=$(BUILD_DIR)/coverage.out | tail -1

clean:
	rm -rf $(BUILD_DIR)

help:
	@echo "Available targets:"
	@echo "  all             - Build all platforms (darwin-arm64, linux-arm64, linux-amd64)"
	@echo "  build           - Build for current platform (output: bin/metrics-governor)"
	@echo "  darwin-arm64    - Build for macOS ARM64"
	@echo "  linux-arm64     - Build for Linux ARM64"
	@echo "  linux-amd64     - Build for Linux AMD64"
	@echo "  docker          - Build Docker image"
	@echo "  docker-multiarch - Build multi-arch Docker image (requires buildx)"
	@echo "  test            - Run tests"
	@echo "  test-verbose    - Run tests with verbose output"
	@echo "  test-coverage   - Run tests with coverage report"
	@echo "  clean           - Remove build artifacts"
	@echo "  help            - Show this help"

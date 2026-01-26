BINARY_NAME=metrics-governor
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-s -w -X github.com/slawomirskowron/metrics-governor/internal/config.version=$(VERSION)"

BUILD_DIR=bin

.PHONY: all build clean darwin-arm64 linux-arm64 linux-amd64 docker

all: darwin-arm64 linux-arm64 linux-amd64

build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/metrics-governor

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

clean:
	rm -rf $(BUILD_DIR)
	rm -f $(BINARY_NAME)

help:
	@echo "Available targets:"
	@echo "  all           - Build all platforms (darwin-arm64, linux-arm64, linux-amd64)"
	@echo "  build         - Build for current platform"
	@echo "  darwin-arm64  - Build for macOS ARM64"
	@echo "  linux-arm64   - Build for Linux ARM64"
	@echo "  linux-amd64   - Build for Linux AMD64"
	@echo "  docker        - Build Docker image"
	@echo "  docker-multiarch - Build multi-arch Docker image (requires buildx)"
	@echo "  clean         - Remove build artifacts"
	@echo "  help          - Show this help"

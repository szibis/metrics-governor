# Build stage
FROM golang:1.25.7-alpine AS builder

WORKDIR /app

# Install git for version info
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build (uses PGO profile if default.pgo exists in the build context)
ARG VERSION=dev
ARG GOEXPERIMENT=greenteagc
RUN PGO_FLAG=""; \
    if [ -f default.pgo ]; then PGO_FLAG="-pgo=default.pgo"; echo "Building with PGO profile"; fi; \
    GOEXPERIMENT=${GOEXPERIMENT} CGO_ENABLED=0 go build $PGO_FLAG \
    -ldflags "-s -w -X github.com/szibis/metrics-governor/internal/config.version=${VERSION}" \
    -o metrics-governor ./cmd/metrics-governor

# Runtime stage
FROM alpine:3.23

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/metrics-governor /app/metrics-governor

# OTLP gRPC and HTTP ports
EXPOSE 4317 4318

ENTRYPOINT ["/app/metrics-governor"]

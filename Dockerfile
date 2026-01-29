# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git for version info
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/slawomirskowron/metrics-governor/internal/config.version=${VERSION}" \
    -o metrics-governor ./cmd/metrics-governor

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/metrics-governor /app/metrics-governor

# OTLP gRPC and HTTP ports
EXPOSE 4317 4318

ENTRYPOINT ["/app/metrics-governor"]

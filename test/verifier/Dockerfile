FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY main.go .

# Initialize module and build
RUN go mod init verifier && \
    go build -o verifier .

FROM alpine:3.19

COPY --from=builder /app/verifier /usr/local/bin/verifier

ENTRYPOINT ["verifier"]

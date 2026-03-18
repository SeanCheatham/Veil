# Veil Antithesis Dockerfile
# Multi-stage build for all Veil services

# =============================================================================
# Base stage with Go and common dependencies
# =============================================================================
FROM golang:1.22-alpine AS base

RUN apk add --no-cache git ca-certificates bash

WORKDIR /app

# Copy go.mod and go.sum first for better layer caching
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy the rest of the source
COPY . .

# =============================================================================
# Builder stage - compiles all binaries
# =============================================================================
FROM base AS builder

# Build all services (these will be implemented in later phases)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/veil-pool ./cmd/pool || echo "package main" > /tmp/stub.go && echo 'func main() { select {} }' >> /tmp/stub.go && go build -o /bin/veil-pool /tmp/stub.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/veil-validator ./cmd/validator || go build -o /bin/veil-validator /tmp/stub.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/veil-relay ./cmd/relay || go build -o /bin/veil-relay /tmp/stub.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/veil-workload ./cmd/workload || go build -o /bin/veil-workload /tmp/stub.go

# =============================================================================
# Runtime base - minimal image for running Go binaries
# =============================================================================
FROM alpine:3.19 AS runtime-base

RUN apk add --no-cache ca-certificates bash

# Create antithesis output directory
RUN mkdir -p /tmp/antithesis

# Copy setup-complete script
COPY antithesis/setup-complete.sh /usr/local/bin/setup-complete.sh
RUN chmod +x /usr/local/bin/setup-complete.sh

# =============================================================================
# Message Pool service
# =============================================================================
FROM runtime-base AS pool

COPY --from=builder /bin/veil-pool /usr/local/bin/veil-pool

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/veil-pool"]

# =============================================================================
# Validator service
# =============================================================================
FROM runtime-base AS validator

COPY --from=builder /bin/veil-validator /usr/local/bin/veil-validator

EXPOSE 9000

ENTRYPOINT ["/usr/local/bin/veil-validator"]

# =============================================================================
# Relay service
# =============================================================================
FROM runtime-base AS relay

COPY --from=builder /bin/veil-relay /usr/local/bin/veil-relay

EXPOSE 7000

ENTRYPOINT ["/usr/local/bin/veil-relay"]

# =============================================================================
# Workload service - contains test composer commands
# =============================================================================
FROM runtime-base AS workload

COPY --from=builder /bin/veil-workload /usr/local/bin/veil-workload

# Copy test composer commands
COPY antithesis/test-composer/veil /opt/antithesis/test/v1/veil/
RUN chmod +x /opt/antithesis/test/v1/veil/* || true

# The workload container emits setup_complete and then waits for test commands
CMD ["/bin/sh", "-c", "/usr/local/bin/setup-complete.sh && tail -f /dev/null"]

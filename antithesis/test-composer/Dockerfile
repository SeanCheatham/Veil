# syntax=docker/dockerfile:1

# Build stage - compiles all workload binaries
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build all workload binaries - first_* commands (quiescent)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_setup ./antithesis/workloads/first_setup
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_pool_health ./antithesis/workloads/pool_health
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_validator_health ./antithesis/workloads/validator_health
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_relay_health ./antithesis/workloads/relay_health
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_onion_integrity ./antithesis/workloads/onion_integrity
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_consensus_test ./antithesis/workloads/consensus_test
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_cover_traffic_test ./antithesis/workloads/cover_traffic_test
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_epoch_rotation_test ./antithesis/workloads/epoch_rotation_test
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/first_recipient_decryption ./antithesis/workloads/recipient_decryption

# Build parallel_driver commands (run during faults)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/parallel_driver_message_sender ./antithesis/workloads/parallel_driver
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/parallel_driver_longevity ./antithesis/workloads/longevity

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Create the Test Composer directory structure
RUN mkdir -p /opt/antithesis/test/v1/veil

# Copy all workload binaries to Test Composer path
# Using "first_" prefix for setup/quiescent commands
COPY --from=builder /bin/first_setup /opt/antithesis/test/v1/veil/first_setup
COPY --from=builder /bin/first_pool_health /opt/antithesis/test/v1/veil/first_pool_health
COPY --from=builder /bin/first_validator_health /opt/antithesis/test/v1/veil/first_validator_health
COPY --from=builder /bin/first_relay_health /opt/antithesis/test/v1/veil/first_relay_health
COPY --from=builder /bin/first_onion_integrity /opt/antithesis/test/v1/veil/first_onion_integrity
COPY --from=builder /bin/first_consensus_test /opt/antithesis/test/v1/veil/first_consensus_test
COPY --from=builder /bin/first_cover_traffic_test /opt/antithesis/test/v1/veil/first_cover_traffic_test
COPY --from=builder /bin/first_epoch_rotation_test /opt/antithesis/test/v1/veil/first_epoch_rotation_test
COPY --from=builder /bin/first_recipient_decryption /opt/antithesis/test/v1/veil/first_recipient_decryption

# Copy parallel_driver commands (run during faults)
COPY --from=builder /bin/parallel_driver_message_sender /opt/antithesis/test/v1/veil/parallel_driver_message_sender
COPY --from=builder /bin/parallel_driver_longevity /opt/antithesis/test/v1/veil/parallel_driver_longevity

# Ensure all binaries are executable
RUN chmod +x /opt/antithesis/test/v1/veil/*

# Keep container running - Test Composer will execute the commands
CMD ["sleep", "infinity"]

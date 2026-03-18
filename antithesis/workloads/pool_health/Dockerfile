# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the workload binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /pool_health ./antithesis/workloads/pool_health

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /pool_health /app/pool_health

# Run the workload
CMD ["/app/pool_health"]

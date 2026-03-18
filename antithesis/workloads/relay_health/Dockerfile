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

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /relay_health ./antithesis/workloads/relay_health

# Runtime stage
FROM alpine:3.19

# Install ca-certificates for any HTTPS calls
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /relay_health /app/relay_health

# Run the workload
CMD ["/app/relay_health"]

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
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /validator-node ./cmd/validator-node

# Runtime stage
FROM alpine:3.19

# Install wget for healthcheck
RUN apk add --no-cache wget ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /validator-node /app/validator-node

# Expose default port
EXPOSE 8081

# Run the service
CMD ["/app/validator-node"]

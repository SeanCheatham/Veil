# Veil Makefile
# Build, test, and lint targets for the Veil project

.PHONY: all build test lint clean fmt vet tidy help
.PHONY: build-message-pool build-validator-node build-relay-node build-sender-workload build-receiver-workload

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOFMT=gofmt
GOMOD=$(GOCMD) mod

# Binary output directory
BIN_DIR=bin

# Services
SERVICES=message-pool validator-node relay-node sender-workload receiver-workload

# Default target
all: tidy lint test build

# Build all services
build: $(addprefix build-,$(SERVICES))
	@echo "All services built successfully"

# Individual service build targets
build-message-pool:
	@echo "Building message-pool..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/message-pool ./cmd/message-pool

build-validator-node:
	@echo "Building validator-node..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/validator-node ./cmd/validator-node

build-relay-node:
	@echo "Building relay-node..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/relay-node ./cmd/relay-node

build-sender-workload:
	@echo "Building sender-workload..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/sender-workload ./cmd/sender-workload

build-receiver-workload:
	@echo "Building receiver-workload..."
	@mkdir -p $(BIN_DIR)
	$(GOBUILD) -o $(BIN_DIR)/receiver-workload ./cmd/receiver-workload

# Run all tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race ./...

# Run linting checks
lint: fmt vet
	@echo "Linting complete"

# Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

# Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	$(GOCMD) clean

# Help
help:
	@echo "Veil Makefile targets:"
	@echo "  all       - Run tidy, lint, test, and build (default)"
	@echo "  build     - Build all services"
	@echo "  test      - Run all tests"
	@echo "  lint      - Run linting (fmt + vet)"
	@echo "  fmt       - Format code with gofmt"
	@echo "  vet       - Run go vet"
	@echo "  tidy      - Run go mod tidy"
	@echo "  clean     - Remove build artifacts"
	@echo ""
	@echo "Individual service builds:"
	@echo "  build-message-pool"
	@echo "  build-validator-node"
	@echo "  build-relay-node"
	@echo "  build-sender-workload"
	@echo "  build-receiver-workload"

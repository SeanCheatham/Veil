#!/usr/bin/env bash
# Validates the skeleton Antithesis setup
# This script temporarily swaps in the skeleton compose file for validation

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="$SCRIPT_DIR/config"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

cleanup() {
    local exit_code=$?
    log_info "Cleaning up..."

    # Restore original files if they were backed up
    if [[ -f "$CONFIG_DIR/docker-compose.yaml.backup" ]]; then
        mv "$CONFIG_DIR/docker-compose.yaml.backup" "$CONFIG_DIR/docker-compose.yaml"
        log_info "Restored docker-compose.yaml"
    fi

    if [[ -f "$CONFIG_DIR/docker-compose.override.yaml.backup" ]]; then
        mv "$CONFIG_DIR/docker-compose.override.yaml.backup" "$CONFIG_DIR/docker-compose.override.yaml"
        log_info "Restored docker-compose.override.yaml"
    fi

    exit $exit_code
}

trap cleanup EXIT

main() {
    log_info "Validating skeleton Antithesis setup..."

    # Check required files exist
    if [[ ! -f "$CONFIG_DIR/docker-compose.skeleton.yaml" ]]; then
        log_error "Skeleton compose file not found: $CONFIG_DIR/docker-compose.skeleton.yaml"
        exit 1
    fi

    # Back up current compose files
    if [[ -f "$CONFIG_DIR/docker-compose.yaml" ]]; then
        log_info "Backing up docker-compose.yaml"
        mv "$CONFIG_DIR/docker-compose.yaml" "$CONFIG_DIR/docker-compose.yaml.backup"
    fi

    if [[ -f "$CONFIG_DIR/docker-compose.override.yaml" ]]; then
        log_info "Backing up docker-compose.override.yaml"
        mv "$CONFIG_DIR/docker-compose.override.yaml" "$CONFIG_DIR/docker-compose.override.yaml.backup"
    fi

    # Copy skeleton as main compose file
    log_info "Using skeleton compose file for validation"
    cp "$CONFIG_DIR/docker-compose.skeleton.yaml" "$CONFIG_DIR/docker-compose.yaml"

    # Build images
    log_info "Building skeleton images..."
    if command -v podman &> /dev/null; then
        podman compose -f "$CONFIG_DIR/docker-compose.yaml" build
    else
        docker compose -f "$CONFIG_DIR/docker-compose.yaml" build
    fi

    # Run validation
    log_info "Running snouty validate..."
    if snouty validate "$CONFIG_DIR"; then
        log_info "Skeleton validation PASSED"
    else
        log_error "Skeleton validation FAILED"
        exit 1
    fi
}

main "$@"

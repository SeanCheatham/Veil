#!/usr/bin/env bash
# Submit a run to Antithesis
# Usage: ./submit.sh [--duration MINUTES] [--desc DESCRIPTION] [--skeleton]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="$SCRIPT_DIR/config"

# Default values
DURATION=""
DESC=""
USE_SKELETON=false
EXTRA_ARGS=()

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Submit a test run to Antithesis.

Options:
    --duration MINUTES   Duration of the test run (default: 30)
    --desc DESCRIPTION   Description for the test run
    --skeleton           Use skeleton compose file instead of full system
    -h, --help           Show this help message

Environment Variables:
    ANTITHESIS_REPOSITORY   Required. Registry path for images.

Example:
    export ANTITHESIS_REPOSITORY=registry.example.com/team/veil
    ./submit.sh --duration 30 --desc "first test run"
EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --duration)
            DURATION="$2"
            shift 2
            ;;
        --desc)
            DESC="$2"
            shift 2
            ;;
        --skeleton)
            USE_SKELETON=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            EXTRA_ARGS+=("$1")
            shift
            ;;
    esac
done

cleanup() {
    local exit_code=$?

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
    # Check for required environment variable
    if [[ -z "${ANTITHESIS_REPOSITORY:-}" ]]; then
        log_error "ANTITHESIS_REPOSITORY environment variable is not set"
        log_error "Please set it to your registry path, e.g.:"
        log_error "  export ANTITHESIS_REPOSITORY=registry.example.com/team/veil"
        exit 1
    fi

    log_info "Submitting to Antithesis..."
    log_info "Repository: $ANTITHESIS_REPOSITORY"

    # Handle skeleton mode
    if [[ "$USE_SKELETON" == "true" ]]; then
        log_info "Using skeleton compose file..."

        if [[ ! -f "$CONFIG_DIR/docker-compose.skeleton.yaml" ]]; then
            log_error "Skeleton compose file not found: $CONFIG_DIR/docker-compose.skeleton.yaml"
            exit 1
        fi

        # Back up current compose files
        if [[ -f "$CONFIG_DIR/docker-compose.yaml" ]]; then
            mv "$CONFIG_DIR/docker-compose.yaml" "$CONFIG_DIR/docker-compose.yaml.backup"
        fi

        if [[ -f "$CONFIG_DIR/docker-compose.override.yaml" ]]; then
            mv "$CONFIG_DIR/docker-compose.override.yaml" "$CONFIG_DIR/docker-compose.override.yaml.backup"
        fi

        cp "$CONFIG_DIR/docker-compose.skeleton.yaml" "$CONFIG_DIR/docker-compose.yaml"
    fi

    # Build images
    log_info "Building images..."
    if command -v podman &> /dev/null; then
        podman compose -f "$CONFIG_DIR/docker-compose.yaml" build
    else
        docker compose -f "$CONFIG_DIR/docker-compose.yaml" build
    fi

    # Prepare snouty run arguments
    SNOUTY_ARGS=("--config" "$CONFIG_DIR")

    if [[ -n "$DURATION" ]]; then
        SNOUTY_ARGS+=("--duration" "$DURATION")
    fi

    if [[ -n "$DESC" ]]; then
        SNOUTY_ARGS+=("--desc" "$DESC")
    fi

    # Add any extra arguments
    SNOUTY_ARGS+=("${EXTRA_ARGS[@]}")

    # Run snouty
    log_info "Running: snouty run ${SNOUTY_ARGS[*]}"
    snouty run "${SNOUTY_ARGS[@]}"
}

main "$@"

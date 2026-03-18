#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/config/docker-compose.yaml"
COMPOSE_WAIT_TIMEOUT_SECONDS="60"

compose() {
  # Antithesis uses podman compose behind the scenes, so prefer it locally
  # for increased compatibility; fall back to docker compose if unavailable.
  if command -v podman &>/dev/null; then
    podman compose -f "${COMPOSE_FILE}" "$@"
  else
    docker compose -f "${COMPOSE_FILE}" "$@"
  fi
}

echo "Building Veil Docker images..."
if command -v podman &>/dev/null; then
  podman build -t veil-relay:latest -f "${SCRIPT_DIR}/Dockerfile" --target relay "${REPO_ROOT}"
  podman build -t veil-validator:latest -f "${SCRIPT_DIR}/Dockerfile" --target validator "${REPO_ROOT}"
  podman build -t veil-pool:latest -f "${SCRIPT_DIR}/Dockerfile" --target pool "${REPO_ROOT}"
  podman build -t veil-workload:latest -f "${SCRIPT_DIR}/Dockerfile" --target workload "${REPO_ROOT}"
else
  docker build -t veil-relay:latest -f "${SCRIPT_DIR}/Dockerfile" --target relay "${REPO_ROOT}"
  docker build -t veil-validator:latest -f "${SCRIPT_DIR}/Dockerfile" --target validator "${REPO_ROOT}"
  docker build -t veil-pool:latest -f "${SCRIPT_DIR}/Dockerfile" --target pool "${REPO_ROOT}"
  docker build -t veil-workload:latest -f "${SCRIPT_DIR}/Dockerfile" --target workload "${REPO_ROOT}"
fi

echo "Starting Veil services..."
if ! compose up -d --wait --wait-timeout "${COMPOSE_WAIT_TIMEOUT_SECONDS}"; then
  echo "Timed out waiting for compose services to become ready." >&2
  echo "Recent logs:" >&2
  compose logs --no-color --tail=200 >&2 || true
  exit 1
fi

echo "Running test composer commands..."

# Run first_ commands (bootstrap)
echo "Running first_genesis..."
compose exec workload /opt/antithesis/test/v1/veil/first_genesis || true

# Run parallel drivers briefly
echo "Running parallel drivers..."
timeout 10 compose exec workload /opt/antithesis/test/v1/veil/parallel_driver_send &
timeout 10 compose exec workload /opt/antithesis/test/v1/veil/parallel_driver_receive &
wait

# Run anytime invariant check
echo "Running anytime_invariant_check..."
compose exec workload /opt/antithesis/test/v1/veil/anytime_invariant_check || true

# Run serial drivers
echo "Running serial_driver_epoch_rotate..."
compose exec workload /opt/antithesis/test/v1/veil/serial_driver_epoch_rotate || true

echo "Running serial_driver_byz_relay..."
compose exec workload /opt/antithesis/test/v1/veil/serial_driver_byz_relay || true

# Run eventually_ commands
echo "Running eventually_consistency..."
compose exec workload /opt/antithesis/test/v1/veil/eventually_consistency || true

# Run finally_ commands
echo "Running finally_no_leakage..."
compose exec workload /opt/antithesis/test/v1/veil/finally_no_leakage || true

echo "Cleaning up..."
compose down

echo "Test run complete!"

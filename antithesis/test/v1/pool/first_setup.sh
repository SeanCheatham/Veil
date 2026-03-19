#!/usr/bin/env bash
set -euo pipefail

# first_ command: runs once at the start of a timeline.
# Verifies the message-pool service is reachable and returns HTTP 200 on /health.

MAX_RETRIES=30
RETRY_INTERVAL=2

resolve_host() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

for i in $(seq 1 "$MAX_RETRIES"); do
  # Try hostname first
  RESPONSE=$(wget -q -O - --timeout=5 http://message-pool:8081/health 2>/dev/null) && {
    echo "message-pool /health returned ok on attempt $i: $RESPONSE"
    exit 0
  }

  # Fall back to IP-based access if DNS fails
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${POOL_IP}:8081/health" 2>/dev/null) && {
      echo "message-pool /health returned ok via IP $POOL_IP on attempt $i: $RESPONSE"
      exit 0
    }
  fi

  echo "Attempt $i: message-pool not reachable, retrying in ${RETRY_INTERVAL}s..."
  sleep "$RETRY_INTERVAL"
done

echo "ERROR: message-pool /health did not respond after $MAX_RETRIES attempts"
exit 1

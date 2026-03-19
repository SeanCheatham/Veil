#!/usr/bin/env bash
set -euo pipefail

# first_ command: runs once at the start of a timeline.
# Verifies all 3 validator services are reachable and return HTTP 200 on /health.

MAX_RETRIES=30
RETRY_INTERVAL=2

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

check_validator() {
  local host="$1"
  local port=8082

  # Try hostname first
  RESPONSE=$(wget -q -O - --timeout=5 "http://${host}:${port}/health" 2>/dev/null) && {
    echo "${host} /health returned ok: $RESPONSE"
    return 0
  }

  # Fall back to IP-based access if DNS fails
  local ip
  ip=$(resolve_host "$host")
  if [ -n "$ip" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${ip}:${port}/health" 2>/dev/null) && {
      echo "${host} /health returned ok via IP $ip: $RESPONSE"
      return 0
    }
  fi

  return 1
}

VALIDATORS="validator-1 validator-2 validator-3"

for host in $VALIDATORS; do
  HEALTHY=false
  for i in $(seq 1 "$MAX_RETRIES"); do
    if check_validator "$host"; then
      HEALTHY=true
      break
    fi
    echo "Attempt $i: $host not reachable, retrying in ${RETRY_INTERVAL}s..."
    sleep "$RETRY_INTERVAL"
  done

  if [ "$HEALTHY" = false ]; then
    echo "ERROR: $host /health did not respond after $MAX_RETRIES attempts"
    exit 1
  fi
done

echo "All validators healthy"
exit 0

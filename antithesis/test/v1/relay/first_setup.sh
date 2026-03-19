#!/usr/bin/env bash
set -euo pipefail

# first_ command: runs once at the start of a timeline.
# Verifies all 5 relay services are reachable and return HTTP 200 on /health.

MAX_RETRIES=30
RETRY_INTERVAL=2

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

check_relay() {
  local host="$1"
  local port=8083

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

RELAYS="relay-1 relay-2 relay-3 relay-4 relay-5"

for host in $RELAYS; do
  HEALTHY=false
  for i in $(seq 1 "$MAX_RETRIES"); do
    if check_relay "$host"; then
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

echo "All relays healthy"
exit 0

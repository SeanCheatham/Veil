#!/usr/bin/env bash
set -euo pipefail

# first_ command: runs once at the start of a timeline.
# Verifies ALL services are reachable and return HTTP 200 on /health.

MAX_RETRIES=30
RETRY_INTERVAL=2

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

check_health() {
  local host="$1"
  local port="$2"

  # Try hostname first
  RESPONSE=$(wget -q -O - --timeout=5 "http://${host}:${port}/health" 2>/dev/null) && {
    echo "${host}:${port} /health returned ok: $RESPONSE"
    return 0
  }

  # Fall back to IP-based access if DNS fails
  local ip
  ip=$(resolve_host "$host")
  if [ -n "$ip" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${ip}:${port}/health" 2>/dev/null) && {
      echo "${host}:${port} /health returned ok via IP $ip: $RESPONSE"
      return 0
    }
  fi

  return 1
}

# All services: host port
SERVICES="
message-pool 8081
validator-1 8082
validator-2 8082
validator-3 8082
relay-1 8083
relay-2 8083
relay-3 8083
relay-4 8083
relay-5 8083
sender 8084
receiver 8085
"

echo "$SERVICES" | while read -r host port; do
  [ -z "$host" ] && continue
  HEALTHY=false
  for i in $(seq 1 "$MAX_RETRIES"); do
    if check_health "$host" "$port"; then
      HEALTHY=true
      break
    fi
    echo "Attempt $i: $host not reachable, retrying in ${RETRY_INTERVAL}s..."
    sleep "$RETRY_INTERVAL"
  done

  if [ "$HEALTHY" = false ]; then
    echo "ERROR: $host:$port /health did not respond after $MAX_RETRIES attempts"
    exit 1
  fi
done

echo "All services healthy"
exit 0

#!/usr/bin/env bash
set -euo pipefail

# first_ command: runs once at the start of a timeline.
# Verifies the hello service is reachable and returns HTTP 200 on /health.

MAX_RETRIES=30
RETRY_INTERVAL=2

# Resolve hello via /etc/hosts or DNS, fall back to getent
resolve_hello() {
  getent hosts hello 2>/dev/null | awk '{print $1}' | head -1
}

for i in $(seq 1 "$MAX_RETRIES"); do
  # Try curl with hostname first, then fall back to resolved IP
  RESPONSE=$(wget -q -O - --timeout=5 http://hello:8080/health 2>/dev/null) && {
    echo "hello service /health returned ok on attempt $i: $RESPONSE"
    exit 0
  }

  # Fall back to IP-based access if DNS fails
  HELLO_IP=$(resolve_hello)
  if [ -n "$HELLO_IP" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${HELLO_IP}:8080/health" 2>/dev/null) && {
      echo "hello service /health returned ok via IP $HELLO_IP on attempt $i: $RESPONSE"
      exit 0
    }
  fi

  echo "Attempt $i: hello not reachable, retrying in ${RETRY_INTERVAL}s..."
  sleep "$RETRY_INTERVAL"
done

echo "ERROR: hello service /health did not respond after $MAX_RETRIES attempts"
exit 1

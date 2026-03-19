#!/usr/bin/env bash
set -euo pipefail

# parallel_driver_ command: runs concurrently and repeatedly.
# Checks the hello service health endpoint.

# Resolve hello via /etc/hosts or DNS, fall back to getent
resolve_hello() {
  getent hosts hello 2>/dev/null | awk '{print $1}' | head -1
}

# Try hostname first
RESPONSE=$(wget -q -O - --timeout=5 http://hello:8080/health 2>/dev/null) && {
  echo "health check passed: $RESPONSE"
  exit 0
}

# Fall back to IP
HELLO_IP=$(resolve_hello)
if [ -n "$HELLO_IP" ]; then
  RESPONSE=$(wget -q -O - --timeout=5 "http://${HELLO_IP}:8080/health" 2>/dev/null) && {
    echo "health check passed via IP: $RESPONSE"
    exit 0
  }
fi

echo "ERROR: hello service health check failed"
exit 1

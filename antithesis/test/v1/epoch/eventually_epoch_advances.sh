#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: Test Composer handles retry semantics.
# Checks if the epoch counter has advanced beyond 0.

resolve_host() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

RESPONSE=$(wget -q -O - --timeout=5 "http://message-pool:8081/epoch" 2>/dev/null) || {
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${POOL_IP}:8081/epoch" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool"
    exit 1
  fi
}

echo "Epoch response: $RESPONSE"

EPOCH=$(echo "$RESPONSE" | sed -n 's/.*"epoch":\([0-9]*\).*/\1/p')
if [ -z "$EPOCH" ]; then
  echo "ERROR: could not parse epoch from response"
  exit 1
fi

if [ "$EPOCH" -gt 0 ]; then
  echo "SUCCESS: epoch has advanced to $EPOCH"
  exit 0
else
  echo "Epoch is still 0, not yet advanced"
  exit 1
fi

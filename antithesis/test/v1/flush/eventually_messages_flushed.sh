#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: Test Composer handles retry semantics.
# Checks if any messages have been flushed (expired) from the pool.

resolve_host() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

STATS=$(wget -q -O - --timeout=5 "http://message-pool:8081/stats" 2>/dev/null) || {
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    STATS=$(wget -q -O - --timeout=5 "http://${POOL_IP}:8081/stats" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool"
    exit 1
  fi
}

echo "Stats response: $STATS"

EXPIRED=$(echo "$STATS" | sed -n 's/.*"expired":\([0-9]*\).*/\1/p')

if [ -z "$EXPIRED" ]; then
  echo "ERROR: could not parse expired count"
  exit 1
fi

if [ "$EXPIRED" -gt 0 ]; then
  echo "SUCCESS: $EXPIRED messages have been flushed"
  exit 0
else
  echo "No messages flushed yet (expired=0)"
  exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: Test Composer handles retry semantics.
# Checks that the anonymity set is being tracked by the message pool.

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

# Check that anonymity_set field exists
ANON_SET=$(echo "$STATS" | sed -n 's/.*"anonymity_set":\([0-9]*\).*/\1/p')
EPOCH=$(echo "$STATS" | sed -n 's/.*"epoch":\([0-9]*\).*/\1/p')

if [ -z "$ANON_SET" ]; then
  echo "ERROR: anonymity_set field not found in stats"
  exit 1
fi

if [ -z "$EPOCH" ]; then
  echo "ERROR: epoch field not found in stats"
  exit 1
fi

if [ "$EPOCH" -gt 0 ]; then
  echo "SUCCESS: anonymity set tracking active (epoch=$EPOCH, anonymity_set=$ANON_SET)"
  exit 0
else
  echo "Epoch is still 0, waiting for epoch to advance"
  exit 1
fi

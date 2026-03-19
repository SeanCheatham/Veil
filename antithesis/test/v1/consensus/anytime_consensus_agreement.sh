#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# Verifies all 3 validators return the same pool view (same messages in same order).

getent hosts validator-1 || true

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

fetch_pool() {
  local host="$1"
  local result
  result=$(wget -q -O - --timeout=5 "http://${host}:8082/pool" 2>/dev/null) || {
    local ip
    ip=$(resolve_host "$host")
    if [ -n "$ip" ]; then
      result=$(wget -q -O - --timeout=5 "http://${ip}:8082/pool" 2>/dev/null)
    else
      echo ""
      return
    fi
  }
  echo "$result"
}

POOL1=$(fetch_pool "validator-1")
POOL2=$(fetch_pool "validator-2")
POOL3=$(fetch_pool "validator-3")

if [ -z "$POOL1" ] || [ -z "$POOL2" ] || [ -z "$POOL3" ]; then
  echo "ERROR: could not fetch pool from one or more validators"
  exit 1
fi

echo "Validator-1 pool: $POOL1"
echo "Validator-2 pool: $POOL2"
echo "Validator-3 pool: $POOL3"

if [ "$POOL1" = "$POOL2" ] && [ "$POOL2" = "$POOL3" ]; then
  echo "SUCCESS: all validators agree on pool contents"
  exit 0
else
  echo "ERROR: validators disagree on pool contents"
  exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# Verifies that expired messages are still accessible by index (returns 200, not 404).

resolve_host() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

BASE_URL="http://message-pool:8081"

# First check if there are any expired messages
STATS=$(wget -q -O - --timeout=5 "${BASE_URL}/stats" 2>/dev/null) || {
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    BASE_URL="http://${POOL_IP}:8081"
    STATS=$(wget -q -O - --timeout=5 "${BASE_URL}/stats" 2>/dev/null)
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

if [ "$EXPIRED" -eq 0 ]; then
  echo "No expired messages yet — nothing to test, passing"
  exit 0
fi

# Try to access index 0 (likely oldest, most likely expired)
GET_RESPONSE=$(wget -q -O - --timeout=5 "${BASE_URL}/messages/0" 2>/dev/null) || {
  echo "ERROR: got non-200 response for /messages/0 (should never be 404 for expired)"
  exit 1
}

echo "GET /messages/0 response: $GET_RESPONSE"

# Verify the response contains a status field
STATUS=$(echo "$GET_RESPONSE" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')

if [ -z "$STATUS" ]; then
  echo "ERROR: response missing status field"
  exit 1
fi

if [ "$STATUS" = "active" ] || [ "$STATUS" = "expired" ]; then
  echo "SUCCESS: /messages/0 returned status=$STATUS (HTTP 200)"
  exit 0
else
  echo "ERROR: unexpected status value: $STATUS"
  exit 1
fi

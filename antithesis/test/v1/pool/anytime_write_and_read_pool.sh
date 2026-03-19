#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# Posts a random ciphertext to the message pool, then reads it back and verifies.

BASE_URL="http://message-pool:8081"

resolve_host() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

# Generate a random base64 ciphertext blob
CIPHERTEXT=$(head -c 32 /dev/urandom | base64)
echo "Generated ciphertext: $CIPHERTEXT"

# POST the ciphertext
POST_BODY="{\"ciphertext\":\"${CIPHERTEXT}\"}"

POST_RESPONSE=$(wget -q -O - --timeout=5 \
  --header="Content-Type: application/json" \
  --post-data="$POST_BODY" \
  "${BASE_URL}/messages" 2>/dev/null) || {
  # Fall back to IP
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    POST_RESPONSE=$(wget -q -O - --timeout=5 \
      --header="Content-Type: application/json" \
      --post-data="$POST_BODY" \
      "http://${POOL_IP}:8081/messages" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool for POST"
    exit 1
  fi
}

echo "POST response: $POST_RESPONSE"

# Extract index from response — expect {"index":N}
INDEX=$(echo "$POST_RESPONSE" | sed -n 's/.*"index":\([0-9]*\).*/\1/p')
if [ -z "$INDEX" ]; then
  echo "ERROR: could not parse index from POST response"
  exit 1
fi
echo "Got index: $INDEX"

# GET the message back by index
GET_RESPONSE=$(wget -q -O - --timeout=5 \
  "${BASE_URL}/messages/${INDEX}" 2>/dev/null) || {
  POOL_IP=$(resolve_host)
  if [ -n "$POOL_IP" ]; then
    GET_RESPONSE=$(wget -q -O - --timeout=5 \
      "http://${POOL_IP}:8081/messages/${INDEX}" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool for GET"
    exit 1
  fi
}

echo "GET response: $GET_RESPONSE"

# Verify the ciphertext matches
RETURNED_CT=$(echo "$GET_RESPONSE" | sed -n 's/.*"ciphertext":"\([^"]*\)".*/\1/p')
if [ "$RETURNED_CT" = "$CIPHERTEXT" ]; then
  echo "SUCCESS: ciphertext matches"
  exit 0
else
  echo "ERROR: ciphertext mismatch. Expected: $CIPHERTEXT Got: $RETURNED_CT"
  exit 1
fi

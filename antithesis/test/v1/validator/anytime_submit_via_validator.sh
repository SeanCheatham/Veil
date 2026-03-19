#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# Picks a random validator, POSTs a ciphertext via /submit, then verifies
# it appears in the message-pool.

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

# Pick a random validator (1-3)
VALIDATOR_NUM=$(( (RANDOM % 3) + 1 ))
VALIDATOR_HOST="validator-${VALIDATOR_NUM}"
echo "Selected validator: $VALIDATOR_HOST"

# Generate a random base64 ciphertext blob
CIPHERTEXT=$(head -c 32 /dev/urandom | base64)
echo "Generated ciphertext: $CIPHERTEXT"

# POST the ciphertext via the validator's /submit endpoint
POST_BODY="{\"ciphertext\":\"${CIPHERTEXT}\"}"

POST_RESPONSE=$(wget -q -O - --timeout=10 \
  --header="Content-Type: application/json" \
  --post-data="$POST_BODY" \
  "http://${VALIDATOR_HOST}:8082/submit" 2>/dev/null) || {
  # Fall back to IP
  V_IP=$(resolve_host "$VALIDATOR_HOST")
  if [ -n "$V_IP" ]; then
    POST_RESPONSE=$(wget -q -O - --timeout=10 \
      --header="Content-Type: application/json" \
      --post-data="$POST_BODY" \
      "http://${V_IP}:8082/submit" 2>/dev/null)
  else
    echo "ERROR: could not reach $VALIDATOR_HOST for POST /submit"
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

# Verify the message appears in the pool by fetching from message-pool directly
GET_RESPONSE=$(wget -q -O - --timeout=5 \
  "http://message-pool:8081/messages/${INDEX}" 2>/dev/null) || {
  POOL_IP=$(resolve_host "message-pool")
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
  echo "SUCCESS: ciphertext submitted via $VALIDATOR_HOST matches in pool at index $INDEX"
  exit 0
else
  echo "ERROR: ciphertext mismatch. Expected: $CIPHERTEXT Got: $RETURNED_CT"
  exit 1
fi

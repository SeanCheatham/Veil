#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: retried by Test Composer until it passes.
# Picks a random validator, POSTs a ciphertext via /submit, then checks
# if it appears in the message-pool. With BFT consensus batching, the
# message may take several seconds to reach the pool.

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

# Wait for consensus batching to complete
sleep 10

# Check if the ciphertext appears in the pool
POOL_RESPONSE=$(wget -q -O - --timeout=5 \
  "http://message-pool:8081/messages" 2>/dev/null) || {
  POOL_IP=$(resolve_host "message-pool")
  if [ -n "$POOL_IP" ]; then
    POOL_RESPONSE=$(wget -q -O - --timeout=5 \
      "http://${POOL_IP}:8081/messages" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool"
    exit 1
  fi
}

echo "Pool response: $POOL_RESPONSE"

# Check if our ciphertext is in the pool
if echo "$POOL_RESPONSE" | grep -q "$CIPHERTEXT"; then
  echo "SUCCESS: ciphertext submitted via $VALIDATOR_HOST found in pool"
  exit 0
else
  echo "NOT YET: ciphertext not found in pool (consensus may still be batching)"
  exit 1
fi

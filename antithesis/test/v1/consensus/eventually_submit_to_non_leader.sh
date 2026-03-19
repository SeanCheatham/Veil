#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: retried by Test Composer until it passes.
# POSTs a ciphertext to a non-leader validator (validator-2 or validator-3),
# then checks if it appears in the message-pool. With round-robin leadership
# and 3-second propose intervals, this may take up to 9 seconds.

getent hosts validator-2 || true

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

# Pick validator-2 or validator-3 (non-leader for seq 0)
NON_LEADER_NUM=$(( (RANDOM % 2) + 2 ))
NON_LEADER_HOST="validator-${NON_LEADER_NUM}"
echo "Selected non-leader validator: $NON_LEADER_HOST"

# Generate a random base64 ciphertext blob
CIPHERTEXT=$(head -c 32 /dev/urandom | base64)
echo "Generated ciphertext: $CIPHERTEXT"

# POST the ciphertext
POST_BODY="{\"ciphertext\":\"${CIPHERTEXT}\"}"

POST_RESPONSE=$(wget -q -O - --timeout=10 \
  --header="Content-Type: application/json" \
  --post-data="$POST_BODY" \
  "http://${NON_LEADER_HOST}:8082/submit" 2>/dev/null) || {
  V_IP=$(resolve_host "$NON_LEADER_HOST")
  if [ -n "$V_IP" ]; then
    POST_RESPONSE=$(wget -q -O - --timeout=10 \
      --header="Content-Type: application/json" \
      --post-data="$POST_BODY" \
      "http://${V_IP}:8082/submit" 2>/dev/null)
  else
    echo "ERROR: could not reach $NON_LEADER_HOST"
    exit 1
  fi
}

echo "POST response: $POST_RESPONSE"

# Wait for consensus to process
sleep 12

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

if echo "$POOL_RESPONSE" | grep -q "$CIPHERTEXT"; then
  echo "SUCCESS: ciphertext submitted to non-leader $NON_LEADER_HOST found in pool"
  exit 0
else
  echo "NOT YET: ciphertext not found in pool"
  exit 1
fi

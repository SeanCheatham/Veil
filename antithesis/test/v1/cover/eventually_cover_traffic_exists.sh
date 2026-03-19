#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: Test Composer handles retry semantics.
# Checks if cover traffic is present by comparing pool message count
# to receiver's received count. Cover messages inflate the pool but
# cannot be decrypted by the receiver, so pool count > received count.

resolve_pool() {
  getent hosts message-pool 2>/dev/null | awk '{print $1}' | head -1
}

resolve_receiver() {
  getent hosts receiver 2>/dev/null | awk '{print $1}' | head -1
}

POOL_RESPONSE=$(wget -q -O - --timeout=5 "http://message-pool:8081/messages" 2>/dev/null) || {
  POOL_IP=$(resolve_pool)
  if [ -n "$POOL_IP" ]; then
    POOL_RESPONSE=$(wget -q -O - --timeout=5 "http://${POOL_IP}:8081/messages" 2>/dev/null)
  else
    echo "ERROR: could not reach message-pool"
    exit 1
  fi
}

RECV_RESPONSE=$(wget -q -O - --timeout=5 "http://receiver:8085/received" 2>/dev/null) || {
  RECV_IP=$(resolve_receiver)
  if [ -n "$RECV_IP" ]; then
    RECV_RESPONSE=$(wget -q -O - --timeout=5 "http://${RECV_IP}:8085/received" 2>/dev/null)
  else
    echo "ERROR: could not reach receiver"
    exit 1
  fi
}

echo "Pool response: $POOL_RESPONSE"
echo "Receiver response: $RECV_RESPONSE"

POOL_COUNT=$(echo "$POOL_RESPONSE" | sed -n 's/.*"count":\([0-9]*\).*/\1/p')
RECV_COUNT=$(echo "$RECV_RESPONSE" | sed -n 's/.*"count":\([0-9]*\).*/\1/p')

if [ -z "$POOL_COUNT" ] || [ -z "$RECV_COUNT" ]; then
  echo "ERROR: could not parse counts"
  exit 1
fi

echo "Pool messages: $POOL_COUNT, Receiver received: $RECV_COUNT"

if [ "$POOL_COUNT" -gt "$RECV_COUNT" ]; then
  echo "SUCCESS: pool has $POOL_COUNT messages but receiver only decrypted $RECV_COUNT — cover traffic exists"
  exit 0
else
  echo "No cover traffic detected yet (pool=$POOL_COUNT, received=$RECV_COUNT)"
  exit 1
fi

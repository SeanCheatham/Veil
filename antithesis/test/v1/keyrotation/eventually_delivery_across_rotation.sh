#!/bin/bash
# eventually_delivery_across_rotation: Verify messages are delivered even after
# key rotation has occurred. Checks that receiver has messages AND epoch > 0.

set -euo pipefail

# DNS fallback
getent hosts receiver || true
getent hosts message-pool || true

# Check receiver has messages
RECEIVED=$(wget -qO- http://receiver:8085/received 2>/dev/null)
if [ -z "$RECEIVED" ]; then
  echo "FAIL: could not fetch received messages"
  exit 1
fi
COUNT=$(echo "$RECEIVED" | sed 's/.*"count":\([0-9]*\).*/\1/')
echo "Received count: $COUNT"

# Check epoch > 0
EPOCH_RESP=$(wget -qO- http://message-pool:8081/epoch 2>/dev/null)
if [ -z "$EPOCH_RESP" ]; then
  echo "FAIL: could not fetch epoch"
  exit 1
fi
EPOCH=$(echo "$EPOCH_RESP" | sed 's/.*"epoch":\([0-9]*\).*/\1/')
echo "Current epoch: $EPOCH"

if [ "$COUNT" -gt 0 ] && [ "$EPOCH" -gt 0 ]; then
  echo "PASS: messages delivered across key rotation (count=$COUNT, epoch=$EPOCH)"
  exit 0
else
  echo "FAIL: conditions not met (count=$COUNT, epoch=$EPOCH)"
  exit 1
fi

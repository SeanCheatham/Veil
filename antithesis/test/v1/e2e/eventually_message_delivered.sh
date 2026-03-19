#!/usr/bin/env bash
set -euo pipefail

# eventually_ command: Test Composer handles retry semantics.
# Checks if the receiver has received at least one message.

resolve_host() {
  getent hosts receiver 2>/dev/null | awk '{print $1}' | head -1
}

RESPONSE=$(wget -q -O - --timeout=5 "http://receiver:8085/received" 2>/dev/null) || {
  RECV_IP=$(resolve_host)
  if [ -n "$RECV_IP" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${RECV_IP}:8085/received" 2>/dev/null)
  else
    echo "ERROR: could not reach receiver"
    exit 1
  fi
}

echo "Receiver response: $RESPONSE"

COUNT=$(echo "$RESPONSE" | sed -n 's/.*"count":\([0-9]*\).*/\1/p')
if [ -z "$COUNT" ]; then
  echo "ERROR: could not parse count from response"
  exit 1
fi

if [ "$COUNT" -gt 0 ]; then
  echo "SUCCESS: receiver has $COUNT messages"
  exit 0
else
  echo "No messages received yet"
  exit 1
fi

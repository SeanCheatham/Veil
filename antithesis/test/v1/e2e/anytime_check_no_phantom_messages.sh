#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# Verifies every received message_id was actually sent (no phantom messages).

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

fetch() {
  local host="$1"
  local port="$2"
  local path="$3"

  RESULT=$(wget -q -O - --timeout=5 "http://${host}:${port}${path}" 2>/dev/null) && {
    echo "$RESULT"
    return 0
  }

  local ip
  ip=$(resolve_host "$host")
  if [ -n "$ip" ]; then
    RESULT=$(wget -q -O - --timeout=5 "http://${ip}:${port}${path}" 2>/dev/null) && {
      echo "$RESULT"
      return 0
    }
  fi

  return 1
}

SENT_RESPONSE=$(fetch sender 8084 /sent) || {
  echo "ERROR: could not reach sender"
  exit 1
}

RECEIVED_RESPONSE=$(fetch receiver 8085 /received) || {
  echo "ERROR: could not reach receiver"
  exit 1
}

echo "Sent response: $SENT_RESPONSE"
echo "Received response: $RECEIVED_RESPONSE"

# Extract received count
RECV_COUNT=$(echo "$RECEIVED_RESPONSE" | sed -n 's/.*"count":\([0-9]*\).*/\1/p')
if [ -z "$RECV_COUNT" ] || [ "$RECV_COUNT" -eq 0 ]; then
  echo "No received messages yet, check passes trivially"
  exit 0
fi

# For each message_id in received, check it exists in sent
# Extract all message_ids from received
RECV_IDS=$(echo "$RECEIVED_RESPONSE" | grep -o '"message_id":"[^"]*"' | sed 's/"message_id":"//;s/"//')

for mid in $RECV_IDS; do
  if ! echo "$SENT_RESPONSE" | grep -q "$mid"; then
    echo "ERROR: phantom message detected! message_id=$mid found in received but not in sent"
    exit 1
  fi
done

echo "SUCCESS: all received message_ids found in sent list"
exit 0

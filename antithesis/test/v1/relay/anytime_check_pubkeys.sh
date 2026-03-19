#!/usr/bin/env bash
set -euo pipefail

# anytime_ command: can run at any point during testing.
# For each relay (1-5), GET /pubkey and verify the response contains
# relay_id and public_key fields.

resolve_host() {
  getent hosts "$1" 2>/dev/null | awk '{print $1}' | head -1
}

fetch_pubkey() {
  local host="$1"
  local port=8083

  # Try hostname first
  RESPONSE=$(wget -q -O - --timeout=5 "http://${host}:${port}/pubkey" 2>/dev/null) && {
    echo "$RESPONSE"
    return 0
  }

  # Fall back to IP
  local ip
  ip=$(resolve_host "$host")
  if [ -n "$ip" ]; then
    RESPONSE=$(wget -q -O - --timeout=5 "http://${ip}:${port}/pubkey" 2>/dev/null) && {
      echo "$RESPONSE"
      return 0
    }
  fi

  return 1
}

ALL_OK=true

for N in 1 2 3 4 5; do
  HOST="relay-${N}"
  echo "Checking $HOST /pubkey..."

  RESPONSE=$(fetch_pubkey "$HOST") || {
    echo "ERROR: could not reach $HOST for GET /pubkey"
    ALL_OK=false
    continue
  }

  echo "Response from $HOST: $RESPONSE"

  # Verify relay_id field is present
  if ! echo "$RESPONSE" | grep -q '"relay_id"'; then
    echo "ERROR: $HOST response missing relay_id field"
    ALL_OK=false
    continue
  fi

  # Verify public_key field is present
  if ! echo "$RESPONSE" | grep -q '"public_key"'; then
    echo "ERROR: $HOST response missing public_key field"
    ALL_OK=false
    continue
  fi

  echo "$HOST pubkey OK"
done

if [ "$ALL_OK" = true ]; then
  echo "SUCCESS: all relays returned valid pubkey responses"
  exit 0
else
  echo "ERROR: one or more relays failed pubkey check"
  exit 1
fi

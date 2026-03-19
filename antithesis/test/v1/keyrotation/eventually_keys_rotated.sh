#!/bin/bash
# eventually_keys_rotated: Verify that relay keys rotate across epochs.
# GET relay-1 pubkey, wait one epoch, GET again, compare.

set -euo pipefail

# DNS fallback
getent hosts relay-1 || true

# Get initial public key
INITIAL=$(wget -qO- http://relay-1:8083/pubkey 2>/dev/null)
if [ -z "$INITIAL" ]; then
  echo "FAIL: could not fetch initial pubkey"
  exit 1
fi
INITIAL_KEY=$(echo "$INITIAL" | sed 's/.*"public_key":"\([^"]*\)".*/\1/')
echo "Initial key: $INITIAL_KEY"

# Sleep just over one epoch (15s epoch duration)
sleep 20

# Get pubkey again
AFTER=$(wget -qO- http://relay-1:8083/pubkey 2>/dev/null)
if [ -z "$AFTER" ]; then
  echo "FAIL: could not fetch pubkey after sleep"
  exit 1
fi
AFTER_KEY=$(echo "$AFTER" | sed 's/.*"public_key":"\([^"]*\)".*/\1/')
echo "After key: $AFTER_KEY"

if [ "$INITIAL_KEY" != "$AFTER_KEY" ]; then
  echo "PASS: keys rotated"
  exit 0
else
  echo "FAIL: keys did not rotate"
  exit 1
fi

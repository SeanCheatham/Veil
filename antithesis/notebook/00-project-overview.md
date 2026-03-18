# Veil Antithesis Project Overview

## System Under Test

Veil is a decentralized anonymous messaging protocol - a mix network with consensus-ordered delivery.

### Architecture

```
+-------------------+     +-------------------+     +-------------------+
| sender-workload   | --> | relay-1..5        | --> | message-pool      |
| (test driver)     |     | (onion peeling)   |     | (append-only)     |
+-------------------+     +-------------------+     +-------------------+
                                   |                        ^
                                   v                        |
                          +-------------------+             |
                          | validator-1..3    |-------------+
                          | (BFT consensus)   |
                          +-------------------+
                                   ^
                                   |
                          +-------------------+
                          | receiver-workload |
                          | (test driver)     |
                          +-------------------+
```

### Services

| Service | Count | Role |
|---------|-------|------|
| relay-node | 5 | Onion layer peeling, mix-and-forward |
| validator-node | 3 | BFT consensus, message pool ordering |
| message-pool | 1 | Append-only ciphertext store |
| sender-workload | 1 | Test driver: generates and sends messages |
| receiver-workload | 1 | Test driver: polls pool, asserts delivery |

## Properties to Validate

### Always (Safety)
- `relay_unlinkability` — No relay's inbound message ID linked to outbound
- `validator_agreement` — All validators agree on batch ordering
- `message_integrity` — No message modified in transit
- `epoch_boundaries` — Epoch ticks never skip or duplicate
- `anonymity_set_size` — Active relay count ≥ threshold k
- `key_scope` — Session key never leaves intended relay context

### Sometimes (Liveness)
- `message_forwarding` — Messages eventually reach pool
- `chain_progression` — Validator chain commits new batches
- `key_rotation` — Keys rotate at epoch boundary
- `cover_traffic` — Dummy messages injected
- `byzantine_input` — Byzantine relay delivers malicious input

## Build Order

1. Message pool → `message_integrity`
2. Epoch clock + key rotation → `epoch_boundaries`, `key_rotation`
3. Consensus layer → `validator_agreement`, `chain_progression`
4. Relay layer → `relay_unlinkability`, `anonymity_set_size` (hardest)
5. Client layer → `message_forwarding`, `cover_traffic`

## Bug Classes to Find

1. **Epoch-boundary race** — message in-flight during key rotation
2. **Consensus partition cascade** — 2/3 quorum during partition
3. **Anonymity set collapse** — relay count drops below k
4. **Key scope violation on recovery** — stale key after crash

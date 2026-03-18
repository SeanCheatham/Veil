# Veil Property Catalog

This document catalogs all Antithesis properties used in the Veil system. Properties are verified using the Antithesis SDK's `assert.Always` (safety invariants) and `assert.Sometimes` (liveness/coverage) assertions.

## Overview

| Layer | Always | Sometimes | Total |
|-------|--------|-----------|-------|
| Network (Relay) | 4 | 5 | 9 |
| Consensus (PBFT) | 1 | 2 | 3 |
| Storage (Message Pool) | 2 | 1 | 3 |
| Validator | 1 | 1 | 2 |
| Crypto (Onion) | 2 | 1 | 3 |
| Epoch | 1 | 1 | 2 |
| Workload (Sender) | 2 | 2 | 4 |
| Workload (Receiver) | 3 | 2 | 5 |
| Test Commands | 8 | 9 | 17 |
| **Total** | **24** | **24** | **48** |

---

## Network Layer Properties

**Source:** `internal/relay/relay.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| System transitions through epochs | Sometimes | Epoch boundaries are crossed during operation | `newEpoch > oldEpoch` |
| Messages forwarded are not empty | Always | Every forwarded message has non-zero content | `len(payload) > 0` |
| Messages traverse the relay network | Sometimes | Messages successfully flow through relays | On successful forward |
| Only cryptographic errors are acceptable | Always | Non-crypto errors indicate bugs in relay processing | `crypto.IsCryptoError(err)` |
| Full onion unwrapping succeeds | Sometimes | Onion decryption completes for valid messages | On successful peel |
| Relays always have 1-2 valid key sets | Always | Key rotation maintains validity during transitions | `validKeyCount >= 1 && validKeyCount <= 2` |
| Messages decrypt with current epoch keys | Sometimes | Current epoch keys successfully decrypt messages | On current key success |
| Messages in grace period decrypt with previous epoch keys | Sometimes | Previous epoch keys work during grace period | On previous key success in grace |

---

## Consensus Layer Properties

**Source:** `internal/consensus/pbft.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Proposals are initiated by validators | Sometimes | Validators receive and process proposals | On proposal initiation |
| Messages are committed in sequence order | Always | Uniform total order safety - commits follow sequence | `inOrder` (expectedNext == sequence) |
| Consensus completes and messages reach pool | Sometimes | Liveness: PBFT consensus makes progress | `err == nil` on pool submission |

---

## Storage Layer Properties

**Source:** `internal/messagepool/store.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Duplicate consensus sequence rejected | Sometimes | Deduplication correctly handles repeated sequences | On duplicate detection |
| Messages once appended are never lost | Always | Durability within epoch - store grows monotonically | `len(s.messages) > prevLen` |
| Message ordering is consistent across all reads | Always | Linearizability - reads return ordered results | `isOrdered(result)` |

---

## Validator Layer Properties

**Source:** `internal/validator/validator.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Proposals are accepted by validators | Sometimes | Validators successfully process incoming proposals | On proposal acceptance |
| Valid proposals enter consensus | Always | No proposal drops for valid input | `err == nil \|\| isRetryableError(err)` |

---

## Crypto Layer Properties

**Source:** `internal/crypto/onion.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Onion has correct number of layers | Always | Structural integrity of onion encryption | `layerCount == len(relayPubKeys)` |
| Peeling wrong layer fails with crypto error | Always | Security: incorrect keys produce crypto errors | On decryption failure |
| Full onion unwrapping succeeds | Sometimes | Encryption works end-to-end | On successful complete peel |

---

## Epoch Layer Properties

**Source:** `internal/epoch/epoch.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| System transitions through epochs | Sometimes | Time progresses and epochs advance | `epoch > oldEpoch` |
| Grace period allows previous epoch keys | Always | Grace period correctness - two key sets valid | During grace period check |

---

## Workload Sender Properties

**Source:** `internal/workload/sender.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Generated messages have valid structure | Always | Sender creates valid payloads | `len(payload) > 0` |
| Sender successfully submits messages | Sometimes | Send path works end-to-end | `err == nil` on send |
| Cover traffic is generated and sent | Sometimes | Cover traffic generation works | `err == nil` on cover send |
| Cover traffic processed identically to real traffic | Always | No side channels in cover processing | `err == nil \|\| isNetworkError(err)` |

---

## Workload Receiver Properties

**Source:** `internal/workload/receiver.go`

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Received messages match expected format | Always | Message integrity - format validation | `matches` format check |
| Messages are received and verified | Sometimes | Receive path works end-to-end | On successful verification |
| No duplicate messages received | Always | Exactly-once semantics | `!isDuplicate` |
| Cover messages are correctly identified | Sometimes | Cover detection works properly | `isCover` detected |
| Cover messages never leak to recipients as real | Always | Cover is discarded, never reported as real | On cover discard |

---

## Test Command Properties

### Setup Check (`first_setup_check.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Relay network is reachable at startup | Always | Network connectivity established | On successful health check |
| Relay network becomes reachable | Sometimes | Network eventually becomes available | Failed if never reachable |

### Send Message (`parallel_driver_send_message.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Batch send has some successful messages | Sometimes | Send reliability - some messages succeed | `successCount > 0` |

### Verify Messages (`parallel_driver_verify_messages.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Receiver verifies messages from pool | Sometimes | End-to-end message verification | `totalVerified > 0` |

### Delivery Check (`finally_delivery_check.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Message pool is readable at test end | Sometimes | Pool accessibility at end of test | On successful poll |
| Messages are delivered to pool by test end | Sometimes | Delivery liveness | `len(messages) > 0` |
| All delivered messages have valid format | Always | Format integrity of all delivered messages | `invalidCount == 0` |

### Spurious Messages (`anytime_no_spurious_messages.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| No spurious messages appear in pool | Always | No phantom/unexpected messages | `spuriousCount == 0` |
| Message ordering is strictly increasing | Always | Order preservation in pool | `orderingValid` |

### Consensus Ordering (`anytime_consensus_ordering.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Validators have consistent committed sequences | Always | Consensus agreement across validators | `allMatch` (within 5 seq tolerance) |
| No gaps in committed sequence numbers | Always | Sequence integrity - no holes | `noGaps` |
| Validators have consistent next sequence numbers | Sometimes | Proposal synchronization | `nextSeqsMatch` |

### Epoch Transitions (`anytime_epoch_transitions.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| All relays report same epoch within tolerance | Always | Epoch synchronization | `epochSpread <= 1` |
| System transitions through epochs | Sometimes | Time progresses | `maxEpoch > 0` |
| Relays are actively forwarding messages | Sometimes | Message flow is active | `totalForwards > 0` |
| All relays have public keys configured | Always | Key configuration present | `keysPresent` |

### Cover Traffic (`anytime_cover_traffic.go`)

| Property | Type | Description | Condition |
|----------|------|-------------|-----------|
| Cover traffic is being generated and received | Sometimes | Cover traffic active | `coverCount > 0` |
| Cover messages never leak to recipients as real | Always | Cover never reported as real | `!coverLeaked` |

---

## Property Categories

### Safety Properties (Always)
These must hold at all times, including during fault injection:
- Message ordering consistency
- No duplicate messages
- Cryptographic error handling
- Cover traffic isolation
- Consensus sequence ordering
- Valid message formats

### Liveness Properties (Sometimes)
These should eventually be satisfied during normal operation:
- Messages traverse the network
- Consensus completes
- Epochs transition
- Cover traffic flows
- Messages are delivered

### Fault Tolerance Properties
Properties that validate system behavior under faults:
- Validators maintain consistency within tolerance
- Relays stay synchronized within 1 epoch
- Grace period allows key overlap
- Network errors are distinguishable from bugs

---

## Antithesis Test Command Reference

| Prefix | Execution | Purpose |
|--------|-----------|---------|
| `first_` | Once at start | Setup validation |
| `parallel_driver_` | Concurrent, repeated | Load generation |
| `anytime_` | Any time, with faults | Invariant checking |
| `eventually_` | Faults paused | Recovery verification |
| `finally_` | Once at end | Final state validation |

---

## Service Partition Groups

For fault injection targeting:

| Group | Services | Purpose |
|-------|----------|---------|
| `validators` | validator-node0, validator-node1, validator-node2 | Test consensus under partition |
| `relays` | relay-node0 through relay-node4 | Test message flow under partition |

---

## Adding New Properties

When adding new Antithesis assertions:

1. Choose the correct assertion type:
   - `assert.Always(condition, description, details)` - Must hold at all times
   - `assert.Sometimes(condition, description, details)` - Should eventually be true

2. Provide meaningful details map with relevant context

3. Add the property to this catalog with:
   - Source file
   - Property description
   - Condition that triggers it
   - Whether it's safety (Always) or liveness (Sometimes)

4. Consider fault scenarios:
   - Will this property hold during network partitions?
   - Will it hold during service restarts?
   - Is the tolerance appropriate for distributed execution?

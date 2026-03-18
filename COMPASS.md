# Veil

A decentralized anonymous messaging protocol. A mix network with consensus-ordered delivery, designed from the ground up as an Antithesis testing showcase.

## What it is

Veil hides not just message content but metadata — who is talking to whom, how often, and when. There is no centralized server operator to subpoena, no social graph exposed to infrastructure providers.

### Message flow

1. Sender wraps a message in layered onion encryption and submits to the network
2. Each relay node strips one encryption layer and forwards — no relay knows both origin and destination
3. Final ciphertext lands in a shared **message pool** (append-only, ordered by BFT validators)
4. Recipients poll the pool and attempt decryption on every blob — succeeding only on messages addressed to them
5. Cover traffic (dummy messages) pads the pool to defeat traffic analysis
6. System ticks in **epochs**: session keys rotate, anonymity set is checked, stalled messages flushed

---

## Architecture

**Language:** Go

**Services (5 containers):**

| Service | Scale | Role |
|---|---|---|
| `relay-node` | ×5 | Onion layer peeling, mix-and-forward |
| `validator-node` | ×3 | BFT consensus, message pool ordering |
| `message-pool` | ×1 | Append-only ciphertext store |
| `sender-workload` | ×1 | Test driver: generates and sends messages |
| `receiver-workload` | ×1 | Test driver: polls pool, asserts delivery |

---

## Development strategy

The system should be designed and developed around Antithesis. It should employ property-driven-development and test-composer-driven-development. No tasks and features should ideally map to properties or test-composer tests. When designing a feature, consider the properties that must be satisfied, and design a test-composer workload that exercises the feature and satisfies the properties.

Additionally, be sure to follow Antithesis best-practices and guidance with the docker-compose.yml file and corresponding Dockerfiles.

**IMPORTANT: You MUST load the relevant Antithesis Skills. When prompting the PM or Developer sub-agents, also instruct them to load the Antithesis skills.**

If an Antithesis tenant is available, try to employ red/green development practices: define properties and test-composer tests, ensure the properties result in "red" runs before proceeding with the feature implementation. If an Antithesis tenant is not properly configured, proceed with the implementation.

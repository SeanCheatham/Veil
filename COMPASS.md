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

**Stack:** Go · Docker Compose · Antithesis Go SDK

**Services (5 containers):**

| Service | Scale | Role |
|---|---|---|
| `relay-node` | ×5 | Onion layer peeling, mix-and-forward |
| `validator-node` | ×3 | BFT consensus, message pool ordering |
| `message-pool` | ×1 | Append-only ciphertext store |
| `sender-workload` | ×1 | Test driver: generates and sends messages |
| `receiver-workload` | ×1 | Test driver: polls pool, asserts delivery |

---

## Properties

These are the system's correctness invariants, expressed as Antithesis always/sometimes properties.

### Always (safety) — disproved by a single counterexample

| Property | Assertion |
|---|---|
| Relay unlinkability | No relay's inbound message ID is ever linked to its outbound message ID in any log or data structure |
| Validator agreement | All validators agree on the same batch ordering |
| Message integrity | No message is modified in transit through the pool |
| Epoch boundaries | Epoch ticks never skip or duplicate |
| Anonymity set size | Active relay count never drops below threshold `k` |
| Key scope | No session key material ever leaves its intended relay context |

### Sometimes (liveness) — proved by a single example

| Property | Assertion |
|---|---|
| Message forwarding | All submitted messages eventually reach the pool |
| Chain progression | The validator chain always commits new batches |
| Key rotation | Session keys rotate at each epoch boundary |
| Cover traffic | Dummy messages are sometimes injected into the pool |
| Byzantine input | The byzantine relay workload sometimes delivers malicious input |

---

## Key SDK instrumentation points

```go
// relay.go — fires after every forward
antithesis.Always(
    !relay.InboundLog.Contains(outMsg.ID),
    "relay_unlinkability",
    map[string]any{"relay_id": r.ID, "msg_id": outMsg.ID},
)

// pool.go — cover traffic liveness
antithesis.Sometimes(
    pool.LastBatch.ContainsCoverTraffic(),
    "cover_traffic_injected",
    nil,
)

// network.go — anonymity set floor
antithesis.Always(
    network.ActiveRelayCount() >= AnonymityThreshold,
    "anonymity_set_above_k",
    map[string]any{"active": network.ActiveRelayCount(), "k": AnonymityThreshold},
)
```

---

## Bug classes Antithesis will find

These are the races and failure modes that conventional testing cannot surface:

- **Epoch-boundary race** — a message is in-flight through a relay exactly when the epoch ticks and keys rotate. Does the retry logic drop it, double-apply it, or log enough state to re-link inbound to outbound? The window is microseconds wide in production.
- **Consensus partition cascade** — two validators partition from the third, form a 2/3 quorum, and commit a batch. On heal, if reconciliation has any asymmetry in batch numbering, the pool diverges permanently.
- **Anonymity set collapse** — under rapid relay churn (Antithesis fault injector killing/restarting nodes), the active relay count briefly drops below `k`. If the system doesn't pause forwarding and inject cover traffic during this window, it silently degrades the anonymity guarantee.
- **Key scope violation on recovery** — a relay reconstitutes state from disk after a crash. If the session key serialization format is wrong, a stale or duplicated key crosses relay context boundaries.

---

## Development strategy — Ralph Wiggum loop

"Ralph Wiggum style" means the agent writes code, Antithesis finds bugs with perfect reproducibility, the agent reads findings as structured feedback, and iterates. No human in the loop for individual bug cycles.

IMPORTANT: You MUST load the relevant Antithesis Skills. When prompting the PM or Developer sub-agents, also instruct them to load the Antithesis skills.

### Why this works

Antithesis findings are always **actionable and local**: each finding includes the exact execution trace, the specific assertion that failed, and a replay seed. The agent doesn't search the codebase for the bug — it reads the trace, identifies the violating code path, and patches precisely that.

The sometimes-properties act as a **coverage floor**: if `cover_traffic_injected` never fires, the agent knows the padding mechanism is dead code rather than correct code. It cannot fool itself into a green state by writing code that simply never exercises hard paths.

### Build order

Build in order of property density — highest-stakes components first, so every test cycle provides signal on them.

1. **Message pool** — append-only log, no concurrency. Instruments `message_not_modified`.
2. **Epoch clock + key rotation** — instruments `epoch_boundaries_never_skip`, `keys_rotate_each_epoch`.
3. **Consensus layer** — BFT validators. Instruments `validators_agree_on_order`, `chain_always_progresses`.
4. **Relay layer** — onion peeling, mix-and-forward. Instruments `relay_unlinkability` (hardest).
5. **Client layer** — sender/receiver workloads. Exercises the full end-to-end path.

After each component: `docker build` → push → fire a short Antithesis run via webhook → read findings before moving on.

### Scaffold before coding

Write properties and test templates **before** writing Veil itself. The first commit should contain:

- `properties.go` — all `antithesis.Always` / `antithesis.Sometimes` calls with stub implementations that compile
- `testcomposer/` — shell scripts for `first_genesis`, `parallel_driver_send`, `parallel_driver_receive`, `anytime_invariant_check`, `eventually_consistency` (can be `sleep` + `exit 0` initially)
- `docker-compose.yml` — all five services named, even if images are just `alpine`

This gives Antithesis a runnable zero-state baseline: all sometimes-properties fail (nothing has happened), all always-properties pass (nothing wrong happened). That's the known-good starting point every subsequent run is measured against.

### Test Composer structure

```
testcomposer/
  first_genesis               # bootstrap relay network + validators
  parallel_driver_send        # continuous: random sender patterns
  parallel_driver_receive     # continuous: poll pool, assert delivery
  anytime_invariant_check     # permanent: anonymity set, relay unlinkability, epoch health
  serial_driver_epoch_rotate  # disruptive: force epoch boundary with in-flight messages
  serial_driver_byz_relay     # disruptive: make one relay behave maliciously
  eventually_consistency      # post-fault: all sent messages delivered, pool consistent
  finally_no_leakage          # post-run audit: no identity revealed in any log
```

### Antithesis webhook trigger

```bash
POST https://<tenant>.antithesis.com/api/v1/launch_experiment
{
  "params": {
    "antithesis.images": "relay:latest;validator:latest;pool:latest",
    "antithesis.duration": "0.5"
  }
}
```

Agent polls the report endpoint after each run. Each finding contains: which assertion failed, the execution history that triggered it, container logs at the moment of failure, and a replay seed.

### Escalation when findings run dry

1. **Sharpen always-properties** — tighten the unlinkability check from "ID not present in log" to "no byte sequence from the inbound header appears anywhere in the outbound message"
2. **Add adversarial sometimes-properties** — assert that Byzantine behavior was actually attempted; if `byzantine_input_was_attempted` never fires, the test is weaker than intended
3. **Increase fault aggressiveness** — read the `utilization` section of the Antithesis report, identify under-explored code paths, adjust workload scripts to drive more traffic through them

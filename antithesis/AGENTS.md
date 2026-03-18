This directory contains files relevant to running tests in Antithesis.

## Scripts

**submit.sh**
Use this script to build and push any required images, then launch an Antithesis test run via `snouty run`.

**test.sh**
Use this script to test the Antithesis harness locally.

**setup-complete.sh**
Inject this script into a Dockerfile in order to notify Antithesis that setup is complete. This script should only run once the system under test is ready for testing to begin. Antithesis will not run any Test Composer Test Templates until it receives this event. You may forego this script in place of calling the setup complete method via the Antithesis SDK if it makes more sense for your system.

## Directories

**config**
This directory contains the `docker-compose.yaml` file used to bring up this system within the Antithesis environment. It also contains a `Dockerfile` used to build a container image that only contains what Docker compose needs.

**notebook**
This directory can be used as a working space for LLMs to think. Put any plans, notes, or TODOs relevant to Antithesis in this directory. Maintain this directory as you do Antithesis related work.

**test-composer**
This directory should contain one or more Test Templates. A Test Template is a directory containing Test Command executable files. Each Test Command must have a valid Test Command prefix: `parallel_driver_, singleton_driver_, serial_driver_, first_, eventually_, finally_, anytime_`. Prefixes constrain when and how the Test Composer will compose different commands together in a single timeline.

## Veil-Specific Notes

### Services Under Test

| Service | Scale | Role |
|---|---|---|
| `relay-node` | ×5 | Onion layer peeling, mix-and-forward |
| `validator-node` | ×3 | BFT consensus, message pool ordering |
| `message-pool` | ×1 | Append-only ciphertext store |
| `sender-workload` | ×1 | Test driver: generates and sends messages |
| `receiver-workload` | ×1 | Test driver: polls pool, asserts delivery |

### Test Composer Structure

```
test-composer/veil/
  first_genesis               # bootstrap relay network + validators
  parallel_driver_send        # continuous: random sender patterns
  parallel_driver_receive     # continuous: poll pool, assert delivery
  anytime_invariant_check     # permanent: anonymity set, relay unlinkability, epoch health
  serial_driver_epoch_rotate  # disruptive: force epoch boundary with in-flight messages
  serial_driver_byz_relay     # disruptive: make one relay behave maliciously
  eventually_consistency      # post-fault: all sent messages delivered, pool consistent
  finally_no_leakage          # post-run audit: no identity revealed in any log
```

### Properties to Validate

**Always (Safety)**
- `relay_unlinkability` — No relay's inbound message ID linked to outbound
- `validator_agreement` — All validators agree on batch ordering
- `message_integrity` — No message modified in transit
- `epoch_boundaries` — Epoch ticks never skip or duplicate
- `anonymity_set_size` — Active relay count ≥ threshold k
- `key_scope` — Session key never leaves intended relay context

**Sometimes (Liveness)**
- `message_forwarding` — Messages eventually reach pool
- `chain_progression` — Validator chain commits new batches
- `key_rotation` — Keys rotate at epoch boundary
- `cover_traffic` — Dummy messages injected
- `byzantine_input` — Byzantine relay delivers malicious input

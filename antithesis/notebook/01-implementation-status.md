# Implementation Status

## Overview

Veil is a decentralized anonymous messaging protocol — a mix network with consensus-ordered delivery, designed as an Antithesis testing showcase.

---

## Phase Status Summary

| Phase | Description | Status |
|-------|-------------|--------|
| 0 | Antithesis Bootstrap Scaffold | COMPLETED |
| 1 | Message Pool | COMPLETED |
| 2 | Epoch Clock + Key Rotation | COMPLETED |
| 3 | Consensus Layer | COMPLETED |
| 4 | Relay Layer | COMPLETED |
| 5 | Client Layer | COMPLETED |
| 6 | Byzantine Behaviors | COMPLETED |
| 7 | Workload Driver | COMPLETED |
| 8 | Integration & Wiring | COMPLETED |
| 9a | Build Verification | COMPLETED |
| 9b | Docker Build & Compose | COMPLETED |
| 9c | Notebook Documentation | COMPLETED |
| 9d | Antithesis Cloud Submission | BLOCKED (user credentials required) |

---

## Completed Implementation

### Phase 0: Antithesis Bootstrap Scaffold
- Created `antithesis/` directory structure
- Created AGENTS.md with project-specific notes
- Created notebook directory for planning
- Created entrypoint scripts (setup-complete.sh, submit.sh, test.sh)
- Created config/docker-compose.yaml with all 12 containers
- Created config/Dockerfile for config image
- Created antithesis/Dockerfile for building all services
- Created test-composer/veil/ with all 8 test commands
- Initialized Go module

### Phase 1: Message Pool
- Implemented append-only ciphertext store (`pkg/pool/`)
- Added `message_integrity` property assertion

### Phase 2: Epoch Clock + Key Rotation
- Implemented epoch tick mechanism (`pkg/epoch/clock.go`)
- Implemented key rotation logic (`pkg/epoch/keys.go`)
- Added `epoch_boundaries` and `key_rotation` properties

### Phase 3: Consensus Layer
- Implemented BFT validators (`pkg/consensus/`)
- Added `validator_agreement` and `chain_progression` properties

### Phase 4: Relay Layer
- Implemented onion peeling (`pkg/relay/onion.go`)
- Implemented mix-and-forward (`pkg/relay/mixer.go`)
- Implemented relay network (`pkg/relay/network.go`)
- Added `relay_unlinkability`, `anonymity_set_size`, and `key_scope` properties

### Phase 5: Client Layer
- Implemented sender workload (`pkg/client/sender.go`)
- Implemented receiver workload (`pkg/client/receiver.go`)
- Implemented cover traffic (`pkg/client/cover.go`)
- Added `message_forwarding` and `cover_traffic` properties

### Phase 6: Byzantine Behaviors
- Implemented byzantine relay mode with drop/delay/corrupt behaviors
- Added `byzantine_input` property assertions

### Phase 7: Workload Driver
- Implemented workload binary (`cmd/workload/main.go`)
- Wired sender/receiver modes for test composer

### Phase 8: Integration & Wiring
- Connected all service components
- Verified property assertions are wired at correct locations

### Phase 9a: Build Verification
- `go build ./...` passes
- `go test ./...` passes

### Phase 9b: Docker Build & Compose
- All Docker images built successfully
- docker-compose.yaml verified

### Phase 9c: Notebook Documentation
- Updated implementation status
- Added Phase 9d instructions for user

---

## Antithesis Properties Wired

All 11 Antithesis properties are wired with assertions:

| Property | Type | Location |
|----------|------|----------|
| `message_integrity` | Always | `pkg/pool/server.go:145` |
| `epoch_boundaries` | Always | `pkg/epoch/clock.go:152` |
| `key_rotation` | Sometimes | `pkg/epoch/keys.go:122` |
| `validator_agreement` | Always | `pkg/consensus/validator.go:486` |
| `chain_progression` | Sometimes | `pkg/consensus/validator.go:471` |
| `relay_unlinkability` | Always | `pkg/relay/server.go:606` |
| `anonymity_set_size` | Always | `pkg/relay/server.go:550` |
| `key_scope` | Always | `pkg/relay/server.go:530`, `pkg/relay/server.go:622` |
| `byzantine_input` | Sometimes | `pkg/relay/server.go:285`, `pkg/relay/server.go:303`, `pkg/relay/server.go:337` |
| `message_forwarding` | Sometimes | `pkg/client/receiver.go:153` |
| `cover_traffic` | Sometimes | `pkg/client/cover.go:246` |

---

## Docker Images Built

| Image | Service | Port |
|-------|---------|------|
| `veil-pool:latest` | message-pool | 8080 |
| `veil-validator:latest` | validator (×3) | 9000 |
| `veil-relay:latest` | relay (×5) + relay-byzantine | 7000 |
| `veil-workload:latest` | workload | - |

---

## Test Composer Scripts

All 8 test composer commands are implemented in `antithesis/test-composer/veil/`:

| Script | Purpose |
|--------|---------|
| `first_genesis` | Bootstrap network, verify health endpoints |
| `parallel_driver_send` | Continuous message sending workload |
| `parallel_driver_receive` | Poll pool, verify message delivery |
| `anytime_invariant_check` | Check health, quorum, anonymity set |
| `eventually_consistency` | Verify message delivery consistency |
| `serial_driver_epoch_rotate` | Force epoch rotation, test key boundary |
| `serial_driver_byz_relay` | Inject byzantine relay behaviors |
| `finally_no_leakage` | Audit logs for privacy violations |

---

## Ready for Antithesis

All prerequisites for Antithesis cloud submission are met:

- [x] All service binaries compile and tests pass
- [x] Docker images built for all 4 service types
- [x] docker-compose.yaml defines 12 containers (pool, 3 validators, 5 relays, 1 byzantine relay, workload)
- [x] All 11 Antithesis properties wired with SDK assertions
- [x] All 8 test composer scripts implemented
- [x] Byzantine behaviors implemented for fault injection
- [x] Shared volume for inter-container communication

### Bug Classes Antithesis Will Find

1. **Epoch-boundary race** — message in-flight during key rotation
2. **Consensus partition cascade** — 2/3 quorum violations during partition
3. **Anonymity set collapse** — relay count drops below k=3
4. **Key scope violation on recovery** — stale key usage after crash

---

## Phase 9d: Antithesis Cloud Submission

### Status: BLOCKED — User Action Required

Phase 9d requires Antithesis cloud credentials that must be provided by the user.

### Prerequisites Verified

- [x] `snouty` CLI is installed (`/home/sean/.cargo/bin/snouty`)
- [x] `go build ./...` passes
- [ ] `ANTITHESIS_REPOSITORY` environment variable set
- [ ] Docker authenticated to Antithesis registry

### User Steps to Complete Phase 9d

**Step 1: Set ANTITHESIS_REPOSITORY environment variable**

```bash
export ANTITHESIS_REPOSITORY="us-central1-docker.pkg.dev/<your-project>/<your-repo>"
```

Replace `<your-project>` and `<your-repo>` with your Antithesis registry path.

**Step 2: Authenticate Docker to the registry**

```bash
# For Google Artifact Registry:
gcloud auth configure-docker us-central1-docker.pkg.dev

# Or direct login:
docker login us-central1-docker.pkg.dev
```

**Step 3: Run the submission script**

```bash
./antithesis/submit.sh --duration 30 --desc 'First integration run'
```

This will:
1. Build all 4 Docker images (veil-pool, veil-validator, veil-relay, veil-workload)
2. Tag images for the Antithesis registry
3. Submit to Antithesis cloud via `snouty run`

**Step 4: After run completes**

Use the `antithesis-triage` skill to analyze findings and document bugs in this notebook.

---

## Antithesis Findings

*This section will be populated after Phase 9d: Antithesis Cloud Submission*

| Finding | Severity | Description | Resolution |
|---------|----------|-------------|------------|
| | | | |

---

## Dependencies

- `github.com/antithesishq/antithesis-sdk-go v0.4.4`
- `golang.org/x/crypto v0.21.0` (includes nacl/box)

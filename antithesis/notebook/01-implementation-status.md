# Implementation Status

## Phase 0: Antithesis Bootstrap Scaffold (CURRENT)

### Completed
- [x] Created `antithesis/` directory structure
- [x] Created AGENTS.md with project-specific notes
- [x] Created notebook directory for planning
- [x] Created entrypoint scripts (setup-complete.sh, submit.sh, test.sh)
- [x] Created config/docker-compose.yaml with all 11 containers
- [x] Created config/Dockerfile for config image
- [x] Created antithesis/Dockerfile for building all services
- [x] Created test-composer/veil/ with all 8 test commands
- [x] Initialized Go module

### Pending
- [ ] Implement actual service binaries (Phase 1+)
- [ ] Add Antithesis SDK assertions (Phase 1+)
- [ ] Implement test command logic (Phase 1+)

## Next Phases

### Phase 1: Message Pool
- Implement append-only ciphertext store
- Add `message_integrity` property assertions

### Phase 2: Epoch Clock + Key Rotation
- Implement epoch tick mechanism
- Add `epoch_boundaries`, `key_rotation` properties

### Phase 3: Consensus Layer
- Implement BFT validators
- Add `validator_agreement`, `chain_progression` properties

### Phase 4: Relay Layer
- Implement onion peeling and mix-forward
- Add `relay_unlinkability`, `anonymity_set_size` properties

### Phase 5: Client Layer
- Implement sender/receiver workloads
- Add `message_forwarding`, `cover_traffic` properties

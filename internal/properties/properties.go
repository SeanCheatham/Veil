// Package properties defines Antithesis properties for the Veil protocol.
// These are the system's correctness invariants, expressed as Antithesis
// always/sometimes properties.
package properties

import (
	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// Safety property names (Always - disproved by a single counterexample)
const (
	// RelayUnlinkability asserts that no relay's inbound message ID is ever
	// linked to its outbound message ID in any log or data structure.
	RelayUnlinkability = "relay_unlinkability"

	// ValidatorAgreement asserts that all validators agree on the same batch ordering.
	ValidatorAgreement = "validator_agreement"

	// MessageIntegrity asserts that no message is modified in transit through the pool.
	MessageIntegrity = "message_integrity"

	// EpochBoundaries asserts that epoch ticks never skip or duplicate.
	EpochBoundaries = "epoch_boundaries"

	// AnonymitySetSize asserts that active relay count never drops below threshold k.
	AnonymitySetSize = "anonymity_set_size"

	// KeyScope asserts that no session key material ever leaves its intended relay context.
	KeyScope = "key_scope"
)

// Liveness property names (Sometimes - proved by a single example)
const (
	// MessageForwarding asserts that all submitted messages eventually reach the pool.
	MessageForwarding = "message_forwarding"

	// ChainProgression asserts that the validator chain always commits new batches.
	ChainProgression = "chain_progression"

	// KeyRotation asserts that session keys rotate at each epoch boundary.
	KeyRotation = "key_rotation"

	// CoverTraffic asserts that dummy messages are sometimes injected into the pool.
	CoverTraffic = "cover_traffic"

	// ByzantineInput asserts that the byzantine relay workload sometimes delivers malicious input.
	ByzantineInput = "byzantine_input"
)

// AnonymityThreshold is the minimum number of active relays required
// to maintain anonymity guarantees.
const AnonymityThreshold = 3

// AssertRelayUnlinkability asserts that inbound and outbound message IDs are not linked.
func AssertRelayUnlinkability(condition bool, relayID, msgID string) {
	assert.Always(condition, RelayUnlinkability, map[string]any{
		"relay_id": relayID,
		"msg_id":   msgID,
	})
}

// AssertValidatorAgreement asserts that validators agree on batch ordering.
func AssertValidatorAgreement(condition bool, batchNum uint64, validatorID string) {
	assert.Always(condition, ValidatorAgreement, map[string]any{
		"batch_num":    batchNum,
		"validator_id": validatorID,
	})
}

// AssertMessageIntegrity asserts that a message was not modified in transit.
func AssertMessageIntegrity(condition bool, msgID string, expectedHash, actualHash string) {
	assert.Always(condition, MessageIntegrity, map[string]any{
		"msg_id":        msgID,
		"expected_hash": expectedHash,
		"actual_hash":   actualHash,
	})
}

// AssertEpochBoundaries asserts that epoch transitions are valid (no skips or duplicates).
func AssertEpochBoundaries(condition bool, previousEpoch, currentEpoch uint64) {
	assert.Always(condition, EpochBoundaries, map[string]any{
		"previous_epoch": previousEpoch,
		"current_epoch":  currentEpoch,
	})
}

// AssertAnonymitySetSize asserts that the active relay count is above the threshold.
func AssertAnonymitySetSize(condition bool, activeCount int) {
	assert.Always(condition, AnonymitySetSize, map[string]any{
		"active": activeCount,
		"k":      AnonymityThreshold,
	})
}

// AssertKeyScope asserts that key material stays within its intended context.
func AssertKeyScope(condition bool, keyID, intendedContext, actualContext string) {
	assert.Always(condition, KeyScope, map[string]any{
		"key_id":           keyID,
		"intended_context": intendedContext,
		"actual_context":   actualContext,
	})
}

// ObserveMessageForwarding records when a message successfully reaches the pool.
func ObserveMessageForwarding(condition bool, msgID string) {
	assert.Sometimes(condition, MessageForwarding, map[string]any{
		"msg_id": msgID,
	})
}

// ObserveChainProgression records when a new batch is committed.
func ObserveChainProgression(condition bool, batchNum uint64) {
	assert.Sometimes(condition, ChainProgression, map[string]any{
		"batch_num": batchNum,
	})
}

// ObserveKeyRotation records when session keys are rotated.
func ObserveKeyRotation(condition bool, epoch uint64) {
	assert.Sometimes(condition, KeyRotation, map[string]any{
		"epoch": epoch,
	})
}

// ObserveCoverTraffic records when cover traffic is injected.
func ObserveCoverTraffic(condition bool, batchNum uint64) {
	assert.Sometimes(condition, CoverTraffic, map[string]any{
		"batch_num": batchNum,
	})
}

// ObserveByzantineInput records when byzantine input is delivered.
func ObserveByzantineInput(condition bool, relayID string, inputType string) {
	assert.Sometimes(condition, ByzantineInput, map[string]any{
		"relay_id":   relayID,
		"input_type": inputType,
	})
}

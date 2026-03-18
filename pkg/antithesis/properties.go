// Package antithesis provides Antithesis SDK integration for Veil.
// This file defines all property assertions that Antithesis will validate.
package antithesis

// Property names for Antithesis assertions.
// These correspond to the properties defined in COMPASS.md.
const (
	// Safety properties (Always) - disproved by a single counterexample

	// RelayUnlinkability asserts that no relay's inbound message ID
	// is ever linked to its outbound message ID in any log or data structure.
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

	// Liveness properties (Sometimes) - proved by a single example

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
// for the anonymity set size property to hold.
const AnonymityThreshold = 3

// TODO: Add Antithesis SDK integration
// The actual assertions will be implemented when the Antithesis Go SDK
// is added as a dependency. Example usage:
//
// import "github.com/antithesishq/antithesis-sdk-go/assert"
//
// assert.Always(
//     !relay.InboundLog.Contains(outMsg.ID),
//     RelayUnlinkability,
//     map[string]any{"relay_id": r.ID, "msg_id": outMsg.ID},
// )
//
// assert.Sometimes(
//     pool.LastBatch.ContainsCoverTraffic(),
//     CoverTraffic,
//     nil,
// )

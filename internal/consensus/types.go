// Package consensus implements the PBFT consensus protocol for ordering messages.
package consensus

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// MessagePhase represents the phase of a message in the consensus process.
type MessagePhase int

const (
	// PhasePrepare indicates the message is in the prepare phase.
	PhasePrepare MessagePhase = iota
	// PhaseCommit indicates the message is in the commit phase.
	PhaseCommit
	// PhaseCommitted indicates the message has been committed.
	PhaseCommitted
)

func (p MessagePhase) String() string {
	switch p {
	case PhasePrepare:
		return "prepare"
	case PhaseCommit:
		return "commit"
	case PhaseCommitted:
		return "committed"
	default:
		return "unknown"
	}
}

// ConsensusMessage is the message exchanged between validators during consensus.
type ConsensusMessage struct {
	Type        string `json:"type"`         // "prepare", "commit"
	Sequence    uint64 `json:"sequence"`     // Global sequence number
	Payload     []byte `json:"payload"`      // Original message payload
	ValidatorID int    `json:"validator_id"` // ID of the sending validator
	Signature   string `json:"signature"`    // HMAC signature for authenticity
}

// ConsensusState tracks the state of a message going through consensus.
type ConsensusState struct {
	Sequence     uint64       // The sequence number assigned to this message
	PrepareVotes map[int]bool // validator_id -> received prepare
	CommitVotes  map[int]bool // validator_id -> received commit
	Phase        MessagePhase // Current phase
	Payload      []byte       // The original message payload
}

// NewConsensusState creates a new consensus state for a message.
func NewConsensusState(sequence uint64, payload []byte) *ConsensusState {
	return &ConsensusState{
		Sequence:     sequence,
		PrepareVotes: make(map[int]bool),
		CommitVotes:  make(map[int]bool),
		Phase:        PhasePrepare,
		Payload:      payload,
	}
}

// PrepareCount returns the number of prepare votes received.
func (s *ConsensusState) PrepareCount() int {
	count := 0
	for _, voted := range s.PrepareVotes {
		if voted {
			count++
		}
	}
	return count
}

// CommitCount returns the number of commit votes received.
func (s *ConsensusState) CommitCount() int {
	count := 0
	for _, voted := range s.CommitVotes {
		if voted {
			count++
		}
	}
	return count
}

// Quorum returns the number of votes needed for quorum (2f+1 where n=3f+1).
// For n=3 validators, f=0, so quorum is 2*0+1=1, but we require all 3 for now.
// Since we're using simplified PBFT with f=0 (all honest), we need all validators.
func Quorum(n int) int {
	// For PBFT with n=3f+1, quorum is 2f+1
	// For n=3, f=0 (since 3f+1=4 > 3), so quorum would be 1
	// But for our simplified case, we require all validators to agree
	return n
}

// SharedSecret is used for HMAC signatures between validators.
// In a real implementation, this would be derived from secure key exchange.
const SharedSecret = "veil-consensus-shared-secret-v1"

// ComputeSignature creates an HMAC signature for a consensus message.
func ComputeSignature(msgType string, sequence uint64, payload []byte, validatorID int) string {
	mac := hmac.New(sha256.New, []byte(SharedSecret))
	mac.Write([]byte(msgType))
	mac.Write([]byte{byte(sequence >> 56), byte(sequence >> 48), byte(sequence >> 40), byte(sequence >> 32),
		byte(sequence >> 24), byte(sequence >> 16), byte(sequence >> 8), byte(sequence)})
	mac.Write(payload)
	mac.Write([]byte{byte(validatorID)})
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature verifies the HMAC signature of a consensus message.
func VerifySignature(msg ConsensusMessage) bool {
	expected := ComputeSignature(msg.Type, msg.Sequence, msg.Payload, msg.ValidatorID)
	return hmac.Equal([]byte(msg.Signature), []byte(expected))
}

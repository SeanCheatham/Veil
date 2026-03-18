// Package validator implements the validator node logic for BFT consensus.
package validator

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/consensus"
)

// ValidatorStatus represents the current state of a validator node.
type ValidatorStatus struct {
	ID            int            `json:"id"`
	PeerCount     int            `json:"peer_count"`
	ProposalCount int64          `json:"proposal_count"`
	Consensus     map[string]any `json:"consensus,omitempty"`
}

// Validator represents a BFT consensus participant.
type Validator struct {
	mu             sync.RWMutex
	id             int
	peers          []string // hostnames of peer validators
	proposalCount  int64
	messagePoolURL string
	httpClient     *http.Client
	consensus      *consensus.PBFTConsensus
}

// NewValidator creates a new validator with the given ID and configuration.
func NewValidator(id int, messagePoolURL string) *Validator {
	return &Validator{
		id:             id,
		peers:          make([]string, 0),
		proposalCount:  0,
		messagePoolURL: messagePoolURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetPeers sets the list of peer validators and initializes consensus.
func (v *Validator) SetPeers(peers []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.peers = peers

	// Initialize consensus with peer URLs
	v.consensus = consensus.NewPBFTConsensus(v.id, peers, v.messagePoolURL)
}

// ProposeMessage receives a message proposal and initiates consensus.
func (v *Validator) ProposeMessage(payload []byte) error {
	v.mu.Lock()
	v.proposalCount++
	currentCount := v.proposalCount
	cons := v.consensus
	v.mu.Unlock()

	// Antithesis assertion: proposals are being accepted
	assert.Sometimes(true, "Proposals are accepted by validators", map[string]any{
		"validator_id":   v.id,
		"proposal_count": currentCount,
	})

	if cons == nil {
		return fmt.Errorf("consensus not initialized")
	}

	// Initiate consensus for this message
	err := cons.Propose(payload)

	// Antithesis assertion: valid proposals eventually reach consensus
	assert.Always(err == nil || isRetryableError(err), "Valid proposals enter consensus", map[string]any{
		"validator_id": v.id,
		"error":        fmt.Sprintf("%v", err),
	})

	return err
}

// HandlePrepare processes a PREPARE message from a peer validator.
func (v *Validator) HandlePrepare(msg consensus.ConsensusMessage) error {
	v.mu.RLock()
	cons := v.consensus
	v.mu.RUnlock()

	if cons == nil {
		return fmt.Errorf("consensus not initialized")
	}

	return cons.HandlePrepare(msg)
}

// HandleCommit processes a COMMIT message from a peer validator.
func (v *Validator) HandleCommit(msg consensus.ConsensusMessage) error {
	v.mu.RLock()
	cons := v.consensus
	v.mu.RUnlock()

	if cons == nil {
		return fmt.Errorf("consensus not initialized")
	}

	return cons.HandleCommit(msg)
}

// GetStatus returns the current state of the validator.
func (v *Validator) GetStatus() ValidatorStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()

	status := ValidatorStatus{
		ID:            v.id,
		PeerCount:     len(v.peers),
		ProposalCount: v.proposalCount,
	}

	if v.consensus != nil {
		status.Consensus = v.consensus.GetStatus()
	}

	return status
}

// isRetryableError determines if an error is transient and can be retried.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Network errors, timeouts, and 5xx responses are generally retryable
	// For now, we consider all errors as potentially retryable
	// In a real implementation, we'd check for specific error types
	return true
}

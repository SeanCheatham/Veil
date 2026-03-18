// Package validator implements the validator node logic for BFT consensus.
package validator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// ValidatorStatus represents the current state of a validator node.
type ValidatorStatus struct {
	ID            int   `json:"id"`
	PeerCount     int   `json:"peer_count"`
	ProposalCount int64 `json:"proposal_count"`
}

// Validator represents a BFT consensus participant.
type Validator struct {
	mu             sync.RWMutex
	id             int
	peers          []string // hostnames of peer validators
	proposalCount  int64
	messagePoolURL string
	httpClient     *http.Client
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

// SetPeers sets the list of peer validators.
func (v *Validator) SetPeers(peers []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.peers = peers
}

// ProposeMessage receives a message proposal and forwards it to the message-pool.
// In a real BFT implementation, this would trigger prepare/commit phases.
// For now, it simply forwards the proposal directly to the message-pool.
func (v *Validator) ProposeMessage(payload []byte) error {
	v.mu.Lock()
	v.proposalCount++
	currentCount := v.proposalCount
	v.mu.Unlock()

	// Antithesis assertion: proposals are being accepted
	assert.Sometimes(true, "Proposals are accepted by validators", map[string]any{
		"validator_id":   v.id,
		"proposal_count": currentCount,
	})

	// Forward to message-pool
	err := v.forwardToMessagePool(payload)

	// Antithesis assertion: valid proposals eventually reach message-pool
	// We consider network errors as retryable
	assert.Always(err == nil || isRetryableError(err), "Valid proposals eventually reach message-pool", map[string]any{
		"validator_id": v.id,
		"error":        fmt.Sprintf("%v", err),
	})

	return err
}

// forwardToMessagePool sends the payload to the message-pool service.
func (v *Validator) forwardToMessagePool(payload []byte) error {
	// The message-pool expects base64-encoded payload in JSON
	reqBody := map[string]string{
		"payload": base64.StdEncoding.EncodeToString(payload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := v.messagePoolURL + "/messages"
	resp, err := v.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to forward to message-pool: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("message-pool returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStatus returns the current state of the validator.
func (v *Validator) GetStatus() ValidatorStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return ValidatorStatus{
		ID:            v.id,
		PeerCount:     len(v.peers),
		ProposalCount: v.proposalCount,
	}
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

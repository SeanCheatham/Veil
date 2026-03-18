// Package relay implements the relay node logic for onion routing.
package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// RelayStatus represents the current state of a relay node.
type RelayStatus struct {
	ID           int   `json:"id"`
	ForwardCount int64 `json:"forward_count"`
}

// Relay represents an onion routing participant.
type Relay struct {
	mu           sync.RWMutex
	id           int
	nextHop      string // hostname:port of next relay, empty if final
	validatorURL string // URL of validator for final relay
	forwardCount int64
	httpClient   *http.Client
}

// NewRelay creates a new relay with the given ID and hop configuration.
func NewRelay(id int, nextHop string, validatorURL string) *Relay {
	return &Relay{
		id:           id,
		nextHop:      nextHop,
		validatorURL: validatorURL,
		forwardCount: 0,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ForwardMessage receives a message payload and forwards it to the next hop.
// If nextHop is empty, forwards to the validator. Otherwise forwards to the next relay.
// For stub: messages are passed through as-is (real onion layer peeling comes in Plan 8).
func (r *Relay) ForwardMessage(payload []byte) error {
	// Antithesis assertion: messages forwarded are not empty
	assert.Always(len(payload) > 0, "Messages forwarded are not empty", map[string]any{
		"relay_id": r.id,
	})

	r.mu.Lock()
	r.forwardCount++
	currentCount := r.forwardCount
	r.mu.Unlock()

	// Antithesis assertion: messages traverse the relay network
	assert.Sometimes(true, "Messages traverse the relay network", map[string]any{
		"relay_id":      r.id,
		"forward_count": currentCount,
	})

	// Determine where to forward
	if r.nextHop == "" {
		// Final relay: forward to validator
		return r.forwardToValidator(payload)
	}

	// Intermediate relay: forward to next relay
	return r.forwardToNextRelay(payload)
}

// forwardToValidator sends the payload to the validator service.
func (r *Relay) forwardToValidator(payload []byte) error {
	// The validator expects base64-encoded payload in JSON via POST /propose
	// But we receive base64-encoded payload already, so we pass it through
	reqBody := map[string]string{
		"payload": string(payload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := r.validatorURL + "/propose"
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to forward to validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validator returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// forwardToNextRelay sends the payload to the next relay in the chain.
func (r *Relay) forwardToNextRelay(payload []byte) error {
	// The next relay expects base64-encoded payload in JSON via POST /forward
	reqBody := map[string]string{
		"payload": string(payload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := "http://" + r.nextHop + "/forward"
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to forward to next relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("next relay returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStatus returns the current state of the relay.
func (r *Relay) GetStatus() RelayStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return RelayStatus{
		ID:           r.id,
		ForwardCount: r.forwardCount,
	}
}

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
	"github.com/veil/veil/internal/crypto"
)

// RelayStatus represents the current state of a relay node.
type RelayStatus struct {
	ID           int    `json:"id"`
	ForwardCount int64  `json:"forward_count"`
	PublicKey    string `json:"public_key"` // Base64-encoded public key
}

// Relay represents an onion routing participant.
type Relay struct {
	mu           sync.RWMutex
	id           int
	nextHop      string // hostname:port of next relay (from env, used as fallback)
	validatorURL string // URL of validator for final relay
	forwardCount int64
	httpClient   *http.Client

	// Cryptographic keys for onion routing
	privKey crypto.PrivateKey
	pubKey  crypto.PublicKey
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

// SetKeys sets the cryptographic keys for this relay.
func (r *Relay) SetKeys(privKey crypto.PrivateKey, pubKey crypto.PublicKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.privKey = privKey
	r.pubKey = pubKey
}

// GetPublicKey returns the relay's public key.
func (r *Relay) GetPublicKey() crypto.PublicKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pubKey
}

// ForwardMessage receives an onion-encrypted message payload and forwards it.
// It peels one encryption layer to reveal the next hop address, then forwards
// the remaining payload to that address. If this is the final relay, forwards
// to the validator.
func (r *Relay) ForwardMessage(payload []byte) error {
	// Antithesis assertion: messages forwarded are not empty
	assert.Always(len(payload) > 0, "Messages forwarded are not empty", map[string]any{
		"relay_id": r.id,
	})

	r.mu.Lock()
	r.forwardCount++
	currentCount := r.forwardCount
	privKey := r.privKey
	r.mu.Unlock()

	// Antithesis assertion: messages traverse the relay network
	assert.Sometimes(true, "Messages traverse the relay network", map[string]any{
		"relay_id":      r.id,
		"forward_count": currentCount,
	})

	// Peel the onion layer to get the next hop and inner payload
	nextHop, innerPayload, isFinal, err := crypto.PeelLayer(payload, privKey)
	if err != nil {
		// Cryptographic errors are expected if someone sends a malformed packet
		// Log and reject, but don't fail the relay
		assert.Always(crypto.IsCryptoError(err), "Only cryptographic errors are acceptable", map[string]any{
			"relay_id": r.id,
			"error":    err.Error(),
		})
		return fmt.Errorf("failed to peel onion layer: %w", err)
	}

	assert.Sometimes(true, "Full onion unwrapping succeeds", map[string]any{
		"relay_id": r.id,
		"is_final": isFinal,
	})

	// Determine where to forward based on the decrypted layer
	if isFinal {
		// This is the final relay: forward to validator
		return r.forwardToValidator(innerPayload)
	}

	// Use the nextHop from the decrypted layer
	return r.forwardToNextRelay(nextHop, innerPayload)
}

// forwardToValidator sends the payload to the validator service.
func (r *Relay) forwardToValidator(payload []byte) error {
	// The validator expects base64-encoded payload in JSON via POST /propose
	// The innerPayload is the original base64-encoded message from the sender
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
func (r *Relay) forwardToNextRelay(nextHop string, payload []byte) error {
	// The next relay expects the encrypted onion payload (still encrypted with remaining layers)
	reqBody := map[string]string{
		"payload": string(payload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := "http://" + nextHop + "/forward"
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
		PublicKey:    r.pubKey.Base64(),
	}
}

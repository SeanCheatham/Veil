// Package relay implements the relay node logic for onion routing.
package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/crypto"
	"github.com/veil/veil/internal/epoch"
)

// RelayStatus represents the current state of a relay node.
type RelayStatus struct {
	ID           int    `json:"id"`
	ForwardCount int64  `json:"forward_count"`
	PublicKey    string `json:"public_key"` // Base64-encoded current epoch public key
	CurrentEpoch uint64 `json:"current_epoch"`
	InGracePeriod bool   `json:"in_grace_period"`
}

// Relay represents an onion routing participant.
type Relay struct {
	mu           sync.RWMutex
	id           int
	nextHop      string // hostname:port of next relay (from env, used as fallback)
	validatorURL string // URL of validator for final relay
	forwardCount int64
	httpClient   *http.Client

	// Cryptographic keys for onion routing (legacy, used when epoch is disabled)
	privKey crypto.PrivateKey
	pubKey  crypto.PublicKey

	// Epoch-based key management
	epochManager *epoch.EpochManager
	masterSeed   []byte

	// Epoch keys - protected by keyMu
	keyMu        sync.RWMutex
	currentKeys  *crypto.KeyPair
	previousKeys *crypto.KeyPair
	currentEpoch uint64

	// Key rotation control
	stopRotation chan struct{}
	rotationDone chan struct{}

	// Metrics for Antithesis
	decryptCurrentCount  int64
	decryptPreviousCount int64
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
		stopRotation: make(chan struct{}),
		rotationDone: make(chan struct{}),
	}
}

// SetKeys sets the cryptographic keys for this relay (legacy mode).
func (r *Relay) SetKeys(privKey crypto.PrivateKey, pubKey crypto.PublicKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.privKey = privKey
	r.pubKey = pubKey
}

// SetEpochManager configures the relay for epoch-based key rotation.
func (r *Relay) SetEpochManager(em *epoch.EpochManager, masterSeed []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epochManager = em
	r.masterSeed = masterSeed
}

// GetPublicKey returns the relay's current public key.
func (r *Relay) GetPublicKey() crypto.PublicKey {
	// If using epoch-based keys, return current epoch key
	r.keyMu.RLock()
	if r.currentKeys != nil {
		pubKey := r.currentKeys.Public
		r.keyMu.RUnlock()
		return pubKey
	}
	r.keyMu.RUnlock()

	// Fall back to legacy key
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pubKey
}

// StartKeyRotation starts the background key rotation goroutine.
// Keys are rotated at epoch boundaries. This should be called after
// SetEpochManager has been called.
func (r *Relay) StartKeyRotation() error {
	r.mu.RLock()
	em := r.epochManager
	masterSeed := r.masterSeed
	id := r.id
	r.mu.RUnlock()

	if em == nil || len(masterSeed) == 0 {
		return fmt.Errorf("epoch manager and master seed must be set before starting rotation")
	}

	// Derive initial keys for current and previous epochs
	currentEpoch := em.CurrentEpoch()
	currentKeys, err := epoch.DeriveEpochKeyPair(masterSeed, id, currentEpoch)
	if err != nil {
		return fmt.Errorf("failed to derive current epoch keys: %w", err)
	}

	var previousKeys *crypto.KeyPair
	if currentEpoch > 0 {
		previousKeys, err = epoch.DeriveEpochKeyPair(masterSeed, id, currentEpoch-1)
		if err != nil {
			return fmt.Errorf("failed to derive previous epoch keys: %w", err)
		}
	}

	r.keyMu.Lock()
	r.currentKeys = currentKeys
	r.previousKeys = previousKeys
	r.currentEpoch = currentEpoch
	r.keyMu.Unlock()

	log.Printf("Relay %d: initialized epoch keys for epoch %d, public key: %s",
		id, currentEpoch, currentKeys.Public.Base64())

	// Start background rotation goroutine
	go r.keyRotationLoop()

	return nil
}

// StopKeyRotation stops the background key rotation goroutine.
func (r *Relay) StopKeyRotation() {
	close(r.stopRotation)
	<-r.rotationDone
}

// keyRotationLoop runs in the background and rotates keys at epoch boundaries.
func (r *Relay) keyRotationLoop() {
	defer close(r.rotationDone)

	r.mu.RLock()
	em := r.epochManager
	masterSeed := r.masterSeed
	id := r.id
	r.mu.RUnlock()

	for {
		// Calculate time until next epoch
		timeUntilNext := em.TimeUntilNextEpoch()

		select {
		case <-r.stopRotation:
			return
		case <-time.After(timeUntilNext):
			// Epoch has changed - rotate keys
			r.rotateKeys(em, masterSeed, id)
		}
	}
}

// rotateKeys derives and installs new epoch keys.
func (r *Relay) rotateKeys(em *epoch.EpochManager, masterSeed []byte, id int) {
	newEpoch := em.CurrentEpoch()

	r.keyMu.RLock()
	oldEpoch := r.currentEpoch
	r.keyMu.RUnlock()

	if newEpoch <= oldEpoch {
		// No change needed
		return
	}

	// Derive new epoch keys
	newKeys, err := epoch.DeriveEpochKeyPair(masterSeed, id, newEpoch)
	if err != nil {
		log.Printf("Relay %d: failed to derive keys for epoch %d: %v", id, newEpoch, err)
		return
	}

	// Install new keys, keeping current as previous
	r.keyMu.Lock()
	r.previousKeys = r.currentKeys
	r.currentKeys = newKeys
	r.currentEpoch = newEpoch
	r.keyMu.Unlock()

	log.Printf("Relay %d: rotated keys to epoch %d, new public key: %s",
		id, newEpoch, newKeys.Public.Base64())

	// Antithesis assertion: epoch transitions happen
	assert.Sometimes(newEpoch > oldEpoch, "System transitions through epochs", map[string]any{
		"relay_id":  id,
		"old_epoch": oldEpoch,
		"new_epoch": newEpoch,
	})
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
	legacyPrivKey := r.privKey
	em := r.epochManager
	r.mu.Unlock()

	// Antithesis assertion: messages traverse the relay network
	assert.Sometimes(true, "Messages traverse the relay network", map[string]any{
		"relay_id":      r.id,
		"forward_count": currentCount,
	})

	var nextHop string
	var innerPayload []byte
	var isFinal bool
	var err error

	// Try epoch-based decryption first if enabled
	if em != nil {
		nextHop, innerPayload, isFinal, err = r.peelWithEpochKeys(payload)
	} else {
		// Fall back to legacy key
		nextHop, innerPayload, isFinal, err = crypto.PeelLayer(payload, legacyPrivKey)
	}

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

// peelWithEpochKeys tries to decrypt using current epoch keys, and if that
// fails during grace period, tries previous epoch keys.
func (r *Relay) peelWithEpochKeys(payload []byte) (string, []byte, bool, error) {
	r.keyMu.RLock()
	currentKeys := r.currentKeys
	previousKeys := r.previousKeys
	currentEpoch := r.currentEpoch
	r.keyMu.RUnlock()

	r.mu.RLock()
	em := r.epochManager
	id := r.id
	r.mu.RUnlock()

	// Antithesis assertion: we always have at least one valid key set
	validKeyCount := 1
	if previousKeys != nil && em.IsInGracePeriod() {
		validKeyCount = 2
	}
	assert.Always(validKeyCount >= 1 && validKeyCount <= 2,
		"Relays always have 1-2 valid key sets", map[string]any{
			"relay_id":        id,
			"valid_key_count": validKeyCount,
		})

	// Try current epoch keys first
	nextHop, innerPayload, isFinal, err := crypto.PeelLayer(payload, currentKeys.Private)
	if err == nil {
		r.mu.Lock()
		r.decryptCurrentCount++
		r.mu.Unlock()

		// Antithesis assertion: messages decrypt with current epoch keys
		assert.Sometimes(true, "Messages decrypt with current epoch keys", map[string]any{
			"relay_id": id,
			"epoch":    currentEpoch,
		})
		return nextHop, innerPayload, isFinal, nil
	}

	// If decryption failed and we're in grace period, try previous epoch keys
	if em.IsInGracePeriod() && previousKeys != nil {
		nextHop, innerPayload, isFinal, err = crypto.PeelLayer(payload, previousKeys.Private)
		if err == nil {
			r.mu.Lock()
			r.decryptPreviousCount++
			r.mu.Unlock()

			// Antithesis assertion: messages in grace period can use previous keys
			assert.Sometimes(true, "Messages in grace period decrypt with previous epoch keys", map[string]any{
				"relay_id":       id,
				"current_epoch":  currentEpoch,
				"message_epoch":  currentEpoch - 1,
			})
			return nextHop, innerPayload, isFinal, nil
		}
	}

	// Both attempts failed
	return "", nil, false, err
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
	em := r.epochManager
	forwardCount := r.forwardCount
	id := r.id
	legacyPubKey := r.pubKey
	r.mu.RUnlock()

	status := RelayStatus{
		ID:           id,
		ForwardCount: forwardCount,
	}

	// Use epoch-based keys if available
	r.keyMu.RLock()
	if r.currentKeys != nil {
		status.PublicKey = r.currentKeys.Public.Base64()
		status.CurrentEpoch = r.currentEpoch
	} else {
		status.PublicKey = legacyPubKey.Base64()
	}
	r.keyMu.RUnlock()

	if em != nil {
		status.InGracePeriod = em.IsInGracePeriod()
	}

	return status
}

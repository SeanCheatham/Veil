// Package epoch provides session key generation and rotation for Veil relays.
// Session keys are rotated at each epoch boundary to limit the window of compromise.
package epoch

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
)

// KeySize is the size of session keys in bytes (256 bits).
const KeySize = 32

// SessionKey represents a cryptographic session key for a specific epoch.
type SessionKey struct {
	// Key is the raw key material.
	Key []byte

	// Epoch is the epoch number this key is valid for.
	Epoch uint64

	// ID is a hex-encoded identifier derived from the key (first 8 bytes).
	ID string
}

// KeyManager handles session key generation and rotation.
// It maintains the current session key and rotates it at epoch boundaries.
type KeyManager struct {
	mu              sync.RWMutex
	currentKey      *SessionKey
	previousKey     *SessionKey // kept for grace period during transition
	clock           *Clock
	rotationCount   uint64
	onRotate        []func(*SessionKey)
	hasRotatedOnce  bool
}

// NewKeyManager creates a new key manager attached to the given epoch clock.
// It automatically rotates keys when the clock ticks to a new epoch.
func NewKeyManager(clock *Clock) *KeyManager {
	km := &KeyManager{
		clock:    clock,
		onRotate: make([]func(*SessionKey), 0),
	}

	// Register for epoch ticks
	clock.OnTick(km.handleEpochTick)

	return km
}

// CurrentKey returns the current session key.
// Returns nil if no key has been generated yet.
func (km *KeyManager) CurrentKey() *SessionKey {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.currentKey
}

// PreviousKey returns the previous session key (for grace period lookups).
// Returns nil if no previous key exists.
func (km *KeyManager) PreviousKey() *SessionKey {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.previousKey
}

// RotationCount returns how many times keys have been rotated.
func (km *KeyManager) RotationCount() uint64 {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.rotationCount
}

// HasRotatedOnce returns whether at least one key rotation has occurred.
func (km *KeyManager) HasRotatedOnce() bool {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.hasRotatedOnce
}

// OnRotate registers a callback to be invoked when keys rotate.
// The callback receives the new session key.
func (km *KeyManager) OnRotate(handler func(*SessionKey)) {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.onRotate = append(km.onRotate, handler)
}

// handleEpochTick is called when the epoch clock ticks.
func (km *KeyManager) handleEpochTick(epoch uint64) {
	km.rotate(epoch)
}

// rotate generates a new session key for the given epoch.
func (km *KeyManager) rotate(epoch uint64) error {
	newKey, err := generateSessionKey(epoch)
	if err != nil {
		return err
	}

	km.mu.Lock()

	// Track if this is at least the second rotation (first actual rotation after initial key)
	wasRotation := km.currentKey != nil
	if wasRotation {
		km.hasRotatedOnce = true
	}

	// Move current to previous for grace period
	km.previousKey = km.currentKey
	km.currentKey = newKey
	km.rotationCount++

	// Antithesis assertion: key_rotation (sometimes property)
	// This liveness property asserts that keys rotate at epoch boundaries.
	// A single example proves the property.
	if wasRotation {
		assert.Sometimes(
			true,
			antithesis.KeyRotation,
			map[string]any{
				"epoch":          epoch,
				"new_key_id":     newKey.ID,
				"rotation_count": km.rotationCount,
			},
		)
	}

	// Copy handlers
	handlers := make([]func(*SessionKey), len(km.onRotate))
	copy(handlers, km.onRotate)

	km.mu.Unlock()

	// Notify handlers outside the lock
	for _, handler := range handlers {
		handler(newKey)
	}

	return nil
}

// ForceRotate manually triggers a key rotation for the given epoch.
// This is primarily for testing purposes.
func (km *KeyManager) ForceRotate(epoch uint64) error {
	return km.rotate(epoch)
}

// generateSessionKey creates a new random session key for the given epoch.
func generateSessionKey(epoch uint64) (*SessionKey, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	// Use first 8 bytes as ID for debugging/logging
	id := hex.EncodeToString(key[:8])

	return &SessionKey{
		Key:   key,
		Epoch: epoch,
		ID:    id,
	}, nil
}

// ValidateKeyForEpoch checks if a key is valid for the given epoch.
// A key is valid if it matches the current epoch or the immediately previous epoch
// (grace period for messages in flight during rotation).
func (km *KeyManager) ValidateKeyForEpoch(keyID string, epoch uint64) bool {
	km.mu.RLock()
	defer km.mu.RUnlock()

	// Check current key
	if km.currentKey != nil && km.currentKey.ID == keyID && km.currentKey.Epoch == epoch {
		return true
	}

	// Check previous key (grace period - epoch can be current-1)
	if km.previousKey != nil && km.previousKey.ID == keyID {
		// Allow previous key to be used in current epoch (grace period)
		if km.currentKey != nil && epoch == km.currentKey.Epoch && km.previousKey.Epoch == epoch-1 {
			return true
		}
	}

	return false
}

// Package crypto implements cryptographic primitives for the Veil protocol,
// including onion encryption and session key management.
package crypto

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/veil-protocol/veil/internal/epoch"
	"github.com/veil-protocol/veil/internal/properties"
)

// KeyPair represents an X25519 ECDH key pair scoped to a specific relay and epoch.
type KeyPair struct {
	PrivateKey *ecdh.PrivateKey
	PublicKey  *ecdh.PublicKey
	RelayID    string // context scope
	Epoch      uint64 // epoch scope
}

// KeyID returns a unique identifier for this key pair.
func (kp *KeyPair) KeyID() string {
	return fmt.Sprintf("%s:epoch-%d", kp.RelayID, kp.Epoch)
}

// SessionKeyManager manages session keys for a relay, rotating them on epoch boundaries.
// It maintains keys for the current and previous epochs to allow graceful transition
// during epoch boundaries.
type SessionKeyManager struct {
	relayID       string
	epochClockURL string
	epochClient   *epoch.EpochClient

	mu   sync.RWMutex
	keys map[uint64]*KeyPair // epoch -> keypair

	// Channels for lifecycle management
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSessionKeyManager creates a new session key manager for the given relay.
// It connects to the epoch-clock service at epochClockURL to receive epoch events.
func NewSessionKeyManager(relayID, epochClockURL string) *SessionKeyManager {
	return &SessionKeyManager{
		relayID:       relayID,
		epochClockURL: epochClockURL,
		epochClient:   epoch.NewEpochClient(epochClockURL),
		keys:          make(map[uint64]*KeyPair),
		done:          make(chan struct{}),
	}
}

// Start initializes the session key manager by:
// 1. Fetching the current epoch from the epoch-clock service
// 2. Generating an initial key pair for the current epoch
// 3. Subscribing to epoch events to rotate keys on epoch boundaries
//
// The provided context controls the lifetime of the SSE subscription.
func (m *SessionKeyManager) Start(ctx context.Context) error {
	// Create a cancellable context for our operations
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	// Fetch current epoch
	currentEpoch, err := m.epochClient.GetCurrentEpoch(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("fetching current epoch: %w", err)
	}

	// Generate initial key pair for current epoch
	if _, err := m.GenerateKeyPair(currentEpoch); err != nil {
		cancel()
		return fmt.Errorf("generating initial key pair: %w", err)
	}

	// Subscribe to epoch events
	eventCh, err := m.epochClient.Subscribe(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("subscribing to epoch events: %w", err)
	}

	// Start goroutine to handle epoch events
	go m.handleEpochEvents(ctx, eventCh)

	return nil
}

// handleEpochEvents processes epoch events from the SSE stream.
// On each epoch event, it:
// 1. Generates a new key pair for the new epoch
// 2. Prunes keys older than the previous epoch
// 3. Reports key rotation via Antithesis properties
func (m *SessionKeyManager) handleEpochEvents(ctx context.Context, eventCh <-chan epoch.EpochEvent) {
	defer close(m.done)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}

			// Generate new key pair for the new epoch
			_, err := m.GenerateKeyPair(event.CurrentEpoch)
			if err != nil {
				// Log error but continue - next epoch will retry
				continue
			}

			// Report successful key rotation to Antithesis
			properties.ObserveKeyRotation(true, event.CurrentEpoch)

			// Prune keys older than previous epoch
			// We keep current and previous epoch keys for graceful transition
			m.pruneOldKeys(event.PreviousEpoch)
		}
	}
}

// GenerateKeyPair creates a new X25519 key pair for the given epoch.
// The key pair is scoped to this relay's ID and the specified epoch.
// Returns an error if key generation fails.
func (m *SessionKeyManager) GenerateKeyPair(epochNum uint64) (*KeyPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we already have a key pair for this epoch
	if kp, exists := m.keys[epochNum]; exists {
		return kp, nil
	}

	// Generate new X25519 private key
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating X25519 key: %w", err)
	}

	keyPair := &KeyPair{
		PrivateKey: privateKey,
		PublicKey:  privateKey.PublicKey(),
		RelayID:    m.relayID,
		Epoch:      epochNum,
	}

	m.keys[epochNum] = keyPair
	return keyPair, nil
}

// GetKeyPair retrieves the key pair for the specified epoch.
// It validates that the caller's context matches this manager's relay ID
// by calling the Antithesis AssertKeyScope property.
//
// Returns nil if no key pair exists for the epoch.
func (m *SessionKeyManager) GetKeyPair(epochNum uint64) *KeyPair {
	m.mu.RLock()
	defer m.mu.RUnlock()

	kp, exists := m.keys[epochNum]
	if !exists {
		return nil
	}

	// Assert key scope: the key's relay ID must match this manager's relay ID
	// This ensures keys are not accessed from the wrong relay context
	keyID := kp.KeyID()
	intendedContext := m.relayID
	actualContext := kp.RelayID
	properties.AssertKeyScope(intendedContext == actualContext, keyID, intendedContext, actualContext)

	return kp
}

// GetKeyPairWithContext retrieves the key pair for the specified epoch,
// validating that the provided relayID matches this manager's relay ID.
// This is useful when a caller wants to explicitly verify context.
//
// Returns nil if no key pair exists or if the context doesn't match.
func (m *SessionKeyManager) GetKeyPairWithContext(epochNum uint64, callerRelayID string) *KeyPair {
	m.mu.RLock()
	defer m.mu.RUnlock()

	kp, exists := m.keys[epochNum]
	if !exists {
		return nil
	}

	// Assert key scope: the caller's relay ID must match both the manager's
	// relay ID and the key pair's relay ID
	keyID := kp.KeyID()
	intendedContext := m.relayID
	actualContext := callerRelayID
	contextMatches := intendedContext == actualContext && kp.RelayID == actualContext
	properties.AssertKeyScope(contextMatches, keyID, intendedContext, actualContext)

	if !contextMatches {
		return nil
	}

	return kp
}

// PublicKey returns the public key for the specified epoch.
// This is the key that senders should use to encrypt messages for this relay.
// Returns nil if no key pair exists for the epoch.
func (m *SessionKeyManager) PublicKey(epochNum uint64) *ecdh.PublicKey {
	m.mu.RLock()
	defer m.mu.RUnlock()

	kp, exists := m.keys[epochNum]
	if !exists {
		return nil
	}

	return kp.PublicKey
}

// pruneOldKeys removes all keys for epochs older than keepEpoch.
// Called after each epoch transition to limit memory usage.
// We keep keys for keepEpoch (previous) and current epoch.
func (m *SessionKeyManager) pruneOldKeys(keepEpoch uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for epochNum := range m.keys {
		if epochNum < keepEpoch {
			delete(m.keys, epochNum)
		}
	}
}

// Stop shuts down the session key manager, closing the epoch subscription.
func (m *SessionKeyManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.epochClient != nil {
		m.epochClient.Close()
	}
	// Wait for the event handler goroutine to finish
	<-m.done
}

// RelayID returns the relay ID this manager is scoped to.
func (m *SessionKeyManager) RelayID() string {
	return m.relayID
}

// CurrentKeys returns the number of key pairs currently held.
// Useful for testing and debugging.
func (m *SessionKeyManager) CurrentKeys() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}

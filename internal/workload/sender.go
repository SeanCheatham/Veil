// Package workload implements Antithesis test drivers for the Veil network.
package workload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil/veil/internal/crypto"
	"github.com/veil/veil/internal/epoch"
)

// Sender is an Antithesis test driver that generates and sends messages
// through the Veil relay network using onion encryption.
type Sender struct {
	relayURL   string
	httpClient *http.Client
	messageID  atomic.Int64

	// Relay public keys for onion wrapping (legacy mode)
	relayPubKeys []crypto.PublicKey
	relayHops    []string

	// Epoch-based key management
	epochManager     *epoch.EpochManager
	relayMasterSeeds [][]byte

	// Cached epoch keys - protected by keyMu
	keyMu            sync.RWMutex
	cachedEpoch      uint64
	cachedRelayKeys  []crypto.PublicKey
}

// NewSender creates a new Sender with the given relay URL.
func NewSender(relayURL string) *Sender {
	return &Sender{
		relayURL: relayURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		relayPubKeys: crypto.GetRelayPublicKeys(),
		relayHops:    crypto.GetRelayHops(),
	}
}

// SetEpochManager configures the sender for epoch-based key derivation.
func (s *Sender) SetEpochManager(em *epoch.EpochManager, relayMasterSeeds [][]byte) {
	s.epochManager = em
	s.relayMasterSeeds = relayMasterSeeds
}

// GenerateTestMessage creates an identifiable test payload with format VEIL-MSG-{id}-{timestamp}.
func (s *Sender) GenerateTestMessage(id int) []byte {
	timestamp := time.Now().UnixNano()
	payload := []byte(fmt.Sprintf("VEIL-MSG-%d-%d", id, timestamp))

	// Antithesis assertion: generated messages have valid structure
	assert.Always(len(payload) > 0, "Generated messages have valid structure", map[string]any{
		"message_id":  id,
		"payload_len": len(payload),
	})

	return payload
}

// SendMessage encrypts the payload in onion layers and POSTs it to the first relay node.
// The payload is first base64-encoded, then wrapped in onion encryption for each relay.
func (s *Sender) SendMessage(payload []byte) error {
	msgID := s.messageID.Add(1)

	// Base64 encode the payload (this is what the receiver will decode after all layers are peeled)
	encodedPayload := base64.StdEncoding.EncodeToString(payload)

	// Get the appropriate relay keys for wrapping
	relayKeys, err := s.getRelayKeys()
	if err != nil {
		return fmt.Errorf("failed to get relay keys: %w", err)
	}

	// Wrap the message in onion encryption layers
	wrappedPayload, err := crypto.WrapMessage([]byte(encodedPayload), relayKeys, s.relayHops)
	if err != nil {
		return fmt.Errorf("failed to wrap message in onion: %w", err)
	}

	// Build the request body
	// The wrapped payload is binary, so we send it as a string (JSON will handle escaping)
	reqBody := map[string]string{
		"payload": string(wrappedPayload),
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := s.relayURL + "/forward"
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(jsonBody))

	// Antithesis assertion: sender successfully submits messages
	assert.Sometimes(err == nil, "Sender successfully submits messages", map[string]any{
		"message_id": msgID,
		"relay_url":  s.relayURL,
	})

	if err != nil {
		return fmt.Errorf("failed to send message to relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("relay returned unexpected status %d", resp.StatusCode)
	}

	return nil
}

// getRelayKeys returns the relay public keys to use for wrapping.
// If epoch management is enabled, derives keys for the current epoch.
// Otherwise, falls back to static keys.
func (s *Sender) getRelayKeys() ([]crypto.PublicKey, error) {
	// If epoch management is not configured, use static keys
	if s.epochManager == nil || len(s.relayMasterSeeds) == 0 {
		return s.relayPubKeys, nil
	}

	currentEpoch := s.epochManager.CurrentEpoch()

	// Check if we have cached keys for this epoch
	s.keyMu.RLock()
	if s.cachedEpoch == currentEpoch && len(s.cachedRelayKeys) > 0 {
		keys := s.cachedRelayKeys
		s.keyMu.RUnlock()
		return keys, nil
	}
	s.keyMu.RUnlock()

	// Derive new keys for this epoch
	keys, err := s.deriveEpochKeys(currentEpoch)
	if err != nil {
		return nil, err
	}

	// Cache the keys
	s.keyMu.Lock()
	s.cachedEpoch = currentEpoch
	s.cachedRelayKeys = keys
	s.keyMu.Unlock()

	return keys, nil
}

// deriveEpochKeys derives public keys for all relays for the given epoch.
func (s *Sender) deriveEpochKeys(epochNum uint64) ([]crypto.PublicKey, error) {
	keys := make([]crypto.PublicKey, len(s.relayMasterSeeds))

	for i, seed := range s.relayMasterSeeds {
		pubKey, err := epoch.DeriveEpochPublicKey(seed, i, epochNum)
		if err != nil {
			return nil, fmt.Errorf("failed to derive key for relay %d epoch %d: %w", i, epochNum, err)
		}
		keys[i] = pubKey
	}

	return keys, nil
}

// GetMessageCount returns the total number of messages sent.
func (s *Sender) GetMessageCount() int64 {
	return s.messageID.Load()
}

// GetCurrentEpoch returns the current epoch number if epoch management is enabled.
func (s *Sender) GetCurrentEpoch() (uint64, bool) {
	if s.epochManager == nil {
		return 0, false
	}
	return s.epochManager.CurrentEpoch(), true
}

// RefreshRelayKeys forces a refresh of the cached relay keys.
// This can be called when an epoch transition is detected.
func (s *Sender) RefreshRelayKeys() error {
	if s.epochManager == nil || len(s.relayMasterSeeds) == 0 {
		return nil // No-op if epoch management is not enabled
	}

	currentEpoch := s.epochManager.CurrentEpoch()
	keys, err := s.deriveEpochKeys(currentEpoch)
	if err != nil {
		return err
	}

	s.keyMu.Lock()
	s.cachedEpoch = currentEpoch
	s.cachedRelayKeys = keys
	s.keyMu.Unlock()

	return nil
}

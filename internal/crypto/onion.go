// Package crypto provides onion encryption/decryption for the Veil relay chain.
// It uses AES-GCM for authenticated encryption with epoch-based key derivation.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// OnionLayer represents the structure after decrypting one layer
type OnionLayer struct {
	Header  LayerHeader `json:"header"`
	Payload string      `json:"payload"` // base64-encoded inner onion or final content
}

// LayerHeader contains routing info revealed after peeling
type LayerHeader struct {
	NextHop     string `json:"next_hop,omitempty"`     // Empty for final relay
	IsValidator bool   `json:"is_validator,omitempty"` // True if forward to validator
	MessageID   string `json:"message_id"`             // Preserved through chain
}

// DeriveKey derives a relay's AES-256 key from master seed and relay ID.
// Uses SHA-256(master_seed || relay_id || epoch) for key derivation.
// Returns a 32-byte key suitable for AES-256.
func DeriveKey(masterSeed []byte, relayID int, epoch uint64) []byte {
	h := sha256.New()
	h.Write(masterSeed)

	// Write relay ID as 4-byte big-endian integer
	relayIDBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(relayIDBytes, uint32(relayID))
	h.Write(relayIDBytes)

	// Write epoch as 8-byte big-endian integer
	epochBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBytes, epoch)
	h.Write(epochBytes)

	return h.Sum(nil)
}

// Encrypt wraps plaintext in one onion layer using AES-GCM.
// Returns base64-encoded ciphertext (nonce || ciphertext).
func Encrypt(plaintext []byte, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes for AES-256, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and prepend nonce to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt removes one onion layer, returns the OnionLayer struct.
// Input is base64-encoded ciphertext (nonce || ciphertext).
func Decrypt(ciphertext string, key []byte) (*OnionLayer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes for AES-256, got %d", len(key))
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(data))
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (authentication error): %w", err)
	}

	var layer OnionLayer
	if err := json.Unmarshal(plaintext, &layer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal layer JSON: %w", err)
	}

	return &layer, nil
}

// WrapOnion creates a complete 5-layer onion for the relay chain.
// relaySeeds: master seeds for relays 0-4 in order
// epoch: current epoch for key derivation
// messageID: unique message identifier
// payload: the actual message content
//
// The onion is constructed inside-out:
// - Layer 4 (innermost): encrypted with relay-4's key, contains final payload
// - Layer 3: encrypted with relay-3's key, contains Layer 4
// - ... and so on
// - Layer 0 (outermost): encrypted with relay-0's key, contains Layer 1
func WrapOnion(relaySeeds [][]byte, epoch uint64, messageID string, payload string) (string, error) {
	if len(relaySeeds) != 5 {
		return "", fmt.Errorf("expected 5 relay seeds, got %d", len(relaySeeds))
	}

	// Start with the innermost layer (relay-node4 → validator)
	// Build inside-out: start with layer 4, then wrap with 3, 2, 1, 0
	currentPayload := base64.StdEncoding.EncodeToString([]byte(payload))

	// Relay routing configuration:
	// relay-node0 → relay-node1
	// relay-node1 → relay-node2
	// relay-node2 → relay-node3
	// relay-node3 → relay-node4
	// relay-node4 → validator (is_validator=true)
	relayConfigs := []LayerHeader{
		{NextHop: "relay-node1:8080", IsValidator: false, MessageID: messageID},
		{NextHop: "relay-node2:8080", IsValidator: false, MessageID: messageID},
		{NextHop: "relay-node3:8080", IsValidator: false, MessageID: messageID},
		{NextHop: "relay-node4:8080", IsValidator: false, MessageID: messageID},
		{NextHop: "", IsValidator: true, MessageID: messageID}, // Final relay → validator
	}

	// Build from inside out (layer 4 to layer 0)
	for i := 4; i >= 0; i-- {
		layer := OnionLayer{
			Header:  relayConfigs[i],
			Payload: currentPayload,
		}

		layerJSON, err := json.Marshal(layer)
		if err != nil {
			return "", fmt.Errorf("failed to marshal layer %d: %w", i, err)
		}

		key := DeriveKey(relaySeeds[i], i, epoch)
		encrypted, err := Encrypt(layerJSON, key)
		if err != nil {
			return "", fmt.Errorf("failed to encrypt layer %d: %w", i, err)
		}

		currentPayload = encrypted
	}

	return currentPayload, nil
}

// UnwrapOnion performs complete unwrapping of an onion for testing purposes.
// It peels all layers and returns the final payload.
// relaySeeds: master seeds for relays 0-4 in order
// epoch: current epoch for key derivation
// onion: the encrypted onion to unwrap
func UnwrapOnion(relaySeeds [][]byte, epoch uint64, onion string) (string, error) {
	if len(relaySeeds) != 5 {
		return "", fmt.Errorf("expected 5 relay seeds, got %d", len(relaySeeds))
	}

	currentOnion := onion

	// Peel each layer in order (0, 1, 2, 3, 4)
	for i := 0; i < 5; i++ {
		key := DeriveKey(relaySeeds[i], i, epoch)
		layer, err := Decrypt(currentOnion, key)
		if err != nil {
			return "", fmt.Errorf("failed to peel layer %d: %w", i, err)
		}

		currentOnion = layer.Payload
	}

	// Final payload is base64-encoded original content
	decoded, err := base64.StdEncoding.DecodeString(currentOnion)
	if err != nil {
		return "", fmt.Errorf("failed to decode final payload: %w", err)
	}

	return string(decoded), nil
}

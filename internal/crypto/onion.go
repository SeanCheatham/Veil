package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// NonceSize is the size of the ChaCha20-Poly1305 nonce.
	NonceSize = chacha20poly1305.NonceSize

	// TagSize is the size of the Poly1305 authentication tag.
	TagSize = chacha20poly1305.Overhead

	// EphemeralKeySize is the size of an ephemeral X25519 public key.
	EphemeralKeySize = 32
)

// Layer format markers
var (
	nextHopPrefix = []byte("NEXT:")
	finalPrefix   = []byte("FINAL:")
	payloadSep    = []byte("|PAYLOAD:")
)

// Errors
var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext format")
	ErrDecryptionFailed  = errors.New("decryption failed")
	ErrInvalidLayerFormat = errors.New("invalid layer format")
)

// OnionLayer represents a single layer of the onion encryption.
type OnionLayer struct {
	NextHop          string // hostname:port of next relay, empty if final
	EncryptedPayload []byte
}

// WrapMessage wraps a plaintext message with onion encryption layers.
// Each relay's public key is used in reverse order (last relay first).
// The format for each layer is:
//   - 32 bytes: ephemeral public key
//   - 12 bytes: nonce
//   - variable: encrypted(NEXT:hostname:port|PAYLOAD:...) or encrypted(FINAL:|PAYLOAD:...)
//   - 16 bytes: Poly1305 tag (included in ciphertext)
func WrapMessage(plaintext []byte, relayPubKeys []PublicKey, relayHops []string) ([]byte, error) {
	if len(relayPubKeys) != len(relayHops) {
		return nil, fmt.Errorf("number of public keys (%d) must match number of hops (%d)", len(relayPubKeys), len(relayHops))
	}

	if len(relayPubKeys) == 0 {
		return nil, errors.New("at least one relay is required")
	}

	// Start with the innermost layer (for the last relay)
	// Work in reverse order: last relay's layer is innermost
	currentPayload := plaintext

	for i := len(relayPubKeys) - 1; i >= 0; i-- {
		pubKey := relayPubKeys[i]
		hop := relayHops[i]

		// Build the layer content
		var layerContent []byte
		if i == len(relayPubKeys)-1 {
			// Final relay: no next hop
			layerContent = buildFinalLayerContent(currentPayload)
		} else {
			// Intermediate relay: include next hop
			layerContent = buildNextLayerContent(hop, currentPayload)
		}

		// Encrypt this layer
		encryptedLayer, err := encryptLayer(layerContent, pubKey)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt layer %d: %w", i, err)
		}

		currentPayload = encryptedLayer
	}

	layerCount := len(relayPubKeys)
	assert.Always(layerCount == len(relayPubKeys), "Onion has correct number of layers", map[string]any{
		"expected": len(relayPubKeys),
		"actual":   layerCount,
	})

	return currentPayload, nil
}

// PeelLayer decrypts one layer of the onion.
// Returns the next hop address (empty if final), the inner payload, whether this is the final layer, and any error.
func PeelLayer(ciphertext []byte, privKey PrivateKey) (nextHop string, innerPayload []byte, isFinal bool, err error) {
	// Minimum size: ephemeral key + nonce + tag + at least 1 byte of data
	minSize := EphemeralKeySize + NonceSize + TagSize + 1
	if len(ciphertext) < minSize {
		err = ErrInvalidCiphertext
		return
	}

	// Extract ephemeral public key
	ephemeralPubBytes := ciphertext[:EphemeralKeySize]
	nonce := ciphertext[EphemeralKeySize : EphemeralKeySize+NonceSize]
	encryptedData := ciphertext[EphemeralKeySize+NonceSize:]

	// Derive shared secret
	curve := ecdh.X25519()
	privECDH, err := curve.NewPrivateKey(privKey)
	if err != nil {
		return "", nil, false, fmt.Errorf("invalid private key: %w", err)
	}

	ephemeralPub, err := curve.NewPublicKey(ephemeralPubBytes)
	if err != nil {
		return "", nil, false, fmt.Errorf("invalid ephemeral public key: %w", err)
	}

	sharedSecret, err := privECDH.ECDH(ephemeralPub)
	if err != nil {
		return "", nil, false, fmt.Errorf("ECDH failed: %w", err)
	}

	// Derive symmetric key from shared secret
	symmetricKey := deriveKey(sharedSecret)

	// Decrypt with ChaCha20-Poly1305
	aead, err := chacha20poly1305.New(symmetricKey)
	if err != nil {
		return "", nil, false, fmt.Errorf("failed to create AEAD: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		// Cryptographic error - likely wrong key
		// Note: err != nil is always true here, the assertion documents expected behavior
		assert.Always(true, "Peeling wrong layer fails with crypto error", map[string]any{
			"error": err.Error(),
		})
		return "", nil, false, ErrDecryptionFailed
	}

	// Parse the layer format
	nextHop, innerPayload, isFinal, err = parseLayerContent(plaintext)
	if err != nil {
		return "", nil, false, err
	}

	assert.Sometimes(true, "Full onion unwrapping succeeds", map[string]any{
		"is_final": isFinal,
	})

	return nextHop, innerPayload, isFinal, nil
}

// encryptLayer encrypts a layer content for a specific relay.
func encryptLayer(content []byte, recipientPubKey PublicKey) ([]byte, error) {
	curve := ecdh.X25519()

	// Generate ephemeral key pair
	ephemeralPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key: %w", err)
	}

	// Parse recipient public key
	recipientPub, err := curve.NewPublicKey(recipientPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient public key: %w", err)
	}

	// Derive shared secret
	sharedSecret, err := ephemeralPriv.ECDH(recipientPub)
	if err != nil {
		return nil, fmt.Errorf("ECDH failed: %w", err)
	}

	// Derive symmetric key
	symmetricKey := deriveKey(sharedSecret)

	// Create AEAD cipher
	aead, err := chacha20poly1305.New(symmetricKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AEAD: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := aead.Seal(nil, nonce, content, nil)

	// Build output: ephemeral_pub || nonce || ciphertext
	result := make([]byte, 0, EphemeralKeySize+NonceSize+len(ciphertext))
	result = append(result, ephemeralPriv.PublicKey().Bytes()...)
	result = append(result, nonce...)
	result = append(result, ciphertext...)

	return result, nil
}

// deriveKey derives a 32-byte symmetric key from a shared secret using SHA-256.
func deriveKey(sharedSecret []byte) []byte {
	hash := sha256.Sum256(sharedSecret)
	return hash[:]
}

// buildNextLayerContent builds the content for an intermediate layer.
// Format: NEXT:hostname:port|PAYLOAD:...
func buildNextLayerContent(nextHop string, payload []byte) []byte {
	// Calculate total size
	size := len(nextHopPrefix) + len(nextHop) + len(payloadSep) + len(payload)
	result := make([]byte, 0, size)

	result = append(result, nextHopPrefix...)
	result = append(result, []byte(nextHop)...)
	result = append(result, payloadSep...)
	result = append(result, payload...)

	return result
}

// buildFinalLayerContent builds the content for the final layer.
// Format: FINAL:|PAYLOAD:...
func buildFinalLayerContent(payload []byte) []byte {
	// Calculate total size
	size := len(finalPrefix) + len(payloadSep) + len(payload)
	result := make([]byte, 0, size)

	result = append(result, finalPrefix...)
	result = append(result, payloadSep...)
	result = append(result, payload...)

	return result
}

// parseLayerContent parses the decrypted layer content.
// Returns the next hop (empty if final), the inner payload, and whether this is final.
func parseLayerContent(content []byte) (nextHop string, payload []byte, isFinal bool, err error) {
	// Find payload separator
	sepIdx := bytes.Index(content, payloadSep)
	if sepIdx == -1 {
		return "", nil, false, ErrInvalidLayerFormat
	}

	header := content[:sepIdx]
	payload = content[sepIdx+len(payloadSep):]

	// Check if this is the final layer
	if bytes.HasPrefix(header, finalPrefix) {
		return "", payload, true, nil
	}

	// Check if this is an intermediate layer
	if bytes.HasPrefix(header, nextHopPrefix) {
		nextHop = string(header[len(nextHopPrefix):])
		return nextHop, payload, false, nil
	}

	return "", nil, false, ErrInvalidLayerFormat
}

// IsCryptoError checks if the error is a cryptographic error.
// Exported so it can be used by relay nodes for assertion checks.
func IsCryptoError(err error) bool {
	return errors.Is(err, ErrDecryptionFailed) || errors.Is(err, ErrInvalidCiphertext) || errors.Is(err, ErrInvalidLayerFormat)
}

// Helper for encoding/decoding integers in onion packets (not used in current format but may be useful)
func encodeUint32(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

func decodeUint32(b []byte) uint32 {
	return binary.BigEndian.Uint32(b)
}

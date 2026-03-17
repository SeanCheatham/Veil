// Package crypto implements cryptographic primitives for the Veil protocol,
// including onion encryption and session key management.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

// OnionLayer represents a single layer of onion encryption.
// Each layer contains routing information (NextHop) and the inner payload,
// which may be another encrypted layer or the final message.
type OnionLayer struct {
	NextHop string // Address of next relay or "validator" for final hop
	Payload []byte // Inner encrypted blob (next layer or final message)
}

const (
	// X25519 public key size
	publicKeySize = 32
	// AES-GCM nonce size
	nonceSize = 12
	// AES-256 key size
	aesKeySize = 32
	// AES-GCM tag size
	tagSize = 16
	// Maximum NextHop length (255 bytes)
	maxNextHopLen = 255
)

// WrapOnionLayer encrypts a payload for a specific relay using ECDH + AES-GCM.
// It creates an ephemeral X25519 key pair for each layer to ensure unlinkability.
//
// The encryption process:
// 1. Generate ephemeral X25519 key pair
// 2. Perform ECDH with recipient's public key to get shared secret
// 3. Use HKDF-SHA256 to derive AES-256-GCM key from shared secret
// 4. Encrypt (nextHop length || nextHop || payload) with AES-GCM
//
// Output format: [ephemeral public key (32)][nonce (12)][ciphertext + tag]
func WrapOnionLayer(payload []byte, nextHop string, recipientPubKey *ecdh.PublicKey) ([]byte, error) {
	if recipientPubKey == nil {
		return nil, errors.New("recipient public key is nil")
	}
	if len(nextHop) > maxNextHopLen {
		return nil, fmt.Errorf("nextHop too long: %d > %d", len(nextHop), maxNextHopLen)
	}

	// Generate ephemeral key pair for this layer
	ephemeralPrivKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ephemeral key: %w", err)
	}

	// Perform ECDH to get shared secret
	sharedSecret, err := ephemeralPrivKey.ECDH(recipientPubKey)
	if err != nil {
		return nil, fmt.Errorf("ECDH failed: %w", err)
	}

	// Derive AES key using HKDF-SHA256
	aesKey, err := deriveKey(sharedSecret, ephemeralPrivKey.PublicKey().Bytes())
	if err != nil {
		return nil, fmt.Errorf("deriving key: %w", err)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	// Encode plaintext: [nextHop length (1 byte)][nextHop][payload]
	plaintext := make([]byte, 1+len(nextHop)+len(payload))
	plaintext[0] = byte(len(nextHop))
	copy(plaintext[1:], nextHop)
	copy(plaintext[1+len(nextHop):], payload)

	// Encrypt with AES-GCM
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build output: [ephemeral public key][nonce][ciphertext + tag]
	ephemeralPubBytes := ephemeralPrivKey.PublicKey().Bytes()
	output := make([]byte, publicKeySize+nonceSize+len(ciphertext))
	copy(output[0:publicKeySize], ephemeralPubBytes)
	copy(output[publicKeySize:publicKeySize+nonceSize], nonce)
	copy(output[publicKeySize+nonceSize:], ciphertext)

	return output, nil
}

// UnwrapOnionLayer decrypts one layer of an onion-encrypted blob using the relay's private key.
// Returns the next hop address and the inner payload (which may be another encrypted layer).
//
// The decryption process:
// 1. Extract ephemeral public key from blob
// 2. Perform ECDH with our private key to recover shared secret
// 3. Use HKDF-SHA256 to derive AES-256-GCM key
// 4. Decrypt and authenticate the ciphertext
// 5. Parse nextHop and inner payload from plaintext
func UnwrapOnionLayer(blob []byte, privKey *ecdh.PrivateKey) (nextHop string, innerPayload []byte, err error) {
	if privKey == nil {
		return "", nil, errors.New("private key is nil")
	}

	// Minimum size: pubkey + nonce + 1 byte nextHop len + tag
	minSize := publicKeySize + nonceSize + 1 + tagSize
	if len(blob) < minSize {
		return "", nil, fmt.Errorf("blob too short: %d < %d", len(blob), minSize)
	}

	// Extract ephemeral public key
	ephemeralPubBytes := blob[:publicKeySize]
	ephemeralPubKey, err := ecdh.X25519().NewPublicKey(ephemeralPubBytes)
	if err != nil {
		return "", nil, fmt.Errorf("parsing ephemeral public key: %w", err)
	}

	// Extract nonce
	nonce := blob[publicKeySize : publicKeySize+nonceSize]

	// Extract ciphertext
	ciphertext := blob[publicKeySize+nonceSize:]

	// Perform ECDH to recover shared secret
	sharedSecret, err := privKey.ECDH(ephemeralPubKey)
	if err != nil {
		return "", nil, fmt.Errorf("ECDH failed: %w", err)
	}

	// Derive AES key using HKDF-SHA256
	aesKey, err := deriveKey(sharedSecret, ephemeralPubBytes)
	if err != nil {
		return "", nil, fmt.Errorf("deriving key: %w", err)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", nil, fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, fmt.Errorf("creating GCM: %w", err)
	}

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", nil, fmt.Errorf("decryption failed: %w", err)
	}

	// Parse plaintext: [nextHop length (1 byte)][nextHop][payload]
	if len(plaintext) < 1 {
		return "", nil, errors.New("invalid plaintext: too short")
	}
	nextHopLen := int(plaintext[0])
	if len(plaintext) < 1+nextHopLen {
		return "", nil, fmt.Errorf("invalid plaintext: nextHop length %d exceeds data", nextHopLen)
	}

	nextHop = string(plaintext[1 : 1+nextHopLen])
	innerPayload = plaintext[1+nextHopLen:]

	return nextHop, innerPayload, nil
}

// deriveKey uses HKDF-SHA256 to derive an AES-256 key from the ECDH shared secret.
// The ephemeral public key is used as the info parameter for domain separation.
func deriveKey(sharedSecret, ephemeralPubKey []byte) ([]byte, error) {
	// Use HKDF with SHA-256
	// Salt is nil (uses all-zeros), info is the ephemeral public key for domain separation
	hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, ephemeralPubKey)

	key := make([]byte, aesKeySize)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("HKDF expansion failed: %w", err)
	}

	return key, nil
}

// BuildOnion creates a multi-layer onion message by wrapping the payload
// for each relay in the path, from last to first.
// The path is a list of relay addresses with their corresponding public keys.
// The first element in the path is the first relay to receive the message.
//
// This is a convenience function for constructing complete onion messages.
func BuildOnion(payload []byte, path []string, pubKeys []*ecdh.PublicKey) ([]byte, error) {
	if len(path) != len(pubKeys) {
		return nil, errors.New("path and pubKeys must have same length")
	}
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	// Start with the final payload
	currentBlob := payload

	// Wrap from last relay to first (reverse order)
	for i := len(path) - 1; i >= 0; i-- {
		var nextHop string
		if i == len(path)-1 {
			// Last relay forwards to "validator"
			nextHop = "validator"
		} else {
			// Forward to next relay in path
			nextHop = path[i+1]
		}

		var err error
		currentBlob, err = WrapOnionLayer(currentBlob, nextHop, pubKeys[i])
		if err != nil {
			return nil, fmt.Errorf("wrapping layer %d for %s: %w", i, path[i], err)
		}
	}

	return currentBlob, nil
}

// OnionLayerOverhead returns the byte overhead added by a single layer of encryption.
// This is useful for calculating maximum payload sizes.
// Overhead = ephemeral pubkey (32) + nonce (12) + nextHop length (1) + tag (16) = 61 bytes + nextHop length
func OnionLayerOverhead(nextHopLen int) int {
	return publicKeySize + nonceSize + 1 + nextHopLen + tagSize
}

// encodeUint64 encodes a uint64 in big-endian format (for potential future use)
func encodeUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

// Package crypto provides recipient encryption using X25519 key exchange and AES-GCM.
// This enables end-to-end encryption where only intended recipients can decrypt messages.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// RecipientKeyPair holds an X25519 key pair for recipient encryption.
// The private key is used by the recipient to decrypt messages.
// The public key is shared with senders who want to encrypt messages for this recipient.
type RecipientKeyPair struct {
	PublicKey  [32]byte
	PrivateKey [32]byte
}

// EncryptedMessage wraps encrypted content for a specific recipient.
// It contains all the information needed for the recipient to decrypt the message.
type EncryptedMessage struct {
	EphemeralPubKey string `json:"ephemeral_pub_key"` // Base64-encoded sender's ephemeral public key
	Ciphertext      string `json:"ciphertext"`        // Base64-encoded encrypted content (nonce || ciphertext)
	RecipientPubKey string `json:"recipient_pub_key"` // Base64-encoded recipient's public key (for routing)
}

// GenerateKeyPair creates a new X25519 key pair for a recipient.
// Uses cryptographically secure random number generation.
func GenerateKeyPair() (*RecipientKeyPair, error) {
	var privateKey [32]byte
	if _, err := rand.Read(privateKey[:]); err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Compute public key from private key
	var publicKey [32]byte
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	return &RecipientKeyPair{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, nil
}

// GenerateKeyPairFromSeed creates a deterministic key pair from a seed.
// This is useful for Antithesis testing where reproducibility is required.
// The seed should be at least 32 bytes for full entropy.
func GenerateKeyPairFromSeed(seed []byte) (*RecipientKeyPair, error) {
	if len(seed) == 0 {
		return nil, fmt.Errorf("seed cannot be empty")
	}

	// Use SHA-256 to derive a 32-byte private key from the seed
	// This ensures consistent key generation regardless of seed length
	hash := sha256.Sum256(seed)
	var privateKey [32]byte
	copy(privateKey[:], hash[:])

	// Compute public key from private key
	var publicKey [32]byte
	curve25519.ScalarBaseMult(&publicKey, &privateKey)

	return &RecipientKeyPair{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, nil
}

// EncryptForRecipient encrypts a message for a specific recipient using ECDH + AES-GCM.
// The process:
// 1. Generate ephemeral X25519 key pair
// 2. Compute shared secret via ECDH with recipient's public key
// 3. Derive AES key from shared secret using SHA-256
// 4. Encrypt payload with AES-GCM
//
// Returns EncryptedMessage containing ephemeral public key + ciphertext.
// This provides forward secrecy since each message uses a fresh ephemeral key.
func EncryptForRecipient(payload []byte, recipientPubKey [32]byte) (*EncryptedMessage, error) {
	// Step 1: Generate ephemeral key pair
	ephemeralKeyPair, err := GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key pair: %w", err)
	}

	// Step 2: Compute shared secret via ECDH
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &ephemeralKeyPair.PrivateKey, &recipientPubKey)

	// Step 3: Derive AES key from shared secret using SHA-256
	aesKey := sha256.Sum256(sharedSecret[:])

	// Step 4: Encrypt payload with AES-GCM
	block, err := aes.NewCipher(aesKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and prepend nonce to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, payload, nil)

	return &EncryptedMessage{
		EphemeralPubKey: base64.StdEncoding.EncodeToString(ephemeralKeyPair.PublicKey[:]),
		Ciphertext:      base64.StdEncoding.EncodeToString(ciphertext),
		RecipientPubKey: base64.StdEncoding.EncodeToString(recipientPubKey[:]),
	}, nil
}

// DecryptFromSender decrypts a message using the recipient's private key.
// The process:
// 1. Extract ephemeral public key from message
// 2. Compute shared secret via ECDH with sender's ephemeral public key
// 3. Derive AES key from shared secret
// 4. Decrypt ciphertext with AES-GCM
//
// Returns decrypted payload or error if not the intended recipient.
// The error will be an authentication error if the wrong private key is used.
func DecryptFromSender(msg *EncryptedMessage, recipientPrivKey [32]byte) ([]byte, error) {
	// Step 1: Decode ephemeral public key
	ephemeralPubKeyBytes, err := base64.StdEncoding.DecodeString(msg.EphemeralPubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ephemeral public key: %w", err)
	}
	if len(ephemeralPubKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid ephemeral public key length: expected 32, got %d", len(ephemeralPubKeyBytes))
	}

	var ephemeralPubKey [32]byte
	copy(ephemeralPubKey[:], ephemeralPubKeyBytes)

	// Step 2: Compute shared secret via ECDH
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &recipientPrivKey, &ephemeralPubKey)

	// Step 3: Derive AES key from shared secret using SHA-256
	aesKey := sha256.Sum256(sharedSecret[:])

	// Step 4: Decode ciphertext
	ciphertextBytes, err := base64.StdEncoding.DecodeString(msg.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// Step 5: Decrypt with AES-GCM
	block, err := aes.NewCipher(aesKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertextBytes) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(ciphertextBytes))
	}

	nonce, ciphertext := ciphertextBytes[:nonceSize], ciphertextBytes[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (authentication error): %w", err)
	}

	return plaintext, nil
}

// SerializeEncryptedMessage converts EncryptedMessage to JSON string.
// This is used to embed the encrypted message in the onion payload.
func SerializeEncryptedMessage(msg *EncryptedMessage) (string, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("failed to serialize encrypted message: %w", err)
	}
	return string(data), nil
}

// ParseEncryptedMessage parses JSON string to EncryptedMessage.
// This is used by recipients to extract the encrypted message from the onion payload.
func ParseEncryptedMessage(data string) (*EncryptedMessage, error) {
	var msg EncryptedMessage
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return nil, fmt.Errorf("failed to parse encrypted message: %w", err)
	}
	return &msg, nil
}

// PublicKeyToBase64 encodes a public key to base64 string.
func PublicKeyToBase64(pubKey [32]byte) string {
	return base64.StdEncoding.EncodeToString(pubKey[:])
}

// PublicKeyFromBase64 decodes a base64 string to public key.
func PublicKeyFromBase64(encoded string) ([32]byte, error) {
	var pubKey [32]byte
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return pubKey, fmt.Errorf("failed to decode public key: %w", err)
	}
	if len(decoded) != 32 {
		return pubKey, fmt.Errorf("invalid public key length: expected 32, got %d", len(decoded))
	}
	copy(pubKey[:], decoded)
	return pubKey, nil
}

// PrivateKeyToBase64 encodes a private key to base64 string.
func PrivateKeyToBase64(privKey [32]byte) string {
	return base64.StdEncoding.EncodeToString(privKey[:])
}

// PrivateKeyFromBase64 decodes a base64 string to private key.
func PrivateKeyFromBase64(encoded string) ([32]byte, error) {
	var privKey [32]byte
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return privKey, fmt.Errorf("failed to decode private key: %w", err)
	}
	if len(decoded) != 32 {
		return privKey, fmt.Errorf("invalid private key length: expected 32, got %d", len(decoded))
	}
	copy(privKey[:], decoded)
	return privKey, nil
}

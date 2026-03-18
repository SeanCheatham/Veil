// Package crypto provides cryptographic primitives for onion routing.
package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

// PublicKey is a 32-byte X25519 public key.
type PublicKey []byte

// PrivateKey is a 32-byte X25519 private key.
type PrivateKey []byte

// KeyPair holds a public/private key pair for X25519.
type KeyPair struct {
	Public  PublicKey
	Private PrivateKey
}

// GenerateKeyPair generates a new X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	curve := ecdh.X25519()
	privKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate X25519 key: %w", err)
	}

	return &KeyPair{
		Public:  PublicKey(privKey.PublicKey().Bytes()),
		Private: PrivateKey(privKey.Bytes()),
	}, nil
}

// LoadOrGenerateKey loads a key from the given base64-encoded string or generates a new one.
// If privKeyBase64 is empty, generates a new key pair.
func LoadOrGenerateKey(privKeyBase64 string) (*KeyPair, error) {
	if privKeyBase64 == "" {
		return GenerateKeyPair()
	}

	privBytes, err := base64.StdEncoding.DecodeString(privKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}

	if len(privBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: expected 32, got %d", len(privBytes))
	}

	curve := ecdh.X25519()
	privKey, err := curve.NewPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create private key: %w", err)
	}

	return &KeyPair{
		Public:  PublicKey(privKey.PublicKey().Bytes()),
		Private: PrivateKey(privBytes),
	}, nil
}

// LoadOrGenerateKeyFromFile loads a key from a file or generates a new one.
// If the file doesn't exist, generates a new key and saves it.
func LoadOrGenerateKeyFromFile(path string) (*KeyPair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Generate new key
			kp, err := GenerateKeyPair()
			if err != nil {
				return nil, err
			}

			// Save to file
			if err := os.WriteFile(path, []byte(kp.Private.Base64()), 0600); err != nil {
				return nil, fmt.Errorf("failed to save key: %w", err)
			}

			return kp, nil
		}
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	return LoadOrGenerateKey(string(data))
}

// Base64 encodes the public key as base64.
func (pk PublicKey) Base64() string {
	return base64.StdEncoding.EncodeToString(pk)
}

// Base64 encodes the private key as base64.
func (pk PrivateKey) Base64() string {
	return base64.StdEncoding.EncodeToString(pk)
}

// PublicKeyFromBase64 decodes a base64-encoded public key.
func PublicKeyFromBase64(s string) (PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("invalid public key length: expected 32, got %d", len(b))
	}
	return PublicKey(b), nil
}

// PrivateKeyFromBase64 decodes a base64-encoded private key.
func PrivateKeyFromBase64(s string) (PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("invalid private key length: expected 32, got %d", len(b))
	}
	return PrivateKey(b), nil
}

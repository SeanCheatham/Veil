package epoch

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/veil/veil/internal/crypto"
	"golang.org/x/crypto/hkdf"
)

const (
	// HKDFInfoPrefix is the prefix for HKDF info parameter.
	HKDFInfoPrefix = "veil-relay-"

	// MasterSeedSize is the required size of master seeds (32 bytes).
	MasterSeedSize = 32
)

// DeriveEpochKeyPair derives a deterministic X25519 key pair from a master seed,
// relay ID, and epoch number using HKDF.
//
// The derivation uses:
// - SHA-256 as the hash function
// - Master seed as the input key material
// - Empty salt (deterministic derivation)
// - Info: "veil-relay-{id}-epoch-{num}"
//
// This ensures:
// 1. Different epochs produce different keys
// 2. Different relays produce different keys
// 3. Same inputs always produce the same key pair (deterministic)
func DeriveEpochKeyPair(masterSeed []byte, relayID int, epochNum uint64) (*crypto.KeyPair, error) {
	if len(masterSeed) != MasterSeedSize {
		return nil, fmt.Errorf("master seed must be %d bytes, got %d", MasterSeedSize, len(masterSeed))
	}

	// Build info string: "veil-relay-{id}-epoch-{num}"
	info := buildHKDFInfo(relayID, epochNum)

	// Use HKDF to derive 32 bytes for the private key
	// Salt is nil for deterministic derivation across all nodes
	hkdfReader := hkdf.New(sha256.New, masterSeed, nil, info)

	derivedKey := make([]byte, 32)
	if _, err := hkdfReader.Read(derivedKey); err != nil {
		return nil, fmt.Errorf("HKDF failed: %w", err)
	}

	// Create X25519 key pair from derived bytes
	curve := ecdh.X25519()
	privKey, err := curve.NewPrivateKey(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create private key from derived bytes: %w", err)
	}

	return &crypto.KeyPair{
		Private: crypto.PrivateKey(privKey.Bytes()),
		Public:  crypto.PublicKey(privKey.PublicKey().Bytes()),
	}, nil
}

// DeriveEpochPublicKey derives only the public key for a given epoch.
// This is used by senders who need relay public keys but not private keys.
func DeriveEpochPublicKey(masterSeed []byte, relayID int, epochNum uint64) (crypto.PublicKey, error) {
	keyPair, err := DeriveEpochKeyPair(masterSeed, relayID, epochNum)
	if err != nil {
		return nil, err
	}
	return keyPair.Public, nil
}

// buildHKDFInfo builds the info parameter for HKDF.
// Format: "veil-relay-{id}-epoch-{num}" as bytes
func buildHKDFInfo(relayID int, epochNum uint64) []byte {
	// Pre-allocate buffer for efficiency
	// Max format: "veil-relay-XXXX-epoch-XXXXXXXXXXXXXXXXXX"
	buf := make([]byte, 0, 64)

	buf = append(buf, HKDFInfoPrefix...)
	buf = appendInt(buf, relayID)
	buf = append(buf, "-epoch-"...)
	buf = appendUint64(buf, epochNum)

	return buf
}

// appendInt appends an integer to a byte slice as a string.
func appendInt(buf []byte, n int) []byte {
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	if n == 0 {
		return append(buf, '0')
	}

	// Convert to digits in reverse
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}

	// Reverse and append
	for i := len(digits) - 1; i >= 0; i-- {
		buf = append(buf, digits[i])
	}
	return buf
}

// appendUint64 appends a uint64 to a byte slice as a string.
func appendUint64(buf []byte, n uint64) []byte {
	if n == 0 {
		return append(buf, '0')
	}

	// Convert to digits in reverse
	digits := make([]byte, 0, 20)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}

	// Reverse and append
	for i := len(digits) - 1; i >= 0; i-- {
		buf = append(buf, digits[i])
	}
	return buf
}

// EpochKeyPairFromSeed is a convenience function that creates an epoch key pair
// from a base64-encoded master seed.
func EpochKeyPairFromSeed(masterSeedBase64 string, relayID int, epochNum uint64) (*crypto.KeyPair, error) {
	seed, err := decodeMasterSeed(masterSeedBase64)
	if err != nil {
		return nil, err
	}
	return DeriveEpochKeyPair(seed, relayID, epochNum)
}

// decodeMasterSeed decodes a base64-encoded master seed.
func decodeMasterSeed(encoded string) ([]byte, error) {
	// Use the same base64 encoding as crypto package
	seed := make([]byte, MasterSeedSize)
	n := 0
	for i := 0; i < len(encoded); i++ {
		// Simple base64 decoder using standard table
		c := encoded[i]
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+':
			v = 62
		case c == '/':
			v = 63
		case c == '=':
			continue // padding
		default:
			return nil, fmt.Errorf("invalid base64 character: %c", c)
		}

		// Accumulate bits
		switch i % 4 {
		case 0:
			if n < MasterSeedSize {
				seed[n] = v << 2
			}
		case 1:
			if n < MasterSeedSize {
				seed[n] |= v >> 4
				n++
			}
			if n < MasterSeedSize {
				seed[n] = v << 4
			}
		case 2:
			if n < MasterSeedSize {
				seed[n] |= v >> 2
				n++
			}
			if n < MasterSeedSize {
				seed[n] = v << 6
			}
		case 3:
			if n < MasterSeedSize {
				seed[n] |= v
				n++
			}
		}
	}

	if n < MasterSeedSize {
		return nil, fmt.Errorf("master seed too short: got %d bytes, need %d", n, MasterSeedSize)
	}

	return seed, nil
}

// Helper to ensure binary package is used (for potential future use)
var _ = binary.BigEndian

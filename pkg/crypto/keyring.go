package crypto

import (
	"errors"
	"sync"
)

// KeyRing holds current and previous keypairs for epoch-based key rotation.
// The previous keypair provides a one-epoch grace period so messages encrypted
// with the old key are still deliverable.
type KeyRing struct {
	current  KeyPair
	previous *KeyPair
	mu       sync.RWMutex
}

// NewKeyRing generates an initial keypair and returns a new KeyRing.
func NewKeyRing() (*KeyRing, error) {
	kp, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &KeyRing{current: kp}, nil
}

// Rotate generates a new keypair, moves current to previous (grace period).
// The old previous key is discarded.
func (kr *KeyRing) Rotate() error {
	newKP, err := GenerateKeyPair()
	if err != nil {
		return err
	}
	kr.mu.Lock()
	defer kr.mu.Unlock()
	old := kr.current
	kr.previous = &old
	kr.current = newKP
	return nil
}

// Current returns the current keypair (thread-safe).
func (kr *KeyRing) Current() KeyPair {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.current
}

// TryDecrypt tries to decrypt ciphertext with the current key first, then the
// previous key. Returns the first successful decryption or an error if both fail.
func (kr *KeyRing) TryDecrypt(ciphertext []byte) ([]byte, error) {
	kr.mu.RLock()
	current := kr.current
	prev := kr.previous
	kr.mu.RUnlock()

	plaintext, err := FinalDecrypt(ciphertext, current.Private)
	if err == nil {
		return plaintext, nil
	}

	if prev != nil {
		plaintext, err = FinalDecrypt(ciphertext, prev.Private)
		if err == nil {
			return plaintext, nil
		}
	}

	return nil, errors.New("decryption failed with both current and previous keys")
}

// TryPeelLayer tries to peel an onion layer with the current key first, then
// the previous key. Returns the first successful result or an error if both fail.
func (kr *KeyRing) TryPeelLayer(ciphertext []byte) (inner []byte, nextHop string, isFinal bool, err error) {
	kr.mu.RLock()
	current := kr.current
	prev := kr.previous
	kr.mu.RUnlock()

	inner, nextHop, isFinal, err = PeelLayer(ciphertext, current.Private)
	if err == nil {
		return inner, nextHop, isFinal, nil
	}

	if prev != nil {
		inner, nextHop, isFinal, err = PeelLayer(ciphertext, prev.Private)
		if err == nil {
			return inner, nextHop, isFinal, nil
		}
	}

	return nil, "", false, errors.New("peel layer failed with both current and previous keys")
}

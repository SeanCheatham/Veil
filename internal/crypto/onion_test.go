package crypto

import (
	"bytes"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if len(kp.Public) != 32 {
		t.Errorf("Public key length = %d, want 32", len(kp.Public))
	}

	if len(kp.Private) != 32 {
		t.Errorf("Private key length = %d, want 32", len(kp.Private))
	}
}

func TestKeyBase64RoundTrip(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Test public key round-trip
	pubB64 := kp.Public.Base64()
	pubDecoded, err := PublicKeyFromBase64(pubB64)
	if err != nil {
		t.Fatalf("PublicKeyFromBase64 failed: %v", err)
	}
	if !bytes.Equal(kp.Public, pubDecoded) {
		t.Error("Public key round-trip failed")
	}

	// Test private key round-trip
	privB64 := kp.Private.Base64()
	privDecoded, err := PrivateKeyFromBase64(privB64)
	if err != nil {
		t.Fatalf("PrivateKeyFromBase64 failed: %v", err)
	}
	if !bytes.Equal(kp.Private, privDecoded) {
		t.Error("Private key round-trip failed")
	}
}

func TestLoadOrGenerateKey(t *testing.T) {
	// Test with empty string (should generate new key)
	kp1, err := LoadOrGenerateKey("")
	if err != nil {
		t.Fatalf("LoadOrGenerateKey with empty string failed: %v", err)
	}
	if len(kp1.Public) != 32 {
		t.Error("Generated key has wrong public key length")
	}

	// Test with valid base64 key
	kp2, err := LoadOrGenerateKey(kp1.Private.Base64())
	if err != nil {
		t.Fatalf("LoadOrGenerateKey with valid key failed: %v", err)
	}
	if !bytes.Equal(kp1.Public, kp2.Public) {
		t.Error("Loaded key has different public key")
	}

	// Test with invalid base64
	_, err = LoadOrGenerateKey("not-valid-base64!")
	if err == nil {
		t.Error("LoadOrGenerateKey should fail with invalid base64")
	}

	// Test with wrong length
	_, err = LoadOrGenerateKey("dG9vc2hvcnQ=") // "tooshort" in base64
	if err == nil {
		t.Error("LoadOrGenerateKey should fail with wrong length key")
	}
}

func TestWrapAndPeelSingleLayer(t *testing.T) {
	// Generate a single key pair
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("Hello, World!")
	pubKeys := []PublicKey{kp.Public}
	hops := []string{""} // Final relay has empty hop

	// Wrap the message
	wrapped, err := WrapMessage(plaintext, pubKeys, hops)
	if err != nil {
		t.Fatalf("WrapMessage failed: %v", err)
	}

	// Peel the layer
	nextHop, payload, isFinal, err := PeelLayer(wrapped, kp.Private)
	if err != nil {
		t.Fatalf("PeelLayer failed: %v", err)
	}

	if !isFinal {
		t.Error("Expected isFinal to be true for single-layer onion")
	}

	if nextHop != "" {
		t.Errorf("Expected empty nextHop for final layer, got %q", nextHop)
	}

	if !bytes.Equal(payload, plaintext) {
		t.Errorf("Payload mismatch: got %q, want %q", payload, plaintext)
	}
}

func TestWrapAndPeelFiveLayers(t *testing.T) {
	// Generate 5 key pairs (matching our 5-relay network)
	keyPairs := make([]*KeyPair, 5)
	pubKeys := make([]PublicKey, 5)
	for i := 0; i < 5; i++ {
		kp, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("GenerateKeyPair failed for relay %d: %v", i, err)
		}
		keyPairs[i] = kp
		pubKeys[i] = kp.Public
	}

	// Define hops (each relay forwards to the next, last one is empty)
	hops := []string{
		"relay-node1:8080",
		"relay-node2:8080",
		"relay-node3:8080",
		"relay-node4:8080",
		"", // Final relay
	}

	plaintext := []byte("Secret message through 5 relays!")

	// Wrap the message
	wrapped, err := WrapMessage(plaintext, pubKeys, hops)
	if err != nil {
		t.Fatalf("WrapMessage failed: %v", err)
	}

	// Peel through all 5 layers
	currentPayload := wrapped
	for i := 0; i < 5; i++ {
		nextHop, innerPayload, isFinal, err := PeelLayer(currentPayload, keyPairs[i].Private)
		if err != nil {
			t.Fatalf("PeelLayer failed at relay %d: %v", i, err)
		}

		if i == 4 {
			// Last relay
			if !isFinal {
				t.Errorf("Relay %d: expected isFinal=true, got false", i)
			}
			if nextHop != "" {
				t.Errorf("Relay %d: expected empty nextHop, got %q", i, nextHop)
			}
			if !bytes.Equal(innerPayload, plaintext) {
				t.Errorf("Relay %d: payload mismatch: got %q, want %q", i, innerPayload, plaintext)
			}
		} else {
			// Intermediate relay
			if isFinal {
				t.Errorf("Relay %d: expected isFinal=false, got true", i)
			}
			if nextHop != hops[i] {
				t.Errorf("Relay %d: nextHop = %q, want %q", i, nextHop, hops[i])
			}
			currentPayload = innerPayload
		}
	}
}

func TestPeelWithWrongKey(t *testing.T) {
	// Generate two different key pairs
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("Secret message")
	pubKeys := []PublicKey{kp1.Public}
	hops := []string{""}

	// Wrap with kp1's public key
	wrapped, err := WrapMessage(plaintext, pubKeys, hops)
	if err != nil {
		t.Fatalf("WrapMessage failed: %v", err)
	}

	// Try to peel with kp2's private key (wrong key)
	_, _, _, err = PeelLayer(wrapped, kp2.Private)
	if err == nil {
		t.Error("PeelLayer should fail with wrong key")
	}
	if err != ErrDecryptionFailed {
		t.Errorf("Expected ErrDecryptionFailed, got %v", err)
	}
}

func TestPayloadIntegrity(t *testing.T) {
	// Test that modifying the ciphertext causes decryption to fail
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("Sensitive data that must not be tampered with")
	pubKeys := []PublicKey{kp.Public}
	hops := []string{""}

	wrapped, err := WrapMessage(plaintext, pubKeys, hops)
	if err != nil {
		t.Fatalf("WrapMessage failed: %v", err)
	}

	// Tamper with the ciphertext (flip a bit in the encrypted portion)
	tamperedIdx := len(wrapped) - 10
	tampered := make([]byte, len(wrapped))
	copy(tampered, wrapped)
	tampered[tamperedIdx] ^= 0x01

	// Try to decrypt the tampered ciphertext
	_, _, _, err = PeelLayer(tampered, kp.Private)
	if err == nil {
		t.Error("PeelLayer should fail on tampered ciphertext")
	}
}

func TestWrapMessageWithMismatchedKeyCount(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	plaintext := []byte("test")
	pubKeys := []PublicKey{kp.Public, kp.Public}
	hops := []string{""} // Only one hop for two keys

	_, err = WrapMessage(plaintext, pubKeys, hops)
	if err == nil {
		t.Error("WrapMessage should fail when key count doesn't match hop count")
	}
}

func TestWrapMessageWithEmptyKeys(t *testing.T) {
	plaintext := []byte("test")
	pubKeys := []PublicKey{}
	hops := []string{}

	_, err := WrapMessage(plaintext, pubKeys, hops)
	if err == nil {
		t.Error("WrapMessage should fail with empty key list")
	}
}

func TestPeelLayerWithTruncatedCiphertext(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Ciphertext too short
	shortCiphertext := make([]byte, 10)
	_, _, _, err = PeelLayer(shortCiphertext, kp.Private)
	if err == nil {
		t.Error("PeelLayer should fail on truncated ciphertext")
	}
	if err != ErrInvalidCiphertext {
		t.Errorf("Expected ErrInvalidCiphertext, got %v", err)
	}
}

func TestStaticRelayKeys(t *testing.T) {
	// Test that the static keys in keydir.go work correctly
	pubKeys := GetRelayPublicKeys()
	hops := GetRelayHops()

	if len(pubKeys) != 5 {
		t.Fatalf("GetRelayPublicKeys returned %d keys, want 5", len(pubKeys))
	}

	if len(hops) != 5 {
		t.Fatalf("GetRelayHops returned %d hops, want 5", len(hops))
	}

	// Verify all public keys are valid 32-byte keys
	for i, pk := range pubKeys {
		if len(pk) != 32 {
			t.Errorf("Relay %d public key has length %d, want 32", i, len(pk))
		}
	}

	// Test that we can load the private keys and derive matching public keys
	for i := 0; i < 5; i++ {
		privKeyB64 := GetRelayPrivateKeyByID(i)
		kp, err := LoadOrGenerateKey(privKeyB64)
		if err != nil {
			t.Fatalf("Failed to load relay %d private key: %v", i, err)
		}
		if !bytes.Equal(kp.Public, pubKeys[i]) {
			t.Errorf("Relay %d: derived public key doesn't match stored public key", i)
		}
	}
}

func TestEndToEndWithStaticKeys(t *testing.T) {
	// Test full wrap/peel using the static relay keys
	pubKeys := GetRelayPublicKeys()
	hops := GetRelayHops()

	// Load private keys
	privKeys := make([]PrivateKey, 5)
	for i := 0; i < 5; i++ {
		kp, err := LoadOrGenerateKey(GetRelayPrivateKeyByID(i))
		if err != nil {
			t.Fatalf("Failed to load relay %d key: %v", i, err)
		}
		privKeys[i] = kp.Private
	}

	plaintext := []byte("VEIL-MSG-12345-1234567890")

	// Wrap the message
	wrapped, err := WrapMessage(plaintext, pubKeys, hops)
	if err != nil {
		t.Fatalf("WrapMessage failed: %v", err)
	}

	// Peel through all 5 layers
	currentPayload := wrapped
	for i := 0; i < 5; i++ {
		nextHop, innerPayload, isFinal, err := PeelLayer(currentPayload, privKeys[i])
		if err != nil {
			t.Fatalf("PeelLayer failed at relay %d: %v", i, err)
		}

		t.Logf("Relay %d: nextHop=%q, isFinal=%v, payloadLen=%d", i, nextHop, isFinal, len(innerPayload))

		if i == 4 {
			if !isFinal {
				t.Error("Expected isFinal=true at relay 4")
			}
			if !bytes.Equal(innerPayload, plaintext) {
				t.Errorf("Final payload mismatch: got %q, want %q", innerPayload, plaintext)
			}
		} else {
			if isFinal {
				t.Errorf("Expected isFinal=false at relay %d", i)
			}
			currentPayload = innerPayload
		}
	}
}

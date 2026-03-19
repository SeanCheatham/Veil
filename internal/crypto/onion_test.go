package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Test relay seeds (base64-encoded, matching docker-compose.yaml)
var testSeeds = []string{
	"KzNhvFWQe7yR8pBwXdC4TmJgUoHaLsYx1q9nI3rE6Mk=", // relay-node0
	"VpQcGtYw2hXjNsKmL8bFrDe5oU7iA0nZ4xWvJz3C1Hg=", // relay-node1
	"BwS9fXkMnYz1pTqR7vO2hJc4uE6gLaI8xKdNmW0sAVe=", // relay-node2
	"DqH4mZy5oP1tVw8nKjR3bF6iA9xSgLcE7uN2aWsXkMf=", // relay-node3
	"FzK7pYn5wT0rMeB8jC1hXgU3vI6aLdS4oQsN9xWmEqH=", // relay-node4
}

func decodeTestSeeds(t *testing.T) [][]byte {
	seeds := make([][]byte, len(testSeeds))
	for i, s := range testSeeds {
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("failed to decode seed %d: %v", i, err)
		}
		seeds[i] = decoded
	}
	return seeds
}

func TestDeriveKey_Consistency(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])

	// Same inputs should produce same key
	key1 := DeriveKey(seed, 0, 100)
	key2 := DeriveKey(seed, 0, 100)

	if len(key1) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key1))
	}

	if string(key1) != string(key2) {
		t.Error("DeriveKey is not deterministic")
	}
}

func TestDeriveKey_DifferentInputs(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])

	// Different relay IDs should produce different keys
	key0 := DeriveKey(seed, 0, 100)
	key1 := DeriveKey(seed, 1, 100)
	if string(key0) == string(key1) {
		t.Error("different relay IDs should produce different keys")
	}

	// Different epochs should produce different keys
	keyEpoch100 := DeriveKey(seed, 0, 100)
	keyEpoch101 := DeriveKey(seed, 0, 101)
	if string(keyEpoch100) == string(keyEpoch101) {
		t.Error("different epochs should produce different keys")
	}

	// Different seeds should produce different keys
	seed2, _ := base64.StdEncoding.DecodeString(testSeeds[1])
	keySeed0 := DeriveKey(seed, 0, 100)
	keySeed1 := DeriveKey(seed2, 0, 100)
	if string(keySeed0) == string(keySeed1) {
		t.Error("different seeds should produce different keys")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])
	key := DeriveKey(seed, 0, 100)

	// Create a test layer
	layer := OnionLayer{
		Header: LayerHeader{
			NextHop:     "relay-node1:8080",
			IsValidator: false,
			MessageID:   "test-msg-123",
		},
		Payload: base64.StdEncoding.EncodeToString([]byte("inner content")),
	}

	// Marshal to JSON
	plaintext := `{"header":{"next_hop":"relay-node1:8080","message_id":"test-msg-123"},"payload":"aW5uZXIgY29udGVudA=="}`

	// Encrypt
	ciphertext, err := Encrypt([]byte(plaintext), key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Decrypt
	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	// Verify
	if decrypted.Header.NextHop != layer.Header.NextHop {
		t.Errorf("NextHop mismatch: got %q, want %q", decrypted.Header.NextHop, layer.Header.NextHop)
	}
	if decrypted.Header.MessageID != layer.Header.MessageID {
		t.Errorf("MessageID mismatch: got %q, want %q", decrypted.Header.MessageID, layer.Header.MessageID)
	}
	if decrypted.Payload != layer.Payload {
		t.Errorf("Payload mismatch: got %q, want %q", decrypted.Payload, layer.Payload)
	}
}

func TestEncrypt_DifferentCiphertexts(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])
	key := DeriveKey(seed, 0, 100)

	plaintext := []byte(`{"header":{"message_id":"test"},"payload":"data"}`)

	// Encrypt twice - should produce different ciphertexts (random nonce)
	ct1, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("First Encrypt failed: %v", err)
	}

	ct2, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Second Encrypt failed: %v", err)
	}

	if ct1 == ct2 {
		t.Error("Same plaintext should produce different ciphertexts due to random nonce")
	}

	// Both should decrypt to same content
	layer1, _ := Decrypt(ct1, key)
	layer2, _ := Decrypt(ct2, key)

	if layer1.Payload != layer2.Payload {
		t.Error("Both ciphertexts should decrypt to same content")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	seed0, _ := base64.StdEncoding.DecodeString(testSeeds[0])
	seed1, _ := base64.StdEncoding.DecodeString(testSeeds[1])

	key0 := DeriveKey(seed0, 0, 100)
	key1 := DeriveKey(seed1, 1, 100)

	plaintext := []byte(`{"header":{"message_id":"test"},"payload":"secret"}`)

	// Encrypt with key0
	ciphertext, err := Encrypt(plaintext, key0)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Decrypt with wrong key should fail
	_, err = Decrypt(ciphertext, key1)
	if err == nil {
		t.Error("Decrypt with wrong key should fail")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("Expected authentication error, got: %v", err)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])
	key := DeriveKey(seed, 0, 100)

	plaintext := []byte(`{"header":{"message_id":"test"},"payload":"data"}`)
	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Tamper with the ciphertext
	decoded, _ := base64.StdEncoding.DecodeString(ciphertext)
	decoded[len(decoded)-1] ^= 0xFF // Flip bits in last byte
	tampered := base64.StdEncoding.EncodeToString(decoded)

	// Decryption should fail due to GCM authentication
	_, err = Decrypt(tampered, key)
	if err == nil {
		t.Error("Decrypt with tampered ciphertext should fail")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("Expected authentication error, got: %v", err)
	}
}

func TestWrapUnwrap_FullCycle(t *testing.T) {
	seeds := decodeTestSeeds(t)
	epoch := uint64(100)
	messageID := "test-msg-full-cycle"
	payload := "Hello, this is the secret message!"

	// Wrap the onion
	onion, err := WrapOnion(seeds, epoch, messageID, payload)
	if err != nil {
		t.Fatalf("WrapOnion failed: %v", err)
	}

	// Unwrap the onion
	result, err := UnwrapOnion(seeds, epoch, onion)
	if err != nil {
		t.Fatalf("UnwrapOnion failed: %v", err)
	}

	if result != payload {
		t.Errorf("Payload mismatch: got %q, want %q", result, payload)
	}
}

func TestWrapOnion_LayerByLayer(t *testing.T) {
	seeds := decodeTestSeeds(t)
	epoch := uint64(100)
	messageID := "test-msg-layer-by-layer"
	payload := "Secret data"

	// Wrap the onion
	onion, err := WrapOnion(seeds, epoch, messageID, payload)
	if err != nil {
		t.Fatalf("WrapOnion failed: %v", err)
	}

	// Peel layer 0
	key0 := DeriveKey(seeds[0], 0, epoch)
	layer0, err := Decrypt(onion, key0)
	if err != nil {
		t.Fatalf("Failed to peel layer 0: %v", err)
	}
	if layer0.Header.NextHop != "relay-node1:8080" {
		t.Errorf("Layer 0 NextHop: got %q, want %q", layer0.Header.NextHop, "relay-node1:8080")
	}
	if layer0.Header.MessageID != messageID {
		t.Errorf("Layer 0 MessageID: got %q, want %q", layer0.Header.MessageID, messageID)
	}

	// Peel layer 1
	key1 := DeriveKey(seeds[1], 1, epoch)
	layer1, err := Decrypt(layer0.Payload, key1)
	if err != nil {
		t.Fatalf("Failed to peel layer 1: %v", err)
	}
	if layer1.Header.NextHop != "relay-node2:8080" {
		t.Errorf("Layer 1 NextHop: got %q, want %q", layer1.Header.NextHop, "relay-node2:8080")
	}

	// Peel layer 2
	key2 := DeriveKey(seeds[2], 2, epoch)
	layer2, err := Decrypt(layer1.Payload, key2)
	if err != nil {
		t.Fatalf("Failed to peel layer 2: %v", err)
	}
	if layer2.Header.NextHop != "relay-node3:8080" {
		t.Errorf("Layer 2 NextHop: got %q, want %q", layer2.Header.NextHop, "relay-node3:8080")
	}

	// Peel layer 3
	key3 := DeriveKey(seeds[3], 3, epoch)
	layer3, err := Decrypt(layer2.Payload, key3)
	if err != nil {
		t.Fatalf("Failed to peel layer 3: %v", err)
	}
	if layer3.Header.NextHop != "relay-node4:8080" {
		t.Errorf("Layer 3 NextHop: got %q, want %q", layer3.Header.NextHop, "relay-node4:8080")
	}

	// Peel layer 4 (final)
	key4 := DeriveKey(seeds[4], 4, epoch)
	layer4, err := Decrypt(layer3.Payload, key4)
	if err != nil {
		t.Fatalf("Failed to peel layer 4: %v", err)
	}
	if layer4.Header.NextHop != "" {
		t.Errorf("Layer 4 NextHop should be empty, got %q", layer4.Header.NextHop)
	}
	if !layer4.Header.IsValidator {
		t.Error("Layer 4 should have IsValidator=true")
	}

	// Decode final payload
	decoded, err := base64.StdEncoding.DecodeString(layer4.Payload)
	if err != nil {
		t.Fatalf("Failed to decode final payload: %v", err)
	}
	if string(decoded) != payload {
		t.Errorf("Final payload: got %q, want %q", string(decoded), payload)
	}
}

func TestWrapOnion_InvalidSeedCount(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])

	// Too few seeds
	_, err := WrapOnion([][]byte{seed}, 100, "msg", "payload")
	if err == nil {
		t.Error("WrapOnion should fail with wrong number of seeds")
	}

	// Too many seeds
	_, err = WrapOnion([][]byte{seed, seed, seed, seed, seed, seed}, 100, "msg", "payload")
	if err == nil {
		t.Error("WrapOnion should fail with wrong number of seeds")
	}
}

func TestUnwrapOnion_WrongEpoch(t *testing.T) {
	seeds := decodeTestSeeds(t)
	epoch := uint64(100)
	messageID := "test-msg-wrong-epoch"
	payload := "Secret data"

	// Wrap with epoch 100
	onion, err := WrapOnion(seeds, epoch, messageID, payload)
	if err != nil {
		t.Fatalf("WrapOnion failed: %v", err)
	}

	// Try to unwrap with different epoch (should fail)
	_, err = UnwrapOnion(seeds, epoch+1, onion)
	if err == nil {
		t.Error("UnwrapOnion with wrong epoch should fail")
	}
}

func TestEncrypt_InvalidKeyLength(t *testing.T) {
	shortKey := []byte("short")
	_, err := Encrypt([]byte("data"), shortKey)
	if err == nil {
		t.Error("Encrypt with short key should fail")
	}

	longKey := make([]byte, 64)
	_, err = Encrypt([]byte("data"), longKey)
	if err == nil {
		t.Error("Encrypt with long key should fail")
	}
}

func TestDecrypt_InvalidKeyLength(t *testing.T) {
	seed, _ := base64.StdEncoding.DecodeString(testSeeds[0])
	key := DeriveKey(seed, 0, 100)

	ct, _ := Encrypt([]byte(`{"header":{},"payload":"x"}`), key)

	shortKey := []byte("short")
	_, err := Decrypt(ct, shortKey)
	if err == nil {
		t.Error("Decrypt with short key should fail")
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key := make([]byte, 32)
	_, err := Decrypt("!!!invalid-base64!!!", key)
	if err == nil {
		t.Error("Decrypt with invalid base64 should fail")
	}
}

func TestDecrypt_CiphertextTooShort(t *testing.T) {
	key := make([]byte, 32)
	shortData := base64.StdEncoding.EncodeToString([]byte("x"))
	_, err := Decrypt(shortData, key)
	if err == nil {
		t.Error("Decrypt with too short ciphertext should fail")
	}
}

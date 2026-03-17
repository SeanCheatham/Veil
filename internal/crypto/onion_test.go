package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

func TestWrapUnwrapOnionLayer(t *testing.T) {
	// Generate a key pair for the relay
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	pubKey := privKey.PublicKey()

	payload := []byte("secret message for the relay")
	nextHop := "relay-2:8081"

	// Wrap the layer
	blob, err := WrapOnionLayer(payload, nextHop, pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer failed: %v", err)
	}

	// Unwrap the layer
	gotNextHop, gotPayload, err := UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if gotNextHop != nextHop {
		t.Errorf("nextHop = %q, want %q", gotNextHop, nextHop)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Errorf("payload = %v, want %v", gotPayload, payload)
	}
}

func TestWrapOnionLayerEphemeralKeys(t *testing.T) {
	// Generate a relay key pair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	pubKey := privKey.PublicKey()

	payload := []byte("same payload")
	nextHop := "validator"

	// Wrap the same payload twice
	blob1, err := WrapOnionLayer(payload, nextHop, pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer 1 failed: %v", err)
	}

	blob2, err := WrapOnionLayer(payload, nextHop, pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer 2 failed: %v", err)
	}

	// The blobs should be different due to ephemeral keys and random nonces
	if bytes.Equal(blob1, blob2) {
		t.Error("Two wrappings of the same payload should produce different blobs")
	}

	// The ephemeral public keys should be different (first 32 bytes)
	if bytes.Equal(blob1[:32], blob2[:32]) {
		t.Error("Ephemeral public keys should be different for each wrap")
	}

	// Both should still decrypt correctly
	gotNextHop1, gotPayload1, err := UnwrapOnionLayer(blob1, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer 1 failed: %v", err)
	}
	gotNextHop2, gotPayload2, err := UnwrapOnionLayer(blob2, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer 2 failed: %v", err)
	}

	if gotNextHop1 != nextHop || gotNextHop2 != nextHop {
		t.Error("NextHops should match original")
	}
	if !bytes.Equal(gotPayload1, payload) || !bytes.Equal(gotPayload2, payload) {
		t.Error("Payloads should match original")
	}
}

func TestUnwrapWithWrongKey(t *testing.T) {
	// Generate two different key pairs
	correctPrivKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	correctPubKey := correctPrivKey.PublicKey()

	wrongPrivKey, _ := ecdh.X25519().GenerateKey(rand.Reader)

	payload := []byte("secret message")
	nextHop := "relay-2"

	// Wrap with correct public key
	blob, err := WrapOnionLayer(payload, nextHop, correctPubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer failed: %v", err)
	}

	// Try to unwrap with wrong private key - should fail
	_, _, err = UnwrapOnionLayer(blob, wrongPrivKey)
	if err == nil {
		t.Error("UnwrapOnionLayer should fail with wrong private key")
	}
}

func TestUnwrapTamperedBlob(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	payload := []byte("secret message")
	nextHop := "relay-2"

	blob, err := WrapOnionLayer(payload, nextHop, pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer failed: %v", err)
	}

	// Tamper with the ciphertext portion
	tampered := make([]byte, len(blob))
	copy(tampered, blob)
	tampered[len(tampered)-5] ^= 0xff

	// Should fail authentication
	_, _, err = UnwrapOnionLayer(tampered, privKey)
	if err == nil {
		t.Error("UnwrapOnionLayer should fail with tampered blob")
	}
}

func TestBuildOnion(t *testing.T) {
	// Create 3 relay key pairs
	numRelays := 3
	privKeys := make([]*ecdh.PrivateKey, numRelays)
	pubKeys := make([]*ecdh.PublicKey, numRelays)
	path := make([]string, numRelays)

	for i := 0; i < numRelays; i++ {
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey %d failed: %v", i, err)
		}
		privKeys[i] = priv
		pubKeys[i] = priv.PublicKey()
		path[i] = "relay-" + string(rune('1'+i)) + ":8081"
	}
	// Fix path names
	path[0] = "relay-1:8081"
	path[1] = "relay-2:8081"
	path[2] = "relay-3:8081"

	originalPayload := []byte("final secret message")

	// Build the onion
	onion, err := BuildOnion(originalPayload, path, pubKeys)
	if err != nil {
		t.Fatalf("BuildOnion failed: %v", err)
	}

	// Peel layer 1 (relay-1)
	nextHop, innerBlob, err := UnwrapOnionLayer(onion, privKeys[0])
	if err != nil {
		t.Fatalf("Unwrap layer 1 failed: %v", err)
	}
	if nextHop != "relay-2:8081" {
		t.Errorf("Layer 1 nextHop = %q, want %q", nextHop, "relay-2:8081")
	}

	// Peel layer 2 (relay-2)
	nextHop, innerBlob, err = UnwrapOnionLayer(innerBlob, privKeys[1])
	if err != nil {
		t.Fatalf("Unwrap layer 2 failed: %v", err)
	}
	if nextHop != "relay-3:8081" {
		t.Errorf("Layer 2 nextHop = %q, want %q", nextHop, "relay-3:8081")
	}

	// Peel layer 3 (relay-3)
	nextHop, finalPayload, err := UnwrapOnionLayer(innerBlob, privKeys[2])
	if err != nil {
		t.Fatalf("Unwrap layer 3 failed: %v", err)
	}
	if nextHop != "validator" {
		t.Errorf("Layer 3 nextHop = %q, want %q", nextHop, "validator")
	}
	if !bytes.Equal(finalPayload, originalPayload) {
		t.Errorf("Final payload = %v, want %v", finalPayload, originalPayload)
	}
}

func TestWrapNilPublicKey(t *testing.T) {
	_, err := WrapOnionLayer([]byte("test"), "next", nil)
	if err == nil {
		t.Error("WrapOnionLayer should fail with nil public key")
	}
}

func TestUnwrapNilPrivateKey(t *testing.T) {
	_, _, err := UnwrapOnionLayer(make([]byte, 100), nil)
	if err == nil {
		t.Error("UnwrapOnionLayer should fail with nil private key")
	}
}

func TestUnwrapTooShortBlob(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)

	// Too short to contain even the minimum required data
	shortBlob := make([]byte, 30)
	_, _, err := UnwrapOnionLayer(shortBlob, privKey)
	if err == nil {
		t.Error("UnwrapOnionLayer should fail with too short blob")
	}
}

func TestWrapLongNextHop(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	// NextHop longer than 255 bytes
	longNextHop := make([]byte, 300)
	for i := range longNextHop {
		longNextHop[i] = 'a'
	}

	_, err := WrapOnionLayer([]byte("test"), string(longNextHop), pubKey)
	if err == nil {
		t.Error("WrapOnionLayer should fail with too long nextHop")
	}
}

func TestEmptyPayload(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	// Empty payload should work
	blob, err := WrapOnionLayer([]byte{}, "relay-2", pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer with empty payload failed: %v", err)
	}

	nextHop, payload, err := UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if nextHop != "relay-2" {
		t.Errorf("nextHop = %q, want %q", nextHop, "relay-2")
	}
	if len(payload) != 0 {
		t.Errorf("payload length = %d, want 0", len(payload))
	}
}

func TestEmptyNextHop(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	// Empty nextHop should work (edge case)
	blob, err := WrapOnionLayer([]byte("payload"), "", pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer with empty nextHop failed: %v", err)
	}

	nextHop, payload, err := UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if nextHop != "" {
		t.Errorf("nextHop = %q, want empty string", nextHop)
	}
	if !bytes.Equal(payload, []byte("payload")) {
		t.Errorf("payload mismatch")
	}
}

func TestOnionLayerOverhead(t *testing.T) {
	// Test overhead calculation
	overhead := OnionLayerOverhead(10) // 10 byte nextHop
	// 32 (pubkey) + 12 (nonce) + 1 (len) + 10 (nextHop) + 16 (tag) = 71
	expected := 71
	if overhead != expected {
		t.Errorf("OnionLayerOverhead(10) = %d, want %d", overhead, expected)
	}
}

func TestBuildOnionEmptyPath(t *testing.T) {
	_, err := BuildOnion([]byte("test"), []string{}, []*ecdh.PublicKey{})
	if err == nil {
		t.Error("BuildOnion should fail with empty path")
	}
}

func TestBuildOnionMismatchedLengths(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	_, err := BuildOnion([]byte("test"), []string{"relay-1", "relay-2"}, []*ecdh.PublicKey{pubKey})
	if err == nil {
		t.Error("BuildOnion should fail with mismatched path and pubKeys lengths")
	}
}

func TestLargePayload(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	// Large payload (10KB)
	largePayload := make([]byte, 10*1024)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	blob, err := WrapOnionLayer(largePayload, "relay-2", pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer with large payload failed: %v", err)
	}

	_, gotPayload, err := UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if !bytes.Equal(gotPayload, largePayload) {
		t.Error("Large payload was corrupted")
	}
}

func TestMaxNextHopLength(t *testing.T) {
	privKey, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pubKey := privKey.PublicKey()

	// Exactly 255 bytes (maximum)
	maxNextHop := make([]byte, 255)
	for i := range maxNextHop {
		maxNextHop[i] = 'x'
	}

	blob, err := WrapOnionLayer([]byte("payload"), string(maxNextHop), pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer with max nextHop failed: %v", err)
	}

	gotNextHop, _, err := UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if gotNextHop != string(maxNextHop) {
		t.Error("Max-length nextHop was corrupted")
	}
}

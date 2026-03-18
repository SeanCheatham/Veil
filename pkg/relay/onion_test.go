package relay

import (
	"bytes"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if kp1 == nil {
		t.Fatal("GenerateKeyPair returned nil")
	}

	// Verify keys are non-zero
	var zero [KeySize]byte
	if kp1.PublicKey == zero {
		t.Error("Public key is all zeros")
	}
	if kp1.PrivateKey == zero {
		t.Error("Private key is all zeros")
	}

	// Generate another pair and verify they're different
	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if kp1.PublicKey == kp2.PublicKey {
		t.Error("Two generated key pairs have same public key")
	}
}

func TestGenerateMessageID(t *testing.T) {
	id1, err := GenerateMessageID()
	if err != nil {
		t.Fatalf("GenerateMessageID failed: %v", err)
	}

	if len(id1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("Expected ID length 32, got %d", len(id1))
	}

	// Generate another and verify they're different
	id2, err := GenerateMessageID()
	if err != nil {
		t.Fatalf("GenerateMessageID failed: %v", err)
	}

	if id1 == id2 {
		t.Error("Two generated IDs are the same")
	}
}

func TestWrapAndPeelLayer(t *testing.T) {
	// Generate relay key pair
	relayKeys, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Test data
	nextHop := "relay-2:7000"
	innerPayload := []byte("test inner payload")

	// Wrap a layer
	msg, err := WrapLayer(nextHop, innerPayload, &relayKeys.PublicKey)
	if err != nil {
		t.Fatalf("WrapLayer failed: %v", err)
	}

	if msg == nil {
		t.Fatal("WrapLayer returned nil")
	}

	if msg.ID == "" {
		t.Error("Wrapped message has empty ID")
	}

	// Peel the layer
	layer, newID, err := PeelLayer(msg, &relayKeys.PrivateKey)
	if err != nil {
		t.Fatalf("PeelLayer failed: %v", err)
	}

	// Verify peeled data
	if layer.NextHop != nextHop {
		t.Errorf("Expected next hop %s, got %s", nextHop, layer.NextHop)
	}

	if !bytes.Equal(layer.InnerPayload, innerPayload) {
		t.Errorf("Inner payload mismatch: expected %v, got %v", innerPayload, layer.InnerPayload)
	}

	// Verify new ID is different from original (unlinkability)
	if newID == msg.ID {
		t.Error("New ID should be different from original ID for unlinkability")
	}
}

func TestWrapLayerWithWrongKey(t *testing.T) {
	// Generate two key pairs
	relayKeys1, _ := GenerateKeyPair()
	relayKeys2, _ := GenerateKeyPair()

	// Wrap with relay1's public key
	msg, err := WrapLayer("relay-2:7000", []byte("test"), &relayKeys1.PublicKey)
	if err != nil {
		t.Fatalf("WrapLayer failed: %v", err)
	}

	// Try to peel with relay2's private key (should fail)
	_, _, err = PeelLayer(msg, &relayKeys2.PrivateKey)
	if err != ErrDecryptionFailed {
		t.Errorf("Expected ErrDecryptionFailed, got %v", err)
	}
}

func TestSerializeDeserializeOnionMessage(t *testing.T) {
	// Generate a key pair
	relayKeys, _ := GenerateKeyPair()

	// Create an onion message
	original, err := WrapLayer("relay-2:7000", []byte("test payload"), &relayKeys.PublicKey)
	if err != nil {
		t.Fatalf("WrapLayer failed: %v", err)
	}

	// Serialize
	data := SerializeOnionMessage(original)

	// Deserialize
	restored, err := DeserializeOnionMessage(data)
	if err != nil {
		t.Fatalf("DeserializeOnionMessage failed: %v", err)
	}

	// Verify fields match
	if restored.ID != original.ID {
		t.Errorf("ID mismatch: %s vs %s", restored.ID, original.ID)
	}

	if restored.Nonce != original.Nonce {
		t.Error("Nonce mismatch")
	}

	if restored.SenderPubKey != original.SenderPubKey {
		t.Error("SenderPubKey mismatch")
	}

	if !bytes.Equal(restored.Ciphertext, original.Ciphertext) {
		t.Error("Ciphertext mismatch")
	}
}

func TestCreateOnion(t *testing.T) {
	// Create 3 relay key pairs
	relay1, _ := GenerateKeyPair()
	relay2, _ := GenerateKeyPair()
	relay3, _ := GenerateKeyPair()

	// Define path
	path := []PathHop{
		{Address: "relay-1:7000", PublicKey: relay1.PublicKey},
		{Address: "relay-2:7000", PublicKey: relay2.PublicKey},
		{Address: "validator-1:9000", PublicKey: relay3.PublicKey},
	}

	payload := []byte("final message payload")

	// Create onion
	onion, err := CreateOnion(path, payload)
	if err != nil {
		t.Fatalf("CreateOnion failed: %v", err)
	}

	// Peel first layer (relay 1)
	layer1, _, err := PeelLayer(onion, &relay1.PrivateKey)
	if err != nil {
		t.Fatalf("Failed to peel layer 1: %v", err)
	}

	if layer1.NextHop != "relay-2:7000" {
		t.Errorf("Expected next hop relay-2:7000, got %s", layer1.NextHop)
	}

	// Deserialize inner message
	msg2, err := DeserializeOnionMessage(layer1.InnerPayload)
	if err != nil {
		t.Fatalf("Failed to deserialize layer 1 inner: %v", err)
	}

	// Peel second layer (relay 2)
	layer2, _, err := PeelLayer(msg2, &relay2.PrivateKey)
	if err != nil {
		t.Fatalf("Failed to peel layer 2: %v", err)
	}

	if layer2.NextHop != "validator-1:9000" {
		t.Errorf("Expected next hop validator-1:9000, got %s", layer2.NextHop)
	}

	// Deserialize inner message
	msg3, err := DeserializeOnionMessage(layer2.InnerPayload)
	if err != nil {
		t.Fatalf("Failed to deserialize layer 2 inner: %v", err)
	}

	// Peel third layer (relay 3 / validator)
	layer3, _, err := PeelLayer(msg3, &relay3.PrivateKey)
	if err != nil {
		t.Fatalf("Failed to peel layer 3: %v", err)
	}

	// The final payload should be our original message
	if !bytes.Equal(layer3.InnerPayload, payload) {
		t.Errorf("Final payload mismatch: expected %v, got %v", payload, layer3.InnerPayload)
	}
}

func TestEncodeDecodeLayer(t *testing.T) {
	tests := []struct {
		name     string
		nextHop  string
		payload  []byte
	}{
		{"simple", "relay-1:7000", []byte("hello")},
		{"empty_payload", "relay-2:7000", []byte{}},
		{"long_hop", "really-long-hostname.example.com:7000", []byte("test")},
		{"binary_payload", "relay:7000", []byte{0x00, 0x01, 0x02, 0xff}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := encodeLayer(tt.nextHop, tt.payload)
			if err != nil {
				t.Fatalf("encodeLayer failed: %v", err)
			}

			decoded, err := decodeLayer(encoded)
			if err != nil {
				t.Fatalf("decodeLayer failed: %v", err)
			}

			if decoded.NextHop != tt.nextHop {
				t.Errorf("NextHop mismatch: %s vs %s", decoded.NextHop, tt.nextHop)
			}

			if !bytes.Equal(decoded.InnerPayload, tt.payload) {
				t.Errorf("Payload mismatch")
			}
		})
	}
}

func TestUnlinkability(t *testing.T) {
	// This test verifies that message IDs change at each hop
	relayKeys, _ := GenerateKeyPair()

	msg, err := WrapLayer("next:7000", []byte("test"), &relayKeys.PublicKey)
	if err != nil {
		t.Fatalf("WrapLayer failed: %v", err)
	}

	originalID := msg.ID

	// Peel layer multiple times (simulating multiple hops with same key)
	for i := 0; i < 10; i++ {
		msg, _ = WrapLayer("next:7000", []byte("test"), &relayKeys.PublicKey)
		_, newID, err := PeelLayer(msg, &relayKeys.PrivateKey)
		if err != nil {
			t.Fatalf("PeelLayer failed at iteration %d: %v", i, err)
		}

		if newID == originalID {
			t.Errorf("New ID matches original ID at iteration %d - unlinkability violated", i)
		}

		if newID == msg.ID {
			t.Errorf("New ID matches inbound ID at iteration %d - unlinkability violated", i)
		}
	}
}

package crypto

import (
	"testing"
)

func TestKeyPairGeneration(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// Generate a key pair for epoch 1
	kp, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Verify key pair is properly constructed
	if kp.PrivateKey == nil {
		t.Error("PrivateKey should not be nil")
	}
	if kp.PublicKey == nil {
		t.Error("PublicKey should not be nil")
	}
	if kp.RelayID != "relay-1" {
		t.Errorf("RelayID = %q, want %q", kp.RelayID, "relay-1")
	}
	if kp.Epoch != 1 {
		t.Errorf("Epoch = %d, want %d", kp.Epoch, 1)
	}
}

func TestKeyPairIDFormat(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	kp, err := manager.GenerateKeyPair(5)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	expectedID := "relay-1:epoch-5"
	if kp.KeyID() != expectedID {
		t.Errorf("KeyID() = %q, want %q", kp.KeyID(), expectedID)
	}
}

func TestGenerateKeyPairIdempotent(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// Generate key pair for epoch 1
	kp1, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate again for same epoch - should return same key pair
	kp2, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Should be the exact same key pair
	if kp1 != kp2 {
		t.Error("Expected same key pair for same epoch")
	}
}

func TestGetKeyPair(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// Get key pair that doesn't exist
	kp := manager.GetKeyPair(1)
	if kp != nil {
		t.Error("GetKeyPair for non-existent epoch should return nil")
	}

	// Generate and then get
	_, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	kp = manager.GetKeyPair(1)
	if kp == nil {
		t.Error("GetKeyPair should return key pair after generation")
	}
}

func TestGetKeyPairWithContext(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	_, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Correct context should return key pair
	kp := manager.GetKeyPairWithContext(1, "relay-1")
	if kp == nil {
		t.Error("GetKeyPairWithContext should return key pair with correct context")
	}

	// Wrong context should return nil
	kp = manager.GetKeyPairWithContext(1, "relay-2")
	if kp != nil {
		t.Error("GetKeyPairWithContext should return nil with wrong context")
	}
}

func TestPublicKey(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// PublicKey for non-existent epoch
	pk := manager.PublicKey(1)
	if pk != nil {
		t.Error("PublicKey for non-existent epoch should return nil")
	}

	// Generate and get public key
	kp, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	pk = manager.PublicKey(1)
	if pk == nil {
		t.Error("PublicKey should return public key after generation")
	}

	// Should be the same public key
	if pk != kp.PublicKey {
		t.Error("PublicKey should return the same public key as in key pair")
	}
}

func TestPruneOldKeys(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// Generate keys for epochs 1, 2, 3
	for i := uint64(1); i <= 3; i++ {
		_, err := manager.GenerateKeyPair(i)
		if err != nil {
			t.Fatalf("GenerateKeyPair failed for epoch %d: %v", i, err)
		}
	}

	if manager.CurrentKeys() != 3 {
		t.Errorf("CurrentKeys() = %d, want 3", manager.CurrentKeys())
	}

	// Prune keys older than epoch 2 (keeps epoch 2 and 3)
	manager.pruneOldKeys(2)

	if manager.CurrentKeys() != 2 {
		t.Errorf("After pruning, CurrentKeys() = %d, want 2", manager.CurrentKeys())
	}

	// Epoch 1 should be gone
	if manager.GetKeyPair(1) != nil {
		t.Error("Epoch 1 key should have been pruned")
	}

	// Epochs 2 and 3 should still exist
	if manager.GetKeyPair(2) == nil {
		t.Error("Epoch 2 key should still exist")
	}
	if manager.GetKeyPair(3) == nil {
		t.Error("Epoch 3 key should still exist")
	}
}

func TestMultipleKeyPairsForDifferentEpochs(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	// Generate keys for multiple epochs
	keys := make([]*KeyPair, 5)
	for i := uint64(1); i <= 5; i++ {
		kp, err := manager.GenerateKeyPair(i)
		if err != nil {
			t.Fatalf("GenerateKeyPair failed for epoch %d: %v", i, err)
		}
		keys[i-1] = kp
	}

	// Verify all keys are unique
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			// Private keys should be different
			if keys[i].PrivateKey.Equal(keys[j].PrivateKey) {
				t.Errorf("Keys for epochs %d and %d should be different", i+1, j+1)
			}
		}
	}

	// Verify all can be retrieved
	for i := uint64(1); i <= 5; i++ {
		kp := manager.GetKeyPair(i)
		if kp == nil {
			t.Errorf("GetKeyPair(%d) returned nil", i)
		}
		if kp.Epoch != i {
			t.Errorf("GetKeyPair(%d).Epoch = %d, want %d", i, kp.Epoch, i)
		}
	}
}

func TestManagerRelayID(t *testing.T) {
	manager := NewSessionKeyManager("relay-test", "http://localhost:8083")

	if manager.RelayID() != "relay-test" {
		t.Errorf("RelayID() = %q, want %q", manager.RelayID(), "relay-test")
	}
}

func TestX25519KeySize(t *testing.T) {
	manager := NewSessionKeyManager("relay-1", "http://localhost:8083")

	kp, err := manager.GenerateKeyPair(1)
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// X25519 public keys are 32 bytes
	publicKeyBytes := kp.PublicKey.Bytes()
	if len(publicKeyBytes) != 32 {
		t.Errorf("PublicKey length = %d, want 32", len(publicKeyBytes))
	}
}

package crypto

import (
	"testing"
)

func TestNewKeyRing(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}
	if kr.previous != nil {
		t.Fatal("new KeyRing should have no previous key")
	}
	// Current key should be non-zero
	var zero PublicKey
	if kr.Current().Public == zero {
		t.Fatal("current public key should not be zero")
	}
}

func TestRotateAdvancesKeys(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	firstKey := kr.Current()

	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}

	secondKey := kr.Current()
	if secondKey.Public == firstKey.Public {
		t.Fatal("rotation should produce a new key")
	}
	if kr.previous == nil {
		t.Fatal("previous key should be set after rotation")
	}
	if kr.previous.Public != firstKey.Public {
		t.Fatal("previous key should be the old current key")
	}

	// Rotate again: previous should now be secondKey
	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	thirdKey := kr.Current()
	if thirdKey.Public == secondKey.Public {
		t.Fatal("second rotation should produce a new key")
	}
	if kr.previous.Public != secondKey.Public {
		t.Fatal("previous should be the second key after second rotation")
	}
}

func TestTryDecryptCurrentKey(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	plaintext := []byte("hello world")
	currentKey := kr.Current()

	// Encrypt to current key
	ciphertext, err := encryptLayer(plaintext, "", &currentKey.Public)
	if err != nil {
		t.Fatalf("encryptLayer error: %v", err)
	}

	decrypted, err := kr.TryDecrypt(ciphertext)
	if err != nil {
		t.Fatalf("TryDecrypt with current key failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestTryDecryptPreviousKey(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	plaintext := []byte("hello world")
	oldKey := kr.Current()

	// Encrypt to old key
	ciphertext, err := encryptLayer(plaintext, "", &oldKey.Public)
	if err != nil {
		t.Fatalf("encryptLayer error: %v", err)
	}

	// Rotate keys
	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}

	// Should still decrypt with previous key
	decrypted, err := kr.TryDecrypt(ciphertext)
	if err != nil {
		t.Fatalf("TryDecrypt with previous key failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestTryDecryptFailsWithNeitherKey(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	plaintext := []byte("hello world")
	oldKey := kr.Current()

	// Encrypt to old key
	ciphertext, err := encryptLayer(plaintext, "", &oldKey.Public)
	if err != nil {
		t.Fatalf("encryptLayer error: %v", err)
	}

	// Rotate twice — old key is now gone
	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}

	_, err = kr.TryDecrypt(ciphertext)
	if err == nil {
		t.Fatal("TryDecrypt should fail when neither key matches")
	}
}

func TestTryPeelLayerCurrentKey(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	innerData := []byte("inner payload")
	currentKey := kr.Current()

	ciphertext, err := encryptLayer(innerData, "next-relay:8083", &currentKey.Public)
	if err != nil {
		t.Fatalf("encryptLayer error: %v", err)
	}

	inner, nextHop, isFinal, err := kr.TryPeelLayer(ciphertext)
	if err != nil {
		t.Fatalf("TryPeelLayer failed: %v", err)
	}
	if string(inner) != string(innerData) {
		t.Fatalf("inner = %q, want %q", inner, innerData)
	}
	if nextHop != "next-relay:8083" {
		t.Fatalf("nextHop = %q, want %q", nextHop, "next-relay:8083")
	}
	if isFinal {
		t.Fatal("isFinal should be false")
	}
}

func TestTryPeelLayerPreviousKey(t *testing.T) {
	kr, err := NewKeyRing()
	if err != nil {
		t.Fatalf("NewKeyRing() error: %v", err)
	}

	innerData := []byte("inner payload")
	oldKey := kr.Current()

	ciphertext, err := encryptLayer(innerData, "", &oldKey.Public)
	if err != nil {
		t.Fatalf("encryptLayer error: %v", err)
	}

	if err := kr.Rotate(); err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}

	inner, nextHop, isFinal, err := kr.TryPeelLayer(ciphertext)
	if err != nil {
		t.Fatalf("TryPeelLayer with previous key failed: %v", err)
	}
	if string(inner) != string(innerData) {
		t.Fatalf("inner = %q, want %q", inner, innerData)
	}
	if nextHop != "" {
		t.Fatalf("nextHop = %q, want empty", nextHop)
	}
	if !isFinal {
		t.Fatal("isFinal should be true")
	}
}

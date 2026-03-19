package crypto

import (
	"strings"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	// Generate two key pairs
	kp1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	kp2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Keys should be 32 bytes
	if len(kp1.PublicKey) != 32 {
		t.Errorf("expected 32-byte public key, got %d", len(kp1.PublicKey))
	}
	if len(kp1.PrivateKey) != 32 {
		t.Errorf("expected 32-byte private key, got %d", len(kp1.PrivateKey))
	}

	// Different key pairs should have different keys
	if kp1.PublicKey == kp2.PublicKey {
		t.Error("two generated key pairs should have different public keys")
	}
	if kp1.PrivateKey == kp2.PrivateKey {
		t.Error("two generated key pairs should have different private keys")
	}
}

func TestGenerateKeyPairFromSeed_Deterministic(t *testing.T) {
	seed := []byte("test-seed-for-deterministic-key-generation")

	// Generate from same seed twice
	kp1, err := GenerateKeyPairFromSeed(seed)
	if err != nil {
		t.Fatalf("GenerateKeyPairFromSeed failed: %v", err)
	}

	kp2, err := GenerateKeyPairFromSeed(seed)
	if err != nil {
		t.Fatalf("GenerateKeyPairFromSeed failed: %v", err)
	}

	// Same seed should produce same keys
	if kp1.PublicKey != kp2.PublicKey {
		t.Error("same seed should produce same public key")
	}
	if kp1.PrivateKey != kp2.PrivateKey {
		t.Error("same seed should produce same private key")
	}
}

func TestGenerateKeyPairFromSeed_DifferentSeeds(t *testing.T) {
	seed1 := []byte("seed-alpha")
	seed2 := []byte("seed-beta")

	kp1, err := GenerateKeyPairFromSeed(seed1)
	if err != nil {
		t.Fatalf("GenerateKeyPairFromSeed failed: %v", err)
	}

	kp2, err := GenerateKeyPairFromSeed(seed2)
	if err != nil {
		t.Fatalf("GenerateKeyPairFromSeed failed: %v", err)
	}

	// Different seeds should produce different keys
	if kp1.PublicKey == kp2.PublicKey {
		t.Error("different seeds should produce different public keys")
	}
	if kp1.PrivateKey == kp2.PrivateKey {
		t.Error("different seeds should produce different private keys")
	}
}

func TestGenerateKeyPairFromSeed_EmptySeed(t *testing.T) {
	_, err := GenerateKeyPairFromSeed([]byte{})
	if err == nil {
		t.Error("GenerateKeyPairFromSeed with empty seed should fail")
	}
}

func TestRecipientEncryptDecrypt_RoundTrip(t *testing.T) {
	// Generate recipient key pair
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Encrypt a message
	payload := []byte("Hello, this is a secret message!")
	encMsg, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	// Verify encrypted message structure
	if encMsg.EphemeralPubKey == "" {
		t.Error("EphemeralPubKey should not be empty")
	}
	if encMsg.Ciphertext == "" {
		t.Error("Ciphertext should not be empty")
	}
	if encMsg.RecipientPubKey == "" {
		t.Error("RecipientPubKey should not be empty")
	}

	// Decrypt the message
	decrypted, err := DecryptFromSender(encMsg, recipient.PrivateKey)
	if err != nil {
		t.Fatalf("DecryptFromSender failed: %v", err)
	}

	if string(decrypted) != string(payload) {
		t.Errorf("decrypted payload mismatch: got %q, want %q", string(decrypted), string(payload))
	}
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	// Generate two different recipients
	recipientA, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	recipientB, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Encrypt for recipient A
	payload := []byte("Secret message for recipient A")
	encMsg, err := EncryptForRecipient(payload, recipientA.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	// Try to decrypt with recipient B's key (should fail)
	_, err = DecryptFromSender(encMsg, recipientB.PrivateKey)
	if err == nil {
		t.Error("DecryptFromSender with wrong key should fail")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("expected authentication error, got: %v", err)
	}
}

func TestEncryptDecrypt_DeterministicKeys(t *testing.T) {
	// Use seeded key generation for deterministic testing
	senderSeed := []byte("test-sender-seed")
	recipientSeed := []byte("test-recipient-seed")

	recipient, err := GenerateKeyPairFromSeed(recipientSeed)
	if err != nil {
		t.Fatalf("GenerateKeyPairFromSeed failed: %v", err)
	}

	_ = senderSeed // Sender doesn't need a seeded key; ephemeral key is always random

	// Encrypt and decrypt
	payload := []byte("Deterministic test message")
	encMsg, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	decrypted, err := DecryptFromSender(encMsg, recipient.PrivateKey)
	if err != nil {
		t.Fatalf("DecryptFromSender failed: %v", err)
	}

	if string(decrypted) != string(payload) {
		t.Errorf("decrypted payload mismatch: got %q, want %q", string(decrypted), string(payload))
	}
}

func TestRecipientEncrypt_DifferentCiphertexts(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	payload := []byte("Same message encrypted twice")

	// Encrypt twice
	encMsg1, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("First EncryptForRecipient failed: %v", err)
	}

	encMsg2, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("Second EncryptForRecipient failed: %v", err)
	}

	// Ephemeral keys should be different
	if encMsg1.EphemeralPubKey == encMsg2.EphemeralPubKey {
		t.Error("each encryption should use different ephemeral keys")
	}

	// Ciphertexts should be different (different nonces)
	if encMsg1.Ciphertext == encMsg2.Ciphertext {
		t.Error("same payload should produce different ciphertexts")
	}

	// Both should decrypt to same content
	dec1, _ := DecryptFromSender(encMsg1, recipient.PrivateKey)
	dec2, _ := DecryptFromSender(encMsg2, recipient.PrivateKey)

	if string(dec1) != string(dec2) {
		t.Error("both ciphertexts should decrypt to same content")
	}
}

func TestSerializeParseEncryptedMessage_RoundTrip(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	payload := []byte("Test message for serialization")
	encMsg, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	// Serialize
	serialized, err := SerializeEncryptedMessage(encMsg)
	if err != nil {
		t.Fatalf("SerializeEncryptedMessage failed: %v", err)
	}

	// Should be valid JSON
	if serialized == "" {
		t.Error("serialized message should not be empty")
	}

	// Parse back
	parsed, err := ParseEncryptedMessage(serialized)
	if err != nil {
		t.Fatalf("ParseEncryptedMessage failed: %v", err)
	}

	// Should match original
	if parsed.EphemeralPubKey != encMsg.EphemeralPubKey {
		t.Error("parsed EphemeralPubKey should match original")
	}
	if parsed.Ciphertext != encMsg.Ciphertext {
		t.Error("parsed Ciphertext should match original")
	}
	if parsed.RecipientPubKey != encMsg.RecipientPubKey {
		t.Error("parsed RecipientPubKey should match original")
	}

	// Should still decrypt correctly
	decrypted, err := DecryptFromSender(parsed, recipient.PrivateKey)
	if err != nil {
		t.Fatalf("DecryptFromSender after parse failed: %v", err)
	}

	if string(decrypted) != string(payload) {
		t.Errorf("decrypted payload mismatch: got %q, want %q", string(decrypted), string(payload))
	}
}

func TestParseEncryptedMessage_InvalidJSON(t *testing.T) {
	_, err := ParseEncryptedMessage("not valid json")
	if err == nil {
		t.Error("ParseEncryptedMessage with invalid JSON should fail")
	}
}

func TestDecryptFromSender_InvalidEphemeralPubKey(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Create message with invalid ephemeral public key
	msg := &EncryptedMessage{
		EphemeralPubKey: "not-valid-base64!!!",
		Ciphertext:      "YWJjZGVm",
		RecipientPubKey: PublicKeyToBase64(recipient.PublicKey),
	}

	_, err = DecryptFromSender(msg, recipient.PrivateKey)
	if err == nil {
		t.Error("DecryptFromSender with invalid ephemeral key should fail")
	}
}

func TestDecryptFromSender_ShortEphemeralPubKey(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Create message with short ephemeral public key (not 32 bytes)
	msg := &EncryptedMessage{
		EphemeralPubKey: "c2hvcnQ=", // "short" in base64
		Ciphertext:      "YWJjZGVm",
		RecipientPubKey: PublicKeyToBase64(recipient.PublicKey),
	}

	_, err = DecryptFromSender(msg, recipient.PrivateKey)
	if err == nil {
		t.Error("DecryptFromSender with short ephemeral key should fail")
	}
}

func TestDecryptFromSender_ShortCiphertext(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Generate a valid ephemeral key
	ephemeral, _ := GenerateKeyPair()

	// Create message with short ciphertext (less than nonce size)
	msg := &EncryptedMessage{
		EphemeralPubKey: PublicKeyToBase64(ephemeral.PublicKey),
		Ciphertext:      "YQ==", // "a" in base64, too short
		RecipientPubKey: PublicKeyToBase64(recipient.PublicKey),
	}

	_, err = DecryptFromSender(msg, recipient.PrivateKey)
	if err == nil {
		t.Error("DecryptFromSender with short ciphertext should fail")
	}
}

func TestPublicKeyBase64_RoundTrip(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	encoded := PublicKeyToBase64(kp.PublicKey)
	decoded, err := PublicKeyFromBase64(encoded)
	if err != nil {
		t.Fatalf("PublicKeyFromBase64 failed: %v", err)
	}

	if decoded != kp.PublicKey {
		t.Error("decoded public key should match original")
	}
}

func TestPrivateKeyBase64_RoundTrip(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	encoded := PrivateKeyToBase64(kp.PrivateKey)
	decoded, err := PrivateKeyFromBase64(encoded)
	if err != nil {
		t.Fatalf("PrivateKeyFromBase64 failed: %v", err)
	}

	if decoded != kp.PrivateKey {
		t.Error("decoded private key should match original")
	}
}

func TestPublicKeyFromBase64_Invalid(t *testing.T) {
	// Invalid base64
	_, err := PublicKeyFromBase64("not-valid!!!")
	if err == nil {
		t.Error("PublicKeyFromBase64 with invalid base64 should fail")
	}

	// Wrong length
	_, err = PublicKeyFromBase64("c2hvcnQ=") // "short"
	if err == nil {
		t.Error("PublicKeyFromBase64 with wrong length should fail")
	}
}

func TestPrivateKeyFromBase64_Invalid(t *testing.T) {
	// Invalid base64
	_, err := PrivateKeyFromBase64("not-valid!!!")
	if err == nil {
		t.Error("PrivateKeyFromBase64 with invalid base64 should fail")
	}

	// Wrong length
	_, err = PrivateKeyFromBase64("c2hvcnQ=") // "short"
	if err == nil {
		t.Error("PrivateKeyFromBase64 with wrong length should fail")
	}
}

func TestEncryptDecrypt_LargePayload(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Create a large payload (10KB)
	payload := make([]byte, 10*1024)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	encMsg, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	decrypted, err := DecryptFromSender(encMsg, recipient.PrivateKey)
	if err != nil {
		t.Fatalf("DecryptFromSender failed: %v", err)
	}

	if string(decrypted) != string(payload) {
		t.Error("large payload decryption mismatch")
	}
}

func TestEncryptDecrypt_EmptyPayload(t *testing.T) {
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Encrypt empty payload
	payload := []byte{}
	encMsg, err := EncryptForRecipient(payload, recipient.PublicKey)
	if err != nil {
		t.Fatalf("EncryptForRecipient failed: %v", err)
	}

	decrypted, err := DecryptFromSender(encMsg, recipient.PrivateKey)
	if err != nil {
		t.Fatalf("DecryptFromSender failed: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(decrypted))
	}
}

package crypto

import (
	"bytes"
	"testing"
)

func TestRoundTrip3Relays(t *testing.T) {
	// Generate 3 relay keypairs + 1 recipient keypair
	relay1, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	relay2, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	relay3, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("hello anonymous world")

	relayPubs := []PublicKey{relay1.Public, relay2.Public, relay3.Public}
	relayHosts := []string{"relay-1:8083", "relay-2:8083", "relay-3:8083"}

	wrapped, err := WrapMessage(plaintext, recipient.Public, relayPubs, relayHosts)
	if err != nil {
		t.Fatal(err)
	}

	// Peel layer 1 (relay-1)
	inner1, nextHop1, isFinal1, err := PeelLayer(wrapped, relay1.Private)
	if err != nil {
		t.Fatal(err)
	}
	if isFinal1 {
		t.Error("expected isFinal=false for first relay")
	}
	if nextHop1 != "relay-2:8083" {
		t.Errorf("expected next hop relay-2:8083, got %s", nextHop1)
	}

	// Peel layer 2 (relay-2)
	inner2, nextHop2, isFinal2, err := PeelLayer(inner1, relay2.Private)
	if err != nil {
		t.Fatal(err)
	}
	if isFinal2 {
		t.Error("expected isFinal=false for second relay")
	}
	if nextHop2 != "relay-3:8083" {
		t.Errorf("expected next hop relay-3:8083, got %s", nextHop2)
	}

	// Peel layer 3 (relay-3) — last relay, isFinal should be true
	inner3, nextHop3, isFinal3, err := PeelLayer(inner2, relay3.Private)
	if err != nil {
		t.Fatal(err)
	}
	if !isFinal3 {
		t.Error("expected isFinal=true for last relay")
	}
	if nextHop3 != "" {
		t.Errorf("expected empty next hop for last relay, got %s", nextHop3)
	}

	// Final decrypt by recipient
	decrypted, err := FinalDecrypt(inner3, recipient.Private)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestRoundTrip1Relay(t *testing.T) {
	relay, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("single relay test")

	wrapped, err := WrapMessage(plaintext, recipient.Public, []PublicKey{relay.Public}, []string{"relay-1:8083"})
	if err != nil {
		t.Fatal(err)
	}

	// Peel the only relay layer — should be final
	inner, nextHop, isFinal, err := PeelLayer(wrapped, relay.Private)
	if err != nil {
		t.Fatal(err)
	}
	if !isFinal {
		t.Error("expected isFinal=true for single relay")
	}
	if nextHop != "" {
		t.Errorf("expected empty next hop, got %s", nextHop)
	}

	// Final decrypt
	decrypted, err := FinalDecrypt(inner, recipient.Private)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestPeelLayerWrongKey(t *testing.T) {
	relay, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("wrong key test")
	wrapped, err := WrapMessage(plaintext, recipient.Public, []PublicKey{relay.Public}, []string{"relay-1:8083"})
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = PeelLayer(wrapped, wrongKey.Private)
	if err == nil {
		t.Error("expected error when peeling with wrong key")
	}
}

func TestFinalDecryptWrongKey(t *testing.T) {
	relay, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("wrong recipient key test")
	wrapped, err := WrapMessage(plaintext, recipient.Public, []PublicKey{relay.Public}, []string{"relay-1:8083"})
	if err != nil {
		t.Fatal(err)
	}

	// Peel relay layer correctly
	inner, _, _, err := PeelLayer(wrapped, relay.Private)
	if err != nil {
		t.Fatal(err)
	}

	// Try to decrypt with wrong recipient key
	_, err = FinalDecrypt(inner, wrongKey.Private)
	if err == nil {
		t.Error("expected error when decrypting with wrong recipient key")
	}
}

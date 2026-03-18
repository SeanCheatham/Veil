package client

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/veil-protocol/veil/pkg/relay"
	"golang.org/x/crypto/nacl/box"
)

func TestNewReceiver(t *testing.T) {
	receiver, err := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatalf("NewReceiver failed: %v", err)
	}

	if receiver == nil {
		t.Fatal("NewReceiver returned nil")
	}

	if receiver.KeyPair == nil {
		t.Error("KeyPair not generated")
	}

	if receiver.PoolAddr != "message-pool:8080" {
		t.Errorf("unexpected pool address: %s", receiver.PoolAddr)
	}
}

func TestNewReceiverWithKeyPair(t *testing.T) {
	keyPair, err := relay.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	receiver, err := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
		KeyPair:  keyPair,
	})
	if err != nil {
		t.Fatalf("NewReceiver failed: %v", err)
	}

	if receiver.KeyPair != keyPair {
		t.Error("provided key pair not used")
	}
}

func TestReceiverTryDecrypt(t *testing.T) {
	// Create receiver with known key pair
	receiverKeyPair, err := relay.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	receiver, err := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
		KeyPair:  receiverKeyPair,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a message encrypted to the receiver's public key
	payload := []byte("secret message for receiver")

	// Generate sender key pair
	senderPub, senderPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// Generate nonce
	var nonce [relay.NonceSize]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}

	// Encrypt
	sealed := box.Seal(nonce[:], payload, &nonce, &receiverKeyPair.PublicKey, senderPriv)

	// Create final payload: [sender_pub:32][nonce:24][sealed:rest]
	finalPayload := make([]byte, relay.KeySize+len(sealed))
	copy(finalPayload[:relay.KeySize], senderPub[:])
	copy(finalPayload[relay.KeySize:], sealed)

	// Try to decrypt
	decrypted, err := receiver.TryDecrypt(finalPayload)
	if err != nil {
		t.Fatalf("TryDecrypt failed: %v", err)
	}

	if string(decrypted) != string(payload) {
		t.Errorf("decrypted message mismatch: got %q, want %q", string(decrypted), string(payload))
	}
}

func TestReceiverTryDecryptWrongKey(t *testing.T) {
	// Create receiver with one key pair
	receiver, err := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a different key pair (not the receiver's)
	otherKeyPair, err := relay.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// Create a message encrypted to the OTHER public key
	payload := []byte("secret message for someone else")

	senderPub, senderPriv, _ := box.GenerateKey(rand.Reader)
	var nonce [relay.NonceSize]byte
	sealed := box.Seal(nonce[:], payload, &nonce, &otherKeyPair.PublicKey, senderPriv)

	finalPayload := make([]byte, relay.KeySize+len(sealed))
	copy(finalPayload[:relay.KeySize], senderPub[:])
	copy(finalPayload[relay.KeySize:], sealed)

	// Try to decrypt - should fail
	_, err = receiver.TryDecrypt(finalPayload)
	if err == nil {
		t.Error("expected decryption to fail with wrong key")
	}
}

func TestReceiverPoll(t *testing.T) {
	// Create a test server that simulates the message pool
	receiverKeyPair, _ := relay.GenerateKeyPair()

	// Create an encrypted message
	senderPub, senderPriv, _ := box.GenerateKey(rand.Reader)
	var nonce [relay.NonceSize]byte
	payload := []byte("test message")
	sealed := box.Seal(nonce[:], payload, &nonce, &receiverKeyPair.PublicKey, senderPriv)
	finalPayload := make([]byte, relay.KeySize+len(sealed))
	copy(finalPayload[:relay.KeySize], senderPub[:])
	copy(finalPayload[relay.KeySize:], sealed)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages":
			resp := ListMessagesResponse{
				Messages: []string{"msg-1"},
				Count:    1,
			}
			json.NewEncoder(w).Encode(resp)
		case "/messages/msg-1":
			resp := GetMessageResponse{
				ID:         "msg-1",
				Ciphertext: base64.StdEncoding.EncodeToString(finalPayload),
				Timestamp:  time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
				Epoch:      1,
			}
			json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	receiver, _ := NewReceiver(ReceiverConfig{
		PoolAddr: server.Listener.Addr().String(),
		KeyPair:  receiverKeyPair,
	})

	// Poll for messages
	newCount, decrypted, err := receiver.Poll()
	if err != nil {
		t.Fatalf("Poll failed: %v", err)
	}

	if newCount != 1 {
		t.Errorf("expected 1 new message, got %d", newCount)
	}

	if len(decrypted) != 1 {
		t.Errorf("expected 1 decrypted message, got %d", len(decrypted))
	}

	if len(decrypted) > 0 && string(decrypted[0].Plaintext) != string(payload) {
		t.Errorf("decrypted payload mismatch")
	}
}

func TestReceiverSeenTracking(t *testing.T) {
	receiver, _ := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
	})

	if receiver.SeenCount() != 0 {
		t.Errorf("initial seen count should be 0, got %d", receiver.SeenCount())
	}

	// Manually mark a message as seen
	receiver.mu.Lock()
	receiver.SeenMessages["test-id"] = true
	receiver.mu.Unlock()

	if receiver.SeenCount() != 1 {
		t.Errorf("seen count should be 1, got %d", receiver.SeenCount())
	}

	receiver.ClearSeen()

	if receiver.SeenCount() != 0 {
		t.Errorf("seen count should be 0 after clear, got %d", receiver.SeenCount())
	}
}

func TestReceiverGetPublicKey(t *testing.T) {
	receiver, _ := NewReceiver(ReceiverConfig{
		PoolAddr: "message-pool:8080",
	})

	pubKey := receiver.GetPublicKey()

	var emptyKey [relay.KeySize]byte
	if pubKey == emptyKey {
		t.Error("GetPublicKey returned empty key")
	}
}

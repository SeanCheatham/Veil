package client

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/veil-protocol/veil/pkg/relay"
)

func TestNewSender(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000"},
		ValidatorAddrs: []string{"validator-1:9000"},
	})

	if sender == nil {
		t.Fatal("NewSender returned nil")
	}

	if len(sender.RelayAddresses) != 3 {
		t.Errorf("expected 3 relay addresses, got %d", len(sender.RelayAddresses))
	}

	if len(sender.SentMessages) != 0 {
		t.Errorf("expected 0 sent messages, got %d", len(sender.SentMessages))
	}
}

func TestSenderFetchRelayKeys(t *testing.T) {
	// Create a test server that returns a mock public key
	keyPair, err := relay.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pubkey" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := PubKeyResponse{
			ID:        "test-relay",
			PublicKey: base64.StdEncoding.EncodeToString(keyPair.PublicKey[:]),
			Epoch:     1,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Extract host:port from test server URL
	addr := server.Listener.Addr().String()

	sender := NewSender(SenderConfig{
		RelayAddresses: []string{addr},
	})

	count, err := sender.FetchRelayKeys()
	if err != nil {
		t.Fatalf("FetchRelayKeys failed: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 key fetched, got %d", count)
	}

	if len(sender.RelayKeys) != 1 {
		t.Errorf("expected 1 cached key, got %d", len(sender.RelayKeys))
	}

	keyInfo, ok := sender.RelayKeys[addr]
	if !ok {
		t.Fatal("key not cached for address")
	}

	if keyInfo.ID != "test-relay" {
		t.Errorf("expected ID 'test-relay', got '%s'", keyInfo.ID)
	}
}

func TestSenderBuildPath(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000", "relay-3:7000", "relay-4:7000"},
	})

	// Manually add cached keys
	for _, addr := range sender.RelayAddresses {
		keyPair, _ := relay.GenerateKeyPair()
		sender.RelayKeys[addr] = RelayKeyInfo{
			ID:        addr,
			PublicKey: keyPair.PublicKey,
		}
	}

	path, err := sender.BuildPath()
	if err != nil {
		t.Fatalf("BuildPath failed: %v", err)
	}

	if len(path) != 3 {
		t.Errorf("expected 3-hop path, got %d hops", len(path))
	}

	// Verify all hops have valid addresses and keys
	for i, hop := range path {
		if hop.Address == "" {
			t.Errorf("hop %d has empty address", i)
		}
		var emptyKey [relay.KeySize]byte
		if hop.PublicKey == emptyKey {
			t.Errorf("hop %d has empty public key", i)
		}
	}
}

func TestSenderBuildPathNotEnoughRelays(t *testing.T) {
	sender := NewSender(SenderConfig{
		RelayAddresses: []string{"relay-1:7000", "relay-2:7000"},
	})

	// Add only 2 relay keys
	for _, addr := range sender.RelayAddresses {
		keyPair, _ := relay.GenerateKeyPair()
		sender.RelayKeys[addr] = RelayKeyInfo{
			ID:        addr,
			PublicKey: keyPair.PublicKey,
		}
	}

	_, err := sender.BuildPath()
	if err == nil {
		t.Error("expected error when fewer than 3 relays, got nil")
	}
}

func TestEncryptForReceiver(t *testing.T) {
	receiverKeyPair, err := relay.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("test message payload")

	ciphertext, senderPubKey, err := encryptForReceiver(payload, &receiverKeyPair.PublicKey)
	if err != nil {
		t.Fatalf("encryptForReceiver failed: %v", err)
	}

	if len(ciphertext) == 0 {
		t.Error("ciphertext is empty")
	}

	if senderPubKey == nil {
		t.Error("sender public key is nil")
	}

	// Verify ciphertext is longer than original payload (due to nonce + overhead)
	minLen := relay.NonceSize + relay.OverheadSize + len(payload)
	if len(ciphertext) < minLen {
		t.Errorf("ciphertext too short: %d < %d", len(ciphertext), minLen)
	}
}

func TestGenerateRandomPayload(t *testing.T) {
	payload, err := GenerateRandomPayload(100)
	if err != nil {
		t.Fatalf("GenerateRandomPayload failed: %v", err)
	}

	if len(payload) != 100 {
		t.Errorf("expected 100 bytes, got %d", len(payload))
	}
}

func TestGenerateRandomPayloadSize(t *testing.T) {
	for i := 0; i < 100; i++ {
		size := GenerateRandomPayloadSize(50, 150)
		if size < 50 || size >= 150 {
			t.Errorf("size %d out of range [50, 150)", size)
		}
	}
}

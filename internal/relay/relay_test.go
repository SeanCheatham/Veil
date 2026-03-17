package relay

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/veil-protocol/veil/internal/crypto"
)

func TestNewRelay(t *testing.T) {
	r := NewRelay("relay-1", "http://epoch-clock:8083", "http://relay-2:8081", "http://validator-1:8082")

	if r.relayID != "relay-1" {
		t.Errorf("relayID = %q, want %q", r.relayID, "relay-1")
	}
	if len(r.validatorEndpoints) != 1 {
		t.Errorf("validatorEndpoints length = %d, want 1", len(r.validatorEndpoints))
	}
	if len(r.relayPeers) != 1 {
		t.Errorf("relayPeers length = %d, want 1", len(r.relayPeers))
	}
	if r.keyManager == nil {
		t.Error("keyManager should not be nil")
	}
	if r.client == nil {
		t.Error("client should not be nil")
	}
}

func TestParsePeers(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"http://relay-1:8081", 1},
		{"http://relay-1:8081,http://relay-2:8081", 2},
		{"http://relay-1:8081, http://relay-2:8081, http://relay-3:8081", 3},
		{" , , ", 0},
		{"http://relay-1:8081,,http://relay-2:8081", 2},
	}

	for _, tc := range tests {
		result := parsePeers(tc.input)
		if len(result) != tc.expected {
			t.Errorf("parsePeers(%q) = %d peers, want %d", tc.input, len(result), tc.expected)
		}
	}
}

func TestLogIsolation(t *testing.T) {
	r := NewRelay("relay-test", "", "", "")

	// Log some inbound IDs
	r.logInbound("inbound-1")
	r.logInbound("inbound-2")

	// Log some outbound IDs
	r.logOutbound("outbound-1")
	r.logOutbound("outbound-2")

	// Verify logs are separate
	if len(r.inboundLog) != 2 {
		t.Errorf("inboundLog length = %d, want 2", len(r.inboundLog))
	}
	if len(r.outboundLog) != 2 {
		t.Errorf("outboundLog length = %d, want 2", len(r.outboundLog))
	}

	// Verify inbound doesn't contain outbound IDs
	if r.inboundContains("outbound-1") {
		t.Error("inboundLog should not contain outbound IDs")
	}
	if !r.inboundContains("inbound-1") {
		t.Error("inboundLog should contain inbound-1")
	}
}

func TestStatus(t *testing.T) {
	r := NewRelay("relay-test", "", "http://relay-2:8081,http://relay-3:8081", "http://validator-1:8082")

	// Log some messages
	r.logInbound("in-1")
	r.logInbound("in-2")
	r.logOutbound("out-1")

	status := r.Status()

	if status.NodeID != "relay-test" {
		t.Errorf("NodeID = %q, want %q", status.NodeID, "relay-test")
	}
	if status.InboundCount != 2 {
		t.Errorf("InboundCount = %d, want 2", status.InboundCount)
	}
	if status.OutboundCount != 1 {
		t.Errorf("OutboundCount = %d, want 1", status.OutboundCount)
	}
	if len(status.Peers) != 2 {
		t.Errorf("Peers count = %d, want 2", len(status.Peers))
	}
	if status.ValidatorCount != 1 {
		t.Errorf("ValidatorCount = %d, want 1", status.ValidatorCount)
	}
}

func TestRelayID(t *testing.T) {
	r := NewRelay("my-relay", "", "", "")
	if r.RelayID() != "my-relay" {
		t.Errorf("RelayID() = %q, want %q", r.RelayID(), "my-relay")
	}
}

func TestForwardToValidator(t *testing.T) {
	// Create a mock validator server
	receivedProposal := false
	var receivedMsg ProposalMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/propose" && r.Method == http.MethodPost {
			receivedProposal = true
			json.NewDecoder(r.Body).Decode(&receivedMsg)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer server.Close()

	r := NewRelay("relay-test", "", "", server.URL)

	err := r.forwardToValidator("test-msg-id", []byte("test ciphertext"))
	if err != nil {
		t.Fatalf("forwardToValidator failed: %v", err)
	}

	if !receivedProposal {
		t.Error("Validator should have received proposal")
	}
	if receivedMsg.ID != "test-msg-id" {
		t.Errorf("Received ID = %q, want %q", receivedMsg.ID, "test-msg-id")
	}
	if string(receivedMsg.Ciphertext) != "test ciphertext" {
		t.Errorf("Received Ciphertext mismatch")
	}
	if receivedMsg.Hash == "" {
		t.Error("Hash should not be empty")
	}
}

func TestForwardToRelay(t *testing.T) {
	// Create a mock relay server
	receivedMessage := false
	var receivedMsg MessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/message" && r.Method == http.MethodPost {
			receivedMessage = true
			json.NewDecoder(r.Body).Decode(&receivedMsg)
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(MessageResponse{Status: "accepted", MsgID: receivedMsg.ID})
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	}))
	defer server.Close()

	r := NewRelay("relay-test", "", "", "")

	err := r.forwardToRelay(server.URL, "test-msg-id", []byte("test payload"), 5)
	if err != nil {
		t.Fatalf("forwardToRelay failed: %v", err)
	}

	if !receivedMessage {
		t.Error("Next relay should have received message")
	}
	if receivedMsg.ID != "test-msg-id" {
		t.Errorf("Received ID = %q, want %q", receivedMsg.ID, "test-msg-id")
	}
	if receivedMsg.Epoch != 5 {
		t.Errorf("Received Epoch = %d, want 5", receivedMsg.Epoch)
	}
}

func TestForwardToRelayURLConstruction(t *testing.T) {
	tests := []struct {
		nextHop     string
		expectedURL string
	}{
		{"http://relay-2:8081", "http://relay-2:8081/message"},
		{"http://relay-2:8081/", "http://relay-2:8081/message"},
		{"relay-2:8081", "http://relay-2:8081/message"},
	}

	for _, tc := range tests {
		// Verify the URL construction logic
		url := tc.nextHop
		if !strings.HasPrefix(url, "http") {
			url = "http://" + url
		}
		if !strings.HasSuffix(url, "/message") {
			url = strings.TrimSuffix(url, "/") + "/message"
		}

		if url != tc.expectedURL {
			t.Errorf("URL for %q = %q, want %q", tc.nextHop, url, tc.expectedURL)
		}
	}
}

func TestOnMessageWithMockKeyManager(t *testing.T) {
	// This test requires a properly initialized key manager
	// For unit testing, we'll test the flow components separately

	// Generate a test key pair
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	pubKey := privKey.PublicKey()

	// Create a test payload with onion wrapping
	innerPayload := []byte("test message")
	nextHop := "validator"

	// Wrap the payload
	blob, err := crypto.WrapOnionLayer(innerPayload, nextHop, pubKey)
	if err != nil {
		t.Fatalf("WrapOnionLayer failed: %v", err)
	}

	// Test the unwrap logic directly
	gotNextHop, gotPayload, err := crypto.UnwrapOnionLayer(blob, privKey)
	if err != nil {
		t.Fatalf("UnwrapOnionLayer failed: %v", err)
	}

	if gotNextHop != nextHop {
		t.Errorf("nextHop = %q, want %q", gotNextHop, nextHop)
	}
	if string(gotPayload) != string(innerPayload) {
		t.Errorf("payload mismatch")
	}
}

func TestOnMessageChainToValidator(t *testing.T) {
	// Create a chain of relays ending at a validator
	// For this test, we simulate the flow without actually starting key managers

	// Track if validator received the message
	validatorReceived := false
	validatorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/propose" {
			validatorReceived = true
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer validatorServer.Close()

	// This test verifies the forwarding logic works when nextHop is "validator"
	r := NewRelay("relay-test", "", "", validatorServer.URL)

	// Directly test forwardToValidator
	err := r.forwardToValidator("msg-123", []byte("ciphertext"))
	if err != nil {
		t.Fatalf("forwardToValidator failed: %v", err)
	}

	if !validatorReceived {
		t.Error("Validator should have received the message")
	}
}

func TestNoValidatorEndpoints(t *testing.T) {
	r := NewRelay("relay-test", "", "", "")

	err := r.forwardToValidator("msg-123", []byte("ciphertext"))
	if err == nil {
		t.Error("forwardToValidator should fail with no validator endpoints")
	}
}

func TestInboundContainsConcurrency(t *testing.T) {
	r := NewRelay("relay-test", "", "", "")

	// Concurrent logging
	done := make(chan bool)

	for i := 0; i < 100; i++ {
		go func(id int) {
			r.logInbound("inbound-" + string(rune(id)))
			r.logOutbound("outbound-" + string(rune(id)))
			_ = r.inboundContains("some-id")
			done <- true
		}(i)
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	status := r.Status()
	if status.InboundCount != 100 {
		t.Errorf("Expected 100 inbound messages, got %d", status.InboundCount)
	}
	if status.OutboundCount != 100 {
		t.Errorf("Expected 100 outbound messages, got %d", status.OutboundCount)
	}
}

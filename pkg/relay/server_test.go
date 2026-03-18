package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewRelay(t *testing.T) {
	cfg := RelayConfig{
		ID:            "1",
		PeerAddresses: []string{"relay-2:7000"},
		ValidatorAddr: "validator-1:9000",
		EpochDuration: 1 * time.Second,
	}

	r, err := NewRelay(cfg)
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	if r.ID != "1" {
		t.Errorf("Expected ID 1, got %s", r.ID)
	}

	if r.validatorAddr != "validator-1:9000" {
		t.Errorf("Expected validator addr validator-1:9000, got %s", r.validatorAddr)
	}
}

func TestRelayStartStop(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})

	if r.IsRunning() {
		t.Error("Relay should not be running initially")
	}

	err := r.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !r.IsRunning() {
		t.Error("Relay should be running after Start")
	}

	// Key pair should be generated
	pubKey, epoch := r.GetPublicKey()
	var zero [KeySize]byte
	if pubKey == zero {
		t.Error("Public key should not be zero after start")
	}
	if epoch == 0 {
		t.Error("Epoch should not be 0 after start")
	}

	r.Stop()

	if r.IsRunning() {
		t.Error("Relay should not be running after Stop")
	}
}

func TestRelayGetStatus(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "test-relay",
		PeerAddresses: []string{"relay-2:7000"},
		EpochDuration: 100 * time.Millisecond,
	})

	r.Start()
	defer r.Stop()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	status := r.GetStatus()

	if status.ID != "test-relay" {
		t.Errorf("Expected ID test-relay, got %s", status.ID)
	}

	if !status.Running {
		t.Error("Expected running to be true")
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	server := NewServer(r, ":7000")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("Expected status healthy, got %s", resp.Status)
	}

	if resp.ID != "1" {
		t.Errorf("Expected ID 1, got %s", resp.ID)
	}
}

func TestServerStatusEndpoint(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		PeerAddresses: []string{"relay-2:7000"},
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	// Give it time to initialize
	time.Sleep(50 * time.Millisecond)

	server := NewServer(r, ":7000")

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.ID != "1" {
		t.Errorf("Expected ID 1, got %s", resp.ID)
	}

	if !resp.Running {
		t.Error("Expected running to be true")
	}
}

func TestServerPubKeyEndpoint(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	time.Sleep(50 * time.Millisecond)

	server := NewServer(r, ":7000")

	req := httptest.NewRequest("GET", "/pubkey", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp PubKeyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.ID != "1" {
		t.Errorf("Expected ID 1, got %s", resp.ID)
	}

	if resp.PublicKey == "" {
		t.Error("Expected non-empty public key")
	}

	// Decode and verify length
	pubKeyBytes, err := base64.StdEncoding.DecodeString(resp.PublicKey)
	if err != nil {
		t.Fatalf("Failed to decode public key: %v", err)
	}

	if len(pubKeyBytes) != KeySize {
		t.Errorf("Expected public key size %d, got %d", KeySize, len(pubKeyBytes))
	}
}

func TestServerForwardEndpointValidation(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	server := NewServer(r, ":7000")

	tests := []struct {
		name         string
		body         string
		expectedCode int
	}{
		{
			name:         "empty body",
			body:         "",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "invalid json",
			body:         "not json",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "missing fields",
			body:         `{"id":"test"}`,
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "invalid nonce",
			body:         `{"id":"test","nonce":"not-base64!!!","sender_pub_key":"AAAA","ciphertext":"AAAA"}`,
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/forward", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()

			server.mux.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("Expected status %d, got %d", tt.expectedCode, w.Code)
			}
		})
	}
}

func TestServerForwardWithValidMessage(t *testing.T) {
	// Create relay with keys
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	time.Sleep(50 * time.Millisecond)

	// Get relay's public key
	pubKey, _ := r.GetPublicKey()

	// Create a valid onion message
	msg, err := WrapLayer("validator-1:9000", []byte("test payload"), &pubKey)
	if err != nil {
		t.Fatalf("WrapLayer failed: %v", err)
	}

	server := NewServer(r, ":7000")

	// Create forward request
	forwardReq := ForwardRequest{
		ID:           msg.ID,
		Nonce:        base64.StdEncoding.EncodeToString(msg.Nonce[:]),
		SenderPubKey: base64.StdEncoding.EncodeToString(msg.SenderPubKey[:]),
		Ciphertext:   base64.StdEncoding.EncodeToString(msg.Ciphertext),
	}

	body, _ := json.Marshal(forwardReq)
	req := httptest.NewRequest("POST", "/forward", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ForwardResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.Accepted {
		t.Errorf("Expected accepted=true, got error: %s", resp.Error)
	}
}

func TestContainsKeyMaterial(t *testing.T) {
	key := []byte{0x01, 0x02, 0x03, 0x04, 0x05}

	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "contains key",
			data:     []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
			expected: true,
		},
		{
			name:     "does not contain key",
			data:     []byte{0x00, 0x01, 0x02, 0x03, 0x06, 0x07, 0x08},
			expected: false,
		},
		{
			name:     "empty data",
			data:     []byte{},
			expected: false,
		},
		{
			name:     "key at start",
			data:     []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsKeyMaterial(tt.data, key)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRelayProcessMessageUnlinkability(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 100 * time.Millisecond,
	})
	r.Start()
	defer r.Stop()

	time.Sleep(50 * time.Millisecond)

	pubKey, _ := r.GetPublicKey()

	// Create multiple messages and verify IDs change
	for i := 0; i < 5; i++ {
		msg, err := WrapLayer("validator-1:9000", []byte("test"), &pubKey)
		if err != nil {
			t.Fatalf("WrapLayer failed: %v", err)
		}

		originalID := msg.ID

		err = r.ProcessMessage(msg)
		if err != nil {
			t.Fatalf("ProcessMessage failed: %v", err)
		}

		// The message was processed; verify inbound ID was recorded
		r.inboundLogMu.RLock()
		_, found := r.inboundLog[originalID]
		r.inboundLogMu.RUnlock()

		if !found {
			t.Errorf("Inbound ID %s should be recorded for unlinkability", originalID)
		}
	}
}

func TestRelayKeyRotation(t *testing.T) {
	r, _ := NewRelay(RelayConfig{
		ID:            "1",
		EpochDuration: 50 * time.Millisecond, // Fast epochs for testing
	})
	r.Start()
	defer r.Stop()

	// Get initial key
	time.Sleep(30 * time.Millisecond)
	initialKey, initialEpoch := r.GetPublicKey()

	// Wait for key rotation
	time.Sleep(100 * time.Millisecond)

	newKey, newEpoch := r.GetPublicKey()

	// Key should have changed
	if newKey == initialKey {
		t.Error("Key should have rotated")
	}

	// Epoch should have advanced
	if newEpoch <= initialEpoch {
		t.Errorf("Epoch should have advanced: %d -> %d", initialEpoch, newEpoch)
	}
}

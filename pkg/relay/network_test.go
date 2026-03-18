package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewNetwork(t *testing.T) {
	peers := []string{"relay-1:7000", "relay-2:7000"}

	net := NewNetwork(NetworkConfig{
		SelfID:             "relay-3",
		PeerAddresses:      peers,
		AnonymityThreshold: 3,
	})

	if net == nil {
		t.Fatal("NewNetwork returned nil")
	}

	if net.selfID != "relay-3" {
		t.Errorf("Expected selfID relay-3, got %s", net.selfID)
	}

	if len(net.peers) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(net.peers))
	}

	if net.anonymityThreshold != 3 {
		t.Errorf("Expected threshold 3, got %d", net.anonymityThreshold)
	}
}

func TestNetworkStartStop(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID: "relay-1",
	})

	if net.IsRunning() {
		t.Error("Network should not be running initially")
	}

	net.Start()

	if !net.IsRunning() {
		t.Error("Network should be running after Start")
	}

	// Starting again should be no-op
	net.Start()

	if !net.IsRunning() {
		t.Error("Network should still be running")
	}

	net.Stop()

	if net.IsRunning() {
		t.Error("Network should not be running after Stop")
	}
}

func TestNetworkAddRemovePeer(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID: "relay-1",
	})

	// Initially no peers
	if len(net.GetPeers()) != 0 {
		t.Error("Expected no peers initially")
	}

	// Add a peer
	var pubKey [KeySize]byte
	pubKey[0] = 1
	net.AddPeer("relay-2:7000", pubKey)

	peers := net.GetPeers()
	if len(peers) != 1 {
		t.Errorf("Expected 1 peer, got %d", len(peers))
	}

	if peers[0].Address != "relay-2:7000" {
		t.Errorf("Expected address relay-2:7000, got %s", peers[0].Address)
	}

	if peers[0].PublicKey != pubKey {
		t.Error("Public key mismatch")
	}

	// Remove the peer
	net.RemovePeer("relay-2:7000")

	if len(net.GetPeers()) != 0 {
		t.Error("Expected no peers after removal")
	}
}

func TestNetworkGetPeer(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{"relay-2:7000"},
	})

	// Get existing peer
	peer, ok := net.GetPeer("relay-2:7000")
	if !ok {
		t.Error("Expected to find peer")
	}
	if peer.Address != "relay-2:7000" {
		t.Errorf("Expected address relay-2:7000, got %s", peer.Address)
	}

	// Get non-existing peer
	_, ok = net.GetPeer("relay-99:7000")
	if ok {
		t.Error("Should not find non-existing peer")
	}
}

func TestNetworkActiveRelayCount(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:             "relay-1",
		PeerAddresses:      []string{"relay-2:7000", "relay-3:7000"},
		AnonymityThreshold: 3,
	})

	// Initially only self is counted (peers are unknown)
	count := net.ActiveRelayCount()
	if count != 1 {
		t.Errorf("Expected active count 1 (self only), got %d", count)
	}

	// Mark a peer as healthy
	net.mu.Lock()
	if peer, ok := net.peers["relay-2:7000"]; ok {
		peer.Status = PeerHealthy
	}
	net.mu.Unlock()

	count = net.ActiveRelayCount()
	if count != 2 {
		t.Errorf("Expected active count 2, got %d", count)
	}
}

func TestNetworkBelowThreshold(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:             "relay-1",
		PeerAddresses:      []string{"relay-2:7000", "relay-3:7000"},
		AnonymityThreshold: 3,
	})

	// Initially below threshold (only self is active)
	if !net.IsBelowAnonymityThreshold() {
		t.Error("Should be below threshold with only 1 active relay")
	}

	// Mark all peers as healthy
	net.mu.Lock()
	for _, peer := range net.peers {
		peer.Status = PeerHealthy
	}
	net.mu.Unlock()

	// Now should have 3 active relays (self + 2 healthy peers)
	if net.IsBelowAnonymityThreshold() {
		t.Error("Should NOT be below threshold with 3 active relays")
	}
}

func TestNetworkHealthCheck(t *testing.T) {
	// Create a test server that returns healthy
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"healthy"}`))
		}
	}))
	defer server.Close()

	// Extract host:port from server URL
	addr := strings.TrimPrefix(server.URL, "http://")

	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{addr},
	})

	// Manually check the peer
	net.checkPeer(addr)

	peer, ok := net.GetPeer(addr)
	if !ok {
		t.Fatal("Expected to find peer")
	}

	if peer.Status != PeerHealthy {
		t.Errorf("Expected peer status healthy, got %v", peer.Status)
	}

	if peer.FailedChecks != 0 {
		t.Errorf("Expected 0 failed checks, got %d", peer.FailedChecks)
	}
}

func TestNetworkUnhealthyPeer(t *testing.T) {
	// Create a test server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")

	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{addr},
	})

	// Check multiple times to exceed threshold
	for i := 0; i < UnhealthyThreshold; i++ {
		net.checkPeer(addr)
	}

	peer, _ := net.GetPeer(addr)
	if peer.Status != PeerUnhealthy {
		t.Errorf("Expected peer status unhealthy, got %v", peer.Status)
	}
}

func TestNetworkStatus(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:             "relay-1",
		PeerAddresses:      []string{"relay-2:7000", "relay-3:7000"},
		AnonymityThreshold: 3,
	})

	// Set one peer healthy, one unhealthy
	net.mu.Lock()
	net.peers["relay-2:7000"].Status = PeerHealthy
	net.peers["relay-3:7000"].Status = PeerUnhealthy
	net.mu.Unlock()

	status := net.Status()

	if status.SelfID != "relay-1" {
		t.Errorf("Expected self ID relay-1, got %s", status.SelfID)
	}

	if status.TotalPeers != 2 {
		t.Errorf("Expected 2 total peers, got %d", status.TotalPeers)
	}

	if status.HealthyPeers != 1 {
		t.Errorf("Expected 1 healthy peer, got %d", status.HealthyPeers)
	}

	if status.UnhealthyPeers != 1 {
		t.Errorf("Expected 1 unhealthy peer, got %d", status.UnhealthyPeers)
	}

	if status.ActiveRelayCount != 2 { // self + 1 healthy peer
		t.Errorf("Expected 2 active relays, got %d", status.ActiveRelayCount)
	}

	if status.AnonymityThreshold != 3 {
		t.Errorf("Expected threshold 3, got %d", status.AnonymityThreshold)
	}

	if !status.BelowThreshold {
		t.Error("Should be below threshold with only 2 active relays")
	}
}

func TestNetworkAnonymityChangeCallback(t *testing.T) {
	callbackCalled := false
	var lastActiveCount int
	var lastBelowThreshold bool

	net := NewNetwork(NetworkConfig{
		SelfID:             "relay-1",
		PeerAddresses:      []string{"relay-2:7000", "relay-3:7000"},
		AnonymityThreshold: 3,
		OnAnonymityChange: func(activeCount int, belowThreshold bool) {
			callbackCalled = true
			lastActiveCount = activeCount
			lastBelowThreshold = belowThreshold
		},
	})

	// Trigger a change by modifying peer status and checking threshold
	net.mu.Lock()
	net.peers["relay-2:7000"].Status = PeerHealthy
	net.peers["relay-3:7000"].Status = PeerHealthy
	net.mu.Unlock()

	net.checkAnonymityThreshold()

	if !callbackCalled {
		t.Error("Anonymity change callback should have been called")
	}

	if lastActiveCount != 3 {
		t.Errorf("Expected active count 3, got %d", lastActiveCount)
	}

	if lastBelowThreshold {
		t.Error("Should not be below threshold with 3 active relays")
	}
}

func TestNetworkSetGetPeerPublicKey(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{"relay-2:7000"},
	})

	// Set public key
	var pubKey [KeySize]byte
	pubKey[0] = 0xAB
	pubKey[31] = 0xCD

	net.SetPeerPublicKey("relay-2:7000", pubKey)

	// Get public key
	retrieved, ok := net.GetPeerPublicKey("relay-2:7000")
	if !ok {
		t.Error("Expected to find public key")
	}

	if retrieved != pubKey {
		t.Error("Public key mismatch")
	}

	// Get non-existing peer's key
	_, ok = net.GetPeerPublicKey("relay-99:7000")
	if ok {
		t.Error("Should not find non-existing peer's key")
	}
}

func TestNetworkGetHealthyPeers(t *testing.T) {
	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{"relay-2:7000", "relay-3:7000", "relay-4:7000"},
	})

	// Set different statuses
	net.mu.Lock()
	net.peers["relay-2:7000"].Status = PeerHealthy
	net.peers["relay-3:7000"].Status = PeerUnhealthy
	net.peers["relay-4:7000"].Status = PeerHealthy
	net.mu.Unlock()

	healthy := net.GetHealthyPeers()

	if len(healthy) != 2 {
		t.Errorf("Expected 2 healthy peers, got %d", len(healthy))
	}

	// Verify both healthy peers are returned
	addresses := make(map[string]bool)
	for _, p := range healthy {
		addresses[p.Address] = true
	}

	if !addresses["relay-2:7000"] {
		t.Error("relay-2:7000 should be in healthy list")
	}
	if !addresses["relay-4:7000"] {
		t.Error("relay-4:7000 should be in healthy list")
	}
	if addresses["relay-3:7000"] {
		t.Error("relay-3:7000 should NOT be in healthy list")
	}
}

func TestPeerStatusString(t *testing.T) {
	tests := []struct {
		status   PeerStatus
		expected string
	}{
		{PeerUnknown, "unknown"},
		{PeerHealthy, "healthy"},
		{PeerUnhealthy, "unhealthy"},
		{PeerStatus(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.status.String()
		if result != tt.expected {
			t.Errorf("Status %d: expected %s, got %s", tt.status, tt.expected, result)
		}
	}
}

func TestNetworkPeerLastSeen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")

	net := NewNetwork(NetworkConfig{
		SelfID:        "relay-1",
		PeerAddresses: []string{addr},
	})

	before := time.Now()
	net.checkPeer(addr)
	after := time.Now()

	peer, _ := net.GetPeer(addr)

	if peer.LastSeen.Before(before) || peer.LastSeen.After(after) {
		t.Error("LastSeen should be within the check time range")
	}

	if peer.LastCheck.Before(before) || peer.LastCheck.After(after) {
		t.Error("LastCheck should be within the check time range")
	}
}

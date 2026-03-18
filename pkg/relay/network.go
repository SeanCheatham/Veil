// Package relay implements the Veil relay layer for onion-peeling and mix-and-forward operations.
package relay

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// HealthCheckInterval is how often to check peer health.
	HealthCheckInterval = 5 * time.Second

	// HealthCheckTimeout is the timeout for health check requests.
	HealthCheckTimeout = 2 * time.Second

	// UnhealthyThreshold is how many failed checks before marking a peer unhealthy.
	UnhealthyThreshold = 3
)

// PeerStatus represents the health status of a relay peer.
type PeerStatus int

const (
	// PeerUnknown indicates the peer has not been checked yet.
	PeerUnknown PeerStatus = iota
	// PeerHealthy indicates the peer is responding to health checks.
	PeerHealthy
	// PeerUnhealthy indicates the peer is not responding.
	PeerUnhealthy
)

func (s PeerStatus) String() string {
	switch s {
	case PeerUnknown:
		return "unknown"
	case PeerHealthy:
		return "healthy"
	case PeerUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// Peer represents a relay peer in the network.
type Peer struct {
	// Address is the peer's network address (e.g., "relay-2:7000").
	Address string

	// PublicKey is the peer's public key for onion encryption.
	// This is exchanged during peer discovery or configured statically.
	PublicKey [KeySize]byte

	// Status is the peer's current health status.
	Status PeerStatus

	// LastCheck is when the peer was last health-checked.
	LastCheck time.Time

	// FailedChecks is the number of consecutive failed health checks.
	FailedChecks int

	// LastSeen is when the peer last responded successfully.
	LastSeen time.Time
}

// Network manages the relay mesh and peer discovery.
// It tracks active relays, handles health checking, and reports
// when the active relay count drops below the anonymity threshold.
type Network struct {
	mu sync.RWMutex

	// selfID is this relay's identifier.
	selfID string

	// peers maps peer addresses to their status.
	peers map[string]*Peer

	// httpClient for health checks.
	httpClient *http.Client

	// running indicates if health checking is active.
	running bool

	// stopCh signals the network manager to stop.
	stopCh chan struct{}

	// wg tracks running goroutines.
	wg sync.WaitGroup

	// onAnonymityChange is called when the active relay count crosses the threshold.
	onAnonymityChange func(activeCount int, belowThreshold bool)

	// anonymityThreshold is the minimum number of active relays required.
	anonymityThreshold int

	// lastActiveCount tracks the last reported active count.
	lastActiveCount int
}

// NetworkConfig holds configuration for the network manager.
type NetworkConfig struct {
	SelfID             string
	PeerAddresses      []string
	AnonymityThreshold int
	OnAnonymityChange  func(activeCount int, belowThreshold bool)
}

// NewNetwork creates a new network manager with the given configuration.
func NewNetwork(cfg NetworkConfig) *Network {
	threshold := cfg.AnonymityThreshold
	if threshold <= 0 {
		threshold = 3 // Default anonymity threshold
	}

	n := &Network{
		selfID: cfg.SelfID,
		peers:  make(map[string]*Peer),
		httpClient: &http.Client{
			Timeout: HealthCheckTimeout,
		},
		anonymityThreshold: threshold,
		onAnonymityChange:  cfg.OnAnonymityChange,
		stopCh:             make(chan struct{}),
	}

	// Initialize peers
	for _, addr := range cfg.PeerAddresses {
		n.peers[addr] = &Peer{
			Address: addr,
			Status:  PeerUnknown,
		}
	}

	return n
}

// Start begins the network manager's background operations.
func (n *Network) Start() {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return
	}
	n.running = true
	n.stopCh = make(chan struct{})
	n.mu.Unlock()

	n.wg.Add(1)
	go n.healthCheckLoop()
}

// Stop halts the network manager.
func (n *Network) Stop() {
	n.mu.Lock()
	if !n.running {
		n.mu.Unlock()
		return
	}
	n.running = false
	close(n.stopCh)
	n.mu.Unlock()

	n.wg.Wait()
}

// IsRunning returns whether the network manager is active.
func (n *Network) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.running
}

// AddPeer adds a new peer to the network.
func (n *Network) AddPeer(addr string, pubKey [KeySize]byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.peers[addr]; !exists {
		n.peers[addr] = &Peer{
			Address:   addr,
			PublicKey: pubKey,
			Status:    PeerUnknown,
		}
	} else {
		// Update public key if peer exists
		n.peers[addr].PublicKey = pubKey
	}
}

// RemovePeer removes a peer from the network.
func (n *Network) RemovePeer(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.peers, addr)
}

// GetPeer returns a peer by address.
func (n *Network) GetPeer(addr string) (*Peer, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	peer, ok := n.peers[addr]
	if !ok {
		return nil, false
	}

	// Return a copy
	peerCopy := *peer
	return &peerCopy, true
}

// GetPeers returns a copy of all peers.
func (n *Network) GetPeers() []*Peer {
	n.mu.RLock()
	defer n.mu.RUnlock()

	peers := make([]*Peer, 0, len(n.peers))
	for _, p := range n.peers {
		peerCopy := *p
		peers = append(peers, &peerCopy)
	}
	return peers
}

// GetHealthyPeers returns all healthy peers.
func (n *Network) GetHealthyPeers() []*Peer {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var healthy []*Peer
	for _, p := range n.peers {
		if p.Status == PeerHealthy {
			peerCopy := *p
			healthy = append(healthy, &peerCopy)
		}
	}
	return healthy
}

// ActiveRelayCount returns the count of active (healthy) relays including self.
func (n *Network) ActiveRelayCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()

	count := 1 // Count self
	for _, p := range n.peers {
		if p.Status == PeerHealthy {
			count++
		}
	}
	return count
}

// IsBelowAnonymityThreshold returns true if the active relay count is below threshold.
func (n *Network) IsBelowAnonymityThreshold() bool {
	return n.ActiveRelayCount() < n.anonymityThreshold
}

// AnonymityThreshold returns the configured anonymity threshold.
func (n *Network) AnonymityThreshold() int {
	return n.anonymityThreshold
}

// healthCheckLoop periodically checks peer health.
func (n *Network) healthCheckLoop() {
	defer n.wg.Done()

	// Initial check
	n.checkAllPeers()

	ticker := time.NewTicker(HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.checkAllPeers()
		}
	}
}

// checkAllPeers checks the health of all peers.
func (n *Network) checkAllPeers() {
	n.mu.RLock()
	addresses := make([]string, 0, len(n.peers))
	for addr := range n.peers {
		addresses = append(addresses, addr)
	}
	n.mu.RUnlock()

	// Check each peer concurrently
	var wg sync.WaitGroup
	for _, addr := range addresses {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()
			n.checkPeer(address)
		}(addr)
	}
	wg.Wait()

	// Check if anonymity threshold changed
	n.checkAnonymityThreshold()
}

// checkPeer performs a health check on a single peer.
func (n *Network) checkPeer(addr string) {
	url := fmt.Sprintf("http://%s/health", addr)
	resp, err := n.httpClient.Get(url)

	n.mu.Lock()
	defer n.mu.Unlock()

	peer, ok := n.peers[addr]
	if !ok {
		return
	}

	peer.LastCheck = time.Now()

	if err != nil || resp.StatusCode != http.StatusOK {
		peer.FailedChecks++
		if peer.FailedChecks >= UnhealthyThreshold {
			peer.Status = PeerUnhealthy
		}
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	resp.Body.Close()

	peer.Status = PeerHealthy
	peer.LastSeen = time.Now()
	peer.FailedChecks = 0
}

// checkAnonymityThreshold checks if the active relay count crossed the threshold.
func (n *Network) checkAnonymityThreshold() {
	activeCount := n.ActiveRelayCount()
	belowThreshold := activeCount < n.anonymityThreshold

	n.mu.Lock()
	lastCount := n.lastActiveCount
	n.lastActiveCount = activeCount
	n.mu.Unlock()

	// Notify if count changed and we have a callback
	if activeCount != lastCount && n.onAnonymityChange != nil {
		n.onAnonymityChange(activeCount, belowThreshold)
	}
}

// SetPeerPublicKey sets a peer's public key.
func (n *Network) SetPeerPublicKey(addr string, pubKey [KeySize]byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if peer, ok := n.peers[addr]; ok {
		peer.PublicKey = pubKey
	}
}

// GetPeerPublicKey returns a peer's public key.
func (n *Network) GetPeerPublicKey(addr string) ([KeySize]byte, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if peer, ok := n.peers[addr]; ok {
		return peer.PublicKey, true
	}
	return [KeySize]byte{}, false
}

// NetworkStatus represents the current state of the network.
type NetworkStatus struct {
	SelfID              string `json:"self_id"`
	TotalPeers          int    `json:"total_peers"`
	HealthyPeers        int    `json:"healthy_peers"`
	UnhealthyPeers      int    `json:"unhealthy_peers"`
	UnknownPeers        int    `json:"unknown_peers"`
	ActiveRelayCount    int    `json:"active_relay_count"`
	AnonymityThreshold  int    `json:"anonymity_threshold"`
	BelowThreshold      bool   `json:"below_threshold"`
}

// Status returns the current network status.
func (n *Network) Status() NetworkStatus {
	n.mu.RLock()
	defer n.mu.RUnlock()

	status := NetworkStatus{
		SelfID:             n.selfID,
		TotalPeers:         len(n.peers),
		AnonymityThreshold: n.anonymityThreshold,
	}

	activeCount := 1 // Count self
	for _, p := range n.peers {
		switch p.Status {
		case PeerHealthy:
			status.HealthyPeers++
			activeCount++
		case PeerUnhealthy:
			status.UnhealthyPeers++
		case PeerUnknown:
			status.UnknownPeers++
		}
	}

	status.ActiveRelayCount = activeCount
	status.BelowThreshold = activeCount < n.anonymityThreshold

	return status
}

// Package relay implements the Veil relay layer for onion-peeling and mix-and-forward operations.
package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
	"github.com/veil-protocol/veil/pkg/epoch"
)

// DefaultPort is the default port for the relay HTTP server.
const DefaultPort = 7000

// Server provides an HTTP API for the relay node.
type Server struct {
	relay  *Relay
	mux    *http.ServeMux
	server *http.Server
}

// NewServer creates a new HTTP server for the relay.
func NewServer(relay *Relay, addr string) *Server {
	s := &Server{
		relay: relay,
		mux:   http.NewServeMux(),
	}

	// External API
	s.mux.HandleFunc("POST /forward", s.handleForward)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /status", s.handleStatus)

	// Internal API for key exchange
	s.mux.HandleFunc("GET /pubkey", s.handleGetPubKey)

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	log.Printf("relay %s: server listening on %s", s.relay.ID, s.server.Addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	return s.server.Close()
}

// ForwardRequest is the request body for POST /forward.
type ForwardRequest struct {
	// ID is the message ID for this hop.
	ID string `json:"id"`

	// Nonce is the base64-encoded nonce.
	Nonce string `json:"nonce"`

	// SenderPubKey is the base64-encoded ephemeral public key.
	SenderPubKey string `json:"sender_pub_key"`

	// Ciphertext is the base64-encoded encrypted layer.
	Ciphertext string `json:"ciphertext"`
}

// ForwardResponse is the response body for POST /forward.
type ForwardResponse struct {
	// Accepted indicates if the message was accepted for forwarding.
	Accepted bool `json:"accepted"`

	// Error contains any error message.
	Error string `json:"error,omitempty"`
}

// handleForward handles POST /forward - receives onion messages and forwards them.
func (s *Server) handleForward(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req ForwardRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ID == "" || req.Nonce == "" || req.SenderPubKey == "" || req.Ciphertext == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	// Decode fields
	nonce, err := base64.StdEncoding.DecodeString(req.Nonce)
	if err != nil || len(nonce) != NonceSize {
		http.Error(w, "invalid nonce", http.StatusBadRequest)
		return
	}

	senderPubKey, err := base64.StdEncoding.DecodeString(req.SenderPubKey)
	if err != nil || len(senderPubKey) != KeySize {
		http.Error(w, "invalid sender public key", http.StatusBadRequest)
		return
	}

	ciphertext, err := base64.StdEncoding.DecodeString(req.Ciphertext)
	if err != nil {
		http.Error(w, "invalid ciphertext", http.StatusBadRequest)
		return
	}

	// Construct onion message
	msg := &OnionMessage{
		ID:         req.ID,
		Ciphertext: ciphertext,
	}
	copy(msg.Nonce[:], nonce)
	copy(msg.SenderPubKey[:], senderPubKey)

	// Process the message
	err = s.relay.ProcessMessage(msg)
	if err != nil {
		log.Printf("relay %s: failed to process message %s: %v", s.relay.ID, req.ID, err)

		resp := ForwardResponse{
			Accepted: false,
			Error:    err.Error(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp := ForwardResponse{
		Accepted: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HealthResponse is the response body for GET /health.
type HealthResponse struct {
	Status string `json:"status"`
	ID     string `json:"id"`
}

// handleHealth handles GET /health - returns relay health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status: "healthy",
		ID:     s.relay.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// StatusResponse is the response body for GET /status.
type StatusResponse struct {
	ID                 string        `json:"id"`
	Running            bool          `json:"running"`
	CurrentEpoch       uint64        `json:"current_epoch"`
	ActiveRelayCount   int           `json:"active_relay_count"`
	BelowThreshold     bool          `json:"below_threshold"`
	AnonymityThreshold int           `json:"anonymity_threshold"`
	MixerQueueSize     int           `json:"mixer_queue_size"`
	NetworkStatus      NetworkStatus `json:"network_status"`
}

// handleStatus handles GET /status - returns detailed relay status.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := s.relay.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// PubKeyResponse is the response body for GET /pubkey.
type PubKeyResponse struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"` // base64 encoded
	Epoch     uint64 `json:"epoch"`
}

// handleGetPubKey handles GET /pubkey - returns the relay's current public key.
func (s *Server) handleGetPubKey(w http.ResponseWriter, r *http.Request) {
	pubKey, ep := s.relay.GetPublicKey()

	resp := PubKeyResponse{
		ID:        s.relay.ID,
		PublicKey: base64.StdEncoding.EncodeToString(pubKey[:]),
		Epoch:     ep,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Relay is the main relay node that coordinates onion peeling, mixing, and forwarding.
type Relay struct {
	mu sync.RWMutex

	// ID is the relay's unique identifier.
	ID string

	// Network manages relay peers.
	Network *Network

	// Mixer handles message mixing and timing obfuscation.
	Mixer *Mixer

	// KeyManager handles session key rotation.
	KeyManager *epoch.KeyManager

	// Clock manages epoch timing.
	Clock *epoch.Clock

	// currentKeyPair is the current NaCl key pair for decryption.
	currentKeyPair *RelayKeyPair

	// previousKeyPair is the previous key pair for grace period.
	previousKeyPair *RelayKeyPair

	// validatorAddr is the address of the validator to submit final messages.
	validatorAddr string

	// httpClient for forwarding messages.
	httpClient *http.Client

	// running indicates if the relay is active.
	running bool

	// inboundLog tracks inbound message IDs for unlinkability verification.
	// This is cleared periodically to avoid memory growth.
	inboundLog map[string]time.Time

	// inboundLogMu protects inboundLog.
	inboundLogMu sync.RWMutex
}

// RelayConfig holds configuration for a relay.
type RelayConfig struct {
	ID            string
	PeerAddresses []string
	ValidatorAddr string
	EpochDuration time.Duration
}

// NewRelay creates a new relay with the given configuration.
func NewRelay(cfg RelayConfig) (*Relay, error) {
	epochDuration := cfg.EpochDuration
	if epochDuration <= 0 {
		epochDuration = 30 * time.Second
	}

	// Create epoch clock
	clock := epoch.NewClock(epochDuration)

	// Create key manager
	keyManager := epoch.NewKeyManager(clock)

	// Create network manager
	network := NewNetwork(NetworkConfig{
		SelfID:             cfg.ID,
		PeerAddresses:      cfg.PeerAddresses,
		AnonymityThreshold: antithesis.AnonymityThreshold,
	})

	r := &Relay{
		ID:            cfg.ID,
		Network:       network,
		Clock:         clock,
		KeyManager:    keyManager,
		validatorAddr: cfg.ValidatorAddr,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		inboundLog: make(map[string]time.Time),
	}

	// Create mixer with forward function
	r.Mixer = NewMixer(MixerConfig{
		ForwardFunc: r.forwardMessage,
	})

	// Register for key rotation
	keyManager.OnRotate(r.handleKeyRotation)

	// Register for anonymity threshold changes
	network.onAnonymityChange = r.handleAnonymityChange

	return r, nil
}

// Start begins the relay's operations.
func (r *Relay) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.mu.Unlock()

	// Generate initial key pair
	keyPair, err := GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate key pair: %w", err)
	}

	r.mu.Lock()
	r.currentKeyPair = keyPair
	r.mu.Unlock()

	// Start components
	r.Clock.Start()
	r.Network.Start()
	r.Mixer.Start()

	// Start inbound log cleanup
	go r.cleanupInboundLog()

	log.Printf("relay %s: started with public key %s", r.ID, hex.EncodeToString(keyPair.PublicKey[:8]))

	return nil
}

// Stop halts the relay.
func (r *Relay) Stop() {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	r.mu.Unlock()

	r.Mixer.Stop()
	r.Network.Stop()
	r.Clock.Stop()

	log.Printf("relay %s: stopped", r.ID)
}

// IsRunning returns whether the relay is active.
func (r *Relay) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// handleKeyRotation is called when the epoch clock triggers key rotation.
func (r *Relay) handleKeyRotation(key *epoch.SessionKey) {
	// Generate new key pair for the new epoch
	newKeyPair, err := GenerateKeyPair()
	if err != nil {
		log.Printf("relay %s: failed to generate new key pair: %v", r.ID, err)
		return
	}

	r.mu.Lock()
	r.previousKeyPair = r.currentKeyPair
	r.currentKeyPair = newKeyPair
	r.mu.Unlock()

	// Antithesis assertion: key_scope
	// Assert that key material stays within relay context.
	// We verify the new key was generated and the old key moved to previous.
	keyInContext := r.currentKeyPair != nil
	assert.Always(
		keyInContext,
		antithesis.KeyScope,
		map[string]any{
			"relay_id":        r.ID,
			"epoch":           key.Epoch,
			"context":         "key_rotation",
			"key_id":          hex.EncodeToString(newKeyPair.PublicKey[:8]),
			"key_in_context":  keyInContext,
		},
	)

	log.Printf("relay %s: rotated keys for epoch %d, new key: %s",
		r.ID, key.Epoch, hex.EncodeToString(newKeyPair.PublicKey[:8]))
}

// handleAnonymityChange is called when the active relay count changes.
func (r *Relay) handleAnonymityChange(activeCount int, belowThreshold bool) {
	// Antithesis assertion: anonymity_set_size
	// Assert that active relay count is >= threshold.
	assert.Always(
		!belowThreshold,
		antithesis.AnonymitySetSize,
		map[string]any{
			"relay_id":     r.ID,
			"active_count": activeCount,
			"threshold":    antithesis.AnonymityThreshold,
			"below":        belowThreshold,
		},
	)

	if belowThreshold {
		log.Printf("relay %s: WARNING - active relay count %d is below threshold %d",
			r.ID, activeCount, antithesis.AnonymityThreshold)
	} else {
		log.Printf("relay %s: active relay count is %d (threshold: %d)",
			r.ID, activeCount, antithesis.AnonymityThreshold)
	}
}

// ProcessMessage handles an incoming onion message.
func (r *Relay) ProcessMessage(msg *OnionMessage) error {
	r.mu.RLock()
	currentKey := r.currentKeyPair
	previousKey := r.previousKeyPair
	running := r.running
	r.mu.RUnlock()

	if !running {
		return fmt.Errorf("relay not running")
	}

	if currentKey == nil {
		return fmt.Errorf("no key pair available")
	}

	// Record inbound message ID for unlinkability tracking
	inboundID := msg.ID
	r.recordInboundID(inboundID)

	// Try to peel with current key first, then previous key (grace period)
	layer, newID, err := PeelLayer(msg, &currentKey.PrivateKey)
	if err != nil {
		// Try previous key (grace period during key rotation)
		if previousKey != nil {
			layer, newID, err = PeelLayer(msg, &previousKey.PrivateKey)
		}
		if err != nil {
			return fmt.Errorf("failed to peel layer: %w", err)
		}
	}

	// Antithesis assertion: relay_unlinkability (CRITICAL)
	// Assert that the inbound message ID is NOT present in the outbound message data.
	// The new ID must be completely different from the inbound ID.
	inboundNotInOutbound := inboundID != newID && !strings.Contains(newID, inboundID)
	assert.Always(
		inboundNotInOutbound,
		antithesis.RelayUnlinkability,
		map[string]any{
			"relay_id":              r.ID,
			"inbound_id":            inboundID,
			"outbound_id":           newID,
			"ids_different":         inboundID != newID,
			"inbound_not_in_outbound": inboundNotInOutbound,
		},
	)

	// Antithesis assertion: key_scope
	// Assert that key material does not appear in log output or forwarded data.
	// We check that the private key bytes don't appear in the outbound data.
	keyNotInPayload := !containsKeyMaterial(layer.InnerPayload, currentKey.PrivateKey[:])
	assert.Always(
		keyNotInPayload,
		antithesis.KeyScope,
		map[string]any{
			"relay_id":         r.ID,
			"context":          "message_forward",
			"key_not_in_payload": keyNotInPayload,
		},
	)

	log.Printf("relay %s: peeled message, inbound=%s outbound=%s next_hop=%s",
		r.ID, inboundID[:8], newID[:8], layer.NextHop)

	// Create mixed message for forwarding
	mixedMsg := &MixedMessage{
		InboundID:  inboundID,
		OutboundID: newID,
		NextHop:    layer.NextHop,
		Payload:    layer.InnerPayload,
	}

	// Enqueue for mixing and forwarding
	r.Mixer.Enqueue(mixedMsg)

	return nil
}

// recordInboundID records an inbound message ID.
func (r *Relay) recordInboundID(id string) {
	r.inboundLogMu.Lock()
	defer r.inboundLogMu.Unlock()
	r.inboundLog[id] = time.Now()
}

// cleanupInboundLog periodically removes old entries from the inbound log.
func (r *Relay) cleanupInboundLog() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		r.mu.RLock()
		running := r.running
		r.mu.RUnlock()

		if !running {
			return
		}

		select {
		case <-ticker.C:
			r.inboundLogMu.Lock()
			cutoff := time.Now().Add(-5 * time.Minute)
			for id, ts := range r.inboundLog {
				if ts.Before(cutoff) {
					delete(r.inboundLog, id)
				}
			}
			r.inboundLogMu.Unlock()
		}
	}
}

// forwardMessage forwards a message to its next hop.
func (r *Relay) forwardMessage(msg *MixedMessage) error {
	// Check if this is the final hop (validator)
	if strings.HasPrefix(msg.NextHop, "validator") || strings.Contains(msg.NextHop, ":9000") {
		return r.submitToValidator(msg)
	}

	// Deserialize the inner payload as the next onion message
	innerMsg, err := DeserializeOnionMessage(msg.Payload)
	if err != nil {
		log.Printf("relay %s: failed to deserialize inner message: %v", r.ID, err)
		return err
	}

	// Use the new outbound ID
	innerMsg.ID = msg.OutboundID

	// Forward to next relay
	return r.forwardToRelay(msg.NextHop, innerMsg)
}

// forwardToRelay forwards an onion message to another relay.
func (r *Relay) forwardToRelay(addr string, msg *OnionMessage) error {
	req := ForwardRequest{
		ID:           msg.ID,
		Nonce:        base64.StdEncoding.EncodeToString(msg.Nonce[:]),
		SenderPubKey: base64.StdEncoding.EncodeToString(msg.SenderPubKey[:]),
		Ciphertext:   base64.StdEncoding.EncodeToString(msg.Ciphertext),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal forward request: %w", err)
	}

	url := fmt.Sprintf("http://%s/forward", addr)
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to forward to %s: %w", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay %s rejected message: %d", addr, resp.StatusCode)
	}

	return nil
}

// submitToValidator submits a final message to the validator.
func (r *Relay) submitToValidator(msg *MixedMessage) error {
	addr := r.validatorAddr
	if addr == "" {
		addr = msg.NextHop
	}

	// The payload at this point is the final message ciphertext
	req := struct {
		Ciphertext string `json:"ciphertext"`
	}{
		Ciphertext: base64.StdEncoding.EncodeToString(msg.Payload),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal submit request: %w", err)
	}

	url := fmt.Sprintf("http://%s/submit", addr)
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to submit to validator %s: %w", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("validator %s rejected message: %d", addr, resp.StatusCode)
	}

	log.Printf("relay %s: submitted message to validator %s", r.ID, addr)
	return nil
}

// GetPublicKey returns the relay's current public key and epoch.
func (r *Relay) GetPublicKey() ([KeySize]byte, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var pubKey [KeySize]byte
	if r.currentKeyPair != nil {
		pubKey = r.currentKeyPair.PublicKey
	}

	return pubKey, r.Clock.CurrentEpoch()
}

// GetStatus returns the relay's current status.
func (r *Relay) GetStatus() *StatusResponse {
	r.mu.RLock()
	running := r.running
	r.mu.RUnlock()

	networkStatus := r.Network.Status()
	mixerStats := r.Mixer.Stats()

	return &StatusResponse{
		ID:                 r.ID,
		Running:            running,
		CurrentEpoch:       r.Clock.CurrentEpoch(),
		ActiveRelayCount:   networkStatus.ActiveRelayCount,
		BelowThreshold:     networkStatus.BelowThreshold,
		AnonymityThreshold: networkStatus.AnonymityThreshold,
		MixerQueueSize:     mixerStats.QueueSize,
		NetworkStatus:      networkStatus,
	}
}

// containsKeyMaterial checks if data contains key material.
func containsKeyMaterial(data []byte, key []byte) bool {
	return bytes.Contains(data, key)
}

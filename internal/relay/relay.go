// Package relay implements the onion layer peeling and mix-and-forward logic.
package relay

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/veil-protocol/veil/internal/crypto"
	"github.com/veil-protocol/veil/internal/properties"
)

// Relay handles onion layer peeling and message forwarding.
// Critical design: inboundLog and outboundLog are NEVER cross-referenced
// to maintain relay unlinkability.
type Relay struct {
	relayID    string
	keyManager *crypto.SessionKeyManager
	client     *http.Client

	// CRITICAL: These must be completely separate - never cross-reference
	mu          sync.RWMutex
	inboundLog  []string // Only inbound IDs - append-only
	outboundLog []string // Only outbound IDs - append-only

	// Configuration
	validatorEndpoints []string
	relayPeers         []string
}

// MessageRequest is the JSON structure for incoming messages.
type MessageRequest struct {
	ID    string `json:"id"`    // Inbound message ID
	Blob  []byte `json:"blob"`  // Encrypted onion layer
	Epoch uint64 `json:"epoch"` // Epoch number for key selection
}

// MessageResponse is the JSON structure for message forwarding responses.
type MessageResponse struct {
	Status string `json:"status"`
	MsgID  string `json:"msg_id"`
}

// ProposalMessage is the format expected by validators at /propose.
// Must match validator.ProposalMessage.
type ProposalMessage struct {
	ID         string `json:"id"`
	Ciphertext []byte `json:"ciphertext"`
	Hash       string `json:"hash"` // SHA256 of ciphertext
}

// Status represents the current status of a relay.
type Status struct {
	NodeID         string   `json:"node_id"`
	InboundCount   int      `json:"inbound_count"`
	OutboundCount  int      `json:"outbound_count"`
	Peers          []string `json:"peers"`
	ValidatorCount int      `json:"validator_count"`
}

// NewRelay creates a new relay instance.
// relayID is the unique identifier for this relay (e.g., "relay-1").
// epochClockURL is the URL of the epoch-clock service for key rotation.
// relayPeers is a comma-separated list of peer relay URLs.
// validatorEndpoints is a comma-separated list of validator URLs.
func NewRelay(relayID, epochClockURL, relayPeers, validatorEndpoints string) *Relay {
	return &Relay{
		relayID:            relayID,
		keyManager:         crypto.NewSessionKeyManager(relayID, epochClockURL),
		client:             &http.Client{Timeout: 10 * time.Second},
		inboundLog:         make([]string, 0),
		outboundLog:        make([]string, 0),
		validatorEndpoints: parsePeers(validatorEndpoints),
		relayPeers:         parsePeers(relayPeers),
	}
}

// parsePeers parses a comma-separated list of peer URLs.
func parsePeers(peers string) []string {
	if peers == "" {
		return []string{}
	}
	parts := strings.Split(peers, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// Start initializes the relay by starting the session key manager.
func (r *Relay) Start(ctx context.Context) error {
	return r.keyManager.Start(ctx)
}

// Stop shuts down the relay gracefully.
func (r *Relay) Stop() {
	r.keyManager.Stop()
}

// OnMessage processes an incoming message by:
// 1. Logging the inbound ID (isolated)
// 2. Peeling one onion layer using the current epoch's private key
// 3. Generating a NEW outbound ID (never derived from inbound)
// 4. Logging the outbound ID (isolated)
// 5. Asserting relay unlinkability
// 6. Forwarding to next hop (relay or validator)
func (r *Relay) OnMessage(inboundID string, blob []byte, epoch uint64) error {
	// Step 1: Log inbound ID (isolated, append-only)
	r.logInbound(inboundID)

	// Step 2: Get the private key for this epoch
	keyPair := r.keyManager.GetKeyPair(epoch)
	if keyPair == nil {
		// Try previous epoch (for epoch boundary transitions)
		if epoch > 0 {
			keyPair = r.keyManager.GetKeyPair(epoch - 1)
		}
		if keyPair == nil {
			return fmt.Errorf("no key pair available for epoch %d", epoch)
		}
	}

	// Step 3: Peel one onion layer
	nextHop, innerPayload, err := crypto.UnwrapOnionLayer(blob, keyPair.PrivateKey)
	if err != nil {
		return fmt.Errorf("failed to unwrap onion layer: %w", err)
	}

	// Step 4: Generate NEW outbound ID - CRITICAL: must be completely independent
	// NEVER derive from inboundID
	outboundID := uuid.New().String()

	// Step 5: Log outbound ID (isolated, append-only)
	r.logOutbound(outboundID)

	// Step 6: Assert relay unlinkability - outboundID must NOT appear in inboundLog
	// This ensures we never accidentally link inbound and outbound IDs
	inboundContainsOutbound := r.inboundContains(outboundID)
	properties.AssertRelayUnlinkability(!inboundContainsOutbound, r.relayID, outboundID)

	// Step 7: Forward to next hop
	if nextHop == "validator" {
		return r.forwardToValidator(outboundID, innerPayload)
	}
	return r.forwardToRelay(nextHop, outboundID, innerPayload, epoch)
}

// logInbound appends an inbound ID to the inbound log.
// This is an isolated operation - never access outboundLog here.
func (r *Relay) logInbound(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inboundLog = append(r.inboundLog, id)
}

// logOutbound appends an outbound ID to the outbound log.
// This is an isolated operation - never access inboundLog here.
func (r *Relay) logOutbound(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outboundLog = append(r.outboundLog, id)
}

// inboundContains checks if the inbound log contains a specific ID.
// Used for unlinkability assertion.
func (r *Relay) inboundContains(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inID := range r.inboundLog {
		if inID == id {
			return true
		}
	}
	return false
}

// forwardToValidator sends the final message to a validator's /propose endpoint.
func (r *Relay) forwardToValidator(msgID string, ciphertext []byte) error {
	if len(r.validatorEndpoints) == 0 {
		return fmt.Errorf("no validator endpoints configured")
	}

	// Compute hash of ciphertext
	hash := sha256.Sum256(ciphertext)
	hashHex := hex.EncodeToString(hash[:])

	proposal := ProposalMessage{
		ID:         msgID,
		Ciphertext: ciphertext,
		Hash:       hashHex,
	}

	body, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("failed to marshal proposal: %w", err)
	}

	// Try each validator until one accepts
	var lastErr error
	for _, validatorURL := range r.validatorEndpoints {
		req, err := http.NewRequest(http.MethodPost, validatorURL+"/propose", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("[%s] Failed to contact validator %s: %v", r.relayID, validatorURL, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
			log.Printf("[%s] Message %s forwarded to validator %s", r.relayID, msgID, validatorURL)
			properties.ObserveMessageForwarding(true, msgID)
			return nil
		}
		lastErr = fmt.Errorf("validator %s returned status %d", validatorURL, resp.StatusCode)
	}

	return fmt.Errorf("failed to forward to any validator: %w", lastErr)
}

// forwardToRelay sends the message to the next relay's /message endpoint.
func (r *Relay) forwardToRelay(nextHop, msgID string, payload []byte, epoch uint64) error {
	msg := MessageRequest{
		ID:    msgID,
		Blob:  payload,
		Epoch: epoch,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// nextHop should be the full URL of the next relay
	url := nextHop
	if !strings.HasPrefix(url, "http") {
		// If nextHop is just a hostname, construct the URL
		url = "http://" + nextHop
	}
	if !strings.HasSuffix(url, "/message") {
		url = strings.TrimSuffix(url, "/") + "/message"
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact relay %s: %w", nextHop, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay %s returned status %d", nextHop, resp.StatusCode)
	}

	log.Printf("[%s] Message %s forwarded to relay %s", r.relayID, msgID, nextHop)
	return nil
}

// PublicKey returns the public key for the specified epoch.
// This is used by senders to encrypt messages for this relay.
func (r *Relay) PublicKey(epoch uint64) *ecdh.PublicKey {
	return r.keyManager.PublicKey(epoch)
}

// Status returns the current status of the relay.
func (r *Relay) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return Status{
		NodeID:         r.relayID,
		InboundCount:   len(r.inboundLog),
		OutboundCount:  len(r.outboundLog),
		Peers:          r.relayPeers,
		ValidatorCount: len(r.validatorEndpoints),
	}
}

// RelayID returns the relay's unique identifier.
func (r *Relay) RelayID() string {
	return r.relayID
}

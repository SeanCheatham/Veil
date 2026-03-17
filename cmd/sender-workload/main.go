// Package main implements the sender-workload test driver.
// This workload generates and sends messages through the relay network for testing.
package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/veil-protocol/veil/internal/crypto"
)

// MessageRequest is the JSON structure for sending messages to relays.
type MessageRequest struct {
	ID    string `json:"id"`    // Message ID
	Blob  []byte `json:"blob"`  // Encrypted onion
	Epoch uint64 `json:"epoch"` // Epoch number
}

// EpochResponse is the response from GET /epoch on the epoch-clock.
type EpochResponse struct {
	Epoch     uint64    `json:"epoch"`
	Timestamp time.Time `json:"timestamp"`
}

// PubKeyResponse is the response from GET /pubkey/:epoch on relays.
type PubKeyResponse struct {
	RelayID   string `json:"relay_id"`
	Epoch     uint64 `json:"epoch"`
	PublicKey string `json:"public_key"` // Base64-encoded X25519 public key
}

// SenderWorkload generates and sends messages through the relay network.
type SenderWorkload struct {
	relayEndpoints []string
	epochClockURL  string
	receiverPubKey *ecdh.PublicKey
	httpClient     *http.Client
}

// NewSenderWorkload creates a new sender workload from environment variables.
func NewSenderWorkload() (*SenderWorkload, error) {
	// Parse RELAY_ENDPOINTS
	relayEndpoints := os.Getenv("RELAY_ENDPOINTS")
	if relayEndpoints == "" {
		return nil, fmt.Errorf("RELAY_ENDPOINTS environment variable is required")
	}
	endpoints := parseEndpoints(relayEndpoints)
	if len(endpoints) < 3 {
		return nil, fmt.Errorf("need at least 3 relay endpoints, got %d", len(endpoints))
	}

	// Parse EPOCH_CLOCK_URL
	epochClockURL := os.Getenv("EPOCH_CLOCK_URL")
	if epochClockURL == "" {
		return nil, fmt.Errorf("EPOCH_CLOCK_URL environment variable is required")
	}

	// Parse RECEIVER_PUBKEY
	receiverPubKeyB64 := os.Getenv("RECEIVER_PUBKEY")
	if receiverPubKeyB64 == "" {
		return nil, fmt.Errorf("RECEIVER_PUBKEY environment variable is required")
	}

	receiverPubKeyBytes, err := base64.StdEncoding.DecodeString(receiverPubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode RECEIVER_PUBKEY: %w", err)
	}

	receiverPubKey, err := ecdh.X25519().NewPublicKey(receiverPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RECEIVER_PUBKEY as X25519 public key: %w", err)
	}

	return &SenderWorkload{
		relayEndpoints: endpoints,
		epochClockURL:  strings.TrimSuffix(epochClockURL, "/"),
		receiverPubKey: receiverPubKey,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// parseEndpoints parses a comma-separated list of endpoints.
func parseEndpoints(endpoints string) []string {
	parts := strings.Split(endpoints, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// getCurrentEpoch fetches the current epoch from the epoch-clock service.
func (s *SenderWorkload) getCurrentEpoch(ctx context.Context) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.epochClockURL+"/epoch", nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching current epoch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result EpochResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	return result.Epoch, nil
}

// getRelayPubKey fetches a relay's public key for a specific epoch.
func (s *SenderWorkload) getRelayPubKey(ctx context.Context, relayEndpoint string, epoch uint64) (*ecdh.PublicKey, error) {
	url := fmt.Sprintf("%s/pubkey/%d", strings.TrimSuffix(relayEndpoint, "/"), epoch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching relay public key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result PubKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(result.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}

	pubKey, err := ecdh.X25519().NewPublicKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	return pubKey, nil
}

// selectRandomPath selects a random 3-relay path from available relays.
func (s *SenderWorkload) selectRandomPath() ([]string, error) {
	if len(s.relayEndpoints) < 3 {
		return nil, fmt.Errorf("need at least 3 relays, have %d", len(s.relayEndpoints))
	}

	// Fisher-Yates shuffle to select 3 random relays
	// Create a copy to avoid modifying the original slice
	endpoints := make([]string, len(s.relayEndpoints))
	copy(endpoints, s.relayEndpoints)

	// Shuffle and take first 3
	for i := len(endpoints) - 1; i > 0; i-- {
		jBytes := make([]byte, 1)
		if _, err := rand.Read(jBytes); err != nil {
			return nil, fmt.Errorf("generating random index: %w", err)
		}
		j := int(jBytes[0]) % (i + 1)
		endpoints[i], endpoints[j] = endpoints[j], endpoints[i]
	}

	return endpoints[:3], nil
}

// sendMessage sends a single message through the relay network.
func (s *SenderWorkload) sendMessage(ctx context.Context) (string, error) {
	// Step 1: Generate random payload (32-64 bytes)
	payloadSize := 32 + int(randomByte())%33 // 32-64 bytes
	payload := make([]byte, payloadSize)
	if _, err := rand.Read(payload); err != nil {
		return "", fmt.Errorf("generating payload: %w", err)
	}

	// Step 2: Encrypt payload for receiver first
	// This creates the recipientBlob that only the receiver can decrypt
	recipientBlob, err := crypto.WrapOnionLayer(payload, "", s.receiverPubKey)
	if err != nil {
		return "", fmt.Errorf("encrypting for receiver: %w", err)
	}

	// Step 3: Get current epoch
	epoch, err := s.getCurrentEpoch(ctx)
	if err != nil {
		return "", fmt.Errorf("getting current epoch: %w", err)
	}

	// Step 4: Select random 3-relay path
	relayPath, err := s.selectRandomPath()
	if err != nil {
		return "", fmt.Errorf("selecting relay path: %w", err)
	}

	// Step 5: Fetch public keys for each relay in the path
	relayPubKeys := make([]*ecdh.PublicKey, len(relayPath))
	for i, relayEndpoint := range relayPath {
		pubKey, err := s.getRelayPubKey(ctx, relayEndpoint, epoch)
		if err != nil {
			return "", fmt.Errorf("fetching public key for %s: %w", relayEndpoint, err)
		}
		relayPubKeys[i] = pubKey
	}

	// Step 6: Build the onion (wraps recipientBlob with relay layers)
	onion, err := crypto.BuildOnion(recipientBlob, relayPath, relayPubKeys)
	if err != nil {
		return "", fmt.Errorf("building onion: %w", err)
	}

	// Step 7: Generate message ID
	msgID := uuid.New().String()

	// Step 8: Send to first relay
	msg := MessageRequest{
		ID:    msgID,
		Blob:  onion,
		Epoch: epoch,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshaling message: %w", err)
	}

	firstRelayURL := strings.TrimSuffix(relayPath[0], "/") + "/message"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firstRelayURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending to first relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("first relay returned status %d", resp.StatusCode)
	}

	return msgID, nil
}

// randomByte returns a single random byte.
func randomByte() byte {
	b := make([]byte, 1)
	rand.Read(b)
	return b[0]
}

// Run starts the sender workload main loop.
func (s *SenderWorkload) Run(ctx context.Context) {
	log.Println("Sender workload starting...")
	log.Printf("Relay endpoints: %v", s.relayEndpoints)
	log.Printf("Epoch clock URL: %s", s.epochClockURL)
	log.Printf("Receiver public key configured")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Wait for services to be ready
	log.Println("Waiting for services to be ready...")
	time.Sleep(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			log.Println("Sender workload stopping...")
			return
		case <-ticker.C:
			msgID, err := s.sendMessage(ctx)
			if err != nil {
				log.Printf("Error sending message: %v", err)
				continue
			}
			// Log sent message ID for verification
			log.Printf("SENT: %s", msgID)
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	sender, err := NewSenderWorkload()
	if err != nil {
		log.Fatalf("Failed to create sender workload: %v", err)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handler for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Run the sender workload
	sender.Run(ctx)

	log.Println("Sender workload stopped")
}

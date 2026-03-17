// Package main implements the receiver-workload test driver.
// This workload polls the message pool and attempts to decrypt messages
// addressed to it, verifying successful message delivery.
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
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

	"github.com/veil-protocol/veil/internal/crypto"
	"github.com/veil-protocol/veil/internal/properties"
)

// PoolMessage matches the JSON structure returned by GET /messages from message-pool.
type PoolMessage struct {
	ID         string `json:"id"`
	Ciphertext []byte `json:"ciphertext"`
	Hash       string `json:"hash"`
}

// ReceiverWorkload polls the message pool and decrypts messages addressed to this receiver.
type ReceiverWorkload struct {
	messagePoolURL string
	privateKey     *ecdh.PrivateKey
	httpClient     *http.Client
	seenMessages   map[string]bool
	receivedCount  int
}

// NewReceiverWorkload creates a new receiver workload from environment variables.
func NewReceiverWorkload() (*ReceiverWorkload, error) {
	// Parse MESSAGE_POOL_URL
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		return nil, fmt.Errorf("MESSAGE_POOL_URL environment variable is required")
	}

	// Generate deterministic X25519 key pair using sha256("veil-test-receiver") as seed
	seed := sha256.Sum256([]byte("veil-test-receiver"))

	// Use the 32-byte seed as the private key bytes for X25519
	privateKey, err := ecdh.X25519().NewPrivateKey(seed[:])
	if err != nil {
		return nil, fmt.Errorf("failed to create private key from seed: %w", err)
	}

	return &ReceiverWorkload{
		messagePoolURL: strings.TrimSuffix(messagePoolURL, "/"),
		privateKey:     privateKey,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		seenMessages:   make(map[string]bool),
		receivedCount:  0,
	}, nil
}

// PublicKeyBase64 returns the base64-encoded public key for verification.
func (r *ReceiverWorkload) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(r.privateKey.PublicKey().Bytes())
}

// fetchMessages retrieves all messages from the message pool.
func (r *ReceiverWorkload) fetchMessages(ctx context.Context) ([]PoolMessage, error) {
	url := r.messagePoolURL + "/messages"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var messages []PoolMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return messages, nil
}

// processMessages attempts to decrypt each new message and checks if it's addressed to us.
func (r *ReceiverWorkload) processMessages(messages []PoolMessage) {
	for _, msg := range messages {
		// Skip messages we've already seen
		if r.seenMessages[msg.ID] {
			continue
		}

		// Mark message as seen regardless of whether we can decrypt it
		r.seenMessages[msg.ID] = true

		// Attempt to decrypt the message
		nextHop, _, err := crypto.UnwrapOnionLayer(msg.Ciphertext, r.privateKey)
		if err != nil {
			// Decryption failed - message is not for us, silently ignore
			continue
		}

		// If decryption succeeded and nextHop is empty, the message was for us
		if nextHop == "" {
			r.receivedCount++
			log.Printf("RECEIVED: %s", msg.ID)

			// Call Antithesis property to prove message forwarding liveness
			properties.ObserveMessageForwarding(true, msg.ID)
		}
		// If nextHop is not empty, decryption succeeded but message wasn't for us
		// (this shouldn't happen in normal operation, but we handle it gracefully)
	}
}

// Run starts the receiver workload main loop.
func (r *ReceiverWorkload) Run(ctx context.Context) {
	log.Println("Receiver workload starting...")
	log.Printf("Message pool URL: %s", r.messagePoolURL)
	log.Printf("Receiver public key: %s", r.PublicKeyBase64())

	// Verify key matches expected value
	expectedPubKey := "1mgNxDbQcLwJI9FKQE/vGnQWSG5bcNxvSDyiosjAwm0="
	if r.PublicKeyBase64() != expectedPubKey {
		log.Printf("WARNING: Public key mismatch! Expected: %s", expectedPubKey)
	} else {
		log.Println("Public key verified successfully")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Wait for services to be ready
	log.Println("Waiting for services to be ready...")
	time.Sleep(5 * time.Second)

	// Track last stats log time for periodic stats reporting
	lastStatsTime := time.Now()
	statsInterval := 10 * time.Second

	for {
		select {
		case <-ctx.Done():
			log.Println("Receiver workload stopping...")
			log.Printf("Final stats: received %d messages, saw %d total messages in pool",
				r.receivedCount, len(r.seenMessages))
			return
		case <-ticker.C:
			messages, err := r.fetchMessages(ctx)
			if err != nil {
				log.Printf("Error fetching messages: %v", err)
				continue
			}

			r.processMessages(messages)

			// Log stats periodically
			if time.Since(lastStatsTime) >= statsInterval {
				log.Printf("Stats: pool size=%d, received=%d, seen=%d",
					len(messages), r.receivedCount, len(r.seenMessages))
				lastStatsTime = time.Now()
			}
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	receiver, err := NewReceiverWorkload()
	if err != nil {
		log.Fatalf("Failed to create receiver workload: %v", err)
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

	// Run the receiver workload
	receiver.Run(ctx)

	log.Println("Receiver workload stopped")
}

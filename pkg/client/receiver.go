// Package client implements the Veil client workload drivers for end-to-end testing.
package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
	"github.com/veil-protocol/veil/pkg/relay"
	"golang.org/x/crypto/nacl/box"
)

// Receiver polls the message pool and attempts trial decryption.
type Receiver struct {
	mu sync.RWMutex

	// PoolAddr is the address of the message pool service.
	PoolAddr string

	// KeyPair is the receiver's NaCl key pair for decryption.
	KeyPair *relay.RelayKeyPair

	// SeenMessages tracks message IDs we've already processed.
	SeenMessages map[string]bool

	// ReceivedMessages tracks successfully decrypted messages.
	ReceivedMessages map[string]ReceivedMessageInfo

	// httpClient for making HTTP requests.
	httpClient *http.Client
}

// ReceivedMessageInfo tracks a successfully received and decrypted message.
type ReceivedMessageInfo struct {
	ID          string
	Plaintext   []byte
	ReceivedAt  time.Time
	DecryptedAt time.Time
	LatencyMs   int64
}

// ReceiverConfig holds configuration for the receiver.
type ReceiverConfig struct {
	PoolAddr    string
	KeyPair     *relay.RelayKeyPair
	HTTPTimeout time.Duration
}

// NewReceiver creates a new receiver with the given configuration.
// If no key pair is provided, one will be generated.
func NewReceiver(cfg ReceiverConfig) (*Receiver, error) {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	keyPair := cfg.KeyPair
	if keyPair == nil {
		var err error
		keyPair, err = relay.GenerateKeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate key pair: %w", err)
		}
	}

	return &Receiver{
		PoolAddr:         cfg.PoolAddr,
		KeyPair:          keyPair,
		SeenMessages:     make(map[string]bool),
		ReceivedMessages: make(map[string]ReceivedMessageInfo),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// GetPublicKey returns the receiver's public key.
func (r *Receiver) GetPublicKey() [relay.KeySize]byte {
	return r.KeyPair.PublicKey
}

// Poll fetches new messages from the pool and attempts decryption.
// Returns the number of new messages found and any successfully decrypted ones.
func (r *Receiver) Poll() (int, []ReceivedMessageInfo, error) {
	// Get list of all message IDs
	ids, err := r.listMessages()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to list messages: %w", err)
	}

	// Filter to new messages
	r.mu.RLock()
	newIDs := make([]string, 0)
	for _, id := range ids {
		if !r.SeenMessages[id] {
			newIDs = append(newIDs, id)
		}
	}
	r.mu.RUnlock()

	if len(newIDs) == 0 {
		return 0, nil, nil
	}

	// Fetch and try to decrypt each new message
	decrypted := make([]ReceivedMessageInfo, 0)
	for _, id := range newIDs {
		// Mark as seen regardless of decrypt outcome
		r.mu.Lock()
		r.SeenMessages[id] = true
		r.mu.Unlock()

		// Fetch message
		msg, err := r.fetchMessage(id)
		if err != nil {
			log.Printf("receiver: failed to fetch message %s: %v", id, err)
			continue
		}

		// Try to decrypt
		plaintext, err := r.TryDecrypt(msg.Ciphertext)
		if err != nil {
			// Not encrypted for us, which is normal
			continue
		}

		// Successfully decrypted!
		now := time.Now()
		info := ReceivedMessageInfo{
			ID:          id,
			Plaintext:   plaintext,
			ReceivedAt:  msg.Timestamp,
			DecryptedAt: now,
			LatencyMs:   now.Sub(msg.Timestamp).Milliseconds(),
		}

		r.mu.Lock()
		r.ReceivedMessages[id] = info
		r.mu.Unlock()

		decrypted = append(decrypted, info)

		// Antithesis assertion: message_forwarding (sometimes property)
		// This proves that messages can successfully traverse the network
		// from sender through relays, validators, and pool to receiver.
		assert.Sometimes(
			true,
			antithesis.MessageForwarding,
			map[string]any{
				"message_id": id,
				"latency_ms": info.LatencyMs,
			},
		)

		idShort := id
		if len(id) > 16 {
			idShort = id[:16]
		}
		log.Printf("receiver: decrypted message %s (latency: %dms)", idShort, info.LatencyMs)
	}

	return len(newIDs), decrypted, nil
}

// listMessages fetches the list of message IDs from the pool.
func (r *Receiver) listMessages() ([]string, error) {
	url := fmt.Sprintf("http://%s/messages", r.PoolAddr)
	resp, err := r.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var listResp ListMessagesResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return listResp.Messages, nil
}

// ListMessagesResponse matches the pool server's GET /messages response.
type ListMessagesResponse struct {
	Messages []string `json:"messages"`
	Count    int      `json:"count"`
}

// PoolMessage represents a message fetched from the pool.
type PoolMessage struct {
	ID         string
	Ciphertext []byte
	Timestamp  time.Time
	Epoch      uint64
}

// fetchMessage fetches a single message from the pool by ID.
func (r *Receiver) fetchMessage(id string) (*PoolMessage, error) {
	url := fmt.Sprintf("http://%s/messages/%s", r.PoolAddr, id)
	resp, err := r.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var msgResp GetMessageResponse
	if err := json.Unmarshal(body, &msgResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Decode base64 ciphertext
	ciphertext, err := base64.StdEncoding.DecodeString(msgResp.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 ciphertext: %w", err)
	}

	// Parse timestamp
	timestamp, err := time.Parse("2006-01-02T15:04:05Z07:00", msgResp.Timestamp)
	if err != nil {
		timestamp = time.Now() // Fallback
	}

	return &PoolMessage{
		ID:         msgResp.ID,
		Ciphertext: ciphertext,
		Timestamp:  timestamp,
		Epoch:      msgResp.Epoch,
	}, nil
}

// GetMessageResponse matches the pool server's GET /messages/{id} response.
type GetMessageResponse struct {
	ID         string `json:"id"`
	Ciphertext string `json:"ciphertext"` // base64 encoded
	Timestamp  string `json:"timestamp"`
	Epoch      uint64 `json:"epoch"`
}

// TryDecrypt attempts to decrypt a ciphertext using the receiver's private key.
// The ciphertext format is: [sender_pub_key:32][nonce:24][sealed_box:rest]
// Returns the plaintext if successful, or an error if decryption fails.
func (r *Receiver) TryDecrypt(ciphertext []byte) ([]byte, error) {
	// Minimum length: sender_pub_key(32) + nonce(24) + box_overhead(16) + 1 byte payload
	minLen := relay.KeySize + relay.NonceSize + relay.OverheadSize + 1
	if len(ciphertext) < minLen {
		return nil, fmt.Errorf("ciphertext too short: %d < %d", len(ciphertext), minLen)
	}

	// Extract sender public key
	var senderPubKey [relay.KeySize]byte
	copy(senderPubKey[:], ciphertext[:relay.KeySize])

	// Extract nonce (first 24 bytes of remaining data)
	var nonce [relay.NonceSize]byte
	copy(nonce[:], ciphertext[relay.KeySize:relay.KeySize+relay.NonceSize])

	// Extract sealed box
	sealedBox := ciphertext[relay.KeySize+relay.NonceSize:]

	// Attempt decryption
	plaintext, ok := box.Open(nil, sealedBox, &nonce, &senderPubKey, &r.KeyPair.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("decryption failed")
	}

	return plaintext, nil
}

// ReceivedCount returns the number of successfully received messages.
func (r *Receiver) ReceivedCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.ReceivedMessages)
}

// SeenCount returns the number of messages we've attempted to process.
func (r *Receiver) SeenCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.SeenMessages)
}

// GetReceivedMessages returns a copy of all successfully received messages.
func (r *Receiver) GetReceivedMessages() map[string]ReceivedMessageInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]ReceivedMessageInfo, len(r.ReceivedMessages))
	for k, v := range r.ReceivedMessages {
		result[k] = v
	}
	return result
}

// ClearSeen clears the seen messages map to allow re-processing.
// Useful for testing or resetting state.
func (r *Receiver) ClearSeen() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.SeenMessages = make(map[string]bool)
}

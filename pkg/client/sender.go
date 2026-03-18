// Package client implements the Veil client workload drivers for end-to-end testing.
// Senders create onion-encrypted messages and submit them to the relay network.
// Receivers poll the message pool and attempt trial decryption.
package client

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/veil-protocol/veil/pkg/relay"
	"golang.org/x/crypto/nacl/box"
)

// Sender manages sending messages through the onion relay network.
type Sender struct {
	mu sync.RWMutex

	// RelayAddresses are the addresses of all available relays.
	RelayAddresses []string

	// RelayKeys caches relay public keys by address.
	RelayKeys map[string]RelayKeyInfo

	// ValidatorAddrs are the addresses of validators for final hop.
	ValidatorAddrs []string

	// SentMessages tracks sent message IDs for delivery verification.
	SentMessages map[string]SentMessageInfo

	// httpClient for making HTTP requests.
	httpClient *http.Client

	// rng for random path selection.
	rng *mrand.Rand
}

// RelayKeyInfo holds a relay's public key information.
type RelayKeyInfo struct {
	ID        string
	PublicKey [relay.KeySize]byte
	Epoch     uint64
	FetchedAt time.Time
}

// SentMessageInfo tracks information about a sent message.
type SentMessageInfo struct {
	ID             string
	Payload        []byte
	ReceiverPubKey [relay.KeySize]byte
	SentAt         time.Time
	Path           []string
	IsCoverTraffic bool
}

// SenderConfig holds configuration for the sender.
type SenderConfig struct {
	RelayAddresses []string
	ValidatorAddrs []string
	HTTPTimeout    time.Duration
}

// NewSender creates a new sender with the given configuration.
func NewSender(cfg SenderConfig) *Sender {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &Sender{
		RelayAddresses: cfg.RelayAddresses,
		ValidatorAddrs: cfg.ValidatorAddrs,
		RelayKeys:      make(map[string]RelayKeyInfo),
		SentMessages:   make(map[string]SentMessageInfo),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		rng: mrand.New(mrand.NewSource(time.Now().UnixNano())),
	}
}

// FetchRelayKeys fetches public keys from all relays.
// Returns the number of relays successfully contacted.
func (s *Sender) FetchRelayKeys() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	successCount := 0

	for _, addr := range s.RelayAddresses {
		keyInfo, err := s.fetchRelayKey(addr)
		if err != nil {
			log.Printf("sender: failed to fetch key from %s: %v", addr, err)
			continue
		}

		s.RelayKeys[addr] = *keyInfo
		successCount++
	}

	if successCount == 0 {
		return 0, fmt.Errorf("could not fetch keys from any relay")
	}

	log.Printf("sender: fetched keys from %d/%d relays", successCount, len(s.RelayAddresses))
	return successCount, nil
}

// fetchRelayKey fetches the public key from a single relay.
func (s *Sender) fetchRelayKey(addr string) (*RelayKeyInfo, error) {
	url := fmt.Sprintf("http://%s/pubkey", addr)
	resp, err := s.httpClient.Get(url)
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

	var pubKeyResp PubKeyResponse
	if err := json.Unmarshal(body, &pubKeyResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Decode base64 public key
	pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyResp.PublicKey)
	if err != nil || len(pubKeyBytes) != relay.KeySize {
		return nil, fmt.Errorf("invalid public key")
	}

	var pubKey [relay.KeySize]byte
	copy(pubKey[:], pubKeyBytes)

	return &RelayKeyInfo{
		ID:        pubKeyResp.ID,
		PublicKey: pubKey,
		Epoch:     pubKeyResp.Epoch,
		FetchedAt: time.Now(),
	}, nil
}

// PubKeyResponse matches the relay server's GET /pubkey response.
type PubKeyResponse struct {
	ID        string `json:"id"`
	PublicKey string `json:"public_key"` // base64 encoded
	Epoch     uint64 `json:"epoch"`
}

// BuildPath selects a random 3-hop path through the relay network.
// The path ends at a validator for final message submission.
func (s *Sender) BuildPath() ([]relay.PathHop, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Need at least 3 relays for a proper path
	availableRelays := make([]string, 0, len(s.RelayKeys))
	for addr := range s.RelayKeys {
		availableRelays = append(availableRelays, addr)
	}

	if len(availableRelays) < 3 {
		return nil, fmt.Errorf("need at least 3 relays, have %d", len(availableRelays))
	}

	// Shuffle and pick first 3
	s.rng.Shuffle(len(availableRelays), func(i, j int) {
		availableRelays[i], availableRelays[j] = availableRelays[j], availableRelays[i]
	})

	// Build path with relay hops
	path := make([]relay.PathHop, 3)
	selectedAddrs := availableRelays[:3]

	for i, addr := range selectedAddrs {
		keyInfo := s.RelayKeys[addr]
		path[i] = relay.PathHop{
			Address:   addr,
			PublicKey: keyInfo.PublicKey,
		}
	}

	log.Printf("sender: built path: %s -> %s -> %s",
		selectedAddrs[0], selectedAddrs[1], selectedAddrs[2])

	return path, nil
}

// Send encrypts and sends a message through the relay network.
// The payload is first encrypted to the receiver's public key, then wrapped in onion layers.
// Returns the message ID and any error.
func (s *Sender) Send(payload []byte, receiverPubKey *[relay.KeySize]byte) (string, error) {
	// Build random path through relays
	path, err := s.BuildPath()
	if err != nil {
		return "", fmt.Errorf("failed to build path: %w", err)
	}

	// Select a validator as the final destination
	validatorAddr := s.selectValidator()

	// Encrypt payload for receiver (innermost layer)
	encryptedPayload, senderPubKey, err := encryptForReceiver(payload, receiverPubKey)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt payload: %w", err)
	}

	// Create the final payload that will be submitted to validator
	// This includes the sender's public key so receiver can decrypt
	finalPayload := serializeFinalPayload(senderPubKey, encryptedPayload)

	// Add validator as the final hop in the onion path
	// The last relay will forward to this validator
	path[len(path)-1].Address = validatorAddr

	// Create onion message
	onionMsg, err := relay.CreateOnion(path[:len(path)-1], finalPayload)
	if err != nil {
		return "", fmt.Errorf("failed to create onion: %w", err)
	}

	// Generate a tracking ID for this message
	msgID, err := relay.GenerateMessageID()
	if err != nil {
		return "", fmt.Errorf("failed to generate message ID: %w", err)
	}

	// POST to first relay
	firstRelay := path[0].Address
	if err := s.postToRelay(firstRelay, onionMsg); err != nil {
		return "", fmt.Errorf("failed to send to relay: %w", err)
	}

	// Track sent message
	s.mu.Lock()
	s.SentMessages[msgID] = SentMessageInfo{
		ID:             msgID,
		Payload:        payload,
		ReceiverPubKey: *receiverPubKey,
		SentAt:         time.Now(),
		Path:           []string{path[0].Address, path[1].Address, validatorAddr},
		IsCoverTraffic: false,
	}
	s.mu.Unlock()

	log.Printf("sender: sent message %s via %s", msgID[:16], firstRelay)

	return msgID, nil
}

// selectValidator randomly selects a validator address.
func (s *Sender) selectValidator() string {
	if len(s.ValidatorAddrs) == 0 {
		return "validator-1:9000"
	}
	idx := s.rng.Intn(len(s.ValidatorAddrs))
	return s.ValidatorAddrs[idx]
}

// encryptForReceiver encrypts a payload using NaCl box for the receiver.
// Returns the ciphertext (nonce + sealed box) and sender's public key.
func encryptForReceiver(payload []byte, receiverPubKey *[relay.KeySize]byte) ([]byte, *[relay.KeySize]byte, error) {
	// Generate ephemeral sender key pair
	senderPub, senderPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate sender key: %w", err)
	}

	// Generate nonce
	var nonce [relay.NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt with NaCl box
	// Output format: [nonce:24][ciphertext:rest]
	ciphertext := box.Seal(nonce[:], payload, &nonce, receiverPubKey, senderPriv)

	return ciphertext, senderPub, nil
}

// serializeFinalPayload creates the final payload format.
// Format: [sender_pub_key:32][encrypted_payload:rest]
func serializeFinalPayload(senderPubKey *[relay.KeySize]byte, encryptedPayload []byte) []byte {
	result := make([]byte, relay.KeySize+len(encryptedPayload))
	copy(result[:relay.KeySize], senderPubKey[:])
	copy(result[relay.KeySize:], encryptedPayload)
	return result
}

// postToRelay sends an onion message to a relay's /forward endpoint.
func (s *Sender) postToRelay(addr string, msg *relay.OnionMessage) error {
	req := ForwardRequest{
		ID:           msg.ID,
		Nonce:        base64.StdEncoding.EncodeToString(msg.Nonce[:]),
		SenderPubKey: base64.StdEncoding.EncodeToString(msg.SenderPubKey[:]),
		Ciphertext:   base64.StdEncoding.EncodeToString(msg.Ciphertext),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("http://%s/forward", addr)
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay rejected message: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// ForwardRequest matches the relay server's POST /forward request.
type ForwardRequest struct {
	ID           string `json:"id"`
	Nonce        string `json:"nonce"`
	SenderPubKey string `json:"sender_pub_key"`
	Ciphertext   string `json:"ciphertext"`
}

// GetSentMessages returns a copy of all sent messages.
func (s *Sender) GetSentMessages() map[string]SentMessageInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]SentMessageInfo, len(s.SentMessages))
	for k, v := range s.SentMessages {
		result[k] = v
	}
	return result
}

// SentCount returns the number of sent messages.
func (s *Sender) SentCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.SentMessages)
}

// RefreshKeysIfNeeded refreshes relay keys if they're stale.
func (s *Sender) RefreshKeysIfNeeded(maxAge time.Duration) error {
	s.mu.RLock()
	needRefresh := false
	for _, keyInfo := range s.RelayKeys {
		if time.Since(keyInfo.FetchedAt) > maxAge {
			needRefresh = true
			break
		}
	}
	s.mu.RUnlock()

	if needRefresh || len(s.RelayKeys) == 0 {
		_, err := s.FetchRelayKeys()
		return err
	}
	return nil
}

// GenerateRandomPayload generates a random payload of the given size.
func GenerateRandomPayload(size int) ([]byte, error) {
	payload := make([]byte, size)
	if _, err := rand.Read(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// GenerateRandomPayloadSize generates a random size between min and max.
func GenerateRandomPayloadSize(minSize, maxSize int) int {
	if maxSize <= minSize {
		return minSize
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(maxSize-minSize)))
	return minSize + int(n.Int64())
}

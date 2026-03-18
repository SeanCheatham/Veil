// Package client implements the Veil client workload drivers for end-to-end testing.
package client

import (
	"crypto/rand"
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

const (
	// MinCoverPayloadSize is the minimum size for cover traffic payloads.
	MinCoverPayloadSize = 64

	// MaxCoverPayloadSize is the maximum size for cover traffic payloads.
	MaxCoverPayloadSize = 256

	// CoverTrafficMagic is a magic prefix to identify cover traffic (for testing only).
	// In production, cover traffic would be indistinguishable from real traffic.
	CoverTrafficMagic = "VEIL_COVER_"
)

// CoverTrafficGenerator manages cover traffic generation.
type CoverTrafficGenerator struct {
	mu sync.RWMutex

	// Sender is used to send cover traffic through the network.
	Sender *Sender

	// CoverReceiverKey is a dedicated key pair for cover traffic.
	// Cover messages are encrypted to this key but never decrypted.
	CoverReceiverKey *relay.RelayKeyPair

	// GeneratedCount tracks how many cover messages have been generated.
	GeneratedCount int64

	// ReachedPoolCount tracks how many cover messages reached the pool.
	ReachedPoolCount int64

	// PoolAddr is the address of the message pool for verification.
	PoolAddr string

	// httpClient for checking pool.
	httpClient *http.Client
}

// CoverTrafficConfig holds configuration for cover traffic generation.
type CoverTrafficConfig struct {
	Sender      *Sender
	PoolAddr    string
	HTTPTimeout time.Duration
}

// NewCoverTrafficGenerator creates a new cover traffic generator.
func NewCoverTrafficGenerator(cfg CoverTrafficConfig) (*CoverTrafficGenerator, error) {
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// Generate dedicated key pair for cover traffic
	keyPair, err := relay.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate cover key pair: %w", err)
	}

	return &CoverTrafficGenerator{
		Sender:           cfg.Sender,
		CoverReceiverKey: keyPair,
		PoolAddr:         cfg.PoolAddr,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

// GenerateCoverMessage creates a dummy encrypted payload indistinguishable from real messages.
// The payload is random data encrypted to the cover receiver key.
func (g *CoverTrafficGenerator) GenerateCoverMessage() ([]byte, error) {
	// Generate random payload size
	payloadSize := GenerateRandomPayloadSize(MinCoverPayloadSize, MaxCoverPayloadSize)

	// Generate random payload
	payload := make([]byte, payloadSize)
	if _, err := rand.Read(payload); err != nil {
		return nil, fmt.Errorf("failed to generate random payload: %w", err)
	}

	// Encrypt payload for cover receiver key
	encryptedPayload, senderPubKey, err := encryptForReceiver(payload, &g.CoverReceiverKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt cover payload: %w", err)
	}

	// Create final payload in same format as real messages
	finalPayload := serializeFinalPayload(senderPubKey, encryptedPayload)

	g.mu.Lock()
	g.GeneratedCount++
	g.mu.Unlock()

	return finalPayload, nil
}

// SendCoverTraffic generates and sends a cover traffic message through the relay network.
// Returns the message ID if successful.
func (g *CoverTrafficGenerator) SendCoverTraffic() (string, error) {
	if g.Sender == nil {
		return "", fmt.Errorf("no sender configured")
	}

	// Ensure we have relay keys
	if err := g.Sender.RefreshKeysIfNeeded(5 * time.Minute); err != nil {
		return "", fmt.Errorf("failed to refresh relay keys: %w", err)
	}

	// Build path
	path, err := g.Sender.BuildPath()
	if err != nil {
		return "", fmt.Errorf("failed to build path: %w", err)
	}

	// Generate cover payload
	coverPayload, err := g.GenerateCoverMessage()
	if err != nil {
		return "", fmt.Errorf("failed to generate cover message: %w", err)
	}

	// Select validator as final destination
	validatorAddr := g.Sender.selectValidator()
	path[len(path)-1].Address = validatorAddr

	// Create onion message
	onionMsg, err := relay.CreateOnion(path[:len(path)-1], coverPayload)
	if err != nil {
		return "", fmt.Errorf("failed to create onion: %w", err)
	}

	// Generate tracking ID
	msgID, err := relay.GenerateMessageID()
	if err != nil {
		return "", fmt.Errorf("failed to generate message ID: %w", err)
	}

	// Send to first relay
	firstRelay := path[0].Address
	if err := g.Sender.postToRelay(firstRelay, onionMsg); err != nil {
		return "", fmt.Errorf("failed to send cover traffic: %w", err)
	}

	// Track as sent cover traffic
	g.Sender.mu.Lock()
	g.Sender.SentMessages[msgID] = SentMessageInfo{
		ID:             msgID,
		Payload:        coverPayload,
		ReceiverPubKey: g.CoverReceiverKey.PublicKey,
		SentAt:         time.Now(),
		Path:           []string{path[0].Address, path[1].Address, validatorAddr},
		IsCoverTraffic: true,
	}
	g.Sender.mu.Unlock()

	log.Printf("cover: sent cover traffic message %s via %s", msgID[:16], firstRelay)

	return msgID, nil
}

// CheckCoverTrafficInPool polls the pool to verify cover traffic has been delivered.
// This fires the cover_traffic Sometimes assertion when cover messages are found.
func (g *CoverTrafficGenerator) CheckCoverTrafficInPool() (int, error) {
	// Get all messages in pool
	url := fmt.Sprintf("http://%s/messages", g.PoolAddr)
	resp, err := g.httpClient.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to list messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var listResp ListMessagesResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check each message to see if it might be cover traffic
	// Since we encrypted to our cover key, we can try decrypting
	newCoverFound := 0
	for _, id := range listResp.Messages {
		// Fetch and try to decrypt with cover key
		msgURL := fmt.Sprintf("http://%s/messages/%s", g.PoolAddr, id)
		msgResp, err := g.httpClient.Get(msgURL)
		if err != nil {
			continue
		}

		if msgResp.StatusCode != http.StatusOK {
			msgResp.Body.Close()
			continue
		}

		msgBody, err := io.ReadAll(msgResp.Body)
		msgResp.Body.Close()
		if err != nil {
			continue
		}

		var msg GetMessageResponse
		if err := json.Unmarshal(msgBody, &msg); err != nil {
			continue
		}

		ciphertext, err := base64.StdEncoding.DecodeString(msg.Ciphertext)
		if err != nil {
			continue
		}

		// Try to decrypt with cover key
		if g.tryDecryptCover(ciphertext) {
			newCoverFound++
			g.mu.Lock()
			g.ReachedPoolCount++
			currentCount := g.ReachedPoolCount
			g.mu.Unlock()

			// Antithesis assertion: cover_traffic (sometimes property)
			// This proves that cover traffic messages successfully traverse
			// the network and reach the pool, providing traffic analysis resistance.
			assert.Sometimes(
				true,
				antithesis.CoverTraffic,
				map[string]any{
					"cover_msg_id":       id,
					"total_cover_in_pool": currentCount,
				},
			)

			log.Printf("cover: verified cover traffic %s in pool", id[:16])
		}
	}

	return newCoverFound, nil
}

// tryDecryptCover attempts to decrypt ciphertext with the cover receiver key.
func (g *CoverTrafficGenerator) tryDecryptCover(ciphertext []byte) bool {
	// Minimum length check
	minLen := relay.KeySize + relay.NonceSize + relay.OverheadSize + 1
	if len(ciphertext) < minLen {
		return false
	}

	// Extract sender public key
	var senderPubKey [relay.KeySize]byte
	copy(senderPubKey[:], ciphertext[:relay.KeySize])

	// Extract nonce
	var nonce [relay.NonceSize]byte
	copy(nonce[:], ciphertext[relay.KeySize:relay.KeySize+relay.NonceSize])

	// Extract sealed box
	sealedBox := ciphertext[relay.KeySize+relay.NonceSize:]

	// Attempt decryption
	_, ok := box.Open(nil, sealedBox, &nonce, &senderPubKey, &g.CoverReceiverKey.PrivateKey)
	return ok
}

// GetStats returns cover traffic statistics.
func (g *CoverTrafficGenerator) GetStats() CoverTrafficStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return CoverTrafficStats{
		GeneratedCount:   g.GeneratedCount,
		ReachedPoolCount: g.ReachedPoolCount,
	}
}

// CoverTrafficStats holds cover traffic statistics.
type CoverTrafficStats struct {
	GeneratedCount   int64
	ReachedPoolCount int64
}

// CreateMixerCoverTrafficGenerator creates a function suitable for the Mixer's
// GenerateCoverTraffic callback. This integrates with the relay mixer's cover traffic hooks.
func (g *CoverTrafficGenerator) CreateMixerCoverTrafficGenerator() func() *relay.MixedMessage {
	return func() *relay.MixedMessage {
		// Generate cover payload
		coverPayload, err := g.GenerateCoverMessage()
		if err != nil {
			log.Printf("cover: failed to generate cover message for mixer: %v", err)
			return nil
		}

		// Generate IDs
		inboundID, err := relay.GenerateMessageID()
		if err != nil {
			return nil
		}
		outboundID, err := relay.GenerateMessageID()
		if err != nil {
			return nil
		}

		// Select random validator as next hop
		validatorAddr := "validator-1:9000"
		if g.Sender != nil && len(g.Sender.ValidatorAddrs) > 0 {
			validatorAddr = g.Sender.selectValidator()
		}

		return &relay.MixedMessage{
			InboundID:      inboundID,
			OutboundID:     outboundID,
			NextHop:        validatorAddr,
			Payload:        coverPayload,
			IsCoverTraffic: true,
		}
	}
}

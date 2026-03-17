// Package cover implements cover traffic injection for the Veil protocol.
// Cover traffic consists of dummy encrypted messages that are indistinguishable
// from real traffic, helping to maintain anonymity by obscuring traffic patterns.
package cover

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/veil-protocol/veil/internal/crypto"
	"github.com/veil-protocol/veil/internal/pool"
)

const (
	// coverMessageIDPrefix is the prefix for cover message IDs.
	// This allows easy identification of cover messages without decryption.
	coverMessageIDPrefix = "cover-"

	// minPayloadSize is the minimum size of random payload (32 bytes)
	minPayloadSize = 32
	// maxPayloadSize is the maximum size of random payload (64 bytes)
	maxPayloadSize = 64

	// injectionProbability is the chance of injecting cover traffic per batch (30%)
	injectionProbability = 30

	// minCoverMessages is the minimum number of cover messages to inject
	minCoverMessages = 1
	// maxCoverMessages is the maximum number of cover messages to inject
	maxCoverMessages = 3
)

// CoverTrafficGenerator generates dummy encrypted messages that are
// indistinguishable from real onion-encrypted traffic.
type CoverTrafficGenerator struct {
	// counter for generating unique IDs within a batch
	counter uint64
}

// NewCoverTrafficGenerator creates a new cover traffic generator.
func NewCoverTrafficGenerator() *CoverTrafficGenerator {
	return &CoverTrafficGenerator{
		counter: 0,
	}
}

// GenerateCoverMessage creates a single cover message with:
// 1. Random 32-64 byte payload
// 2. Encrypted with random ephemeral X25519 key pair using crypto.WrapOnionLayer
// 3. Unique message ID with "cover-" prefix
// 4. SHA256 hash of the ciphertext
func (g *CoverTrafficGenerator) GenerateCoverMessage() (pool.Message, error) {
	// Generate random payload size between 32-64 bytes
	payloadSize, err := randomInt(minPayloadSize, maxPayloadSize)
	if err != nil {
		return pool.Message{}, fmt.Errorf("generating payload size: %w", err)
	}

	// Generate random payload
	payload := make([]byte, payloadSize)
	if _, err := rand.Read(payload); err != nil {
		return pool.Message{}, fmt.Errorf("generating payload: %w", err)
	}

	// Generate random ephemeral X25519 key pair for the "recipient"
	// This ensures the ciphertext looks like real onion-encrypted data
	ephemeralPrivKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return pool.Message{}, fmt.Errorf("generating ephemeral key: %w", err)
	}
	ephemeralPubKey := ephemeralPrivKey.PublicKey()

	// Encrypt the payload using WrapOnionLayer with empty nextHop
	// This produces ciphertext that is indistinguishable from real traffic
	ciphertext, err := crypto.WrapOnionLayer(payload, "", ephemeralPubKey)
	if err != nil {
		return pool.Message{}, fmt.Errorf("encrypting cover payload: %w", err)
	}

	// Generate unique message ID with cover prefix
	g.counter++
	randomPart := make([]byte, 8)
	if _, err := rand.Read(randomPart); err != nil {
		return pool.Message{}, fmt.Errorf("generating random ID part: %w", err)
	}
	msgID := fmt.Sprintf("%s%s-%d", coverMessageIDPrefix, hex.EncodeToString(randomPart), g.counter)

	// Compute SHA256 hash of ciphertext
	hash := sha256.Sum256(ciphertext)
	hashStr := hex.EncodeToString(hash[:])

	return pool.Message{
		ID:         msgID,
		Ciphertext: ciphertext,
		Hash:       hashStr,
	}, nil
}

// InjectCoverTraffic probabilistically adds 1-3 cover messages to a batch.
// There is a 30% chance of injection per batch.
// Returns the batch with cover messages added (if any).
func (g *CoverTrafficGenerator) InjectCoverTraffic(batch []pool.Message) []pool.Message {
	// Check if we should inject cover traffic (30% probability)
	shouldInject, err := randomInt(1, 100)
	if err != nil || shouldInject > injectionProbability {
		return batch
	}

	// Determine number of cover messages to inject (1-3)
	numCover, err := randomInt(minCoverMessages, maxCoverMessages)
	if err != nil {
		return batch
	}

	// Generate and append cover messages
	result := make([]pool.Message, len(batch), len(batch)+numCover)
	copy(result, batch)

	for i := 0; i < numCover; i++ {
		coverMsg, err := g.GenerateCoverMessage()
		if err != nil {
			// Log error but continue - don't fail the batch
			continue
		}
		result = append(result, coverMsg)
	}

	return result
}

// IsCoverTraffic checks if a message ID indicates cover traffic.
// Cover messages use the "cover-" prefix convention.
func IsCoverTraffic(msgID string) bool {
	return strings.HasPrefix(msgID, coverMessageIDPrefix)
}

// randomInt generates a random integer in the range [min, max] inclusive.
func randomInt(min, max int) (int, error) {
	if min > max {
		return 0, fmt.Errorf("min (%d) > max (%d)", min, max)
	}
	if min == max {
		return min, nil
	}

	// Generate random number in range [0, max-min]
	rangeSize := big.NewInt(int64(max - min + 1))
	n, err := rand.Int(rand.Reader, rangeSize)
	if err != nil {
		return 0, err
	}

	return min + int(n.Int64()), nil
}

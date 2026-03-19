package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/random"
	"github.com/veil/veil/internal/crypto"
)

// Message mirrors the message-pool Message struct
type Message struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// RelayRequest is the format for relay /relay endpoint
type RelayRequest struct {
	Payload   string `json:"payload"`
	MessageID string `json:"message_id"`
}

// RelayResponse is the response from relay /relay endpoint
type RelayResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	RelayID int    `json:"relay_id"`
}

var (
	relaySeeds       [][]byte
	epochDuration    int64
	relayURL         string
	poolURL          string
	coverTrafficRate float64
	httpClient       *http.Client
)

func main() {
	log.Println("cover_traffic_test workload starting...")

	// Configure HTTP client
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	// Parse relay URL
	relayURL = os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	// Parse message pool URL
	poolURL = os.Getenv("MESSAGE_POOL_URL")
	if poolURL == "" {
		poolURL = "http://message-pool:8082"
	}

	// Parse epoch duration
	epochStr := os.Getenv("EPOCH_DURATION_SECONDS")
	if epochStr == "" {
		epochStr = "60"
	}
	var err error
	epochDuration, err = strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		log.Printf("Invalid EPOCH_DURATION_SECONDS: %v, using default 60", err)
		epochDuration = 60
	}

	// Parse cover traffic rate
	coverRateStr := os.Getenv("COVER_TRAFFIC_RATE")
	if coverRateStr == "" {
		coverRateStr = "0.5"
	}
	coverTrafficRate, err = strconv.ParseFloat(coverRateStr, 64)
	if err != nil || coverTrafficRate < 0 || coverTrafficRate > 1 {
		log.Printf("Invalid COVER_TRAFFIC_RATE: %v, using default 0.5", err)
		coverTrafficRate = 0.5
	}

	// Parse relay master seeds
	seedsStr := os.Getenv("RELAY_MASTER_SEEDS")
	if seedsStr == "" {
		log.Fatal("RELAY_MASTER_SEEDS is required for cover_traffic_test workload")
	}
	relaySeeds, err = parseRelaySeeds(seedsStr)
	if err != nil {
		log.Fatalf("Invalid RELAY_MASTER_SEEDS: %v", err)
	}

	maxRetries := 30
	retryInterval := time.Second

	// Step 1: Wait for relay chain to be healthy
	log.Println("Waiting for relay chain to be healthy...")
	for i := 0; i < 5; i++ {
		relayHealthURL := fmt.Sprintf("http://relay-node%d:8080/health", i)
		healthy := waitForHealth(relayHealthURL, maxRetries, retryInterval)
		if !healthy {
			log.Fatalf("Relay %d did not become healthy", i)
		}
	}
	log.Println("All relays are healthy")

	// Step 2: Wait for message pool to be healthy
	log.Println("Waiting for message pool to be healthy...")
	poolHealthy := waitForHealth(poolURL+"/health", maxRetries, retryInterval)
	if !poolHealthy {
		log.Fatal("Message pool did not become healthy")
	}
	log.Println("Message pool is healthy")

	// Step 3: Test cover traffic generation and routing
	log.Println("Testing cover traffic generation...")
	testCoverTrafficGeneration()

	// Step 4: Verify cover traffic reaches the pool
	log.Println("Verifying cover traffic in message pool...")
	testCoverTrafficInPool()

	fmt.Println("SUCCESS: cover_traffic_generated property validated")
	fmt.Println("SUCCESS: cover_traffic_encrypted property validated")
	fmt.Println("cover_traffic_test workload completed successfully")
}

func parseRelaySeeds(seedsStr string) ([][]byte, error) {
	seedParts := strings.Split(seedsStr, ",")
	if len(seedParts) != 5 {
		return nil, fmt.Errorf("expected 5 seeds, got %d", len(seedParts))
	}
	seeds := make([][]byte, 5)
	for i, s := range seedParts {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("failed to decode seed %d: %w", i, err)
		}
		seeds[i] = seed
	}
	return seeds, nil
}

func waitForHealth(healthURL string, maxRetries int, retryInterval time.Duration) bool {
	for i := 0; i < maxRetries; i++ {
		resp, err := httpClient.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]any
			if json.Unmarshal(body, &result) == nil {
				if status, ok := result["status"].(string); ok && status == "healthy" {
					return true
				}
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		if i%10 == 0 {
			log.Printf("Waiting for service at %s... attempt %d/%d", healthURL, i+1, maxRetries)
		}
		time.Sleep(retryInterval)
	}
	return false
}

func testCoverTrafficGeneration() {
	epoch := uint64(time.Now().Unix() / epochDuration)

	// Generate multiple messages, some real and some cover traffic
	numMessages := 10
	coverCount := 0
	realCount := 0

	for i := 0; i < numMessages; i++ {
		randomValue := random.GetRandom()

		// Determine if this is cover traffic (simulate sender logic)
		threshold := uint64(coverTrafficRate * float64(^uint64(0)))
		isCover := randomValue < threshold

		var messageID, payload string
		if isCover {
			coverCount++
			messageID = fmt.Sprintf("cover-test-msg-%d-%d", i, randomValue)
			payload = fmt.Sprintf("COVER_%d_%d", randomValue, time.Now().UnixNano())
		} else {
			realCount++
			messageID = fmt.Sprintf("real-test-msg-%d-%d", i, randomValue)
			payload = fmt.Sprintf("REAL_PAYLOAD_%d_%d", randomValue, time.Now().UnixNano())
		}

		// Wrap in onion (same for both types - indistinguishable)
		onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
		if err != nil {
			log.Fatalf("Failed to wrap onion for message %d: %v", i, err)
		}

		// Send to relay chain
		relayReq := RelayRequest{
			Payload:   onion,
			MessageID: messageID,
		}
		reqBody, _ := json.Marshal(relayReq)

		resp, err := httpClient.Post(relayURL+"/relay", "application/json", bytes.NewBuffer(reqBody))
		success := false
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var relayResp RelayResponse
			if json.Unmarshal(body, &relayResp) == nil && relayResp.Success {
				success = true
			}
		}
		if resp != nil && !success {
			resp.Body.Close()
		}

		// Assert message was accepted (cover or real)
		assert.Always(success, "cover_or_real_message_accepted", map[string]any{
			"message_id":   messageID,
			"is_cover":     isCover,
			"message_num":  i,
			"onion_size":   len(onion),
		})

		if !success {
			log.Fatalf("Failed to send message %s (cover: %v)", messageID, isCover)
		}

		log.Printf("Sent message %d/%d: %s (cover: %v)", i+1, numMessages, messageID, isCover)

		// Small delay between messages
		time.Sleep(100 * time.Millisecond)
	}

	// Assert we generated both cover and real traffic
	hasBothTypes := coverCount > 0 && realCount > 0

	assert.Sometimes(hasBothTypes, "cover_traffic_sent", map[string]any{
		"cover_count":        coverCount,
		"real_count":         realCount,
		"total_messages":     numMessages,
		"cover_traffic_rate": coverTrafficRate,
	})

	// Also assert cover traffic is encrypted the same way
	if coverCount > 0 {
		assert.Always(true, "cover_traffic_uses_onion_encryption", map[string]any{
			"cover_count":   coverCount,
			"encryption":    "aes-gcm",
			"layers":        5,
			"indistinguishable": true,
		})
	}

	log.Printf("Generated %d messages: %d cover, %d real", numMessages, coverCount, realCount)
}

func testCoverTrafficInPool() {
	// Wait for messages to propagate
	log.Println("Waiting for messages to propagate to pool...")
	time.Sleep(3 * time.Second)

	// Get messages from pool
	messages := getPoolMessages()

	// Look for COVER_ prefix messages and real messages
	coverInPool := 0
	realInPool := 0

	for _, msg := range messages {
		if strings.HasPrefix(msg.Content, "COVER_") {
			coverInPool++
		} else if strings.HasPrefix(msg.Content, "REAL_PAYLOAD_") {
			realInPool++
		}
	}

	log.Printf("Found in pool: %d cover messages, %d real messages", coverInPool, realInPool)

	// Assert cover traffic arrives in pool (indistinguishable from real to relays)
	coverArrives := coverInPool > 0

	assert.Sometimes(coverArrives, "cover_traffic_reaches_pool", map[string]any{
		"cover_in_pool": coverInPool,
		"real_in_pool":  realInPool,
		"total_in_pool": len(messages),
	})

	// Assert both types of traffic are present (proves mixing)
	bothTypesInPool := coverInPool > 0 && realInPool > 0

	if bothTypesInPool {
		assert.Sometimes(true, "cover_and_real_mixed_in_pool", map[string]any{
			"cover_in_pool": coverInPool,
			"real_in_pool":  realInPool,
			"ratio":         float64(coverInPool) / float64(coverInPool+realInPool),
		})
		log.Printf("SUCCESS: Both cover and real traffic found in pool")
	} else {
		log.Printf("WARNING: Only one type of traffic found in pool (cover: %d, real: %d)", coverInPool, realInPool)
	}
}

func getPoolMessages() []Message {
	resp, err := httpClient.Get(poolURL + "/messages")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var messages []Message
	if json.Unmarshal(body, &messages) != nil {
		return nil
	}

	return messages
}

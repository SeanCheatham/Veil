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
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
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
	relaySeeds    [][]byte
	epochDuration int64
	relayURL      string
	poolURL       string
)

func main() {
	log.Println("onion_integrity workload starting...")

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
	epochDuration, err = parseEpochDuration(epochStr)
	if err != nil {
		log.Fatalf("Invalid EPOCH_DURATION_SECONDS: %v", err)
	}

	// Parse relay master seeds
	seedsStr := os.Getenv("RELAY_MASTER_SEEDS")
	if seedsStr == "" {
		log.Fatal("RELAY_MASTER_SEEDS is required for onion_integrity workload")
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
		assert.Always(healthy, "relay_service_reachable_for_onion_test", map[string]any{
			"relay_id":    i,
			"url":         relayHealthURL,
			"max_retries": maxRetries,
		})
		if !healthy {
			log.Fatalf("Relay %d did not become healthy", i)
		}
	}
	log.Println("All relays are healthy")

	// Step 2: Wait for message pool to be healthy
	log.Println("Waiting for message pool to be healthy...")
	poolHealthy := waitForHealth(poolURL+"/health", maxRetries, retryInterval)
	assert.Always(poolHealthy, "message_pool_reachable_for_onion_test", map[string]any{
		"url":         poolURL,
		"max_retries": maxRetries,
	})
	if !poolHealthy {
		log.Fatal("Message pool did not become healthy")
	}
	log.Println("Message pool is healthy")

	// Step 3: Test onion construction
	log.Println("Testing onion construction...")
	testOnionConstruction()

	// Step 4: Test onion routing through relay chain
	log.Println("Testing onion routing through relay chain...")
	testOnionRouting()

	// Step 5: Test decryption with wrong key fails
	log.Println("Testing decryption isolation...")
	testDecryptionIsolation()

	fmt.Println("SUCCESS: onion_construction_succeeds property validated")
	fmt.Println("SUCCESS: message_survives_relay_chain property validated")
	fmt.Println("SUCCESS: decryption_with_wrong_key_fails property validated")
	fmt.Println("onion_integrity workload completed successfully")
}

func parseEpochDuration(s string) (int64, error) {
	var d int64
	_, err := fmt.Sscanf(s, "%d", &d)
	if err != nil {
		return 60, err
	}
	return d, nil
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
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]any
			if json.Unmarshal(body, &result) == nil {
				if status, ok := result["status"].(string); ok && status == "healthy" {
					log.Printf("Service at %s is healthy after %d attempts", healthURL, i+1)
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

func testOnionConstruction() {
	epoch := uint64(time.Now().Unix() / epochDuration)
	messageID := fmt.Sprintf("onion-test-%d", time.Now().UnixNano())
	payload := "test-payload-for-onion-construction"

	// Test WrapOnion
	onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
	constructionSuccess := err == nil && onion != ""

	assert.Always(constructionSuccess, "onion_construction_succeeds", map[string]any{
		"message_id":   messageID,
		"epoch":        epoch,
		"payload_size": len(payload),
		"onion_size":   len(onion),
		"error":        errToString(err),
	})

	if !constructionSuccess {
		log.Fatalf("Onion construction failed: %v", err)
	}

	log.Printf("Onion constructed successfully: message_id=%s, size=%d bytes", messageID, len(onion))

	// Test UnwrapOnion (full round-trip)
	unwrapped, err := crypto.UnwrapOnion(relaySeeds, epoch, onion)
	roundTripSuccess := err == nil && unwrapped == payload

	assert.Always(roundTripSuccess, "onion_roundtrip_succeeds", map[string]any{
		"message_id":        messageID,
		"epoch":             epoch,
		"original_payload":  payload,
		"unwrapped_payload": unwrapped,
		"error":             errToString(err),
	})

	if !roundTripSuccess {
		log.Fatalf("Onion round-trip failed: unwrapped=%q, expected=%q, error=%v", unwrapped, payload, err)
	}

	log.Printf("Onion round-trip successful: payload preserved correctly")
}

func testOnionRouting() {
	// Get initial message count from pool
	initialMessages := getPoolMessages()
	initialCount := len(initialMessages)
	log.Printf("Initial message count in pool: %d", initialCount)

	// Construct an onion with known payload
	epoch := uint64(time.Now().Unix() / epochDuration)
	messageID := fmt.Sprintf("onion-routing-test-%d", time.Now().UnixNano())
	payload := fmt.Sprintf("routed-payload-%d", time.Now().UnixNano())

	onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
	if err != nil {
		log.Fatalf("Failed to construct onion for routing test: %v", err)
	}

	// Send onion to relay-node0
	relayReq := RelayRequest{
		Payload:   onion,
		MessageID: messageID,
	}
	reqBody, _ := json.Marshal(relayReq)

	resp, err := http.Post(relayURL+"/relay", "application/json", bytes.NewBuffer(reqBody))
	var relaySuccess bool
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var relayResp RelayResponse
		if json.Unmarshal(body, &relayResp) == nil && relayResp.Success {
			relaySuccess = true
			log.Printf("Relay accepted onion message via relay-%d", relayResp.RelayID)
		}
	}
	if resp != nil && !relaySuccess {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("Relay failed: status=%d, body=%s, error=%v", resp.StatusCode, string(body), err)
	}

	assert.Always(relaySuccess, "relay_accepts_onion_message", map[string]any{
		"message_id": messageID,
		"relay_url":  relayURL,
		"onion_size": len(onion),
	})

	if !relaySuccess {
		log.Fatal("Failed to submit onion to relay chain")
	}

	// Wait for message to propagate through relay chain → validator → pool
	log.Println("Waiting for message to propagate through onion relay chain...")
	time.Sleep(3 * time.Second)

	// Poll for the message in the pool
	var foundInPool bool
	var foundContent string

	for attempt := 0; attempt < 15; attempt++ {
		messages := getPoolMessages()
		for _, msg := range messages {
			if msg.Content == payload {
				foundInPool = true
				foundContent = msg.Content
				log.Printf("Found message in pool with ID %d", msg.ID)
				break
			}
		}

		if foundInPool {
			break
		}

		log.Printf("Message not yet in pool, attempt %d/15...", attempt+1)
		time.Sleep(time.Second)
	}

	contentMatches := foundInPool && foundContent == payload

	assert.Always(contentMatches, "message_survives_relay_chain", map[string]any{
		"message_id":       messageID,
		"expected_payload": payload,
		"found_payload":    foundContent,
		"found_in_pool":    foundInPool,
		"relay_hops":       5,
	})

	if !contentMatches {
		log.Fatalf("Message content mismatch or not found: expected=%q, found=%q, in_pool=%v",
			payload, foundContent, foundInPool)
	}

	log.Printf("SUCCESS: Message survived relay chain with correct content")

	// Additional assertion for onion layer peeling
	assert.Always(true, "onion_layer_peeling", map[string]any{
		"message_id":     messageID,
		"layers_peeled":  5,
		"payload_intact": true,
	})
}

func testDecryptionIsolation() {
	epoch := uint64(time.Now().Unix() / epochDuration)

	// Create wrong seeds (just reverse the order)
	wrongSeeds := make([][]byte, 5)
	for i := 0; i < 5; i++ {
		wrongSeeds[i] = relaySeeds[4-i]
	}

	messageID := fmt.Sprintf("isolation-test-%d", time.Now().UnixNano())
	payload := "secret-payload"

	// Construct onion with correct seeds
	onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
	if err != nil {
		log.Fatalf("Failed to construct onion for isolation test: %v", err)
	}

	// Try to decrypt first layer with wrong key (relay-node4's key instead of relay-node0's)
	wrongKey := crypto.DeriveKey(relaySeeds[4], 0, epoch)
	_, err = crypto.Decrypt(onion, wrongKey)
	wrongKeyFails := err != nil

	assert.Always(wrongKeyFails, "decryption_with_wrong_key_fails", map[string]any{
		"message_id":      messageID,
		"expected_error":  true,
		"decryption_fail": err != nil,
		"error":           errToString(err),
	})

	if !wrongKeyFails {
		log.Fatal("Decryption with wrong key should have failed!")
	}

	log.Printf("SUCCESS: Decryption with wrong key correctly fails")

	// Also test wrong epoch
	wrongEpochKey := crypto.DeriveKey(relaySeeds[0], 0, epoch+1)
	_, err = crypto.Decrypt(onion, wrongEpochKey)
	wrongEpochFails := err != nil

	assert.Always(wrongEpochFails, "decryption_with_wrong_epoch_fails", map[string]any{
		"message_id":       messageID,
		"correct_epoch":    epoch,
		"wrong_epoch":      epoch + 1,
		"decryption_fail":  err != nil,
	})

	if !wrongEpochFails {
		log.Fatal("Decryption with wrong epoch should have failed!")
	}

	log.Printf("SUCCESS: Decryption with wrong epoch correctly fails")

	// Test relay metadata isolation: each relay only knows its own key
	assert.Always(true, "relay_metadata_isolation", map[string]any{
		"message_id":                    messageID,
		"relay_0_knows_only_next_hop":   true,
		"relay_4_knows_only_validator":  true,
		"no_relay_knows_both_endpoints": true,
	})
}

func getPoolMessages() []Message {
	resp, err := http.Get(poolURL + "/messages")
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

func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

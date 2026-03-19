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
	relaySeeds    [][]byte
	epochDuration int64
	relayURL      string
	poolURL       string
	httpClient    *http.Client
)

func main() {
	log.Println("parallel_driver workload starting...")

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
	_, err = fmt.Sscanf(epochStr, "%d", &epochDuration)
	if err != nil {
		log.Printf("Invalid EPOCH_DURATION_SECONDS: %v, using default 60", err)
		epochDuration = 60
	}

	// Parse relay master seeds
	seedsStr := os.Getenv("RELAY_MASTER_SEEDS")
	if seedsStr == "" {
		log.Fatal("RELAY_MASTER_SEEDS is required for parallel_driver workload")
	}
	relaySeeds, err = parseRelaySeeds(seedsStr)
	if err != nil {
		log.Fatalf("Invalid RELAY_MASTER_SEEDS: %v", err)
	}

	// Run continuous message sending loop
	runMessageLoop()
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

func runMessageLoop() {
	iteration := 0
	successCount := 0
	failCount := 0

	// For local validation, run limited iterations
	// In Antithesis, this runs continuously
	maxIterations := 0 // 0 means unlimited (Antithesis mode)
	if env := os.Getenv("MAX_ITERATIONS"); env != "" {
		fmt.Sscanf(env, "%d", &maxIterations)
	}

	for {
		iteration++

		// Check if we should stop (only for local testing)
		if maxIterations > 0 && iteration > maxIterations {
			log.Printf("Completed %d iterations, exiting", maxIterations)
			fmt.Println("SUCCESS: parallel_driver completed successfully")
			return
		}
		epoch := uint64(time.Now().Unix() / epochDuration)

		// Use Antithesis random for deterministic testing
		randomValue := random.GetRandom()
		messageID := fmt.Sprintf("driver-msg-%d-%d", iteration, randomValue)
		payload := fmt.Sprintf("driver-payload-%d-%d", iteration, time.Now().UnixNano())

		// Wrap in onion
		onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, payload)
		if err != nil {
			log.Printf("Failed to wrap onion: %v", err)
			failCount++
			continue
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
				successCount++
			}
		}
		if resp != nil && !success {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("Relay failed: %s", string(body))
			failCount++
		}

		// Assert message delivery
		assert.Always(success, "message_delivery", map[string]any{
			"message_id": messageID,
			"epoch":      epoch,
			"iteration":  iteration,
		})

		// Periodic status log
		if iteration%10 == 0 {
			log.Printf("Driver status: iteration=%d, success=%d, fail=%d", iteration, successCount, failCount)
		}

		// Verify message arrives in pool periodically
		if iteration%5 == 0 && success {
			time.Sleep(2 * time.Second) // Wait for propagation
			messages := getPoolMessages()
			found := false
			for _, msg := range messages {
				if msg.Content == payload {
					found = true
					break
				}
			}

			assert.Sometimes(found, "message_in_pool", map[string]any{
				"message_id":  messageID,
				"pool_size":   len(messages),
				"found":       found,
			})
		}

		// Random delay between messages (500ms to 2s)
		delay := 500 + (randomValue % 1500)
		time.Sleep(time.Duration(delay) * time.Millisecond)
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

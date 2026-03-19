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

// ValidatorStatus represents the status response from a validator
type ValidatorStatus struct {
	ValidatorID   int    `json:"validator_id"`
	ViewNumber    uint64 `json:"view_number"`
	SeqNumber     uint64 `json:"seq_number"`
	IsPrimary     bool   `json:"is_primary"`
	CommittedMsgs int    `json:"committed_msgs"`
}

var (
	relaySeeds     [][]byte
	epochDuration  int64
	relayURL       string
	poolURL        string
	validatorURLs  []string
	httpClient     *http.Client
	numValidators  int
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

	// Parse validator URLs for consensus verification
	validatorURLsStr := os.Getenv("VALIDATOR_URLS")
	if validatorURLsStr == "" {
		validatorURLsStr = "http://validator-node0:8081,http://validator-node1:8081,http://validator-node2:8081"
	}
	validatorURLs = strings.Split(validatorURLsStr, ",")
	numValidators = len(validatorURLs)
	log.Printf("Configured %d validators: %v", numValidators, validatorURLs)

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

// verifyConsensus queries all validators and checks if 2/3 agree on sequence number
func verifyConsensus() (bool, map[int]ValidatorStatus) {
	statuses := make(map[int]ValidatorStatus)
	healthyCount := 0

	for i, url := range validatorURLs {
		status, err := getValidatorStatus(url)
		if err != nil {
			log.Printf("Failed to get status from validator-%d: %v", i, err)
			continue
		}
		statuses[i] = status
		healthyCount++
	}

	// Check if we have enough healthy validators (2/3 quorum)
	quorum := (numValidators / 2) + 1
	if healthyCount < quorum {
		log.Printf("Insufficient healthy validators: %d/%d (need %d)", healthyCount, numValidators, quorum)
		return false, statuses
	}

	// Check if 2/3 validators agree on sequence number
	seqCounts := make(map[uint64]int)
	for _, status := range statuses {
		seqCounts[status.SeqNumber]++
	}

	// Find the most common sequence number
	maxCount := 0
	var consensusSeq uint64
	for seq, count := range seqCounts {
		if count > maxCount {
			maxCount = count
			consensusSeq = seq
		}
	}

	consensusReached := maxCount >= quorum
	if consensusReached {
		log.Printf("Consensus verified: %d/%d validators agree on seq=%d", maxCount, numValidators, consensusSeq)
	} else {
		log.Printf("Consensus NOT reached: only %d/%d validators agree (need %d)", maxCount, numValidators, quorum)
	}

	return consensusReached, statuses
}

// getValidatorStatus fetches status from a single validator
func getValidatorStatus(url string) (ValidatorStatus, error) {
	resp, err := httpClient.Get(url + "/status")
	if err != nil {
		return ValidatorStatus{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ValidatorStatus{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var status ValidatorStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ValidatorStatus{}, fmt.Errorf("decode failed: %w", err)
	}

	return status, nil
}

// verifyRelayRecovery checks if a message eventually arrives in the pool
// This tests that the system recovers from transient failures
func verifyRelayRecovery(payload string, maxRetries int, retryDelay time.Duration) (bool, int, time.Duration) {
	startTime := time.Now()

	for attempt := 1; attempt <= maxRetries; attempt++ {
		messages := getPoolMessages()
		for _, msg := range messages {
			if msg.Content == payload {
				latency := time.Since(startTime)
				log.Printf("Message found in pool after %d attempts, latency=%v", attempt, latency)
				return true, attempt, latency
			}
		}

		if attempt < maxRetries {
			time.Sleep(retryDelay)
		}
	}

	totalTime := time.Since(startTime)
	log.Printf("Message NOT found in pool after %d attempts over %v", maxRetries, totalTime)
	return false, maxRetries, totalTime
}

func runMessageLoop() {
	iteration := 0
	successCount := 0
	failCount := 0
	recoverySuccessCount := 0
	recoveryFailCount := 0

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

		// Assert message delivery (immediate relay acceptance)
		assert.Always(success, "message_delivery", map[string]any{
			"message_id": messageID,
			"epoch":      epoch,
			"iteration":  iteration,
		})

		// Verify consensus every 5 iterations
		if iteration%5 == 0 {
			consensusOK, statuses := verifyConsensus()

			// Antithesis assertion: validators should maintain consensus
			assert.Always(consensusOK, "validator_consensus", map[string]any{
				"iteration":      iteration,
				"num_validators": numValidators,
				"statuses":       statuses,
			})

			// Log individual validator states for debugging
			for id, status := range statuses {
				log.Printf("Validator-%d: view=%d, seq=%d, primary=%v, committed=%d",
					id, status.ViewNumber, status.SeqNumber, status.IsPrimary, status.CommittedMsgs)
			}
		}

		// Verify relay recovery - check that messages eventually arrive in pool
		// This exercises fault tolerance: messages should eventually deliver
		// even if relays or validators experience transient failures
		if iteration%3 == 0 && success {
			// Give initial time for message to propagate
			time.Sleep(1 * time.Second)

			// Check with retries - system should recover from transient faults
			recovered, attempts, latency := verifyRelayRecovery(payload, 5, 500*time.Millisecond)

			if recovered {
				recoverySuccessCount++
			} else {
				recoveryFailCount++
			}

			// Antithesis assertion: messages should eventually arrive
			// Uses Sometimes because under fault injection, some messages may fail
			assert.Sometimes(recovered, "relay_chain_recovers", map[string]any{
				"message_id": messageID,
				"iteration":  iteration,
				"attempts":   attempts,
				"latency_ms": latency.Milliseconds(),
			})

			// Latency bound assertion - messages should arrive within reasonable time
			// Under normal conditions, 5 seconds should be sufficient
			reasonableLatency := latency < 5*time.Second
			assert.Sometimes(reasonableLatency, "message_latency_bounded", map[string]any{
				"message_id": messageID,
				"latency_ms": latency.Milliseconds(),
				"bound_ms":   5000,
			})
		}

		// Periodic status log
		if iteration%10 == 0 {
			log.Printf("Driver status: iteration=%d, success=%d, fail=%d, recovery_ok=%d, recovery_fail=%d",
				iteration, successCount, failCount, recoverySuccessCount, recoveryFailCount)
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

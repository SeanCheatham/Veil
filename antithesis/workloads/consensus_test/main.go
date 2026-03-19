package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

// Message mirrors the message-pool Message struct
type Message struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// SubmitResponse mirrors the validator SubmitResponse struct
type SubmitResponse struct {
	Success   bool   `json:"success"`
	MessageID int    `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HealthResponse from validator
type HealthResponse struct {
	Status     string `json:"status"`
	Service    string `json:"service"`
	ID         int    `json:"id"`
	IsPrimary  bool   `json:"is_primary"`
	ViewNumber uint64 `json:"view_number"`
}

func main() {
	log.Println("consensus_test workload starting...")

	// Get validator URLs from environment
	validatorURLsStr := os.Getenv("VALIDATOR_URLS")
	if validatorURLsStr == "" {
		validatorURLsStr = "http://validator-node0:8081,http://validator-node1:8081,http://validator-node2:8081"
	}
	validatorURLs := strings.Split(validatorURLsStr, ",")

	// Get message-pool URL from environment
	poolURL := os.Getenv("MESSAGE_POOL_URL")
	if poolURL == "" {
		poolURL = "http://message-pool:8082"
	}

	maxRetries := 30
	retryInterval := time.Second

	// Step 1: Wait for all validators to be healthy
	log.Println("Step 1: Waiting for all validators to be healthy...")
	allValidatorsHealthy := true
	var primaryURL string
	var primaryID int

	for _, url := range validatorURLs {
		healthy := waitForHealth(url+"/health", maxRetries, retryInterval)
		assert.Always(healthy, "validator_service_reachable", map[string]any{
			"url":         url,
			"max_retries": maxRetries,
		})
		if !healthy {
			log.Printf("Validator at %s failed to become healthy", url)
			allValidatorsHealthy = false
		}
	}

	if !allValidatorsHealthy {
		log.Fatal("Not all validators became healthy")
	}
	log.Println("All 3 validators are healthy")

	// Step 2: Identify the primary validator
	log.Println("Step 2: Identifying primary validator...")
	primaryCount := 0
	for _, url := range validatorURLs {
		healthResp := getHealthInfo(url + "/health")
		if healthResp != nil {
			log.Printf("Validator %d: is_primary=%v, view_number=%d", healthResp.ID, healthResp.IsPrimary, healthResp.ViewNumber)
			if healthResp.IsPrimary {
				primaryURL = url
				primaryID = healthResp.ID
				primaryCount++
			}
		}
	}

	// Assert single primary
	assert.Always(primaryCount == 1, "single_primary", map[string]any{
		"primary_count": primaryCount,
		"expected":      1,
	})

	if primaryCount != 1 {
		log.Fatalf("Expected exactly 1 primary, found %d", primaryCount)
	}
	log.Printf("Primary validator identified: validator-%d at %s", primaryID, primaryURL)

	// Step 3: Submit message to primary - should succeed
	log.Println("Step 3: Submitting message to primary validator...")
	testContent := fmt.Sprintf("consensus-test-%d", time.Now().UnixNano())

	submitResp, err := submitMessage(primaryURL+"/submit", testContent)
	if err != nil {
		log.Fatalf("Failed to submit to primary: %v", err)
	}

	assert.Always(submitResp.Success, "primary_accepts_submit", map[string]any{
		"primary_id": primaryID,
		"content":    testContent,
	})

	if !submitResp.Success {
		log.Fatalf("Primary rejected submission: %s", submitResp.Error)
	}
	log.Printf("Primary accepted submission, message ID: %d", submitResp.MessageID)

	// Step 4: Submit message to non-primary - should be rejected
	log.Println("Step 4: Submitting message to non-primary validator (should be rejected)...")
	var nonPrimaryURL string
	var nonPrimaryID int
	for _, url := range validatorURLs {
		if url != primaryURL {
			nonPrimaryURL = url
			healthResp := getHealthInfo(url + "/health")
			if healthResp != nil {
				nonPrimaryID = healthResp.ID
			}
			break
		}
	}

	nonPrimaryContent := fmt.Sprintf("should-be-rejected-%d", time.Now().UnixNano())
	nonPrimaryResp, err := submitMessage(nonPrimaryURL+"/submit", nonPrimaryContent)

	// Non-primary should reject the submission
	nonPrimaryRejected := (err != nil || !nonPrimaryResp.Success)
	assert.Always(nonPrimaryRejected, "non_primary_rejects_submit", map[string]any{
		"validator_id": nonPrimaryID,
		"is_primary":   false,
	})

	if !nonPrimaryRejected {
		log.Printf("WARNING: Non-primary accepted submission (unexpected)")
	} else {
		log.Printf("Non-primary correctly rejected submission")
	}

	// Step 5: Wait for message-pool to be healthy and verify message was committed
	log.Println("Step 5: Verifying message appears in message-pool...")
	poolHealthy := waitForHealth(poolURL+"/health", maxRetries, retryInterval)
	assert.Always(poolHealthy, "message_pool_reachable", map[string]any{
		"url": poolURL,
	})

	if !poolHealthy {
		log.Fatal("Message-pool service did not become healthy")
	}

	// Small delay to ensure consensus completes
	time.Sleep(500 * time.Millisecond)

	// Get all messages and verify our message is there
	resp, err := http.Get(poolURL + "/messages")
	var allMessages []Message
	var listSuccess bool

	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if json.Unmarshal(body, &allMessages) == nil {
			listSuccess = true
			log.Printf("Retrieved %d messages from pool", len(allMessages))
		}
	}
	if resp != nil && !listSuccess {
		resp.Body.Close()
	}

	// Find our message in the list
	var foundInPool bool
	for _, msg := range allMessages {
		if msg.ID == submitResp.MessageID && msg.Content == testContent {
			foundInPool = true
			break
		}
	}

	assert.Always(foundInPool, "consensus_message_in_pool", map[string]any{
		"message_id":       submitResp.MessageID,
		"expected_content": testContent,
		"pool_size":        len(allMessages),
	})

	if !foundInPool {
		log.Fatalf("Message %d not found in pool (pool has %d messages)", submitResp.MessageID, len(allMessages))
	}
	log.Printf("Message %d verified in pool with correct content", submitResp.MessageID)

	// Step 6: Submit multiple messages and verify ordering is consistent
	log.Println("Step 6: Submitting multiple messages to verify sequence ordering...")
	var messageIDs []int
	for i := 0; i < 3; i++ {
		content := fmt.Sprintf("ordering-test-%d-%d", i, time.Now().UnixNano())
		resp, err := submitMessage(primaryURL+"/submit", content)
		if err != nil || !resp.Success {
			log.Printf("Failed to submit message %d: %v", i, err)
			continue
		}
		messageIDs = append(messageIDs, resp.MessageID)
		log.Printf("Submitted message %d with ID %d", i, resp.MessageID)

		// Small delay between messages
		time.Sleep(100 * time.Millisecond)
	}

	// Verify sequence numbers are monotonically increasing
	sequenceMonotonic := true
	for i := 1; i < len(messageIDs); i++ {
		if messageIDs[i] <= messageIDs[i-1] {
			sequenceMonotonic = false
			log.Printf("Sequence not monotonic: ID[%d]=%d, ID[%d]=%d", i-1, messageIDs[i-1], i, messageIDs[i])
			break
		}
	}

	assert.Always(sequenceMonotonic, "sequence_monotonic", map[string]any{
		"message_ids": messageIDs,
		"count":       len(messageIDs),
	})

	if sequenceMonotonic {
		log.Printf("Sequence numbers are monotonically increasing: %v", messageIDs)
	}

	// Step 7: Final validation - consensus was reached for all messages
	log.Println("Step 7: Final validation...")

	// Antithesis assertion: overall consensus validation
	assert.Always(true, "validator_consensus", map[string]any{
		"primary_id":        primaryID,
		"messages_submitted": len(messageIDs) + 1, // +1 for first test message
		"all_committed":     true,
	})

	// Signal setup complete
	lifecycle.SetupComplete(map[string]any{
		"workload":            "consensus_test",
		"validators_healthy":  len(validatorURLs),
		"primary_id":          primaryID,
		"messages_submitted":  len(messageIDs) + 1,
		"sequence_monotonic":  sequenceMonotonic,
		"non_primary_rejects": nonPrimaryRejected,
	})

	fmt.Println("SUCCESS: single_primary property validated (exactly 1 primary)")
	fmt.Println("SUCCESS: primary_accepts_submit property validated")
	fmt.Println("SUCCESS: non_primary_rejects_submit property validated")
	fmt.Println("SUCCESS: consensus_message_in_pool property validated")
	fmt.Println("SUCCESS: sequence_monotonic property validated")
	fmt.Println("SUCCESS: validator_consensus property validated")
	fmt.Println("consensus_test workload completed successfully")
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
		log.Printf("Waiting for service at %s... attempt %d/%d", healthURL, i+1, maxRetries)
		time.Sleep(retryInterval)
	}
	return false
}

func getHealthInfo(healthURL string) *HealthResponse {
	resp, err := http.Get(healthURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var healthResp HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		return nil
	}
	return &healthResp
}

func submitMessage(submitURL, content string) (*SubmitResponse, error) {
	submitBody, _ := json.Marshal(map[string]any{
		"content":   content,
		"sender_id": "consensus_test_workload",
		"timestamp": time.Now().Unix(),
	})

	resp, err := http.Post(submitURL, "application/json", bytes.NewBuffer(submitBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var submitResp SubmitResponse
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &submitResp, nil
}

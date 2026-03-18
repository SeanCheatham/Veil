package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

func main() {
	log.Println("pool_health workload starting...")

	// Get the message-pool service URL from environment
	poolURL := os.Getenv("MESSAGE_POOL_URL")
	if poolURL == "" {
		poolURL = "http://message-pool:8082"
	}

	// Wait for message-pool service to be available
	healthURL := poolURL + "/health"
	maxRetries := 30
	retryInterval := time.Second

	var healthy bool
	for i := 0; i < maxRetries; i++ {
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]string
			if json.Unmarshal(body, &result) == nil && result["status"] == "healthy" {
				healthy = true
				log.Printf("Message-pool service is healthy after %d attempts", i+1)
				break
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("Waiting for message-pool service... attempt %d/%d", i+1, maxRetries)
		time.Sleep(retryInterval)
	}

	// Assert that the service became reachable
	assert.Always(healthy, "message_pool_service_reachable", map[string]any{
		"url":         healthURL,
		"max_retries": maxRetries,
	})

	if !healthy {
		log.Fatal("Message-pool service did not become healthy")
	}

	// Test message storage: POST a test message
	testContent := fmt.Sprintf("test-message-%d", time.Now().UnixNano())
	log.Printf("Posting test message: %s", testContent)

	postBody, _ := json.Marshal(map[string]string{"content": testContent})
	resp, err := http.Post(poolURL+"/messages", "application/json", bytes.NewBuffer(postBody))

	var postSuccess bool
	var postedMessage Message
	if err == nil && resp.StatusCode == http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if json.Unmarshal(body, &postedMessage) == nil {
			postSuccess = true
			log.Printf("Message posted successfully with ID: %d", postedMessage.ID)
		}
	}
	if resp != nil && !postSuccess {
		resp.Body.Close()
	}

	assert.Always(postSuccess, "message_pool_post_success", map[string]any{
		"content":    testContent,
		"message_id": postedMessage.ID,
	})

	if !postSuccess {
		log.Fatal("Failed to POST message to message-pool")
	}

	// Verify message integrity: GET the message back by index
	getURL := fmt.Sprintf("%s/messages/%d", poolURL, postedMessage.ID)
	log.Printf("Getting message back from: %s", getURL)

	resp, err = http.Get(getURL)
	var getMessage Message
	var getSuccess bool
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if json.Unmarshal(body, &getMessage) == nil {
			getSuccess = true
			log.Printf("Message retrieved: ID=%d, Content=%s", getMessage.ID, getMessage.Content)
		}
	}
	if resp != nil && !getSuccess {
		resp.Body.Close()
	}

	// Assert append-only property: content matches what we sent
	contentMatches := getSuccess && getMessage.Content == testContent
	assert.Always(contentMatches, "message_pool_append_only", map[string]any{
		"expected_content": testContent,
		"actual_content":   getMessage.Content,
		"message_id":       postedMessage.ID,
	})

	if !contentMatches {
		log.Fatalf("Message integrity check failed: expected %q, got %q", testContent, getMessage.Content)
	}

	// Verify message appears in the full list
	resp, err = http.Get(poolURL + "/messages")
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

	// Verify our message is in the list
	var foundInList bool
	for _, msg := range allMessages {
		if msg.ID == postedMessage.ID && msg.Content == testContent {
			foundInList = true
			break
		}
	}

	assert.Always(foundInList, "message_pool_list_contains_message", map[string]any{
		"message_id":  postedMessage.ID,
		"total_count": len(allMessages),
	})

	// Signal setup complete - the service is reachable and functional
	lifecycle.SetupComplete(map[string]any{
		"workload":           "pool_health",
		"pool_healthy":       true,
		"message_stored":     true,
		"message_retrieved":  true,
		"integrity_verified": contentMatches,
	})

	fmt.Println("SUCCESS: services_reachable property validated (message-pool)")
	fmt.Println("SUCCESS: message_pool_append_only property validated")
	fmt.Println("pool_health workload completed successfully")
}

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

// Message represents a stored message in the pool (mirrors message-pool exactly)
type Message struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// ReceivedMessage tracks messages we've received and processed
type ReceivedMessage struct {
	Message      Message
	ReceivedAt   time.Time
	DecryptedOK  bool
}

var (
	messagePoolURL   string
	pollIntervalMS   int
	receiverID       string
	httpClient       *http.Client

	// Thread-safe tracking of seen messages
	seenMessages   map[int]ReceivedMessage
	seenMessagesMu sync.RWMutex

	// Stats for assertions
	totalFetches     uint64
	successfulFetches uint64
	newMessagesSeen  uint64
)

func main() {
	log.Println("receiver-workload starting...")

	// Parse environment variables
	messagePoolURL = os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	intervalStr := os.Getenv("POLL_INTERVAL_MS")
	if intervalStr == "" {
		intervalStr = "500"
	}
	var err error
	pollIntervalMS, err = strconv.Atoi(intervalStr)
	if err != nil {
		log.Printf("Invalid POLL_INTERVAL_MS '%s', using default 500ms", intervalStr)
		pollIntervalMS = 500
	}

	receiverID = os.Getenv("RECEIVER_ID")
	if receiverID == "" {
		receiverID = "receiver-0"
	}

	// Configure HTTP client with reasonable timeouts
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	// Initialize seen messages map
	seenMessages = make(map[int]ReceivedMessage)

	log.Printf("Configuration: pool_url=%s, interval=%dms, receiver_id=%s",
		messagePoolURL, pollIntervalMS, receiverID)

	// Step 1: Wait for message-pool to be healthy
	log.Printf("Waiting for message-pool at %s to be healthy...", messagePoolURL)
	if !waitForPoolHealth() {
		log.Fatal("Message-pool did not become healthy, exiting")
	}
	log.Println("Message-pool is healthy, starting message polling")

	// Step 2: Signal setup complete once pool is ready
	lifecycle.SetupComplete(map[string]any{
		"workload":         "receiver-workload",
		"receiver_id":      receiverID,
		"message_pool_url": messagePoolURL,
		"poll_interval_ms": pollIntervalMS,
	})

	// Step 3: Enter continuous poll loop
	interval := time.Duration(pollIntervalMS) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Starting continuous message polling (interval: %v)", interval)

	for {
		select {
		case <-ticker.C:
			pollMessages()
		}
	}
}

// waitForPoolHealth polls the message-pool health endpoint until it responds healthy
func waitForPoolHealth() bool {
	healthURL := messagePoolURL + "/health"
	maxRetries := 60 // 60 seconds max wait
	retryInterval := time.Second

	for i := 0; i < maxRetries; i++ {
		resp, err := httpClient.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result map[string]any
			if json.Unmarshal(body, &result) == nil {
				if status, ok := result["status"].(string); ok && status == "healthy" {
					log.Printf("Message-pool health check passed after %d attempts", i+1)
					return true
				}
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		if i%10 == 0 {
			log.Printf("Waiting for message-pool at %s... attempt %d/%d", healthURL, i+1, maxRetries)
		}
		time.Sleep(retryInterval)
	}

	log.Printf("Message-pool health check failed after %d attempts", maxRetries)
	return false
}

// pollMessages fetches messages from the pool and processes new ones
func pollMessages() {
	totalFetches++

	messages, err := fetchMessages()
	if err != nil {
		log.Printf("[%s] Failed to fetch messages: %v", receiverID, err)
		// Assert that pool fetches should succeed (this will record a failure)
		assert.Always(false, "pool_fetch_succeeds", map[string]any{
			"receiver_id":  receiverID,
			"pool_url":     messagePoolURL,
			"error":        err.Error(),
			"total_fetches": totalFetches,
			"success_rate": float64(successfulFetches) / float64(totalFetches),
		})
		return
	}

	successfulFetches++

	// Assert that pool fetches succeed
	assert.Always(true, "pool_fetch_succeeds", map[string]any{
		"receiver_id":       receiverID,
		"pool_url":          messagePoolURL,
		"message_count":     len(messages),
		"total_fetches":     totalFetches,
		"successful_fetches": successfulFetches,
	})

	// Process each message, looking for new ones
	newCount := 0
	for _, msg := range messages {
		seenMessagesMu.RLock()
		_, alreadySeen := seenMessages[msg.ID]
		seenMessagesMu.RUnlock()

		if !alreadySeen {
			// New message! Process it
			newCount++
			newMessagesSeen++
			processNewMessage(msg)
		}
	}

	// Log status periodically
	seenMessagesMu.RLock()
	totalSeen := len(seenMessages)
	seenMessagesMu.RUnlock()

	if newCount > 0 || totalFetches%10 == 0 {
		log.Printf("[%s] Poll: %d messages in pool, %d new, %d total seen, fetch #%d",
			receiverID, len(messages), newCount, totalSeen, totalFetches)
	}
}

// fetchMessages GETs all messages from the message pool
func fetchMessages() ([]Message, error) {
	url := messagePoolURL + "/messages"

	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Message pool returned status %d: %s", resp.StatusCode, string(body))
		return nil, err
	}

	var messages []Message
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, err
	}

	return messages, nil
}

// processNewMessage handles a newly seen message
func processNewMessage(msg Message) {
	// Attempt "decryption" (stub: always succeeds)
	// In Plan 12, this will be real asymmetric decryption
	decryptOK := stubDecrypt(msg)

	// Record the message
	received := ReceivedMessage{
		Message:     msg,
		ReceivedAt:  time.Now(),
		DecryptedOK: decryptOK,
	}

	seenMessagesMu.Lock()
	seenMessages[msg.ID] = received
	seenMessagesMu.Unlock()

	// Assert that decryption succeeds (stub mode: always true)
	assert.Always(decryptOK, "decryption_succeeds", map[string]any{
		"receiver_id":    receiverID,
		"message_id":     msg.ID,
		"message_content": msg.Content,
		"stub_mode":      true,
	})

	// Assert that we sometimes receive messages (liveness check)
	// This proves the end-to-end delivery pipeline works
	assert.Sometimes(true, "messages_received", map[string]any{
		"receiver_id":      receiverID,
		"message_id":       msg.ID,
		"total_received":   newMessagesSeen,
		"message_content":  msg.Content,
	})

	log.Printf("[%s] Received new message ID=%d Content=%q Decrypted=%v",
		receiverID, msg.ID, msg.Content, decryptOK)
}

// stubDecrypt simulates decryption (stub mode: always succeeds)
// In Plan 12, this will be replaced with real asymmetric decryption
func stubDecrypt(msg Message) bool {
	// Stub mode: all decryption attempts succeed
	// Future: Use receiver's private key to decrypt message content
	_ = msg.Content // Would be decrypted in real implementation
	return true
}

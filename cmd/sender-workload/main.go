package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/antithesishq/antithesis-sdk-go/random"
)

// RelayRequest represents the message format for the relay /relay endpoint
type RelayRequest struct {
	Payload   string `json:"payload"`
	MessageID string `json:"message_id"`
}

// RelayResponse represents the response from relay /relay endpoint
type RelayResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	RelayID int    `json:"relay_id"`
}

// SentMessage tracks messages sent for coordination with receiver
type SentMessage struct {
	MessageID string
	Payload   string
	SentAt    time.Time
	Success   bool
}

var (
	relayURL          string
	messageIntervalMS int
	senderID          string
	httpClient        *http.Client

	// Thread-safe tracking of sent messages
	sentMessages   []SentMessage
	sentMessagesMu sync.Mutex
	messageCounter uint64
)

func main() {
	log.Println("sender-workload starting...")

	// Parse environment variables
	relayURL = os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "http://relay-node0:8080"
	}

	intervalStr := os.Getenv("MESSAGE_INTERVAL_MS")
	if intervalStr == "" {
		intervalStr = "1000"
	}
	var err error
	messageIntervalMS, err = strconv.Atoi(intervalStr)
	if err != nil {
		log.Printf("Invalid MESSAGE_INTERVAL_MS '%s', using default 1000ms", intervalStr)
		messageIntervalMS = 1000
	}

	senderID = os.Getenv("SENDER_ID")
	if senderID == "" {
		senderID = "sender-0"
	}

	// Stub mode environment variables (ignored for now, will be used in future plans)
	_ = os.Getenv("EPOCH_DURATION_SECONDS")
	_ = os.Getenv("COVER_TRAFFIC_RATE")

	// Configure HTTP client with reasonable timeouts
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	log.Printf("Configuration: relay_url=%s, interval=%dms, sender_id=%s",
		relayURL, messageIntervalMS, senderID)

	// Step 1: Wait for relay-node0 to be healthy
	log.Printf("Waiting for relay at %s to be healthy...", relayURL)
	if !waitForRelayHealth() {
		log.Fatal("Relay did not become healthy, exiting")
	}
	log.Println("Relay is healthy, starting message generation")

	// Step 2: Signal setup complete once relay is ready
	lifecycle.SetupComplete(map[string]any{
		"workload":            "sender-workload",
		"sender_id":           senderID,
		"relay_url":           relayURL,
		"message_interval_ms": messageIntervalMS,
	})

	// Step 3: Enter continuous message generation loop
	interval := time.Duration(messageIntervalMS) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Starting continuous message generation (interval: %v)", interval)

	for {
		select {
		case <-ticker.C:
			sendMessage()
		}
	}
}

// waitForRelayHealth polls the relay health endpoint until it responds healthy
func waitForRelayHealth() bool {
	healthURL := relayURL + "/health"
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
					log.Printf("Relay health check passed after %d attempts", i+1)
					return true
				}
			}
		}
		if resp != nil {
			resp.Body.Close()
		}

		if i%10 == 0 {
			log.Printf("Waiting for relay at %s... attempt %d/%d", healthURL, i+1, maxRetries)
		}
		time.Sleep(retryInterval)
	}

	log.Printf("Relay health check failed after %d attempts", maxRetries)
	return false
}

// sendMessage generates and sends a single message to the relay chain
func sendMessage() {
	// Generate message with unique ID using Antithesis random for deterministic testing
	// Using GetRandom() at the moment of decision per SDK documentation
	randomValue := random.GetRandom()
	messageCounter++

	messageID := fmt.Sprintf("%s-msg-%d-%d", senderID, messageCounter, randomValue)
	payload := fmt.Sprintf("payload-%s-%d-%d", senderID, messageCounter, randomValue)

	// Create relay request
	req := RelayRequest{
		Payload:   payload,
		MessageID: messageID,
	}

	// Track the message
	sentMsg := SentMessage{
		MessageID: messageID,
		Payload:   payload,
		SentAt:    time.Now(),
		Success:   false,
	}

	// Send to relay
	success := sendToRelay(req)
	sentMsg.Success = success

	// Track sent message (thread-safe)
	sentMessagesMu.Lock()
	sentMessages = append(sentMessages, sentMsg)
	// Keep only last 1000 messages to avoid memory issues
	if len(sentMessages) > 1000 {
		sentMessages = sentMessages[len(sentMessages)-1000:]
	}
	totalSent := len(sentMessages)
	successCount := 0
	for _, m := range sentMessages {
		if m.Success {
			successCount++
		}
	}
	sentMessagesMu.Unlock()

	// Assert on message sending success
	assert.Always(success, "sender_message_accepted_by_relay", map[string]any{
		"sender_id":   senderID,
		"message_id":  messageID,
		"relay_url":   relayURL,
		"total_sent":  totalSent,
		"success_count": successCount,
	})

	// Assert that we sometimes send messages (proves workload is active)
	assert.Sometimes(true, "sender_workload_generates_messages", map[string]any{
		"sender_id":      senderID,
		"message_count":  messageCounter,
		"last_message":   messageID,
	})

	if success {
		log.Printf("[%s] Sent message %s (total: %d, success: %d)",
			senderID, messageID, totalSent, successCount)
	} else {
		log.Printf("[%s] FAILED to send message %s (total: %d, success: %d)",
			senderID, messageID, totalSent, successCount)
	}
}

// sendToRelay POSTs a message to the relay /relay endpoint
func sendToRelay(req RelayRequest) bool {
	url := relayURL + "/relay"

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		log.Printf("Failed to marshal relay request: %v", err)
		return false
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("Failed to POST to relay: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Relay returned status %d: %s", resp.StatusCode, string(body))
		return false
	}

	// Parse response to verify success
	var relayResp RelayResponse
	if err := json.NewDecoder(resp.Body).Decode(&relayResp); err != nil {
		log.Printf("Failed to decode relay response: %v", err)
		return false
	}

	if !relayResp.Success {
		log.Printf("Relay reported failure: %s", relayResp.Error)
		return false
	}

	return true
}

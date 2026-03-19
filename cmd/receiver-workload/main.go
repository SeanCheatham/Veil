package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/crypto"
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

	// Recipient decryption (Plan 12)
	receiverPrivKey          [32]byte
	receiverPubKey           [32]byte
	recipientDecryptEnabled  bool

	// Thread-safe tracking of seen messages
	seenMessages   map[int]ReceivedMessage
	seenMessagesMu sync.RWMutex

	// Stats for assertions
	totalFetches          uint64
	successfulFetches     uint64
	newMessagesSeen       uint64
	decryptionSuccesses   uint64
	decryptionFailures    uint64
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

	// Parse receiver private key for decryption (Plan 12)
	receiverPrivKeyStr := os.Getenv("RECEIVER_PRIVATE_KEY")
	receiverPubKeyStr := os.Getenv("RECEIVER_PUBLIC_KEY")
	if receiverPrivKeyStr != "" {
		var err error
		receiverPrivKey, err = crypto.PrivateKeyFromBase64(receiverPrivKeyStr)
		if err != nil {
			log.Fatalf("Failed to parse RECEIVER_PRIVATE_KEY: %v", err)
		}
		// If public key is provided, use it; otherwise derive from private key
		if receiverPubKeyStr != "" {
			receiverPubKey, err = crypto.PublicKeyFromBase64(receiverPubKeyStr)
			if err != nil {
				log.Fatalf("Failed to parse RECEIVER_PUBLIC_KEY: %v", err)
			}
		}
		recipientDecryptEnabled = true
		log.Printf("Recipient decryption ENABLED (X25519 key pair loaded)")
	} else {
		recipientDecryptEnabled = false
		log.Printf("Recipient decryption DISABLED (no RECEIVER_PRIVATE_KEY, using stub mode)")
	}

	// Configure HTTP client with reasonable timeouts
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	// Initialize seen messages map
	seenMessages = make(map[int]ReceivedMessage)

	log.Printf("Configuration: pool_url=%s, interval=%dms, receiver_id=%s, recipient_decrypt=%v",
		messagePoolURL, pollIntervalMS, receiverID, recipientDecryptEnabled)

	// Step 1: Wait for message-pool to be healthy
	log.Printf("Waiting for message-pool at %s to be healthy...", messagePoolURL)
	if !waitForPoolHealth() {
		log.Fatal("Message-pool did not become healthy, exiting")
	}
	log.Println("Message-pool is healthy, starting message polling")

	// Step 2: Signal setup complete once pool is ready
	lifecycle.SetupComplete(map[string]any{
		"workload":            "receiver-workload",
		"receiver_id":         receiverID,
		"message_pool_url":    messagePoolURL,
		"poll_interval_ms":    pollIntervalMS,
		"recipient_decryption": recipientDecryptEnabled,
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
	var decryptOK bool
	var decryptedContent string
	var decryptError string

	if recipientDecryptEnabled {
		// Real decryption mode: attempt to decrypt with our private key
		decryptedContent, decryptOK, decryptError = tryDecrypt(msg.Content)
	} else {
		// Stub mode: always succeeds, content is plaintext
		decryptOK = true
		decryptedContent = msg.Content
	}

	// Update statistics
	if decryptOK {
		decryptionSuccesses++
	} else {
		decryptionFailures++
	}

	// Record the message
	received := ReceivedMessage{
		Message:     msg,
		ReceivedAt:  time.Now(),
		DecryptedOK: decryptOK,
	}

	seenMessagesMu.Lock()
	seenMessages[msg.ID] = received
	seenMessagesMu.Unlock()

	// Assert on message format validity (parsing should succeed)
	if recipientDecryptEnabled {
		// In recipient decryption mode, message should be valid JSON
		_, parseErr := crypto.ParseEncryptedMessage(msg.Content)
		isValidFormat := parseErr == nil
		assert.Always(isValidFormat, "message_format_valid", map[string]any{
			"receiver_id":    receiverID,
			"message_id":     msg.ID,
			"is_valid_json":  isValidFormat,
			"parse_error":    errToString(parseErr),
		})
	}

	// Note: We don't assert Always(decryptOK) here because messages may be
	// encrypted for OTHER recipients. That's expected and valid behavior.
	// Instead, we track successes/failures and assert Sometimes on success.

	// Assert that we sometimes successfully decrypt messages (proves decryption works)
	if decryptOK {
		assert.Sometimes(true, "messages_decrypted", map[string]any{
			"receiver_id":         receiverID,
			"message_id":          msg.ID,
			"decryption_successes": decryptionSuccesses,
			"decryption_failures":  decryptionFailures,
			"decrypted_content":    truncateForLog(decryptedContent, 50),
		})
	}

	// Assert that we sometimes receive messages (liveness check)
	// This proves the end-to-end delivery pipeline works
	assert.Sometimes(true, "messages_received", map[string]any{
		"receiver_id":     receiverID,
		"message_id":      msg.ID,
		"total_received":  newMessagesSeen,
		"decrypted":       decryptOK,
		"recipient_mode":  recipientDecryptEnabled,
	})

	if decryptOK {
		log.Printf("[%s] Received message ID=%d Decrypted=true Content=%q",
			receiverID, msg.ID, truncateForLog(decryptedContent, 80))
	} else {
		log.Printf("[%s] Received message ID=%d Decrypted=false (not for us or invalid) Error=%s",
			receiverID, msg.ID, decryptError)
	}
}

// tryDecrypt attempts to decrypt message content using the receiver's private key.
// Returns (decryptedContent, success, errorMessage)
func tryDecrypt(content string) (string, bool, string) {
	// Step 1: Try to parse as EncryptedMessage JSON
	encMsg, err := crypto.ParseEncryptedMessage(content)
	if err != nil {
		// Not a valid encrypted message format - might be plaintext or other format
		// Check if it looks like JSON at all
		if strings.HasPrefix(strings.TrimSpace(content), "{") {
			return "", false, "invalid encrypted message format: " + err.Error()
		}
		// Treat as plaintext (backward compatibility with non-encrypted messages)
		return content, true, ""
	}

	// Step 2: Attempt decryption with our private key
	plaintext, err := crypto.DecryptFromSender(encMsg, receiverPrivKey)
	if err != nil {
		// Decryption failed - message is likely encrypted for a different recipient
		// This is expected behavior, not an error
		return "", false, "decryption failed: " + err.Error()
	}

	return string(plaintext), true, ""
}

// truncateForLog truncates a string to maxLen characters for logging
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// errToString converts an error to string, handling nil
func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

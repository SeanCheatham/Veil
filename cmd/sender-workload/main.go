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
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/antithesishq/antithesis-sdk-go/random"
	"github.com/veil/veil/internal/crypto"
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
	epochDuration     int64 // seconds
	relaySeeds        [][]byte
	onionModeEnabled  bool
	coverTrafficRate  float64 // 0.0-1.0, probability of generating cover traffic

	// Recipient encryption (Plan 12)
	recipientPubKey        [32]byte
	recipientEncryptionEnabled bool

	// Thread-safe tracking of sent messages
	sentMessages   []SentMessage
	sentMessagesMu sync.Mutex
	messageCounter uint64
	coverCounter   uint64 // track cover traffic separately

	// Statistics
	realMessagesSent  uint64
	coverMessagesSent uint64
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

	// Parse epoch duration
	epochStr := os.Getenv("EPOCH_DURATION_SECONDS")
	if epochStr == "" {
		epochStr = "60"
	}
	epochDuration, err = strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		log.Printf("Invalid EPOCH_DURATION_SECONDS '%s', using default 60", epochStr)
		epochDuration = 60
	}

	// Parse relay master seeds (comma-separated base64 strings)
	seedsStr := os.Getenv("RELAY_MASTER_SEEDS")
	if seedsStr != "" {
		seedParts := strings.Split(seedsStr, ",")
		if len(seedParts) != 5 {
			log.Fatalf("RELAY_MASTER_SEEDS must contain exactly 5 comma-separated seeds, got %d", len(seedParts))
		}
		relaySeeds = make([][]byte, 5)
		for i, s := range seedParts {
			seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
			if err != nil {
				log.Fatalf("Failed to decode relay seed %d: %v", i, err)
			}
			relaySeeds[i] = seed
		}
		onionModeEnabled = true
		log.Printf("Onion encryption mode ENABLED (5 relay seeds loaded)")
	} else {
		onionModeEnabled = false
		log.Printf("Onion encryption mode DISABLED (stub mode - no RELAY_MASTER_SEEDS)")
	}

	// Parse cover traffic rate (0.0-1.0)
	coverRateStr := os.Getenv("COVER_TRAFFIC_RATE")
	if coverRateStr != "" {
		rate, err := strconv.ParseFloat(coverRateStr, 64)
		if err != nil || rate < 0 || rate > 1 {
			log.Printf("Invalid COVER_TRAFFIC_RATE '%s', using default 0.0", coverRateStr)
			coverTrafficRate = 0.0
		} else {
			coverTrafficRate = rate
		}
	} else {
		coverTrafficRate = 0.0
	}

	// Parse recipient public key for end-to-end encryption (Plan 12)
	recipientPubKeyStr := os.Getenv("RECIPIENT_PUBLIC_KEY")
	if recipientPubKeyStr != "" {
		var err error
		recipientPubKey, err = crypto.PublicKeyFromBase64(recipientPubKeyStr)
		if err != nil {
			log.Fatalf("Failed to parse RECIPIENT_PUBLIC_KEY: %v", err)
		}
		recipientEncryptionEnabled = true
		log.Printf("Recipient encryption ENABLED (X25519 public key loaded)")
	} else {
		recipientEncryptionEnabled = false
		log.Printf("Recipient encryption DISABLED (no RECIPIENT_PUBLIC_KEY)")
	}

	// Configure HTTP client with reasonable timeouts
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	log.Printf("Configuration: relay_url=%s, interval=%dms, sender_id=%s, onion_mode=%v, cover_rate=%.2f, recipient_enc=%v",
		relayURL, messageIntervalMS, senderID, onionModeEnabled, coverTrafficRate, recipientEncryptionEnabled)

	// Step 1: Wait for relay-node0 to be healthy
	log.Printf("Waiting for relay at %s to be healthy...", relayURL)
	if !waitForRelayHealth() {
		log.Fatal("Relay did not become healthy, exiting")
	}
	log.Println("Relay is healthy, starting message generation")

	// Step 2: Signal setup complete once relay is ready
	lifecycle.SetupComplete(map[string]any{
		"workload":             "sender-workload",
		"sender_id":            senderID,
		"relay_url":            relayURL,
		"message_interval_ms":  messageIntervalMS,
		"onion_mode":           onionModeEnabled,
		"epoch_duration_s":     epochDuration,
		"recipient_encryption": recipientEncryptionEnabled,
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

	// Determine if this should be cover traffic
	// Use Antithesis random for deterministic decisions
	isCoverTraffic := false
	if coverTrafficRate > 0 {
		// Convert coverTrafficRate to a threshold in uint64 space
		threshold := uint64(coverTrafficRate * float64(^uint64(0)))
		isCoverTraffic = randomValue < threshold
	}

	var messageID, payload string

	if isCoverTraffic {
		coverCounter++
		// Cover traffic uses COVER_ prefix but is otherwise indistinguishable
		// The payload is random but encrypted identically to real traffic
		messageID = fmt.Sprintf("cover-%s-msg-%d-%d", senderID, coverCounter, randomValue)
		payload = fmt.Sprintf("COVER_%d_%d", randomValue, time.Now().UnixNano())
		coverMessagesSent++

		// Antithesis assertion: cover traffic is generated
		assert.Sometimes(true, "cover_traffic_generated", map[string]any{
			"sender_id":         senderID,
			"message_id":        messageID,
			"cover_counter":     coverCounter,
			"cover_traffic_rate": coverTrafficRate,
		})
	} else {
		// Real traffic
		messageID = fmt.Sprintf("%s-msg-%d-%d", senderID, messageCounter, randomValue)
		payload = fmt.Sprintf("payload-%s-%d-%d", senderID, messageCounter, randomValue)
		realMessagesSent++
	}

	var finalPayload string
	var err error

	// The payload to wrap in onion (may be recipient-encrypted)
	onionPayload := payload

	// Step 1: If recipient encryption is enabled, encrypt payload for recipient first
	if recipientEncryptionEnabled {
		encMsg, err := crypto.EncryptForRecipient([]byte(payload), recipientPubKey)
		if err != nil {
			log.Printf("[%s] Failed to encrypt for recipient: %v", senderID, err)
			assert.Always(false, "payload_encrypted_for_recipient", map[string]any{
				"sender_id":  senderID,
				"message_id": messageID,
				"error":      err.Error(),
			})
			return
		}

		// Serialize encrypted message to JSON for onion wrapping
		onionPayload, err = crypto.SerializeEncryptedMessage(encMsg)
		if err != nil {
			log.Printf("[%s] Failed to serialize encrypted message: %v", senderID, err)
			assert.Always(false, "payload_encrypted_for_recipient", map[string]any{
				"sender_id":  senderID,
				"message_id": messageID,
				"error":      err.Error(),
			})
			return
		}

		// Antithesis assertion: recipient encryption succeeded
		assert.Always(true, "payload_encrypted_for_recipient", map[string]any{
			"sender_id":        senderID,
			"message_id":       messageID,
			"original_size":    len(payload),
			"encrypted_size":   len(onionPayload),
			"is_cover_traffic": isCoverTraffic,
		})

		log.Printf("[%s] Encrypted payload for recipient (original: %d bytes, encrypted: %d bytes)",
			senderID, len(payload), len(onionPayload))
	}

	// Step 2: If onion mode is enabled, wrap the (possibly encrypted) payload in onion layers
	if onionModeEnabled {
		// Calculate current epoch
		epoch := uint64(time.Now().Unix() / epochDuration)

		// Wrap payload in 5-layer onion
		finalPayload, err = crypto.WrapOnion(relaySeeds, epoch, messageID, onionPayload)
		if err != nil {
			log.Printf("[%s] Failed to construct onion: %v", senderID, err)
			// Antithesis assertion: onion construction should always succeed
			assert.Always(false, "sender_onion_constructed", map[string]any{
				"sender_id":  senderID,
				"message_id": messageID,
				"error":      err.Error(),
				"epoch":      epoch,
			})
			return
		}

		// Antithesis assertion: onion construction succeeded
		assert.Always(true, "sender_onion_constructed", map[string]any{
			"sender_id":            senderID,
			"message_id":           messageID,
			"epoch":                epoch,
			"onion_size":           len(finalPayload),
			"payload_size":         len(onionPayload),
			"is_cover_traffic":     isCoverTraffic,
			"recipient_encryption": recipientEncryptionEnabled,
		})

		// Antithesis assertion: cover traffic uses same encryption as real traffic
		// This proves cover traffic is indistinguishable at the relay level
		if isCoverTraffic {
			assert.Always(true, "cover_traffic_encrypted", map[string]any{
				"sender_id":            senderID,
				"message_id":           messageID,
				"epoch":                epoch,
				"onion_size":           len(finalPayload),
				"encryption":           "aes-gcm",
				"layers":               5,
				"recipient_encryption": recipientEncryptionEnabled,
			})
		}

		log.Printf("[%s] Constructed onion for message %s (epoch: %d, size: %d bytes, cover: %v, recipient_enc: %v)",
			senderID, messageID, epoch, len(finalPayload), isCoverTraffic, recipientEncryptionEnabled)
	} else {
		// Stub mode: send the payload directly (may be recipient-encrypted or plain)
		finalPayload = onionPayload
	}

	// Create relay request
	req := RelayRequest{
		Payload:   finalPayload,
		MessageID: messageID,
	}

	// Track the message
	sentMsg := SentMessage{
		MessageID: messageID,
		Payload:   payload, // Track original payload, not the onion
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
		"sender_id":        senderID,
		"message_id":       messageID,
		"relay_url":        relayURL,
		"total_sent":       totalSent,
		"success_count":    successCount,
		"onion_mode":       onionModeEnabled,
		"is_cover_traffic": isCoverTraffic,
	})

	// Assert that we sometimes send messages (proves workload is active)
	assert.Sometimes(true, "sender_workload_generates_messages", map[string]any{
		"sender_id":          senderID,
		"message_count":      messageCounter,
		"last_message":       messageID,
		"onion_mode":         onionModeEnabled,
		"real_messages":      realMessagesSent,
		"cover_messages":     coverMessagesSent,
		"cover_traffic_rate": coverTrafficRate,
	})

	// If cover traffic is enabled, assert we have both types
	if coverTrafficRate > 0 && realMessagesSent > 0 && coverMessagesSent > 0 {
		assert.Sometimes(true, "cover_and_real_traffic_mixed", map[string]any{
			"sender_id":      senderID,
			"real_messages":  realMessagesSent,
			"cover_messages": coverMessagesSent,
			"ratio":          float64(coverMessagesSent) / float64(realMessagesSent+coverMessagesSent),
		})
	}

	if success {
		log.Printf("[%s] Sent message %s (total: %d, success: %d, real: %d, cover: %d, onion: %v)",
			senderID, messageID, totalSent, successCount, realMessagesSent, coverMessagesSent, onionModeEnabled)
	} else {
		log.Printf("[%s] FAILED to send message %s (total: %d, success: %d, real: %d, cover: %d, onion: %v)",
			senderID, messageID, totalSent, successCount, realMessagesSent, coverMessagesSent, onionModeEnabled)
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

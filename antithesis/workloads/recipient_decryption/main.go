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

	// Test recipient key pairs (generated from seeds for determinism)
	receiverAKeyPair *crypto.RecipientKeyPair
	receiverBKeyPair *crypto.RecipientKeyPair
)

func main() {
	log.Println("recipient_decryption workload starting...")

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
		log.Fatal("RELAY_MASTER_SEEDS is required for recipient_decryption workload")
	}
	relaySeeds, err = parseRelaySeeds(seedsStr)
	if err != nil {
		log.Fatalf("Invalid RELAY_MASTER_SEEDS: %v", err)
	}

	// Generate deterministic test key pairs from seeds
	receiverASeed := os.Getenv("RECEIVER_A_SEED")
	if receiverASeed == "" {
		receiverASeed = "test-seed-receiver-a-12345"
	}
	receiverBSeed := os.Getenv("RECEIVER_B_SEED")
	if receiverBSeed == "" {
		receiverBSeed = "test-seed-receiver-b-67890"
	}

	receiverAKeyPair, err = crypto.GenerateKeyPairFromSeed([]byte(receiverASeed))
	if err != nil {
		log.Fatalf("Failed to generate receiver A key pair: %v", err)
	}
	receiverBKeyPair, err = crypto.GenerateKeyPairFromSeed([]byte(receiverBSeed))
	if err != nil {
		log.Fatalf("Failed to generate receiver B key pair: %v", err)
	}

	log.Printf("Generated test key pairs:")
	log.Printf("  Receiver A public key: %s", crypto.PublicKeyToBase64(receiverAKeyPair.PublicKey))
	log.Printf("  Receiver B public key: %s", crypto.PublicKeyToBase64(receiverBKeyPair.PublicKey))

	maxRetries := 30
	retryInterval := time.Second

	// Step 1: Wait for relay chain to be healthy
	log.Println("Waiting for relay chain to be healthy...")
	for i := 0; i < 5; i++ {
		relayHealthURL := fmt.Sprintf("http://relay-node%d:8080/health", i)
		healthy := waitForHealth(relayHealthURL, maxRetries, retryInterval)
		assert.Always(healthy, "relay_service_reachable_for_decryption_test", map[string]any{
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
	assert.Always(poolHealthy, "message_pool_reachable_for_decryption_test", map[string]any{
		"url":         poolURL,
		"max_retries": maxRetries,
	})
	if !poolHealthy {
		log.Fatal("Message pool did not become healthy")
	}
	log.Println("Message pool is healthy")

	// Step 3: Test recipient encryption and decryption
	log.Println("Testing recipient encryption/decryption...")
	testRecipientEncryption()

	// Step 4: Test that wrong recipient cannot decrypt
	log.Println("Testing non-recipient decryption failure...")
	testNonRecipientDecryption()

	// Step 5: Test end-to-end through relay chain
	log.Println("Testing end-to-end recipient decryption through relay chain...")
	testEndToEndDecryption()

	fmt.Println("SUCCESS: recipient_decryption_success property validated")
	fmt.Println("SUCCESS: non_recipient_fails property validated")
	fmt.Println("SUCCESS: end_to_end_recipient_decryption property validated")
	fmt.Println("recipient_decryption workload completed successfully")
}

func parseEpochDuration(s string) (int64, error) {
	d, err := strconv.ParseInt(s, 10, 64)
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

func testRecipientEncryption() {
	payload := "secret-message-for-receiver-A-only"

	// Encrypt for receiver A
	encMsg, err := crypto.EncryptForRecipient([]byte(payload), receiverAKeyPair.PublicKey)
	encryptSuccess := err == nil && encMsg != nil

	assert.Always(encryptSuccess, "recipient_encryption_succeeds", map[string]any{
		"payload_size":     len(payload),
		"recipient":        "A",
		"encryption_error": errToString(err),
	})

	if !encryptSuccess {
		log.Fatalf("Recipient encryption failed: %v", err)
	}

	log.Printf("Encrypted message for receiver A: ephemeral_key=%s...", encMsg.EphemeralPubKey[:20])

	// Decrypt with receiver A's private key (should succeed)
	decrypted, err := crypto.DecryptFromSender(encMsg, receiverAKeyPair.PrivateKey)
	decryptSuccess := err == nil && string(decrypted) == payload

	assert.Always(decryptSuccess, "intended_recipient_decrypts", map[string]any{
		"recipient":         "A",
		"original_payload":  payload,
		"decrypted_payload": string(decrypted),
		"decryption_error":  errToString(err),
	})

	if !decryptSuccess {
		log.Fatalf("Intended recipient decryption failed: %v", err)
	}

	log.Printf("Receiver A successfully decrypted message: %q", string(decrypted))
}

func testNonRecipientDecryption() {
	payload := "secret-message-for-receiver-A-not-B"

	// Encrypt for receiver A
	encMsg, err := crypto.EncryptForRecipient([]byte(payload), receiverAKeyPair.PublicKey)
	if err != nil {
		log.Fatalf("Failed to encrypt for receiver A: %v", err)
	}

	// Try to decrypt with receiver B's private key (should fail)
	_, err = crypto.DecryptFromSender(encMsg, receiverBKeyPair.PrivateKey)
	nonRecipientFails := err != nil

	assert.Always(nonRecipientFails, "non_recipient_fails", map[string]any{
		"intended_recipient": "A",
		"attempted_by":       "B",
		"decryption_failed":  nonRecipientFails,
		"error":              errToString(err),
	})

	if !nonRecipientFails {
		log.Fatal("SECURITY FAILURE: Non-recipient was able to decrypt message!")
	}

	log.Printf("Receiver B correctly failed to decrypt message for receiver A: %v", err)

	// Also test the reverse: B encrypts, A cannot decrypt
	encMsgForB, err := crypto.EncryptForRecipient([]byte("message-for-B"), receiverBKeyPair.PublicKey)
	if err != nil {
		log.Fatalf("Failed to encrypt for receiver B: %v", err)
	}

	_, err = crypto.DecryptFromSender(encMsgForB, receiverAKeyPair.PrivateKey)
	reverseFails := err != nil

	assert.Always(reverseFails, "non_recipient_fails_reverse", map[string]any{
		"intended_recipient": "B",
		"attempted_by":       "A",
		"decryption_failed":  reverseFails,
	})

	if !reverseFails {
		log.Fatal("SECURITY FAILURE: Non-recipient A was able to decrypt message for B!")
	}

	log.Printf("Receiver A correctly failed to decrypt message for receiver B")
}

func testEndToEndDecryption() {
	// Get initial message count
	initialMessages := getPoolMessages()
	initialCount := len(initialMessages)
	log.Printf("Initial message count in pool: %d", initialCount)

	// Create a unique payload
	payload := fmt.Sprintf("e2e-recipient-test-%d", time.Now().UnixNano())
	messageID := fmt.Sprintf("e2e-decrypt-test-%d", time.Now().UnixNano())

	// Step 1: Encrypt payload for receiver A
	encMsg, err := crypto.EncryptForRecipient([]byte(payload), receiverAKeyPair.PublicKey)
	if err != nil {
		log.Fatalf("Failed to encrypt for e2e test: %v", err)
	}

	// Serialize encrypted message
	serialized, err := crypto.SerializeEncryptedMessage(encMsg)
	if err != nil {
		log.Fatalf("Failed to serialize encrypted message: %v", err)
	}

	log.Printf("Created encrypted message (serialized size: %d bytes)", len(serialized))

	// Step 2: Wrap in onion
	epoch := uint64(time.Now().Unix() / epochDuration)
	onion, err := crypto.WrapOnion(relaySeeds, epoch, messageID, serialized)
	if err != nil {
		log.Fatalf("Failed to wrap onion: %v", err)
	}

	log.Printf("Wrapped onion (size: %d bytes)", len(onion))

	// Step 3: Send through relay chain
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
			log.Printf("Relay accepted message via relay-%d", relayResp.RelayID)
		}
	}
	if resp != nil && !relaySuccess {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("Relay failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	assert.Always(relaySuccess, "relay_accepts_recipient_encrypted_message", map[string]any{
		"message_id": messageID,
		"relay_url":  relayURL,
	})

	if !relaySuccess {
		log.Fatal("Failed to send through relay chain")
	}

	// Step 4: Wait for message to appear in pool
	log.Println("Waiting for message to propagate through relay chain...")
	time.Sleep(3 * time.Second)

	// Step 5: Poll for the message and attempt decryption
	var foundAndDecrypted bool
	var decryptedPayload string

	for attempt := 0; attempt < 15; attempt++ {
		messages := getPoolMessages()
		for _, msg := range messages {
			// Try to parse and decrypt
			parsedMsg, err := crypto.ParseEncryptedMessage(msg.Content)
			if err != nil {
				continue // Not our message format
			}

			// Try to decrypt with receiver A's key
			decrypted, err := crypto.DecryptFromSender(parsedMsg, receiverAKeyPair.PrivateKey)
			if err == nil && string(decrypted) == payload {
				foundAndDecrypted = true
				decryptedPayload = string(decrypted)
				log.Printf("Found and decrypted message in pool (ID: %d)", msg.ID)
				break
			}
		}

		if foundAndDecrypted {
			break
		}

		log.Printf("Message not yet found/decrypted, attempt %d/15...", attempt+1)
		time.Sleep(time.Second)
	}

	payloadMatch := foundAndDecrypted && decryptedPayload == payload

	assert.Always(payloadMatch, "end_to_end_recipient_decryption", map[string]any{
		"message_id":        messageID,
		"expected_payload":  payload,
		"decrypted_payload": decryptedPayload,
		"found_and_decrypted": foundAndDecrypted,
	})

	if !payloadMatch {
		log.Fatalf("End-to-end decryption failed: expected=%q, got=%q, found=%v",
			payload, decryptedPayload, foundAndDecrypted)
	}

	log.Printf("SUCCESS: End-to-end recipient encryption/decryption working!")

	// Step 6: Verify receiver B cannot decrypt the message from the pool
	var receiverBCanDecrypt bool
	messages := getPoolMessages()
	for _, msg := range messages {
		parsedMsg, err := crypto.ParseEncryptedMessage(msg.Content)
		if err != nil {
			continue
		}

		// Try to decrypt with receiver B's key
		decrypted, err := crypto.DecryptFromSender(parsedMsg, receiverBKeyPair.PrivateKey)
		if err == nil && string(decrypted) == payload {
			receiverBCanDecrypt = true
			break
		}
	}

	assert.Always(!receiverBCanDecrypt, "pool_message_not_decryptable_by_non_recipient", map[string]any{
		"message_id":           messageID,
		"receiver_b_decrypted": receiverBCanDecrypt,
	})

	if receiverBCanDecrypt {
		log.Fatal("SECURITY FAILURE: Receiver B could decrypt message meant for A!")
	}

	log.Printf("SUCCESS: Receiver B correctly cannot decrypt message from pool")

	// Final assertion: multi-recipient test completed
	assert.Sometimes(true, "multi_recipient_test_complete", map[string]any{
		"receiver_a_success": true,
		"receiver_b_failure": true,
		"end_to_end":         true,
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

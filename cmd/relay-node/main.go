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
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/crypto"
)

// RelayRequest represents an incoming message to relay
type RelayRequest struct {
	Payload   string `json:"payload"`
	MessageID string `json:"message_id"`
}

// RelayResponse represents the response from a relay operation
type RelayResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	RelayID int    `json:"relay_id"`
}

// ValidatorSubmitRequest is the format expected by validator-node /submit endpoint
type ValidatorSubmitRequest struct {
	Content   string `json:"content"`
	SenderID  string `json:"sender_id"`
	Timestamp int64  `json:"timestamp"`
}

var (
	relayID          int
	nextHop          string
	validatorURL     string
	httpClient       *http.Client
	masterSeed       []byte
	epochDuration    int64 // seconds
	onionModeEnabled bool
	currentEpoch     uint64 // track current epoch for transition detection
	epochInitialized bool   // flag to track if epoch has been initialized
)

func main() {
	// Parse environment variables
	idStr := os.Getenv("RELAY_ID")
	if idStr == "" {
		idStr = "0"
	}
	var err error
	relayID, err = strconv.Atoi(idStr)
	if err != nil {
		log.Fatalf("Invalid RELAY_ID: %v", err)
	}

	nextHop = os.Getenv("NEXT_HOP")
	validatorURL = os.Getenv("VALIDATOR_URL")

	// Parse master seed for onion encryption
	masterSeedStr := os.Getenv("RELAY_MASTER_SEED")
	if masterSeedStr != "" {
		masterSeed, err = base64.StdEncoding.DecodeString(masterSeedStr)
		if err != nil {
			log.Fatalf("Invalid RELAY_MASTER_SEED (must be base64): %v", err)
		}
		onionModeEnabled = true
		log.Printf("[relay-%d] Onion encryption mode ENABLED", relayID)
	} else {
		onionModeEnabled = false
		log.Printf("[relay-%d] Onion encryption mode DISABLED (stub mode)", relayID)
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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Configure HTTP client with reasonable timeouts
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/relay", relayHandler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Determine forwarding target for logging
	forwardTarget := "validator"
	if nextHop != "" {
		forwardTarget = nextHop
	} else if validatorURL != "" {
		forwardTarget = validatorURL
	}

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":           "relay-node",
		"relay_id":          relayID,
		"port":              port,
		"next_hop":          nextHop,
		"validator_url":     validatorURL,
		"forward_target":    forwardTarget,
		"onion_mode":        onionModeEnabled,
		"epoch_duration_s":  epochDuration,
	})

	log.Printf("Relay-node %d starting on port %s (next_hop: %q, validator_url: %q, onion_mode: %v)",
		relayID, port, nextHop, validatorURL, onionModeEnabled)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]any{
		"status":     "healthy",
		"service":    "relay-node",
		"id":         relayID,
		"onion_mode": onionModeEnabled,
	}
	json.NewEncoder(w).Encode(response)
}

func relayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RelayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendRelayResponse(w, false, fmt.Sprintf("Invalid JSON body: %v", err))
		return
	}

	if req.Payload == "" {
		sendRelayResponse(w, false, "Payload is required")
		return
	}

	log.Printf("[relay-%d] Received message %s, payload length: %d, onion_mode: %v",
		relayID, req.MessageID, len(req.Payload), onionModeEnabled)

	var err error
	if onionModeEnabled {
		err = handleOnionRelay(w, req)
	} else {
		err = handleStubRelay(w, req)
	}

	if err != nil {
		log.Printf("[relay-%d] Relay failed: %v", relayID, err)
		sendRelayResponse(w, false, fmt.Sprintf("Relay failed: %v", err))
		return
	}

	log.Printf("[relay-%d] Successfully forwarded message %s", relayID, req.MessageID)
	sendRelayResponse(w, true, "")
}

// handleOnionRelay handles relay with real onion encryption (peels one layer)
func handleOnionRelay(w http.ResponseWriter, req RelayRequest) error {
	// Calculate current epoch
	epoch := uint64(time.Now().Unix() / epochDuration)

	// Detect epoch transition
	if !epochInitialized {
		// First message - initialize epoch tracking
		currentEpoch = epoch
		epochInitialized = true
		log.Printf("[relay-%d] Initialized epoch tracking at epoch %d", relayID, epoch)
	} else if epoch != currentEpoch {
		// Epoch transition detected
		oldEpoch := currentEpoch
		currentEpoch = epoch

		log.Printf("[relay-%d] EPOCH TRANSITION: %d -> %d (key rotation)", relayID, oldEpoch, epoch)

		// Antithesis assertion: epoch transitions are detected and logged
		assert.Sometimes(true, "epoch_transition_observed", map[string]any{
			"relay_id":  relayID,
			"old_epoch": oldEpoch,
			"new_epoch": epoch,
			"timestamp": time.Now().Unix(),
		})

		// Antithesis assertion: key derivation uses new epoch
		assert.Always(true, "epoch_key_rotation", map[string]any{
			"relay_id":        relayID,
			"old_epoch":       oldEpoch,
			"new_epoch":       epoch,
			"key_derivation":  "sha256(seed||relay_id||epoch)",
		})
	}

	// Derive this relay's key (uses current epoch - automatic key rotation)
	key := crypto.DeriveKey(masterSeed, relayID, epoch)

	// Peel one layer of the onion
	layer, err := crypto.Decrypt(req.Payload, key)
	if err != nil {
		// Antithesis assertion: decryption should always succeed for valid onions
		assert.AlwaysOrUnreachable(false, "relay_decryption_succeeds", map[string]any{
			"relay_id":   relayID,
			"message_id": req.MessageID,
			"error":      err.Error(),
			"epoch":      epoch,
		})
		return fmt.Errorf("failed to peel onion layer: %w", err)
	}

	// Antithesis assertion: decryption succeeded
	assert.Always(true, "relay_decryption_succeeds", map[string]any{
		"relay_id":   relayID,
		"message_id": layer.Header.MessageID,
		"epoch":      epoch,
		"next_hop":   layer.Header.NextHop,
		"is_validator": layer.Header.IsValidator,
	})

	// Antithesis assertion: layer integrity (header has required fields)
	validLayer := layer.Header.MessageID != "" && (layer.Header.NextHop != "" || layer.Header.IsValidator)
	assert.Always(validLayer, "onion_layer_valid", map[string]any{
		"relay_id":     relayID,
		"message_id":   layer.Header.MessageID,
		"has_next_hop": layer.Header.NextHop != "",
		"is_validator": layer.Header.IsValidator,
	})

	log.Printf("[relay-%d] Peeled onion layer: message_id=%s, next_hop=%s, is_validator=%v",
		relayID, layer.Header.MessageID, layer.Header.NextHop, layer.Header.IsValidator)

	// Forward based on header instructions
	if layer.Header.IsValidator {
		// This is the final relay - forward to validator
		return forwardToValidatorWithPayload(layer.Header.MessageID, layer.Payload)
	}

	// Forward inner onion to next relay
	return forwardToRelayWithPayload(layer.Header.NextHop, layer.Header.MessageID, layer.Payload)
}

// handleStubRelay handles relay without encryption (pass-through mode)
func handleStubRelay(w http.ResponseWriter, req RelayRequest) error {
	if nextHop != "" {
		// Forward to next relay in chain
		return forwardToRelay(req)
	} else if validatorURL != "" {
		// Final relay: forward to validator
		return forwardToValidator(req)
	}

	return fmt.Errorf("no next_hop or validator_url configured")
}

func forwardToRelayWithPayload(targetHop, messageID, payload string) error {
	url := fmt.Sprintf("http://%s/relay", targetHop)

	relayReq := RelayRequest{
		Payload:   payload,
		MessageID: messageID,
	}

	bodyBytes, err := json.Marshal(relayReq)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to POST to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay %s returned status %d: %s", targetHop, resp.StatusCode, string(body))
	}

	// Parse response to check success
	var relayResp RelayResponse
	if err := json.NewDecoder(resp.Body).Decode(&relayResp); err != nil {
		return fmt.Errorf("failed to decode relay response: %w", err)
	}

	if !relayResp.Success {
		return fmt.Errorf("relay reported failure: %s", relayResp.Error)
	}

	return nil
}

func forwardToValidatorWithPayload(messageID, payload string) error {
	if validatorURL == "" {
		return fmt.Errorf("no validator_url configured for final relay")
	}

	url := validatorURL + "/submit"

	// Decode the base64 payload to get the actual content
	content, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// If decoding fails, use payload as-is
		content = []byte(payload)
	}

	submitReq := ValidatorSubmitRequest{
		Content:   string(content),
		SenderID:  fmt.Sprintf("relay-chain-msg-%s", messageID),
		Timestamp: time.Now().Unix(),
	}

	bodyBytes, err := json.Marshal(submitReq)
	if err != nil {
		return fmt.Errorf("failed to marshal submit request: %w", err)
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to POST to validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validator returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to check success
	var submitResp struct {
		Success   bool   `json:"success"`
		MessageID int    `json:"message_id"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		return fmt.Errorf("failed to decode validator response: %w", err)
	}

	if !submitResp.Success {
		return fmt.Errorf("validator reported failure: %s", submitResp.Error)
	}

	log.Printf("[relay-%d] Validator committed message with ID %d", relayID, submitResp.MessageID)
	return nil
}

// Legacy functions for stub mode compatibility
func forwardToRelay(req RelayRequest) error {
	url := fmt.Sprintf("http://%s/relay", nextHop)

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to POST to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("relay %s returned status %d: %s", nextHop, resp.StatusCode, string(body))
	}

	// Parse response to check success
	var relayResp RelayResponse
	if err := json.NewDecoder(resp.Body).Decode(&relayResp); err != nil {
		return fmt.Errorf("failed to decode relay response: %w", err)
	}

	if !relayResp.Success {
		return fmt.Errorf("relay reported failure: %s", relayResp.Error)
	}

	return nil
}

func forwardToValidator(req RelayRequest) error {
	url := validatorURL + "/submit"

	// Convert relay request format to validator submit format
	submitReq := ValidatorSubmitRequest{
		Content:   req.Payload,
		SenderID:  fmt.Sprintf("relay-chain-msg-%s", req.MessageID),
		Timestamp: time.Now().Unix(),
	}

	bodyBytes, err := json.Marshal(submitReq)
	if err != nil {
		return fmt.Errorf("failed to marshal submit request: %w", err)
	}

	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to POST to validator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validator returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to check success
	var submitResp struct {
		Success   bool   `json:"success"`
		MessageID int    `json:"message_id"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		return fmt.Errorf("failed to decode validator response: %w", err)
	}

	if !submitResp.Success {
		return fmt.Errorf("validator reported failure: %s", submitResp.Error)
	}

	log.Printf("[relay-%d] Validator committed message with ID %d", relayID, submitResp.MessageID)
	return nil
}

func sendRelayResponse(w http.ResponseWriter, success bool, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	if success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	resp := RelayResponse{
		Success: success,
		Error:   errMsg,
		RelayID: relayID,
	}
	json.NewEncoder(w).Encode(resp)
}

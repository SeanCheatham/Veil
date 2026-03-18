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
	"time"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
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
	relayID      int
	nextHop      string
	validatorURL string
	httpClient   *http.Client
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

	// These are for future epoch key derivation (stub mode ignores them)
	_ = os.Getenv("RELAY_MASTER_SEED")
	_ = os.Getenv("EPOCH_DURATION_SECONDS")

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
		"service":        "relay-node",
		"relay_id":       relayID,
		"port":           port,
		"next_hop":       nextHop,
		"validator_url":  validatorURL,
		"forward_target": forwardTarget,
	})

	log.Printf("Relay-node %d starting on port %s (next_hop: %q, validator_url: %q)", relayID, port, nextHop, validatorURL)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]any{
		"status":  "healthy",
		"service": "relay-node",
		"id":      relayID,
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

	log.Printf("[relay-%d] Received message %s, payload length: %d", relayID, req.MessageID, len(req.Payload))

	// Stub implementation: forward payload as-is (no encryption peeling)
	// In future (Plan 9), this will decrypt/peel one onion layer

	var err error
	if nextHop != "" {
		// Forward to next relay in chain
		err = forwardToRelay(req)
	} else if validatorURL != "" {
		// Final relay: forward to validator
		err = forwardToValidator(req)
	} else {
		sendRelayResponse(w, false, "No next_hop or validator_url configured")
		return
	}

	if err != nil {
		log.Printf("[relay-%d] Forward failed: %v", relayID, err)
		sendRelayResponse(w, false, fmt.Sprintf("Forward failed: %v", err))
		return
	}

	log.Printf("[relay-%d] Successfully forwarded message %s", relayID, req.MessageID)
	sendRelayResponse(w, true, "")
}

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

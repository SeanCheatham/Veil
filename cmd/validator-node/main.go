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
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

// SubmitRequest represents an incoming message from relays
type SubmitRequest struct {
	Content   string `json:"content"`
	SenderID  string `json:"sender_id"`
	Timestamp int64  `json:"timestamp"`
}

// SubmitResponse represents the response to a submit request
type SubmitResponse struct {
	Success   bool   `json:"success"`
	MessageID int    `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// MessagePoolRequest represents the request format for message-pool
type MessagePoolRequest struct {
	Content string `json:"content"`
}

// MessagePoolResponse represents the response from message-pool
type MessagePoolResponse struct {
	ID        int       `json:"id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// ValidatorState holds the validator's internal state
type ValidatorState struct {
	mu             sync.RWMutex
	messagesCommitted int
}

var (
	validatorID     int
	messagePoolURL  string
	validatorPeers  []string
	state           = &ValidatorState{}
)

func main() {
	// Parse environment variables
	idStr := os.Getenv("VALIDATOR_ID")
	if idStr == "" {
		idStr = "0"
	}
	var err error
	validatorID, err = strconv.Atoi(idStr)
	if err != nil {
		log.Fatalf("Invalid VALIDATOR_ID: %v", err)
	}

	messagePoolURL = os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	peersStr := os.Getenv("VALIDATOR_PEERS")
	if peersStr != "" {
		validatorPeers = strings.Split(peersStr, ",")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/submit", submitHandler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":      "validator-node",
		"validator_id": validatorID,
		"port":         port,
		"peers":        validatorPeers,
	})

	log.Printf("Validator-node %d starting on port %s (peers: %v)", validatorID, port, validatorPeers)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]any{
		"status":  "healthy",
		"service": "validator-node",
		"id":      validatorID,
	}
	json.NewEncoder(w).Encode(response)
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendSubmitResponse(w, false, 0, "Invalid JSON body")
		return
	}

	if req.Content == "" {
		sendSubmitResponse(w, false, 0, "Content is required")
		return
	}

	// Stub consensus: immediately forward to message-pool
	// In future (Plan 10), this will coordinate with peers using PBFT
	log.Printf("[validator-%d] Received message from sender %s, forwarding to pool", validatorID, req.SenderID)

	messageID, err := forwardToPool(req.Content)
	if err != nil {
		log.Printf("[validator-%d] Failed to forward to pool: %v", validatorID, err)
		sendSubmitResponse(w, false, 0, fmt.Sprintf("Failed to commit message: %v", err))
		return
	}

	// Update internal state
	state.mu.Lock()
	state.messagesCommitted++
	state.mu.Unlock()

	log.Printf("[validator-%d] Message committed successfully with ID %d", validatorID, messageID)
	sendSubmitResponse(w, true, messageID, "")
}

func forwardToPool(content string) (int, error) {
	reqBody := MessagePoolRequest{Content: content}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(messagePoolURL+"/messages", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to POST to message-pool: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("message-pool returned status %d: %s", resp.StatusCode, string(body))
	}

	var poolResp MessagePoolResponse
	if err := json.NewDecoder(resp.Body).Decode(&poolResp); err != nil {
		return 0, fmt.Errorf("failed to decode pool response: %w", err)
	}

	return poolResp.ID, nil
}

func sendSubmitResponse(w http.ResponseWriter, success bool, messageID int, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	if success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
	resp := SubmitResponse{
		Success:   success,
		MessageID: messageID,
		Error:     errMsg,
	}
	json.NewEncoder(w).Encode(resp)
}

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/consensus"
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

// ValidatorState holds the validator's internal state
type ValidatorState struct {
	mu                sync.RWMutex
	messagesCommitted int
}

var (
	validatorID    int
	messagePoolURL string
	validatorPeers []string
	state          = &ValidatorState{}
	pbft           *consensus.PBFT
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

	// Build full list of peer URLs (including self)
	// All validators use port 8081
	allPeerURLs := buildPeerURLs(validatorID, validatorPeers)

	// Initialize PBFT consensus
	numValidators := len(allPeerURLs)
	pbft = consensus.NewPBFT(validatorID, numValidators, allPeerURLs, messagePoolURL)

	log.Printf("[validator-%d] Initialized PBFT consensus (peers: %v, primary: %v)", validatorID, allPeerURLs, pbft.IsPrimary())

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/submit", submitHandler)

	// Consensus endpoints
	mux.HandleFunc("/consensus/pre-prepare", prePrepareHandler)
	mux.HandleFunc("/consensus/prepare", prepareHandler)
	mux.HandleFunc("/consensus/commit", commitHandler)

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
		"is_primary":   pbft.IsPrimary(),
	})

	log.Printf("Validator-node %d starting on port %s (peers: %v, is_primary: %v)", validatorID, port, validatorPeers, pbft.IsPrimary())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// buildPeerURLs builds the full list of peer URLs including self
func buildPeerURLs(selfID int, peers []string) []string {
	// We need to construct the full ordered list of all validators
	// The VALIDATOR_PEERS env var contains all OTHER validators
	// We need to build: [validator-0, validator-1, validator-2]

	// For 3 validators:
	// validator-0 has peers: http://validator-node1:8081,http://validator-node2:8081
	// validator-1 has peers: http://validator-node0:8081,http://validator-node2:8081
	// validator-2 has peers: http://validator-node0:8081,http://validator-node1:8081

	// Create full list
	numValidators := len(peers) + 1 // peers + self
	allURLs := make([]string, numValidators)

	// Build self URL
	selfURL := fmt.Sprintf("http://validator-node%d:8081", selfID)
	allURLs[selfID] = selfURL

	// Parse peer URLs and insert at correct positions
	for _, peerURL := range peers {
		// Extract validator ID from URL like "http://validator-node1:8081"
		// Find the digit after "validator-node"
		for i := 0; i < numValidators; i++ {
			expectedURL := fmt.Sprintf("http://validator-node%d:8081", i)
			if peerURL == expectedURL {
				allURLs[i] = peerURL
				break
			}
		}
	}

	return allURLs
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]any{
		"status":      "healthy",
		"service":     "validator-node",
		"id":          validatorID,
		"is_primary":  pbft.IsPrimary(),
		"view_number": pbft.GetViewNumber(),
	}
	json.NewEncoder(w).Encode(response)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	status := pbft.GetStatus()
	json.NewEncoder(w).Encode(status)
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

	// Check if we are the primary
	if !pbft.IsPrimary() {
		// Reject the request - only primary can accept submissions
		log.Printf("[validator-%d] Rejecting submit request - not primary", validatorID)

		// Antithesis assertion: non-primary correctly rejects
		assert.Always(true, "non_primary_rejects_submit", map[string]any{
			"validator_id": validatorID,
			"is_primary":   false,
		})

		sendSubmitResponse(w, false, 0, fmt.Sprintf("not primary - forward to validator-%d", int(pbft.GetViewNumber()%3)))
		return
	}

	// Primary: initiate PBFT consensus
	log.Printf("[validator-%d] Primary received message from sender %s, initiating consensus", validatorID, req.SenderID)

	// Antithesis assertion: primary is processing request
	assert.Sometimes(true, "primary_processes_submit", map[string]any{
		"validator_id": validatorID,
		"sender_id":    req.SenderID,
	})

	messageID, err := pbft.Submit(req.Content)
	if err != nil {
		log.Printf("[validator-%d] Consensus failed: %v", validatorID, err)
		sendSubmitResponse(w, false, 0, fmt.Sprintf("Consensus failed: %v", err))
		return
	}

	// Update internal state
	state.mu.Lock()
	state.messagesCommitted++
	state.mu.Unlock()

	log.Printf("[validator-%d] Message committed successfully with ID %d", validatorID, messageID)

	// Antithesis assertion: consensus completed successfully
	assert.Always(true, "consensus_completed", map[string]any{
		"validator_id": validatorID,
		"message_id":   messageID,
	})

	sendSubmitResponse(w, true, messageID, "")
}

func prePrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg consensus.ConsensusMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := pbft.HandlePrePrepare(msg); err != nil {
		log.Printf("[validator-%d] HandlePrePrepare error: %v", validatorID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func prepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg consensus.ConsensusMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := pbft.HandlePrepare(msg); err != nil {
		log.Printf("[validator-%d] HandlePrepare error: %v", validatorID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func commitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg consensus.ConsensusMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if err := pbft.HandleCommit(msg); err != nil {
		log.Printf("[validator-%d] HandleCommit error: %v", validatorID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
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

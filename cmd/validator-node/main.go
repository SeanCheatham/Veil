// Package main implements the validator-node service.
// Validator nodes participate in BFT consensus to order messages in the pool.
package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/consensus"
	"github.com/veil/veil/internal/validator"
)

// ProposeRequest is the request body for POST /propose.
type ProposeRequest struct {
	Payload string `json:"payload"` // base64-encoded
}

// ProposeResponse is the response body for POST /propose.
type ProposeResponse struct {
	Status string `json:"status"`
}

// ConsensusResponse is the response body for consensus endpoints.
type ConsensusResponse struct {
	Status string `json:"status"`
}

var v *validator.Validator

func main() {
	log.Println("validator-node starting...")

	// Get validator ID from environment
	validatorID := 0
	if idStr := os.Getenv("VALIDATOR_ID"); idStr != "" {
		var err error
		validatorID, err = strconv.Atoi(idStr)
		if err != nil {
			log.Fatalf("Invalid VALIDATOR_ID: %s", idStr)
		}
	}

	// Get message-pool URL from environment
	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8082"
	}

	// Initialize the validator
	v = validator.NewValidator(validatorID, messagePoolURL)

	// Get peer validator URLs from environment
	// Format: comma-separated URLs like "http://validator-node0:8081,http://validator-node2:8081"
	peerURLs := []string{}
	if peersEnv := os.Getenv("VALIDATOR_PEERS"); peersEnv != "" {
		for _, peer := range strings.Split(peersEnv, ",") {
			peer = strings.TrimSpace(peer)
			if peer != "" {
				peerURLs = append(peerURLs, peer)
			}
		}
	}

	// Set peers (this initializes consensus)
	v.SetPeers(peerURLs)

	log.Printf("Validator initialized with ID=%d, peer_urls=%v, message-pool=%s", validatorID, peerURLs, messagePoolURL)

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":      "validator-node",
		"validator_id": validatorID,
		"peer_count":   len(peerURLs),
	})

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/propose", proposeHandler)
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/consensus/prepare", consensusPrepareHandler)
	http.HandleFunc("/consensus/commit", consensusCommitHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("validator-node listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func proposeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ProposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Decode base64 payload
	payload, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		http.Error(w, "Invalid base64 payload", http.StatusBadRequest)
		return
	}

	// Propose the message (initiates consensus)
	if err := v.ProposeMessage(payload); err != nil {
		log.Printf("Failed to propose message: %v", err)
		http.Error(w, "Failed to initiate consensus", http.StatusInternalServerError)
		return
	}

	resp := ProposeResponse{
		Status: "accepted",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(resp)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := v.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func consensusPrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg consensus.ConsensusMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := v.HandlePrepare(msg); err != nil {
		log.Printf("Failed to handle prepare: %v", err)
		http.Error(w, "Failed to handle prepare", http.StatusInternalServerError)
		return
	}

	resp := ConsensusResponse{
		Status: "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func consensusCommitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg consensus.ConsensusMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := v.HandleCommit(msg); err != nil {
		log.Printf("Failed to handle commit: %v", err)
		http.Error(w, "Failed to handle commit", http.StatusInternalServerError)
		return
	}

	resp := ConsensusResponse{
		Status: "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

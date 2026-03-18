// Package main implements the relay-node service.
// Relay nodes handle onion layer peeling and mix-and-forward of messages.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/relay"
)

// ForwardRequest is the request body for POST /forward.
type ForwardRequest struct {
	Payload string `json:"payload"` // base64-encoded
}

// ForwardResponse is the response body for POST /forward.
type ForwardResponse struct {
	Status string `json:"status"`
}

var r *relay.Relay

func main() {
	log.Println("relay-node starting...")

	// Get relay ID from environment
	relayID := 0
	if idStr := os.Getenv("RELAY_ID"); idStr != "" {
		var err error
		relayID, err = strconv.Atoi(idStr)
		if err != nil {
			log.Fatalf("Invalid RELAY_ID: %s", idStr)
		}
	}

	// Get next hop from environment (empty means final relay)
	nextHop := os.Getenv("NEXT_HOP")

	// Get validator URL from environment (used by final relay)
	validatorURL := os.Getenv("VALIDATOR_URL")
	if validatorURL == "" {
		validatorURL = "http://validator-node0:8081"
	}

	// Initialize the relay
	r = relay.NewRelay(relayID, nextHop, validatorURL)

	log.Printf("Relay initialized with ID=%d, nextHop=%q, validatorURL=%s", relayID, nextHop, validatorURL)

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service":  "relay-node",
		"relay_id": relayID,
	})

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/forward", forwardHandler)
	http.HandleFunc("/status", statusHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("relay-node listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func forwardHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var fwdReq ForwardRequest
	if err := json.NewDecoder(req.Body).Decode(&fwdReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Forward the message (payload is already base64-encoded, pass through as-is)
	if err := r.ForwardMessage([]byte(fwdReq.Payload)); err != nil {
		log.Printf("Failed to forward message: %v", err)
		http.Error(w, "Failed to forward message", http.StatusInternalServerError)
		return
	}

	resp := ForwardResponse{
		Status: "forwarded",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(resp)
}

func statusHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := r.GetStatus()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

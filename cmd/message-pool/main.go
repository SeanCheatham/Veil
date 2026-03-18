// Package main implements the message-pool service.
// The message pool is an append-only ciphertext store that holds encrypted messages.
package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/messagepool"
)

// AppendRequest is the request body for POST /messages.
type AppendRequest struct {
	Payload string `json:"payload"` // base64-encoded
}

// AppendResponse is the response body for POST /messages.
type AppendResponse struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}

// MessageResponse is the JSON representation of a message for API responses.
type MessageResponse struct {
	ID        string `json:"id"`
	Payload   string `json:"payload"` // base64-encoded
	Timestamp int64  `json:"timestamp"`
	Sequence  uint64 `json:"sequence"`
}

var store *messagepool.Store

func main() {
	log.Println("message-pool starting...")

	// Initialize the store
	store = messagepool.NewStore()

	// Signal to Antithesis that setup is complete
	lifecycle.SetupComplete(map[string]any{
		"service": "message-pool",
	})

	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/messages", messagesHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	log.Printf("message-pool listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func messagesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handlePostMessage(w, r)
	case http.MethodGet:
		handleGetMessages(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handlePostMessage(w http.ResponseWriter, r *http.Request) {
	var req AppendRequest
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

	// Append to store
	msg, err := store.Append(payload)
	if err != nil {
		log.Printf("Failed to append message: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return response
	resp := AppendResponse{
		ID:       msg.ID,
		Sequence: msg.Sequence,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func handleGetMessages(w http.ResponseWriter, r *http.Request) {
	// Parse 'since' query parameter (default 0)
	sinceStr := r.URL.Query().Get("since")
	var since uint64 = 0
	if sinceStr != "" {
		var err error
		since, err = strconv.ParseUint(sinceStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid 'since' parameter", http.StatusBadRequest)
			return
		}
	}

	// Get messages since the given index
	messages := store.GetSince(since)

	// Convert to response format with base64-encoded payloads
	resp := make([]MessageResponse, len(messages))
	for i, msg := range messages {
		resp[i] = MessageResponse{
			ID:        msg.ID,
			Payload:   base64.StdEncoding.EncodeToString(msg.Payload),
			Timestamp: msg.Timestamp,
			Sequence:  msg.Sequence,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

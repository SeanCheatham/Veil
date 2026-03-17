// Package main implements the message-pool service.
// The message pool is an append-only ciphertext store.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/veil-protocol/veil/internal/pool"
)

var messagePool = pool.NewMessagePool()

// handleBatch handles POST /batch to submit a batch of messages.
func handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var batch []pool.Message
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	messagePool.Submit(batch)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "accepted",
		"received": len(batch),
	})
}

// handleMessages handles GET /messages to retrieve all messages.
func handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	messages := messagePool.GetAll()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// handleHealth handles GET /health for health checks.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/batch", handleBatch)
	http.HandleFunc("/messages", handleMessages)
	http.HandleFunc("/health", handleHealth)

	log.Printf("Message pool service starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

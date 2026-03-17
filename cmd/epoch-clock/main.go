// Package main implements the epoch-clock service.
// The epoch clock tracks epoch numbers and notifies subscribers on transitions.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/veil-protocol/veil/internal/epoch"
)

var epochClock = epoch.NewEpochClock()

// handleGetEpoch handles GET /epoch to return the current epoch number.
func handleGetEpoch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	current := epochClock.Current()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"epoch":     current,
		"timestamp": time.Now().UTC(),
	})
}

// handleSubscribe handles GET /subscribe for SSE (Server-Sent Events) stream.
func handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Check if flusher is available
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to epoch events
	eventCh := epochClock.Subscribe()
	defer epochClock.Unsubscribe(eventCh)

	// Send initial connection event with current epoch
	current := epochClock.Current()
	data, _ := json.Marshal(map[string]interface{}{
		"type":      "connected",
		"epoch":     current,
		"timestamp": time.Now().UTC(),
	})
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", data)
	flusher.Flush()

	// Stream epoch events
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				log.Printf("Error marshaling epoch event: %v", err)
				continue
			}
			fmt.Fprintf(w, "event: epoch\ndata: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			// Client disconnected
			log.Printf("Client disconnected from SSE stream")
			return
		}
	}
}

// handleHealth handles GET /health for health checks.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "healthy",
		"epoch":  epochClock.Current(),
	})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	// Epoch interval from environment, default to 10 seconds
	intervalStr := os.Getenv("EPOCH_INTERVAL")
	interval := 10 * time.Second
	if intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			interval = d
		} else {
			log.Printf("Invalid EPOCH_INTERVAL %q, using default 10s", intervalStr)
		}
	}

	// Start the epoch clock
	epochClock.Start(interval)
	log.Printf("Epoch clock started with interval %v", interval)

	http.HandleFunc("/epoch", handleGetEpoch)
	http.HandleFunc("/subscribe", handleSubscribe)
	http.HandleFunc("/health", handleHealth)

	log.Printf("Epoch clock service starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

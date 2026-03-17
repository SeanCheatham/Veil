// Package main implements the relay-node service.
// Relay nodes handle onion layer peeling and mix-and-forward operations.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/internal/relay"
)

var r *relay.Relay

// handleMessage handles POST /message to receive messages from senders or other relays.
// Request body: {"id": string, "blob": []byte (base64), "epoch": uint64}
func handleMessage(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg relay.MessageRequest
	if err := json.NewDecoder(req.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate request
	if msg.ID == "" {
		http.Error(w, "Missing message ID", http.StatusBadRequest)
		return
	}
	if len(msg.Blob) == 0 {
		http.Error(w, "Missing message blob", http.StatusBadRequest)
		return
	}

	// Process the message through the relay
	if err := r.OnMessage(msg.ID, msg.Blob, msg.Epoch); err != nil {
		log.Printf("[%s] Failed to process message %s: %v", r.RelayID(), msg.ID, err)
		http.Error(w, "Failed to process message: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(relay.MessageResponse{
		Status: "accepted",
		MsgID:  msg.ID,
	})
}

// handleStatus handles GET /status to return relay status information.
func handleStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := r.Status()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleHealth handles GET /health for health checks.
func handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	})
}

// handlePubkey handles GET /pubkey/:epoch to return the public key for an epoch.
// This is used by senders to encrypt messages for this relay.
// URL format: /pubkey/5 (for epoch 5)
func handlePubkey(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract epoch from URL path: /pubkey/5
	path := strings.TrimPrefix(req.URL.Path, "/pubkey/")
	if path == "" || path == req.URL.Path {
		http.Error(w, "Missing epoch in URL path", http.StatusBadRequest)
		return
	}

	// Parse epoch number
	var epoch uint64
	_, err := json.Number(path).Int64()
	if err != nil {
		// Try parsing as integer directly
		var e int
		if _, scanErr := json.Number(path).Int64(); scanErr != nil {
			http.Error(w, "Invalid epoch number", http.StatusBadRequest)
			return
		}
		epoch = uint64(e)
	} else {
		// Parse the epoch
		n, _ := json.Number(path).Int64()
		epoch = uint64(n)
	}

	pubKey := r.PublicKey(epoch)
	if pubKey == nil {
		http.Error(w, "No public key available for epoch", http.StatusNotFound)
		return
	}

	// Return public key as base64-encoded bytes
	pubKeyBytes := pubKey.Bytes()
	pubKeyBase64 := base64.StdEncoding.EncodeToString(pubKeyBytes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"relay_id":   r.RelayID(),
		"epoch":      epoch,
		"public_key": pubKeyBase64,
	})
}

func main() {
	// Configuration from environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "relay-1"
	}

	epochClockURL := os.Getenv("EPOCH_CLOCK_URL")
	if epochClockURL == "" {
		epochClockURL = "http://epoch-clock:8083"
	}

	relayPeers := os.Getenv("RELAY_PEERS")
	if relayPeers == "" {
		// Default peers for local development
		relayPeers = "http://relay-node-1:8081,http://relay-node-2:8081,http://relay-node-3:8081,http://relay-node-4:8081,http://relay-node-5:8081"
	}

	validatorEndpoints := os.Getenv("VALIDATOR_ENDPOINTS")
	if validatorEndpoints == "" {
		validatorEndpoints = "http://validator-node-1:8082,http://validator-node-2:8082,http://validator-node-3:8082"
	}

	// Create relay instance
	r = relay.NewRelay(nodeID, epochClockURL, relayPeers, validatorEndpoints)

	log.Printf("Relay %s starting on port %s", nodeID, port)
	log.Printf("Epoch clock: %s", epochClockURL)
	log.Printf("Relay peers: %s", relayPeers)
	log.Printf("Validator endpoints: %s", validatorEndpoints)

	// Set up HTTP handlers
	http.HandleFunc("/message", handleMessage)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/pubkey/", handlePubkey)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the relay's session key manager
	go func() {
		// Retry connection to epoch-clock with backoff
		maxRetries := 10
		for i := 0; i < maxRetries; i++ {
			if err := r.Start(ctx); err != nil {
				log.Printf("Failed to start relay (attempt %d/%d): %v", i+1, maxRetries, err)
				if i < maxRetries-1 {
					time.Sleep(time.Duration(i+1) * time.Second)
					continue
				}
				log.Printf("Warning: Running without epoch clock connection")
			}
			break
		}
	}()

	// Set up graceful shutdown
	server := &http.Server{Addr: ":" + port}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("Shutting down...")
		cancel()
		r.Stop()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Printf("Relay node service starting on port %s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Println("Relay node stopped")
}

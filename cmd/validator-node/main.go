// Package main implements the validator-node service.
// Validator nodes handle BFT consensus and message pool ordering.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/veil-protocol/veil/internal/validator"
)

var v *validator.Validator

// handlePropose handles POST /propose to receive messages from relays.
func handlePropose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg validator.ProposalMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := v.Propose(msg); err != nil {
		http.Error(w, "Failed to propose: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "accepted",
		"msg_id": msg.ID,
	})
}

// handlePrepare handles POST /prepare for BFT consensus (internal).
func handlePrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req validator.PrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := v.HandlePrepare(&req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCommit handles POST /commit for BFT consensus (internal).
func handleCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req validator.CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := v.HandleCommit(&req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStatus handles GET /status to return current batch number and peers.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := v.Status()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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

// batchTriggerLoop periodically triggers batch consensus for the leader.
func batchTriggerLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Batch trigger loop stopped")
			return
		case <-ticker.C:
			if v.IsLeader() && v.PendingCount() > 0 {
				if err := v.TriggerBatch(ctx); err != nil {
					log.Printf("Failed to trigger batch: %v", err)
				}
			}
		}
	}
}

func main() {
	// Configuration from environment variables
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "validator-1"
	}

	validatorPeers := os.Getenv("VALIDATOR_PEERS")
	if validatorPeers == "" {
		// Default peers for local development
		validatorPeers = "http://validator-node-1:8082,http://validator-node-2:8082,http://validator-node-3:8082"
	}

	messagePoolURL := os.Getenv("MESSAGE_POOL_URL")
	if messagePoolURL == "" {
		messagePoolURL = "http://message-pool:8080"
	}

	batchIntervalStr := os.Getenv("BATCH_INTERVAL")
	batchInterval := 2 * time.Second
	if batchIntervalStr != "" {
		if d, err := time.ParseDuration(batchIntervalStr); err == nil {
			batchInterval = d
		}
	}

	// Create validator instance
	v = validator.NewValidator(nodeID, validatorPeers, messagePoolURL)

	log.Printf("Validator %s starting on port %s", nodeID, port)
	log.Printf("Peers: %s", validatorPeers)
	log.Printf("Message pool: %s", messagePoolURL)
	log.Printf("Is leader: %v", v.IsLeader())

	// Set up HTTP handlers
	http.HandleFunc("/propose", handlePropose)
	http.HandleFunc("/prepare", handlePrepare)
	http.HandleFunc("/commit", handleCommit)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/health", handleHealth)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start batch trigger loop in background (only matters for leader)
	go batchTriggerLoop(ctx, batchInterval)

	// Set up graceful shutdown
	server := &http.Server{Addr: ":" + port}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		log.Println("Shutting down...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Printf("Validator node service starting on port %s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Println("Validator node stopped")
}

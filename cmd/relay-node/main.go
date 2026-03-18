// Package main implements the relay-node service.
// Relay nodes handle onion layer peeling and mix-and-forward of messages.
package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil/veil/internal/crypto"
	"github.com/veil/veil/internal/epoch"
	"github.com/veil/veil/internal/relay"
)

// ForwardRequest is the request body for POST /forward.
type ForwardRequest struct {
	Payload string `json:"payload"` // onion-encrypted payload (binary as string)
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

	// Check if epoch-based key management is enabled
	masterSeedB64 := os.Getenv("RELAY_MASTER_SEED")
	epochDurationStr := os.Getenv("EPOCH_DURATION_SECONDS")

	if masterSeedB64 != "" {
		// Epoch-based key management enabled
		masterSeed, err := base64.StdEncoding.DecodeString(masterSeedB64)
		if err != nil {
			log.Fatalf("Invalid RELAY_MASTER_SEED (must be base64): %v", err)
		}
		if len(masterSeed) != 32 {
			log.Fatalf("RELAY_MASTER_SEED must be 32 bytes (got %d)", len(masterSeed))
		}

		// Parse epoch duration
		epochDuration := int64(epoch.DefaultDurationSeconds)
		if epochDurationStr != "" {
			epochDuration, err = strconv.ParseInt(epochDurationStr, 10, 64)
			if err != nil {
				log.Printf("Invalid EPOCH_DURATION_SECONDS %q, using default %d", epochDurationStr, epoch.DefaultDurationSeconds)
				epochDuration = int64(epoch.DefaultDurationSeconds)
			}
		}

		// Create epoch manager
		em := epoch.NewEpochManager(epoch.EpochConfig{
			DurationSeconds:    epochDuration,
			GracePeriodSeconds: epoch.DefaultGracePeriodSeconds,
		})

		// Configure relay for epoch-based keys
		r.SetEpochManager(em, masterSeed)

		// Start key rotation
		if err := r.StartKeyRotation(); err != nil {
			log.Fatalf("Failed to start key rotation: %v", err)
		}

		log.Printf("Relay initialized with epoch-based keys, epoch duration: %ds", epochDuration)
	} else {
		// Legacy mode: use static keys
		privKeyB64 := os.Getenv("RELAY_PRIVATE_KEY")
		if privKeyB64 == "" {
			// Fall back to static keys for development/testing
			privKeyB64 = crypto.GetRelayPrivateKeyByID(relayID)
			log.Printf("No RELAY_PRIVATE_KEY set, using static key for relay %d", relayID)
		}

		keyPair, err := crypto.LoadOrGenerateKey(privKeyB64)
		if err != nil {
			log.Fatalf("Failed to load/generate keys: %v", err)
		}

		r.SetKeys(keyPair.Private, keyPair.Public)
		log.Printf("Relay public key (legacy): %s", keyPair.Public.Base64())
	}

	log.Printf("Relay initialized with ID=%d, nextHop=%q, validatorURL=%s", relayID, nextHop, validatorURL)

	// Signal to Antithesis that setup is complete
	status := r.GetStatus()
	lifecycle.SetupComplete(map[string]any{
		"service":       "relay-node",
		"relay_id":      relayID,
		"public_key":    status.PublicKey,
		"current_epoch": status.CurrentEpoch,
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

	// Forward the message (payload is the onion-encrypted data)
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

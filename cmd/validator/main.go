package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
)

var (
	validatorID string
	leaderHost  string
)

func isLeader() bool {
	return fmt.Sprintf("validator-%s", validatorID) == leaderHost
}

func main() {
	validatorID = os.Getenv("VALIDATOR_ID")
	if validatorID == "" {
		validatorID = "1"
	}
	leaderHost = os.Getenv("LEADER_HOST")
	if leaderHost == "" {
		leaderHost = "validator-1"
	}

	assert.Always(true, "validator_started", map[string]any{
		"validator_id": validatorID,
	})

	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/submit", handleSubmit)
	http.HandleFunc("/pool", handlePool)

	lifecycle.SetupComplete(map[string]any{
		"service": "validator",
		"id":      validatorID,
	})

	fmt.Printf("validator-%s listening on :8082 (leader=%s)\n", validatorID, leaderHost)
	log.Fatal(http.ListenAndServe(":8082", nil))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read body"})
		return
	}
	defer r.Body.Close()

	// Parse to validate the ciphertext field
	var req struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Ciphertext == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request: ciphertext required"})
		return
	}

	if !isLeader() {
		// Forward to leader
		leaderURL := fmt.Sprintf("http://%s:8082/submit", leaderHost)
		resp, err := http.Post(leaderURL, "application/json", bytes.NewReader(body))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to forward to leader"})
			return
		}
		defer resp.Body.Close()

		// Proxy leader's response back
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Leader: forward to message-pool
	poolURL := "http://message-pool:8081/messages"
	resp, err := http.Post(poolURL, "application/json", bytes.NewReader(body))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to forward to message-pool"})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Assertion: on the leader, after message-pool confirms the write
	ctPrefix := req.Ciphertext
	if len(ctPrefix) > 20 {
		ctPrefix = ctPrefix[:20]
	}
	assert.Always(resp.StatusCode == http.StatusCreated, "submitted_message_reaches_pool", map[string]any{
		"validator_id":      validatorID,
		"ciphertext_prefix": ctPrefix,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func handlePool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Proxy to message-pool's GET /messages
	resp, err := http.Get("http://message-pool:8081/messages")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to reach message-pool"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

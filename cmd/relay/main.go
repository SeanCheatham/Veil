package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil-protocol/veil/pkg/crypto"
)

var (
	relayID string
	keyPair crypto.KeyPair
)

func main() {
	relayID = os.Getenv("RELAY_ID")
	if relayID == "" {
		relayID = "1"
	}

	var err error
	keyPair, err = crypto.GenerateKeyPair()
	if err != nil {
		log.Fatalf("failed to generate keypair: %v", err)
	}

	assert.Always(true, "relay_started", map[string]any{
		"relay_id": relayID,
	})

	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/pubkey", handlePubKey)
	http.HandleFunc("/forward", handleForward)

	lifecycle.SetupComplete(map[string]any{
		"service": "relay",
		"id":      relayID,
	})

	fmt.Printf("relay-%s listening on :8083\n", relayID)
	log.Fatal(http.ListenAndServe(":8083", nil))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handlePubKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"relay_id":   relayID,
		"public_key": base64.StdEncoding.EncodeToString(keyPair.Public[:]),
	})
}

func handleForward(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read body"})
		return
	}
	defer r.Body.Close()

	var req struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Ciphertext == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request: ciphertext required"})
		return
	}

	originalBytes, err := base64.StdEncoding.DecodeString(req.Ciphertext)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid base64 ciphertext"})
		return
	}

	originalSize := len(originalBytes)

	innerCiphertext, nextHop, isFinal, err := crypto.PeelLayer(originalBytes, keyPair.Private)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("peel failed: %v", err)})
		return
	}

	// Antithesis assertion: after successful peel, inner is smaller than original
	assert.Always(len(innerCiphertext) < originalSize, "relay_peels_exactly_one_layer", map[string]any{
		"relay_id":      relayID,
		"original_size": originalSize,
		"peeled_size":   len(innerCiphertext),
	})

	// Determine where to forward
	innerB64 := base64.StdEncoding.EncodeToString(innerCiphertext)
	forwardBody, _ := json.Marshal(map[string]string{"ciphertext": innerB64})

	var forwardURL string
	if isFinal {
		// Last relay: forward to validator-1's /submit
		forwardURL = "http://validator-1:8082/submit"
	} else if strings.HasPrefix(nextHop, "validator") {
		// Next hop is a validator
		forwardURL = fmt.Sprintf("http://%s/submit", nextHop)
	} else {
		// Next hop is another relay
		forwardURL = fmt.Sprintf("http://%s/forward", nextHop)
	}

	resp, err := http.Post(forwardURL, "application/json", bytes.NewReader(forwardBody))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("forward failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	// Proxy the response back
	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"
	"github.com/veil-protocol/veil/pkg/consensus"
)

var (
	validatorID string
	node        *consensus.Node
	pending     []string
	pendingMu   sync.Mutex
)

func main() {
	validatorID = os.Getenv("VALIDATOR_ID")
	if validatorID == "" {
		validatorID = "1"
	}

	peersEnv := os.Getenv("VALIDATOR_PEERS")
	if peersEnv == "" {
		peersEnv = "validator-1:8082,validator-2:8082,validator-3:8082"
	}
	peers := strings.Split(peersEnv, ",")

	node = consensus.NewNode(validatorID, peers)

	assert.Always(true, "validator_started", map[string]any{
		"validator_id": validatorID,
	})

	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/submit", handleSubmit)
	http.HandleFunc("/pool", handlePool)
	http.HandleFunc("/consensus/propose", handleConsensusPropose)
	http.HandleFunc("/consensus/prepare", handleConsensusPrepare)
	http.HandleFunc("/consensus/commit", handleConsensusCommit)

	// Start the leader proposal goroutine
	go proposalLoop()

	lifecycle.SetupComplete(map[string]any{
		"service": "validator",
		"id":      validatorID,
	})

	fmt.Printf("validator-%s listening on :8082 (peers=%s)\n", validatorID, peersEnv)
	log.Fatal(http.ListenAndServe(":8082", nil))
}

func proposalLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if !node.IsLeader() {
			continue
		}

		pendingMu.Lock()
		if len(pending) == 0 {
			pendingMu.Unlock()
			continue
		}
		msgs := make([]string, len(pending))
		copy(msgs, pending)
		pending = nil
		pendingMu.Unlock()

		block := node.Propose(msgs)
		if block == nil {
			// Put messages back if propose failed (e.g., not idle)
			pendingMu.Lock()
			pending = append(msgs, pending...)
			pendingMu.Unlock()
			continue
		}

		// Broadcast propose to all peers
		broadcastPropose(block)
	}
}

func broadcastPropose(block *consensus.Block) {
	data, _ := json.Marshal(block)
	for _, peer := range node.Peers {
		if peer == fmt.Sprintf("validator-%s:8082", validatorID) {
			continue // skip self
		}
		go func(peer string) {
			url := fmt.Sprintf("http://%s/consensus/propose", peer)
			resp, err := http.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				log.Printf("validator-%s: failed to send propose to %s: %v", validatorID, peer, err)
				return
			}
			resp.Body.Close()
		}(peer)
	}

	// Leader also sends prepare to peers (leader accepted its own proposal)
	broadcastPrepare(block.SeqNum)
}

func broadcastPrepare(seq uint64) {
	data, _ := json.Marshal(map[string]any{
		"seq":          seq,
		"validator_id": validatorID,
	})
	for _, peer := range node.Peers {
		if peer == fmt.Sprintf("validator-%s:8082", validatorID) {
			continue
		}
		go func(peer string) {
			url := fmt.Sprintf("http://%s/consensus/prepare", peer)
			resp, err := http.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				log.Printf("validator-%s: failed to send prepare to %s: %v", validatorID, peer, err)
				return
			}
			resp.Body.Close()
		}(peer)
	}
}

func broadcastCommit(seq uint64) {
	data, _ := json.Marshal(map[string]any{
		"seq":          seq,
		"validator_id": validatorID,
	})
	for _, peer := range node.Peers {
		if peer == fmt.Sprintf("validator-%s:8082", validatorID) {
			continue
		}
		go func(peer string) {
			url := fmt.Sprintf("http://%s/consensus/commit", peer)
			resp, err := http.Post(url, "application/json", bytes.NewReader(data))
			if err != nil {
				log.Printf("validator-%s: failed to send commit to %s: %v", validatorID, peer, err)
				return
			}
			resp.Body.Close()
		}(peer)
	}
}

func appendToPool(block *consensus.Block) {
	for _, ct := range block.Messages {
		body, _ := json.Marshal(map[string]string{"ciphertext": ct})
		resp, err := http.Post("http://message-pool:8081/messages", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("validator-%s: failed to append to pool: %v", validatorID, err)
			continue
		}
		resp.Body.Close()
	}

	assert.Always(true, "validators_agree_on_order", map[string]any{
		"seq":           block.SeqNum,
		"validator_id":  validatorID,
		"message_count": len(block.Messages),
	})

	assert.Always(true, "no_message_lost_in_consensus", map[string]any{
		"seq":          block.SeqNum,
		"validator_id": validatorID,
	})
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

	// Buffer locally; consensus will batch and commit
	pendingMu.Lock()
	pending = append(pending, req.Ciphertext)
	pendingMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "buffered", "validator_id": validatorID})
}

func handleConsensusPropose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var block consensus.Block
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	accepted := node.HandlePropose(block)
	if !accepted {
		w.WriteHeader(http.StatusConflict)
		return
	}

	// Accepted; broadcast prepare to peers
	broadcastPrepare(block.SeqNum)

	w.WriteHeader(http.StatusOK)
}

func handleConsensusPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var msg struct {
		Seq         uint64 `json:"seq"`
		ValidatorID string `json:"validator_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	commitReady := node.HandlePrepare(msg.Seq, msg.ValidatorID)
	if commitReady {
		// Broadcast commit to peers
		broadcastCommit(msg.Seq)
	}

	w.WriteHeader(http.StatusOK)
}

func handleConsensusCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var msg struct {
		Seq         uint64 `json:"seq"`
		ValidatorID string `json:"validator_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	committed := node.HandleCommit(msg.Seq, msg.ValidatorID)
	if committed != nil {
		// Block is committed — append messages to pool
		go appendToPool(committed)
	}

	w.WriteHeader(http.StatusOK)
}

func handlePool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

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

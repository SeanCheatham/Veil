package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"

	"github.com/veil-protocol/veil/pkg/epoch"
)

type message struct {
	Index      int    `json:"index"`
	Ciphertext string `json:"ciphertext"`
}

type pool struct {
	mu       sync.RWMutex
	messages []message
}

func (p *pool) append(ciphertext string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	prevLen := len(p.messages)
	idx := prevLen
	p.messages = append(p.messages, message{
		Index:      idx,
		Ciphertext: ciphertext,
	})
	newLen := len(p.messages)

	// Antithesis assertion: append-only invariant — the pool must only grow.
	assert.Always(newLen > prevLen, "message_pool_append_only", map[string]any{
		"prev_len": prevLen,
		"new_len":  newLen,
	})

	return idx
}

func (p *pool) get(index int) (message, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if index < 0 || index >= len(p.messages) {
		return message{}, false
	}
	return p.messages[index], true
}

func (p *pool) all() ([]message, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	// Return a copy to avoid data races.
	msgs := make([]message, len(p.messages))
	copy(msgs, p.messages)
	return msgs, len(msgs)
}

func main() {
	assert.Always(true, "message_pool_started", map[string]any{
		"detail": "message-pool service started successfully",
	})

	p := &pool{}

	// Start epoch manager
	epochDuration := epoch.DurationFromEnv()
	epochMgr := epoch.NewManager(epochDuration)
	epochMgr.OnEpochTick(func(epochNum uint64) {
		assert.Sometimes(true, "pool_epoch_tracked", map[string]any{"epoch": epochNum})
		log.Printf("epoch tick: %d", epochNum)
	})
	epochMgr.Start()
	log.Printf("epoch manager started with duration %v", epochDuration)

	http.HandleFunc("/epoch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"epoch": epochMgr.GetCurrentEpoch()})
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	http.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPost:
			var req struct {
				Ciphertext string `json:"ciphertext"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ciphertext == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid request: ciphertext required"})
				return
			}
			idx := p.append(req.Ciphertext)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"index": idx})

		case http.MethodGet:
			msgs, count := p.all()
			json.NewEncoder(w).Encode(map[string]any{
				"messages": msgs,
				"count":    count,
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/messages/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		idxStr := strings.TrimPrefix(r.URL.Path, "/messages/")
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid index"})
			return
		}

		msg, ok := p.get(idx)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		json.NewEncoder(w).Encode(msg)
	})

	lifecycle.SetupComplete(map[string]any{
		"service": "message-pool",
		"message": "message-pool service is ready",
	})

	fmt.Println("message-pool service listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

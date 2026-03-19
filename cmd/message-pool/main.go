package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/antithesishq/antithesis-sdk-go/lifecycle"

	"github.com/veil-protocol/veil/pkg/epoch"
)

// poolMessage is the internal storage type with epoch tracking and expiry.
type poolMessage struct {
	Ciphertext string
	AddedEpoch uint64
	Expired    bool
}

// message is the JSON response type for individual messages.
type message struct {
	Index      int    `json:"index"`
	Ciphertext string `json:"ciphertext"`
	Status     string `json:"status"`
}

type pool struct {
	mu       sync.RWMutex
	messages []poolMessage
}

func (p *pool) append(ciphertext string, currentEpoch uint64) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	prevLen := len(p.messages)
	idx := prevLen
	p.messages = append(p.messages, poolMessage{
		Ciphertext: ciphertext,
		AddedEpoch: currentEpoch,
		Expired:    false,
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
	pm := p.messages[index]
	status := "active"
	if pm.Expired {
		status = "expired"
	}
	return message{
		Index:      index,
		Ciphertext: pm.Ciphertext,
		Status:     status,
	}, true
}

// allActive returns only non-expired messages and the count of active messages.
func (p *pool) allActive() ([]message, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var msgs []message
	for i, pm := range p.messages {
		if !pm.Expired {
			msgs = append(msgs, message{
				Index:      i,
				Ciphertext: pm.Ciphertext,
				Status:     "active",
			})
		}
	}
	if msgs == nil {
		msgs = []message{}
	}
	return msgs, len(msgs)
}

// flushStalled marks messages older than maxAge epochs as expired.
// Returns the number of messages flushed in this call.
func (p *pool) flushStalled(currentEpoch uint64, maxAge uint64) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	flushed := 0
	for i := range p.messages {
		if !p.messages[i].Expired && currentEpoch-p.messages[i].AddedEpoch > maxAge {
			p.messages[i].Expired = true
			p.messages[i].Ciphertext = ""
			flushed++
		}
	}
	return flushed
}

// stats returns total, active, expired counts, and the anonymity set size
// (active messages added in the given epoch).
func (p *pool) stats(currentEpoch uint64) (total, active, expired, anonymitySet int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total = len(p.messages)
	for _, pm := range p.messages {
		if pm.Expired {
			expired++
		} else {
			active++
			if pm.AddedEpoch == currentEpoch {
				anonymitySet++
			}
		}
	}
	return
}

// countActiveInEpoch counts active messages added in a specific epoch.
func (p *pool) countActiveInEpoch(epoch uint64) int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	count := 0
	for _, pm := range p.messages {
		if !pm.Expired && pm.AddedEpoch == epoch {
			count++
		}
	}
	return count
}

func main() {
	assert.Always(true, "message_pool_started", map[string]any{
		"detail": "message-pool service started successfully",
	})

	// Parse configuration
	maxMessageAgeEpochs := uint64(3)
	if v := os.Getenv("MAX_MESSAGE_AGE_EPOCHS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxMessageAgeEpochs = uint64(n)
		}
	}

	minAnonymitySet := 5
	if v := os.Getenv("MIN_ANONYMITY_SET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minAnonymitySet = n
		}
	}

	log.Printf("config: MAX_MESSAGE_AGE_EPOCHS=%d, MIN_ANONYMITY_SET=%d", maxMessageAgeEpochs, minAnonymitySet)

	p := &pool{}

	// Start epoch manager
	epochDuration := epoch.DurationFromEnv()
	epochMgr := epoch.NewManager(epochDuration)
	epochMgr.OnEpochTick(func(epochNum uint64) {
		assert.Sometimes(true, "pool_epoch_tracked", map[string]any{"epoch": epochNum})
		log.Printf("epoch tick: %d", epochNum)

		// Flush stalled messages
		flushed := p.flushStalled(epochNum, maxMessageAgeEpochs)
		if flushed > 0 {
			assert.Sometimes(true, "stalled_messages_flushed", map[string]any{
				"epoch":        epochNum,
				"flushed_count": flushed,
			})
			log.Printf("flushed %d stalled messages at epoch %d", flushed, epochNum)
		}

		// Anonymity set checking — count active messages added in the current epoch
		anonCount := p.countActiveInEpoch(epochNum)
		if anonCount < minAnonymitySet {
			assert.Sometimes(true, "anonymity_set_warning", map[string]any{
				"epoch":   epochNum,
				"count":   anonCount,
				"minimum": minAnonymitySet,
			})
			log.Printf("anonymity set warning at epoch %d: count=%d < minimum=%d", epochNum, anonCount, minAnonymitySet)
		} else {
			assert.Sometimes(true, "anonymity_set_sufficient", map[string]any{
				"epoch": epochNum,
				"count": anonCount,
			})
			log.Printf("anonymity set sufficient at epoch %d: count=%d", epochNum, anonCount)
		}
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

	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		currentEpoch := epochMgr.GetCurrentEpoch()
		total, active, expired, anonymitySet := p.stats(currentEpoch)
		json.NewEncoder(w).Encode(map[string]any{
			"total":         total,
			"active":        active,
			"expired":       expired,
			"epoch":         currentEpoch,
			"anonymity_set": anonymitySet,
		})
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
			currentEpoch := epochMgr.GetCurrentEpoch()
			idx := p.append(req.Ciphertext, currentEpoch)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"index": idx})

		case http.MethodGet:
			msgs, count := p.allActive()
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

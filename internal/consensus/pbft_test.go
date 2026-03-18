package consensus

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestSingleValidatorSelfCommit tests that a single validator can propose and self-commit.
func TestSingleValidatorSelfCommit(t *testing.T) {
	// Create a mock message pool
	poolMessages := make([]map[string]any, 0)
	var poolMu sync.Mutex

	messagePool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/messages" {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			poolMu.Lock()
			poolMessages = append(poolMessages, req)
			poolMu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created"})
		}
	}))
	defer messagePool.Close()

	// Create a single validator with no peers (degenerate case)
	consensus := NewPBFTConsensus(0, []string{}, messagePool.URL)

	// Propose a message
	payload := []byte("test message")
	err := consensus.Propose(payload)
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}

	// Wait for consensus to complete
	time.Sleep(100 * time.Millisecond)

	// Verify message reached the pool
	poolMu.Lock()
	defer poolMu.Unlock()
	if len(poolMessages) != 1 {
		t.Fatalf("Expected 1 message in pool, got %d", len(poolMessages))
	}
}

// TestThreeValidatorsReachConsensus tests that 3 validators coordinate and agree on ordering.
func TestThreeValidatorsReachConsensus(t *testing.T) {
	// Create a mock message pool
	poolMessages := make([]map[string]any, 0)
	var poolMu sync.Mutex
	seenSeqs := make(map[uint64]bool)

	messagePool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/messages" {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			poolMu.Lock()
			// Deduplicate by consensus sequence
			if seq, ok := req["consensus_sequence"].(float64); ok {
				if seenSeqs[uint64(seq)] {
					poolMu.Unlock()
					w.WriteHeader(http.StatusOK) // Duplicate, already processed
					json.NewEncoder(w).Encode(map[string]string{"status": "duplicate"})
					return
				}
				seenSeqs[uint64(seq)] = true
			}
			poolMessages = append(poolMessages, req)
			poolMu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created"})
		}
	}))
	defer messagePool.Close()

	// Create mock servers for each validator to receive consensus messages
	var validators [3]*PBFTConsensus
	var servers [3]*httptest.Server

	for i := 0; i < 3; i++ {
		idx := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var msg ConsensusMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			if r.URL.Path == "/consensus/prepare" {
				validators[idx].HandlePrepare(msg)
				w.WriteHeader(http.StatusOK)
			} else if r.URL.Path == "/consensus/commit" {
				validators[idx].HandleCommit(msg)
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer servers[i].Close()
	}

	// Create validators with peer URLs
	for i := 0; i < 3; i++ {
		peers := make([]string, 0, 2)
		for j := 0; j < 3; j++ {
			if i != j {
				peers = append(peers, servers[j].URL)
			}
		}
		validators[i] = NewPBFTConsensus(i, peers, messagePool.URL)
	}

	// Propose a message from validator 0
	payload := []byte("consensus test message")
	err := validators[0].Propose(payload)
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}

	// Wait for consensus to complete
	time.Sleep(500 * time.Millisecond)

	// Verify message reached the pool (exactly once due to deduplication)
	poolMu.Lock()
	defer poolMu.Unlock()
	if len(poolMessages) != 1 {
		t.Fatalf("Expected 1 message in pool (deduplicated), got %d", len(poolMessages))
	}
}

// TestSequentialProposalsFromSingleValidator tests that sequential proposals
// from a single validator get sequenced correctly.
func TestSequentialProposalsFromSingleValidator(t *testing.T) {
	// Create a mock message pool that tracks sequences
	poolMessages := make([]map[string]any, 0)
	var poolMu sync.Mutex
	seenSeqs := make(map[uint64]bool)

	messagePool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/messages" {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			poolMu.Lock()
			if seq, ok := req["consensus_sequence"].(float64); ok {
				if seenSeqs[uint64(seq)] {
					poolMu.Unlock()
					w.WriteHeader(http.StatusOK)
					return
				}
				seenSeqs[uint64(seq)] = true
			}
			poolMessages = append(poolMessages, req)
			poolMu.Unlock()
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer messagePool.Close()

	var validators [3]*PBFTConsensus
	var servers [3]*httptest.Server

	for i := 0; i < 3; i++ {
		idx := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var msg ConsensusMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}

			if r.URL.Path == "/consensus/prepare" {
				validators[idx].HandlePrepare(msg)
				w.WriteHeader(http.StatusOK)
			} else if r.URL.Path == "/consensus/commit" {
				validators[idx].HandleCommit(msg)
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer servers[i].Close()
	}

	for i := 0; i < 3; i++ {
		peers := make([]string, 0, 2)
		for j := 0; j < 3; j++ {
			if i != j {
				peers = append(peers, servers[j].URL)
			}
		}
		validators[i] = NewPBFTConsensus(i, peers, messagePool.URL)
	}

	// Send multiple sequential proposals from validator 0 (leader)
	numProposals := 3
	for i := 0; i < numProposals; i++ {
		payload := []byte("message " + string(rune('A'+i)))
		err := validators[0].Propose(payload)
		if err != nil {
			t.Fatalf("Proposal %d failed: %v", i, err)
		}
		// Wait for consensus to complete before sending next
		time.Sleep(200 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify all messages reached the pool
	poolMu.Lock()
	defer poolMu.Unlock()
	if len(poolMessages) != numProposals {
		t.Fatalf("Expected %d messages in pool, got %d", numProposals, len(poolMessages))
	}

	// Verify sequences are unique and sequential
	sequences := make([]uint64, 0, len(poolMessages))
	for _, msg := range poolMessages {
		if seq, ok := msg["consensus_sequence"].(float64); ok {
			sequences = append(sequences, uint64(seq))
		}
	}

	seqMap := make(map[uint64]bool)
	for _, seq := range sequences {
		if seqMap[seq] {
			t.Fatalf("Duplicate sequence %d found", seq)
		}
		seqMap[seq] = true
	}

	// Verify sequences are 0, 1, 2
	for i := 0; i < numProposals; i++ {
		if !seqMap[uint64(i)] {
			t.Fatalf("Expected sequence %d not found", i)
		}
	}
}

// TestSignatureVerification tests the signature computation and verification.
func TestSignatureVerification(t *testing.T) {
	msg := ConsensusMessage{
		Type:        "prepare",
		Sequence:    42,
		Payload:     []byte("test payload"),
		ValidatorID: 1,
	}
	msg.Signature = ComputeSignature(msg.Type, msg.Sequence, msg.Payload, msg.ValidatorID)

	if !VerifySignature(msg) {
		t.Fatal("Signature verification failed for valid message")
	}

	// Tamper with the message
	msg.Payload = []byte("tampered payload")
	if VerifySignature(msg) {
		t.Fatal("Signature verification should fail for tampered message")
	}
}

// TestConsensusStateQuorum tests quorum calculations.
func TestConsensusStateQuorum(t *testing.T) {
	state := NewConsensusState(0, []byte("test"))

	// Add prepare votes
	state.PrepareVotes[0] = true
	state.PrepareVotes[1] = true
	state.PrepareVotes[2] = true

	if state.PrepareCount() != 3 {
		t.Fatalf("Expected 3 prepare votes, got %d", state.PrepareCount())
	}

	// For n=3, quorum is 3 (all validators must agree)
	if Quorum(3) != 3 {
		t.Fatalf("Expected quorum of 3, got %d", Quorum(3))
	}
}

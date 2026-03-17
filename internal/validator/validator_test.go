// Package validator tests
package validator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/veil-protocol/veil/internal/cover"
)

func TestParsePeers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single peer",
			input:    "http://localhost:8082",
			expected: []string{"http://localhost:8082"},
		},
		{
			name:     "multiple peers",
			input:    "http://validator-1:8082,http://validator-2:8082,http://validator-3:8082",
			expected: []string{"http://validator-1:8082", "http://validator-2:8082", "http://validator-3:8082"},
		},
		{
			name:     "peers with whitespace",
			input:    "http://validator-1:8082, http://validator-2:8082 , http://validator-3:8082",
			expected: []string{"http://validator-1:8082", "http://validator-2:8082", "http://validator-3:8082"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePeers(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d peers, got %d", len(tt.expected), len(result))
				return
			}
			for i, p := range result {
				if p != tt.expected[i] {
					t.Errorf("peer %d: expected %s, got %s", i, tt.expected[i], p)
				}
			}
		})
	}
}

func TestNewValidator(t *testing.T) {
	v := NewValidator("validator-1", "http://peer1:8082,http://peer2:8082", "http://pool:8080")

	if v == nil {
		t.Fatal("NewValidator returned nil")
	}
	if v.nodeID != "validator-1" {
		t.Errorf("nodeID mismatch: expected validator-1, got %s", v.nodeID)
	}
	if len(v.peers) != 2 {
		t.Errorf("expected 2 peers, got %d", len(v.peers))
	}
	if v.messagePoolURL != "http://pool:8080" {
		t.Errorf("messagePoolURL mismatch: expected http://pool:8080, got %s", v.messagePoolURL)
	}
	if v.currentBatch != 0 {
		t.Errorf("currentBatch should be 0, got %d", v.currentBatch)
	}
}

func TestIsLeader(t *testing.T) {
	v1 := NewValidator("validator-1", "", "")
	v2 := NewValidator("validator-2", "", "")
	v3 := NewValidator("validator-3", "", "")

	if !v1.IsLeader() {
		t.Error("validator-1 should be leader")
	}
	if v2.IsLeader() {
		t.Error("validator-2 should not be leader")
	}
	if v3.IsLeader() {
		t.Error("validator-3 should not be leader")
	}
}

func TestPropose(t *testing.T) {
	v := NewValidator("validator-1", "", "")

	msg := ProposalMessage{
		ID:         "msg-1",
		Ciphertext: []byte("test data"),
		Hash:       "abc123",
	}

	if err := v.Propose(msg); err != nil {
		t.Errorf("Propose failed: %v", err)
	}

	if v.PendingCount() != 1 {
		t.Errorf("expected 1 pending message, got %d", v.PendingCount())
	}
}

func TestProposeConcurrent(t *testing.T) {
	v := NewValidator("validator-1", "", "")
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := ProposalMessage{
				ID:         "msg",
				Ciphertext: []byte("data"),
				Hash:       "hash",
			}
			v.Propose(msg)
		}(i)
	}

	wg.Wait()

	if v.PendingCount() != 10 {
		t.Errorf("expected 10 pending messages, got %d", v.PendingCount())
	}
}

func TestComputeBatchHash(t *testing.T) {
	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: []byte("data1"), Hash: "hash1"},
		{ID: "msg-2", Ciphertext: []byte("data2"), Hash: "hash2"},
	}

	hash1 := computeBatchHash(messages)
	hash2 := computeBatchHash(messages)

	if hash1 != hash2 {
		t.Error("computeBatchHash should be deterministic")
	}

	// Different messages should produce different hashes
	messages2 := []ProposalMessage{
		{ID: "msg-3", Ciphertext: []byte("data3"), Hash: "hash3"},
	}
	hash3 := computeBatchHash(messages2)

	if hash1 == hash3 {
		t.Error("different messages should produce different batch hashes")
	}
}

func TestStatus(t *testing.T) {
	v := NewValidator("validator-1", "http://peer1:8082,http://peer2:8082", "http://pool:8080")

	status := v.Status()

	if status.NodeID != "validator-1" {
		t.Errorf("NodeID mismatch: expected validator-1, got %s", status.NodeID)
	}
	if status.Role != "leader" {
		t.Errorf("Role mismatch: expected leader, got %s", status.Role)
	}
	if status.CurrentBatch != 0 {
		t.Errorf("CurrentBatch should be 0, got %d", status.CurrentBatch)
	}
	if len(status.Peers) != 2 {
		t.Errorf("expected 2 peers, got %d", len(status.Peers))
	}
	if status.PendingCount != 0 {
		t.Errorf("PendingCount should be 0, got %d", status.PendingCount)
	}
}

func TestStatusFollower(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	status := v.Status()

	if status.Role != "follower" {
		t.Errorf("Role mismatch: expected follower, got %s", status.Role)
	}
}

func TestHandlePrepare(t *testing.T) {
	v := NewValidator("validator-2", "", "") // Follower

	ciphertext := []byte("test data")
	hash := func() string {
		h := sha256.Sum256(ciphertext)
		return hex.EncodeToString(h[:])
	}()

	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: ciphertext, Hash: hash},
	}
	batchHash := computeBatchHash(messages)

	req := &PrepareRequest{
		BatchNum:  1,
		Messages:  messages,
		LeaderID:  "validator-1",
		BatchHash: batchHash,
	}

	resp := v.HandlePrepare(req)

	if !resp.Accepted {
		t.Error("HandlePrepare should accept valid request")
	}
	if resp.BatchNum != 1 {
		t.Errorf("BatchNum mismatch: expected 1, got %d", resp.BatchNum)
	}
	if resp.ValidatorID != "validator-2" {
		t.Errorf("ValidatorID mismatch: expected validator-2, got %s", resp.ValidatorID)
	}
	if resp.BatchHash != batchHash {
		t.Errorf("BatchHash mismatch: expected %s, got %s", batchHash, resp.BatchHash)
	}
}

func TestHandlePrepareRejectWrongBatch(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: []byte("data"), Hash: "hash"},
	}
	batchHash := computeBatchHash(messages)

	// Try to prepare batch 2 when we expect batch 1
	req := &PrepareRequest{
		BatchNum:  2, // Wrong batch number
		Messages:  messages,
		LeaderID:  "validator-1",
		BatchHash: batchHash,
	}

	resp := v.HandlePrepare(req)

	if resp.Accepted {
		t.Error("HandlePrepare should reject wrong batch number")
	}
}

func TestHandlePrepareRejectBadHash(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: []byte("data"), Hash: "hash"},
	}

	req := &PrepareRequest{
		BatchNum:  1,
		Messages:  messages,
		LeaderID:  "validator-1",
		BatchHash: "invalid-hash", // Wrong hash
	}

	resp := v.HandlePrepare(req)

	if resp.Accepted {
		t.Error("HandlePrepare should reject invalid batch hash")
	}
}

func TestHandleCommit(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	// First prepare a batch
	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: []byte("data"), Hash: "hash"},
	}
	batchHash := computeBatchHash(messages)

	prepareReq := &PrepareRequest{
		BatchNum:  1,
		Messages:  messages,
		LeaderID:  "validator-1",
		BatchHash: batchHash,
	}
	v.HandlePrepare(prepareReq)

	// Then commit
	commitReq := &CommitRequest{
		BatchNum:  1,
		BatchHash: batchHash,
		LeaderID:  "validator-1",
	}

	resp := v.HandleCommit(commitReq)

	if !resp.Committed {
		t.Error("HandleCommit should commit valid request")
	}
	if resp.BatchNum != 1 {
		t.Errorf("BatchNum mismatch: expected 1, got %d", resp.BatchNum)
	}
	if v.CurrentBatch() != 1 {
		t.Errorf("currentBatch should be 1 after commit, got %d", v.CurrentBatch())
	}
}

func TestHandleCommitRejectNoPrepare(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	// Try to commit without prepare
	commitReq := &CommitRequest{
		BatchNum:  1,
		BatchHash: "some-hash",
		LeaderID:  "validator-1",
	}

	resp := v.HandleCommit(commitReq)

	if resp.Committed {
		t.Error("HandleCommit should reject without prepare")
	}
}

func TestHandleCommitRejectWrongHash(t *testing.T) {
	v := NewValidator("validator-2", "", "")

	// First prepare a batch
	messages := []ProposalMessage{
		{ID: "msg-1", Ciphertext: []byte("data"), Hash: "hash"},
	}
	batchHash := computeBatchHash(messages)

	prepareReq := &PrepareRequest{
		BatchNum:  1,
		Messages:  messages,
		LeaderID:  "validator-1",
		BatchHash: batchHash,
	}
	v.HandlePrepare(prepareReq)

	// Try to commit with wrong hash
	commitReq := &CommitRequest{
		BatchNum:  1,
		BatchHash: "wrong-hash",
		LeaderID:  "validator-1",
	}

	resp := v.HandleCommit(commitReq)

	if resp.Committed {
		t.Error("HandleCommit should reject wrong batch hash")
	}
}

func TestTriggerBatchNotLeader(t *testing.T) {
	v := NewValidator("validator-2", "", "") // Not leader

	err := v.TriggerBatch(context.Background())
	if err == nil {
		t.Error("TriggerBatch should fail for non-leader")
	}
}

func TestTriggerBatchEmpty(t *testing.T) {
	v := NewValidator("validator-1", "", "") // Leader

	// No pending messages
	err := v.TriggerBatch(context.Background())
	if err != nil {
		t.Errorf("TriggerBatch should succeed with no pending messages: %v", err)
	}
}

// TestTriggerBatchWithMockPeers tests the full BFT flow with mock peer validators
func TestTriggerBatchWithMockPeers(t *testing.T) {
	// Track prepared batches per mock validator
	var preparedBatches sync.Map

	// Create mock peer validators
	mockValidator := func(nodeID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/prepare":
				var req PrepareRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				// Store the prepared batch
				preparedBatches.Store(nodeID, req)

				resp := PrepareResponse{
					BatchNum:    req.BatchNum,
					ValidatorID: nodeID,
					Accepted:    true,
					BatchHash:   req.BatchHash,
				}
				json.NewEncoder(w).Encode(resp)

			case "/commit":
				var req CommitRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}

				resp := CommitResponse{
					BatchNum:    req.BatchNum,
					ValidatorID: nodeID,
					Committed:   true,
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
	}

	// Create mock message pool
	poolReceived := make(chan []ProposalMessage, 1)
	mockPool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/batch" && r.Method == http.MethodPost {
			var msgs []struct {
				ID         string `json:"id"`
				Ciphertext []byte `json:"ciphertext"`
				Hash       string `json:"hash"`
			}
			json.NewDecoder(r.Body).Decode(&msgs)

			poolMsgs := make([]ProposalMessage, len(msgs))
			for i, m := range msgs {
				poolMsgs[i] = ProposalMessage{
					ID:         m.ID,
					Ciphertext: m.Ciphertext,
					Hash:       m.Hash,
				}
			}
			select {
			case poolReceived <- poolMsgs:
			default:
			}

			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer mockPool.Close()

	// Create mock validators
	mock1 := mockValidator("validator-1")
	mock2 := mockValidator("validator-2")
	mock3 := mockValidator("validator-3")
	defer mock1.Close()
	defer mock2.Close()
	defer mock3.Close()

	// Create leader validator with mock peers
	peers := mock1.URL + "," + mock2.URL + "," + mock3.URL
	leader := NewValidator("validator-1", peers, mockPool.URL)

	// Add messages to the leader
	msg := ProposalMessage{
		ID:         "msg-1",
		Ciphertext: []byte("test data"),
		Hash:       "test-hash",
	}
	leader.Propose(msg)

	// Trigger batch
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := leader.TriggerBatch(ctx)
	if err != nil {
		t.Errorf("TriggerBatch failed: %v", err)
	}

	// Verify batch was committed
	if leader.CurrentBatch() != 1 {
		t.Errorf("expected currentBatch to be 1, got %d", leader.CurrentBatch())
	}

	// Verify pending is empty
	if leader.PendingCount() != 0 {
		t.Errorf("expected no pending messages, got %d", leader.PendingCount())
	}

	// Verify pool received the batch
	// Note: Cover traffic may be injected (30% probability), so we may have more than 1 message
	select {
	case received := <-poolReceived:
		if len(received) < 1 {
			t.Errorf("pool should receive at least 1 message, got %d", len(received))
		}
		// Count real (non-cover) messages and verify our original message is present
		realMessages := 0
		foundOriginal := false
		for _, msg := range received {
			if !cover.IsCoverTraffic(msg.ID) {
				realMessages++
				if msg.ID == "msg-1" {
					foundOriginal = true
				}
			}
		}
		if realMessages != 1 {
			t.Errorf("expected exactly 1 real message, got %d", realMessages)
		}
		if !foundOriginal {
			t.Error("original message 'msg-1' not found in batch")
		}
	case <-time.After(time.Second):
		t.Error("pool did not receive batch")
	}
}

// TestMultipleBatches tests that batch numbers increment correctly
func TestMultipleBatches(t *testing.T) {
	// Create mock peer validators that accept everything
	mockValidator := func(nodeID string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/prepare":
				var req PrepareRequest
				json.NewDecoder(r.Body).Decode(&req)
				resp := PrepareResponse{
					BatchNum:    req.BatchNum,
					ValidatorID: nodeID,
					Accepted:    true,
					BatchHash:   req.BatchHash,
				}
				json.NewEncoder(w).Encode(resp)
			case "/commit":
				var req CommitRequest
				json.NewDecoder(r.Body).Decode(&req)
				resp := CommitResponse{
					BatchNum:    req.BatchNum,
					ValidatorID: nodeID,
					Committed:   true,
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
	}

	mockPool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer mockPool.Close()

	mock1 := mockValidator("validator-1")
	mock2 := mockValidator("validator-2")
	mock3 := mockValidator("validator-3")
	defer mock1.Close()
	defer mock2.Close()
	defer mock3.Close()

	peers := mock1.URL + "," + mock2.URL + "," + mock3.URL
	leader := NewValidator("validator-1", peers, mockPool.URL)

	ctx := context.Background()

	// Batch 1
	leader.Propose(ProposalMessage{ID: "msg-1", Ciphertext: []byte("data1"), Hash: "hash1"})
	if err := leader.TriggerBatch(ctx); err != nil {
		t.Fatalf("Batch 1 failed: %v", err)
	}
	if leader.CurrentBatch() != 1 {
		t.Errorf("expected batch 1, got %d", leader.CurrentBatch())
	}

	// Batch 2
	leader.Propose(ProposalMessage{ID: "msg-2", Ciphertext: []byte("data2"), Hash: "hash2"})
	if err := leader.TriggerBatch(ctx); err != nil {
		t.Fatalf("Batch 2 failed: %v", err)
	}
	if leader.CurrentBatch() != 2 {
		t.Errorf("expected batch 2, got %d", leader.CurrentBatch())
	}

	// Batch 3
	leader.Propose(ProposalMessage{ID: "msg-3", Ciphertext: []byte("data3"), Hash: "hash3"})
	if err := leader.TriggerBatch(ctx); err != nil {
		t.Fatalf("Batch 3 failed: %v", err)
	}
	if leader.CurrentBatch() != 3 {
		t.Errorf("expected batch 3, got %d", leader.CurrentBatch())
	}
}

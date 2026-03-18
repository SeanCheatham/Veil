package consensus

import (
	"testing"
	"time"
)

func TestNewBatch(t *testing.T) {
	batch := NewBatch(1, "validator-1", 5)

	if batch.SequenceNum != 1 {
		t.Errorf("expected SequenceNum 1, got %d", batch.SequenceNum)
	}
	if batch.ProposerID != "validator-1" {
		t.Errorf("expected ProposerID validator-1, got %s", batch.ProposerID)
	}
	if batch.Epoch != 5 {
		t.Errorf("expected Epoch 5, got %d", batch.Epoch)
	}
	if batch.State != BatchPending {
		t.Errorf("expected State BatchPending, got %v", batch.State)
	}
	if !batch.IsEmpty() {
		t.Error("expected batch to be empty")
	}
}

func TestBatchAddMessage(t *testing.T) {
	batch := NewBatch(1, "validator-1", 1)

	msg := &BatchMessage{
		ID:         "msg-1",
		Ciphertext: []byte("test data"),
		ReceivedAt: time.Now().UTC(),
	}
	batch.AddMessage(msg)

	if batch.Size() != 1 {
		t.Errorf("expected Size 1, got %d", batch.Size())
	}
	if batch.IsEmpty() {
		t.Error("expected batch to not be empty")
	}
}

func TestBatchComputeHash(t *testing.T) {
	batch := NewBatch(1, "validator-1", 1)

	msg := &BatchMessage{
		ID:         "msg-1",
		Ciphertext: []byte("test data"),
	}
	batch.AddMessage(msg)
	batch.ComputeHash()

	if batch.Hash == "" {
		t.Error("expected Hash to be set")
	}

	// Computing hash again should produce the same result
	originalHash := batch.Hash
	batch.ComputeHash()
	if batch.Hash != originalHash {
		t.Error("expected Hash to be deterministic")
	}
}

func TestBatchVoting(t *testing.T) {
	batch := NewBatch(1, "validator-1", 1)

	batch.AddVote("validator-1")
	if batch.VoteCount() != 1 {
		t.Errorf("expected VoteCount 1, got %d", batch.VoteCount())
	}

	// Adding same vote again shouldn't change count
	batch.AddVote("validator-1")
	if batch.VoteCount() != 1 {
		t.Errorf("expected VoteCount 1 after duplicate vote, got %d", batch.VoteCount())
	}

	batch.AddVote("validator-2")
	if batch.VoteCount() != 2 {
		t.Errorf("expected VoteCount 2, got %d", batch.VoteCount())
	}
}

func TestBatchHasQuorum(t *testing.T) {
	batch := NewBatch(1, "validator-1", 1)

	// No votes - no quorum
	if batch.HasQuorum(3) {
		t.Error("expected no quorum with 0 votes")
	}

	// 1 vote - no quorum
	batch.AddVote("validator-1")
	if batch.HasQuorum(3) {
		t.Error("expected no quorum with 1 vote")
	}

	// 2 votes - quorum for 3 validators
	batch.AddVote("validator-2")
	if !batch.HasQuorum(3) {
		t.Error("expected quorum with 2 votes")
	}
}

func TestBatchState(t *testing.T) {
	tests := []struct {
		state    BatchState
		expected string
	}{
		{BatchPending, "pending"},
		{BatchProposed, "proposed"},
		{BatchCommitted, "committed"},
		{BatchState(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("BatchState(%d).String() = %s, want %s", tt.state, got, tt.expected)
		}
	}
}

func TestBatchCollector(t *testing.T) {
	bc := NewBatchCollector("validator-1", 3, 100*time.Millisecond)

	// Initially no pending messages
	if bc.HasPendingMessages() {
		t.Error("expected no pending messages initially")
	}

	// Add first message - not ready yet
	ready, batch := bc.AddMessage("msg-1", []byte("data-1"))
	if ready {
		t.Error("expected batch not ready after first message")
	}
	if batch != nil {
		t.Error("expected no batch returned yet")
	}
	if !bc.HasPendingMessages() {
		t.Error("expected pending messages after adding one")
	}

	// Add second message - still not ready
	ready, batch = bc.AddMessage("msg-2", []byte("data-2"))
	if ready {
		t.Error("expected batch not ready after second message")
	}

	// Add third message - should be ready (max batch size is 3)
	ready, batch = bc.AddMessage("msg-3", []byte("data-3"))
	if !ready {
		t.Error("expected batch ready after third message")
	}
	if batch == nil {
		t.Fatal("expected batch to be returned")
	}
	if batch.Size() != 3 {
		t.Errorf("expected batch size 3, got %d", batch.Size())
	}
	if batch.State != BatchProposed {
		t.Error("expected batch state to be Proposed")
	}
	if batch.Hash == "" {
		t.Error("expected batch hash to be set")
	}

	// After finalization, no pending messages
	if bc.HasPendingMessages() {
		t.Error("expected no pending messages after batch finalized")
	}
}

func TestBatchCollectorTimeout(t *testing.T) {
	bc := NewBatchCollector("validator-1", 10, 50*time.Millisecond)

	// Add message
	ready, _ := bc.AddMessage("msg-1", []byte("data-1"))
	if ready {
		t.Error("expected batch not ready immediately")
	}

	// Check timeout - not enough time passed
	timedOut, _ := bc.CheckTimeout()
	if timedOut {
		t.Error("expected no timeout immediately")
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Now should timeout
	timedOut, batch := bc.CheckTimeout()
	if !timedOut {
		t.Error("expected timeout after waiting")
	}
	if batch == nil {
		t.Fatal("expected batch to be returned on timeout")
	}
	if batch.Size() != 1 {
		t.Errorf("expected batch size 1, got %d", batch.Size())
	}
}

func TestBatchCollectorSequenceNum(t *testing.T) {
	bc := NewBatchCollector("validator-1", 2, time.Hour)

	// First sequence should be 1
	if bc.NextSequenceNum() != 1 {
		t.Errorf("expected next seq 1, got %d", bc.NextSequenceNum())
	}

	// Create first batch
	bc.AddMessage("msg-1", []byte("data-1"))
	_, batch := bc.AddMessage("msg-2", []byte("data-2"))
	if batch == nil {
		t.Fatal("expected batch")
	}
	if batch.SequenceNum != 1 {
		t.Errorf("expected batch seq 1, got %d", batch.SequenceNum)
	}

	// Next sequence should be 2
	if bc.NextSequenceNum() != 2 {
		t.Errorf("expected next seq 2, got %d", bc.NextSequenceNum())
	}

	// Create second batch
	bc.AddMessage("msg-3", []byte("data-3"))
	_, batch = bc.AddMessage("msg-4", []byte("data-4"))
	if batch == nil {
		t.Fatal("expected batch")
	}
	if batch.SequenceNum != 2 {
		t.Errorf("expected batch seq 2, got %d", batch.SequenceNum)
	}
}

func TestBatchCollectorEpoch(t *testing.T) {
	bc := NewBatchCollector("validator-1", 2, time.Hour)

	// Set epoch
	bc.SetEpoch(5)

	// Create batch
	bc.AddMessage("msg-1", []byte("data-1"))
	_, batch := bc.AddMessage("msg-2", []byte("data-2"))
	if batch == nil {
		t.Fatal("expected batch")
	}
	if batch.Epoch != 5 {
		t.Errorf("expected batch epoch 5, got %d", batch.Epoch)
	}
}

func TestComputeBatchHash(t *testing.T) {
	// Same messages with same seq should produce same hash
	msgs := []*BatchMessage{
		{ID: "msg-1", Ciphertext: []byte("data-1")},
		{ID: "msg-2", Ciphertext: []byte("data-2")},
	}

	hash1 := computeBatchHash(1, msgs)
	hash2 := computeBatchHash(1, msgs)

	if hash1 != hash2 {
		t.Error("expected same hash for same inputs")
	}

	// Different seq should produce different hash
	hash3 := computeBatchHash(2, msgs)
	if hash1 == hash3 {
		t.Error("expected different hash for different sequence")
	}

	// Different message order should produce different hash
	msgs2 := []*BatchMessage{
		{ID: "msg-2", Ciphertext: []byte("data-2")},
		{ID: "msg-1", Ciphertext: []byte("data-1")},
	}
	hash4 := computeBatchHash(1, msgs2)
	if hash1 == hash4 {
		t.Error("expected different hash for different message order")
	}
}

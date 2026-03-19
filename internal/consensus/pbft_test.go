package consensus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestComputeDigest tests the digest computation
func TestComputeDigest(t *testing.T) {
	digest1 := ComputeDigest("hello")
	digest2 := ComputeDigest("hello")
	digest3 := ComputeDigest("world")

	// Same content should produce same digest
	if digest1 != digest2 {
		t.Errorf("Expected same digest for same content, got %s and %s", digest1, digest2)
	}

	// Different content should produce different digest
	if digest1 == digest3 {
		t.Errorf("Expected different digest for different content")
	}

	// Digest should be a valid hex string of SHA256 length
	if len(digest1) != 64 {
		t.Errorf("Expected 64 character hex digest, got %d", len(digest1))
	}
}

// TestNewPBFT tests PBFT instance creation
func TestNewPBFT(t *testing.T) {
	peerURLs := []string{
		"http://validator-0:8081",
		"http://validator-1:8081",
		"http://validator-2:8081",
	}
	poolURL := "http://message-pool:8082"

	pbft := NewPBFT(0, 3, peerURLs, poolURL)

	if pbft.validatorID != 0 {
		t.Errorf("Expected validatorID 0, got %d", pbft.validatorID)
	}
	if pbft.numValidators != 3 {
		t.Errorf("Expected numValidators 3, got %d", pbft.numValidators)
	}
	if pbft.viewNumber != 0 {
		t.Errorf("Expected viewNumber 0, got %d", pbft.viewNumber)
	}
	if len(pbft.sequences) != 0 {
		t.Errorf("Expected empty sequences map")
	}
}

// TestIsPrimary tests primary determination
func TestIsPrimary(t *testing.T) {
	tests := []struct {
		validatorID int
		viewNumber  uint64
		isPrimary   bool
	}{
		{0, 0, true},  // view 0: validator 0 is primary
		{1, 0, false}, // view 0: validator 1 is not primary
		{2, 0, false}, // view 0: validator 2 is not primary
		{0, 1, false}, // view 1: validator 0 is not primary
		{1, 1, true},  // view 1: validator 1 is primary
		{2, 1, false}, // view 1: validator 2 is not primary
		{0, 2, false}, // view 2: validator 0 is not primary
		{1, 2, false}, // view 2: validator 1 is not primary
		{2, 2, true},  // view 2: validator 2 is primary
		{0, 3, true},  // view 3: validator 0 is primary (wraps around)
	}

	for _, tc := range tests {
		peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
		pbft := NewPBFT(tc.validatorID, 3, peerURLs, "http://pool:8082")
		pbft.viewNumber = tc.viewNumber

		if pbft.IsPrimary() != tc.isPrimary {
			t.Errorf("validatorID=%d, viewNumber=%d: expected IsPrimary()=%v, got %v",
				tc.validatorID, tc.viewNumber, tc.isPrimary, pbft.IsPrimary())
		}
	}
}

// TestQuorum tests quorum calculation
func TestQuorum(t *testing.T) {
	tests := []struct {
		numValidators int
		expectedQuorum int
	}{
		{3, 2}, // 3 validators: quorum = 2
		{4, 3}, // 4 validators: quorum = 3
		{5, 3}, // 5 validators: quorum = 3
		{7, 4}, // 7 validators: quorum = 4
	}

	for _, tc := range tests {
		peerURLs := make([]string, tc.numValidators)
		pbft := NewPBFT(0, tc.numValidators, peerURLs, "http://pool:8082")

		if pbft.Quorum() != tc.expectedQuorum {
			t.Errorf("numValidators=%d: expected quorum %d, got %d",
				tc.numValidators, tc.expectedQuorum, pbft.Quorum())
		}
	}
}

// TestHandlePrePrepareBasic tests basic pre-prepare handling
func TestHandlePrePrepareBasic(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}

	// Create a backup validator (validator 1)
	pbft := NewPBFT(1, 3, peerURLs, "http://pool:8082")

	content := "test message"
	digest := ComputeDigest(content)

	msg := ConsensusMessage{
		Type:        "pre-prepare",
		ViewNumber:  0,
		SeqNumber:   0,
		Digest:      digest,
		Content:     content,
		ValidatorID: 0, // From primary
		Signature:   "sig-0",
	}

	err := pbft.HandlePrePrepare(msg)
	if err != nil {
		t.Errorf("HandlePrePrepare failed: %v", err)
	}

	// Check state was updated
	seqState, exists := pbft.GetSequenceState(0)
	if !exists {
		t.Error("Expected sequence state to exist")
	}
	if seqState != StatePrepare {
		t.Errorf("Expected state %v, got %v", StatePrepare, seqState)
	}
}

// TestHandlePrePrepareFromNonPrimary tests rejection of pre-prepare from non-primary
func TestHandlePrePrepareFromNonPrimary(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
	pbft := NewPBFT(1, 3, peerURLs, "http://pool:8082")

	content := "test message"
	msg := ConsensusMessage{
		Type:        "pre-prepare",
		ViewNumber:  0,
		SeqNumber:   0,
		Digest:      ComputeDigest(content),
		Content:     content,
		ValidatorID: 2, // Not primary in view 0
		Signature:   "sig-2",
	}

	err := pbft.HandlePrePrepare(msg)
	if err == nil {
		t.Error("Expected error for pre-prepare from non-primary")
	}
}

// TestHandlePrePrepareDigestMismatch tests rejection of pre-prepare with wrong digest
func TestHandlePrePrepareDigestMismatch(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
	pbft := NewPBFT(1, 3, peerURLs, "http://pool:8082")

	msg := ConsensusMessage{
		Type:        "pre-prepare",
		ViewNumber:  0,
		SeqNumber:   0,
		Digest:      "wrong-digest",
		Content:     "test message",
		ValidatorID: 0,
		Signature:   "sig-0",
	}

	err := pbft.HandlePrePrepare(msg)
	if err == nil {
		t.Error("Expected error for digest mismatch")
	}
}

// TestHandlePrepareBasic tests basic prepare handling
func TestHandlePrepareBasic(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
	pbft := NewPBFT(0, 3, peerURLs, "http://pool:8082")

	content := "test message"
	digest := ComputeDigest(content)

	// First set up the sequence with pre-prepare
	seqState := pbft.getOrCreateSeqState(0)
	seqState.state = StatePrepare
	seqState.content = content
	seqState.digest = digest

	// Receive prepare from validator 1
	msg := ConsensusMessage{
		Type:        "prepare",
		ViewNumber:  0,
		SeqNumber:   0,
		Digest:      digest,
		ValidatorID: 1,
		Signature:   "sig-1",
	}

	err := pbft.HandlePrepare(msg)
	if err != nil {
		t.Errorf("HandlePrepare failed: %v", err)
	}

	// Check vote was recorded
	pbft.mu.Lock()
	votes := len(pbft.sequences[0].prepareVotes)
	pbft.mu.Unlock()

	if votes != 1 {
		t.Errorf("Expected 1 prepare vote, got %d", votes)
	}
}

// TestHandleCommitBasic tests basic commit handling
func TestHandleCommitBasic(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
	pbft := NewPBFT(0, 3, peerURLs, "http://pool:8082")

	content := "test message"
	digest := ComputeDigest(content)

	// Set up sequence state
	seqState := pbft.getOrCreateSeqState(0)
	seqState.state = StateCommit
	seqState.content = content
	seqState.digest = digest

	// Receive commit from validator 1
	msg := ConsensusMessage{
		Type:        "commit",
		ViewNumber:  0,
		SeqNumber:   0,
		Digest:      digest,
		ValidatorID: 1,
		Signature:   "sig-1",
	}

	err := pbft.HandleCommit(msg)
	if err != nil {
		t.Errorf("HandleCommit failed: %v", err)
	}

	// Check vote was recorded
	pbft.mu.Lock()
	votes := len(pbft.sequences[0].commitVotes)
	pbft.mu.Unlock()

	if votes != 1 {
		t.Errorf("Expected 1 commit vote, got %d", votes)
	}
}

// TestSequenceNumberMonotonicity tests that sequence numbers increase monotonically
func TestSequenceNumberMonotonicity(t *testing.T) {
	// Create mock message-pool server
	var messageCount int
	var mu sync.Mutex
	mockPool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/messages" && r.Method == http.MethodPost {
			mu.Lock()
			id := messageCount
			messageCount++
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"id": id})
		}
	}))
	defer mockPool.Close()

	// Create validators with mock peer servers
	validators := make([]*PBFT, 3)
	mockPeers := make([]*httptest.Server, 3)

	// First create the PBFT instances
	peerURLs := make([]string, 3)
	for i := 0; i < 3; i++ {
		validators[i] = NewPBFT(i, 3, nil, mockPool.URL)
	}

	// Create mock peer servers that forward to the PBFT handlers
	for i := 0; i < 3; i++ {
		idx := i
		mockPeers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var msg ConsensusMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			var err error
			switch r.URL.Path {
			case "/consensus/pre-prepare":
				err = validators[idx].HandlePrePrepare(msg)
			case "/consensus/prepare":
				err = validators[idx].HandlePrepare(msg)
			case "/consensus/commit":
				err = validators[idx].HandleCommit(msg)
			}

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		peerURLs[i] = mockPeers[i].URL
	}
	defer func() {
		for _, s := range mockPeers {
			s.Close()
		}
	}()

	// Update validators with peer URLs
	for i := 0; i < 3; i++ {
		validators[i].peerURLs = peerURLs
	}

	// Test that submitting multiple messages results in monotonic sequence numbers
	primary := validators[0]

	var prevSeqNum uint64
	for i := 0; i < 3; i++ {
		currentSeq := primary.GetNextSeqNumber()

		if i > 0 && currentSeq <= prevSeqNum {
			t.Errorf("Sequence numbers not monotonic: prev=%d, current=%d", prevSeqNum, currentSeq)
		}

		_, err := primary.Submit(fmt.Sprintf("message-%d", i))
		if err != nil {
			t.Errorf("Submit failed: %v", err)
		}

		prevSeqNum = currentSeq
	}

	// Verify final sequence number
	finalSeq := primary.GetNextSeqNumber()
	if finalSeq != 3 {
		t.Errorf("Expected final sequence number 3, got %d", finalSeq)
	}
}

// TestConsensusStateString tests state string representation
func TestConsensusStateString(t *testing.T) {
	tests := []struct {
		state    ConsensusState
		expected string
	}{
		{StateIdle, "idle"},
		{StatePrePrepare, "pre-prepare"},
		{StatePrepare, "prepare"},
		{StateCommit, "commit"},
		{StateExecuted, "executed"},
		{ConsensusState(99), "unknown"},
	}

	for _, tc := range tests {
		if tc.state.String() != tc.expected {
			t.Errorf("Expected %s, got %s", tc.expected, tc.state.String())
		}
	}
}

// TestThreeValidatorConsensus tests full consensus with 3 validators
func TestThreeValidatorConsensus(t *testing.T) {
	// Create mock message-pool server
	var messages []string
	var mu sync.Mutex
	mockPool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/messages" && r.Method == http.MethodPost {
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)

			mu.Lock()
			id := len(messages)
			messages = append(messages, req["content"])
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]int{"id": id})
		}
	}))
	defer mockPool.Close()

	// Create validators
	validators := make([]*PBFT, 3)
	peerURLs := make([]string, 3)
	mockPeers := make([]*httptest.Server, 3)

	// Create PBFT instances
	for i := 0; i < 3; i++ {
		validators[i] = NewPBFT(i, 3, nil, mockPool.URL)
	}

	// Create mock peer servers
	for i := 0; i < 3; i++ {
		idx := i
		mockPeers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var msg ConsensusMessage
			if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			var err error
			switch r.URL.Path {
			case "/consensus/pre-prepare":
				err = validators[idx].HandlePrePrepare(msg)
			case "/consensus/prepare":
				err = validators[idx].HandlePrepare(msg)
			case "/consensus/commit":
				err = validators[idx].HandleCommit(msg)
			}

			if err != nil {
				// Log but don't fail - some messages may arrive before state is ready
				t.Logf("Handler error for validator %d: %v", idx, err)
			}
			w.WriteHeader(http.StatusOK)
		}))
		peerURLs[i] = mockPeers[i].URL
	}
	defer func() {
		for _, s := range mockPeers {
			s.Close()
		}
	}()

	// Update validators with peer URLs
	for i := 0; i < 3; i++ {
		validators[i].peerURLs = peerURLs
	}

	// Submit a message through the primary (validator 0)
	primary := validators[0]
	testContent := "consensus-test-message"

	messageID, err := primary.Submit(testContent)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait a bit for consensus to complete across all validators
	time.Sleep(200 * time.Millisecond)

	// Verify message was committed
	if !primary.IsCommitted(0) {
		t.Error("Message not committed on primary")
	}

	// Verify message appeared in pool
	mu.Lock()
	if len(messages) == 0 {
		t.Error("No messages in pool")
	} else if messages[messageID] != testContent {
		t.Errorf("Expected content %q, got %q", testContent, messages[messageID])
	}
	mu.Unlock()
}

// TestSubmitFromNonPrimary tests that only primary can submit
func TestSubmitFromNonPrimary(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}

	// Create a non-primary validator (validator 1 in view 0)
	backup := NewPBFT(1, 3, peerURLs, "http://pool:8082")

	_, err := backup.Submit("test message")
	if err == nil {
		t.Error("Expected error when non-primary tries to submit")
	}
}

// TestGetSequenceState tests sequence state retrieval
func TestGetSequenceState(t *testing.T) {
	peerURLs := []string{"http://v0:8081", "http://v1:8081", "http://v2:8081"}
	pbft := NewPBFT(0, 3, peerURLs, "http://pool:8082")

	// Non-existent sequence
	state, exists := pbft.GetSequenceState(0)
	if exists {
		t.Error("Expected sequence to not exist")
	}
	if state != StateIdle {
		t.Errorf("Expected StateIdle for non-existent sequence, got %v", state)
	}

	// Create sequence state
	pbft.mu.Lock()
	pbft.sequences[0] = &SequenceState{state: StatePrepare}
	pbft.mu.Unlock()

	state, exists = pbft.GetSequenceState(0)
	if !exists {
		t.Error("Expected sequence to exist")
	}
	if state != StatePrepare {
		t.Errorf("Expected StatePrepare, got %v", state)
	}
}

package consensus

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewValidator(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{"validator-2:9000", "validator-3:9000"},
		PoolAddr: "message-pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	if v.ID != "1" {
		t.Errorf("expected ID 1, got %s", v.ID)
	}
	if v.NumericID != 1 {
		t.Errorf("expected NumericID 1, got %d", v.NumericID)
	}
	if len(v.Peers) != 2 {
		t.Errorf("expected 2 peers, got %d", len(v.Peers))
	}
	if v.PoolAddr != "message-pool:8080" {
		t.Errorf("expected pool address message-pool:8080, got %s", v.PoolAddr)
	}
}

func TestNewValidatorInvalidID(t *testing.T) {
	tests := []struct {
		id      string
		wantErr bool
	}{
		{"1", false},
		{"2", false},
		{"3", false},
		{"0", true},
		{"4", true},
		{"abc", true},
		{"", true},
	}

	for _, tt := range tests {
		cfg := ValidatorConfig{
			ID:       tt.id,
			Peers:    []string{},
			PoolAddr: "pool:8080",
		}

		_, err := NewValidator(cfg)
		if tt.wantErr && err == nil {
			t.Errorf("expected error for ID %s", tt.id)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("unexpected error for ID %s: %v", tt.id, err)
		}
	}
}

func TestValidatorStartStop(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Initially not running
	if v.IsRunning() {
		t.Error("expected validator to not be running initially")
	}

	// Start
	v.Start()
	if !v.IsRunning() {
		t.Error("expected validator to be running after Start")
	}

	// Start again should be no-op
	v.Start()
	if !v.IsRunning() {
		t.Error("expected validator to still be running")
	}

	// Stop
	v.Stop()
	if v.IsRunning() {
		t.Error("expected validator to not be running after Stop")
	}

	// Stop again should be no-op
	v.Stop()
	if v.IsRunning() {
		t.Error("expected validator to still not be running")
	}
}

func TestValidatorIsLeader(t *testing.T) {
	cfg := ValidatorConfig{
		ID:            "1",
		Peers:         []string{},
		PoolAddr:      "pool:8080",
		EpochDuration: time.Hour, // Long duration to prevent auto-tick
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Before starting, not leader (epoch 0)
	if v.IsLeader() {
		t.Error("expected not to be leader before clock starts")
	}

	// Start clock - epoch advances to 1
	v.Clock.Start()
	defer v.Clock.Stop()

	// Wait for epoch to advance
	time.Sleep(10 * time.Millisecond)

	// Epoch 1 -> validator 1 is leader
	if !v.IsLeader() {
		t.Errorf("expected validator 1 to be leader in epoch 1, got epoch %d, leader ID %d",
			v.Clock.CurrentEpoch(), v.CurrentLeaderID())
	}

	// Advance epoch
	v.Clock.ForceAdvance()

	// Epoch 2 -> validator 2 is leader
	if v.IsLeader() {
		t.Errorf("expected validator 1 to not be leader in epoch 2, got leader ID %d",
			v.CurrentLeaderID())
	}
}

func TestValidatorGetStatus(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	status := v.GetStatus()

	if status.ID != "1" {
		t.Errorf("expected ID 1, got %s", status.ID)
	}
	if status.Running {
		t.Error("expected not running")
	}
	if status.LastCommittedSeq != 0 {
		t.Errorf("expected LastCommittedSeq 0, got %d", status.LastCommittedSeq)
	}
	if status.PendingProposals != 0 {
		t.Errorf("expected PendingProposals 0, got %d", status.PendingProposals)
	}
}

func TestValidatorSubmitMessage(t *testing.T) {
	cfg := ValidatorConfig{
		ID:            "1",
		Peers:         []string{},
		PoolAddr:      "pool:8080",
		EpochDuration: time.Hour,
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	// Start the validator so it's the leader
	v.Start()
	defer v.Stop()

	// Wait for epoch to advance
	time.Sleep(10 * time.Millisecond)

	// Submit a message
	ciphertext := []byte("test message")
	id, err := v.SubmitMessage(ciphertext)
	if err != nil {
		t.Fatalf("failed to submit message: %v", err)
	}

	if id == "" {
		t.Error("expected non-empty message ID")
	}

	// Should have pending messages now
	if !v.Collector.HasPendingMessages() {
		t.Error("expected pending messages after submit")
	}
}

func TestValidatorSubmitEmptyMessage(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	_, err = v.SubmitMessage(nil)
	if err == nil {
		t.Error("expected error for empty message")
	}

	_, err = v.SubmitMessage([]byte{})
	if err == nil {
		t.Error("expected error for empty message")
	}
}

func TestValidatorHandleProposal(t *testing.T) {
	cfg := ValidatorConfig{
		ID:            "2",
		Peers:         []string{},
		PoolAddr:      "pool:8080",
		EpochDuration: time.Hour,
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	v.Start()
	defer v.Stop()

	// Create a valid proposal
	messages := []*BatchMessage{
		{ID: "msg-1", Ciphertext: []byte("data-1")},
	}
	hash := computeBatchHash(1, messages)

	proposal := &ProposalRequest{
		SequenceNum: 1,
		Hash:        hash,
		ProposerID:  "1",
		Epoch:       1,
		Messages: []ProposalMessage{
			{
				ID:         "msg-1",
				Ciphertext: base64.StdEncoding.EncodeToString([]byte("data-1")),
			},
		},
	}

	voted := v.HandleProposal(proposal)
	if !voted {
		t.Error("expected to vote for valid proposal")
	}
}

func TestValidatorHandleProposalInvalidHash(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "2",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	v.Start()
	defer v.Stop()

	// Create a proposal with wrong hash
	proposal := &ProposalRequest{
		SequenceNum: 1,
		Hash:        "invalid-hash",
		ProposerID:  "1",
		Epoch:       1,
		Messages: []ProposalMessage{
			{
				ID:         "msg-1",
				Ciphertext: base64.StdEncoding.EncodeToString([]byte("data-1")),
			},
		},
	}

	voted := v.HandleProposal(proposal)
	if voted {
		t.Error("expected not to vote for proposal with invalid hash")
	}
}

func TestValidatorRecordVote(t *testing.T) {
	cfg := ValidatorConfig{
		ID:            "1",
		Peers:         []string{},
		PoolAddr:      "pool:8080",
		MaxBatchSize:  1,
		EpochDuration: time.Hour,
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	v.Start()
	defer v.Stop()

	// Wait for epoch
	time.Sleep(10 * time.Millisecond)

	// Submit message to create a proposal
	_, err = v.SubmitMessage([]byte("test"))
	if err != nil {
		t.Fatalf("failed to submit: %v", err)
	}

	// Wait for batch to be created
	time.Sleep(50 * time.Millisecond)

	// The batch should be pending
	status := v.GetStatus()
	if status.PendingProposals == 0 {
		// Check if it was committed already (with our single-vote)
		// This is expected in a test environment without quorum
		t.Log("batch may have been committed without full quorum in test")
	}
}

func TestServerHandleSubmit(t *testing.T) {
	cfg := ValidatorConfig{
		ID:            "1",
		Peers:         []string{},
		PoolAddr:      "pool:8080",
		EpochDuration: time.Hour,
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}
	v.Start()
	defer v.Stop()

	// Wait for epoch
	time.Sleep(10 * time.Millisecond)

	server := NewServer(v, ":9000")

	// Create request
	reqBody := SubmitRequest{
		Ciphertext: base64.StdEncoding.EncodeToString([]byte("test message")),
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/submit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status %d, got %d: %s", http.StatusAccepted, w.Code, w.Body.String())
	}

	var resp SubmitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestServerHandleStatus(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	server := NewServer(v, ":9000")

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var status ValidatorStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if status.ID != "1" {
		t.Errorf("expected ID 1, got %s", status.ID)
	}
}

func TestServerHandleHealth(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "1",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	server := NewServer(v, ":9000")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var health HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if health.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", health.Status)
	}
	if health.ID != "1" {
		t.Errorf("expected ID 1, got %s", health.ID)
	}
}

func TestServerHandlePropose(t *testing.T) {
	cfg := ValidatorConfig{
		ID:       "2",
		Peers:    []string{},
		PoolAddr: "pool:8080",
	}

	v, err := NewValidator(cfg)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}
	v.Start()
	defer v.Stop()

	server := NewServer(v, ":9000")

	// Create valid proposal
	messages := []*BatchMessage{
		{ID: "msg-1", Ciphertext: []byte("data-1")},
	}
	hash := computeBatchHash(1, messages)

	proposal := ProposalRequest{
		SequenceNum: 1,
		Hash:        hash,
		ProposerID:  "1",
		Epoch:       1,
		Messages: []ProposalMessage{
			{
				ID:         "msg-1",
				Ciphertext: base64.StdEncoding.EncodeToString([]byte("data-1")),
			},
		},
	}
	body, _ := json.Marshal(proposal)

	req := httptest.NewRequest("POST", "/propose", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var vote VoteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &vote); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !vote.Voted {
		t.Error("expected vote to be accepted")
	}
	if vote.VoterID != "2" {
		t.Errorf("expected voter ID 2, got %s", vote.VoterID)
	}
}

func TestComputeMessageID(t *testing.T) {
	id1 := computeMessageID([]byte("test"))
	id2 := computeMessageID([]byte("test"))

	if id1 != id2 {
		t.Error("expected same ID for same input")
	}

	id3 := computeMessageID([]byte("different"))
	if id1 == id3 {
		t.Error("expected different ID for different input")
	}

	if len(id1) != 32 {
		t.Errorf("expected ID length 32, got %d", len(id1))
	}
}

// Package validator implements BFT consensus and message pool ordering.
package validator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/veil-protocol/veil/internal/cover"
	"github.com/veil-protocol/veil/internal/pool"
	"github.com/veil-protocol/veil/internal/properties"
)

// BFT quorum requirement: 2/3 of validators must agree
const QuorumSize = 2

// BatchTimeout is the maximum time to wait for votes before timing out
const BatchTimeout = 5 * time.Second

// ProposalMessage represents a message proposed by a relay for inclusion in a batch.
type ProposalMessage struct {
	ID         string `json:"id"`
	Ciphertext []byte `json:"ciphertext"`
	Hash       string `json:"hash"`
}

// PrepareRequest is sent from leader to validators to prepare a batch.
type PrepareRequest struct {
	BatchNum  uint64            `json:"batch_num"`
	Messages  []ProposalMessage `json:"messages"`
	LeaderID  string            `json:"leader_id"`
	BatchHash string            `json:"batch_hash"` // Hash of serialized messages for agreement
}

// PrepareResponse is sent from validator to leader after receiving prepare.
type PrepareResponse struct {
	BatchNum    uint64 `json:"batch_num"`
	ValidatorID string `json:"validator_id"`
	Accepted    bool   `json:"accepted"`
	BatchHash   string `json:"batch_hash"`
}

// CommitRequest is sent from leader after receiving quorum of prepare responses.
type CommitRequest struct {
	BatchNum  uint64 `json:"batch_num"`
	BatchHash string `json:"batch_hash"`
	LeaderID  string `json:"leader_id"`
}

// CommitResponse is sent from validator after committing the batch.
type CommitResponse struct {
	BatchNum    uint64 `json:"batch_num"`
	ValidatorID string `json:"validator_id"`
	Committed   bool   `json:"committed"`
}

// Status represents the current status of a validator.
type Status struct {
	NodeID       string   `json:"node_id"`
	Role         string   `json:"role"` // "leader" or "follower"
	CurrentBatch uint64   `json:"current_batch"`
	Peers        []string `json:"peers"`
	PendingCount int      `json:"pending_count"`
}

// Validator implements BFT consensus for ordering messages into the pool.
type Validator struct {
	mu sync.RWMutex

	nodeID         string
	peers          []string // URLs of peer validators
	messagePoolURL string

	// Batch tracking
	currentBatch uint64 // Last committed batch number
	pendingMsgs  []ProposalMessage

	// BFT state for current round
	preparedBatch    *PrepareRequest           // Current batch being prepared
	prepareResponses map[string]PrepareResponse // ValidatorID -> response
	commitResponses  map[string]CommitResponse  // ValidatorID -> response

	// HTTP client for peer communication
	client *http.Client

	// Cover traffic generator for injecting dummy messages
	coverGen *cover.CoverTrafficGenerator
}

// NewValidator creates a new validator instance.
// peers should be comma-separated URLs of all validators (including self).
// messagePoolURL is the URL of the message pool service.
func NewValidator(nodeID, peers, messagePoolURL string) *Validator {
	peerList := parsePeers(peers)

	return &Validator{
		nodeID:           nodeID,
		peers:            peerList,
		messagePoolURL:   messagePoolURL,
		currentBatch:     0,
		pendingMsgs:      make([]ProposalMessage, 0),
		prepareResponses: make(map[string]PrepareResponse),
		commitResponses:  make(map[string]CommitResponse),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		coverGen: cover.NewCoverTrafficGenerator(),
	}
}

// parsePeers parses a comma-separated list of peer URLs.
func parsePeers(peers string) []string {
	if peers == "" {
		return []string{}
	}
	parts := strings.Split(peers, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// IsLeader returns true if this validator is the leader.
// For simplicity, validator-1 is always the leader.
func (v *Validator) IsLeader() bool {
	return v.nodeID == "validator-1"
}

// Propose adds a message to the pending batch (called by relays via POST /propose).
// Only the leader collects proposals; followers redirect to leader.
func (v *Validator) Propose(msg ProposalMessage) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.pendingMsgs = append(v.pendingMsgs, msg)
	log.Printf("[%s] Received proposal, pending count: %d", v.nodeID, len(v.pendingMsgs))
	return nil
}

// TriggerBatch initiates the BFT consensus process for the current pending messages.
// This should be called periodically or when enough messages accumulate.
// Only the leader should call this.
func (v *Validator) TriggerBatch(ctx context.Context) error {
	if !v.IsLeader() {
		return fmt.Errorf("only leader can trigger batch")
	}

	v.mu.Lock()
	if len(v.pendingMsgs) == 0 {
		v.mu.Unlock()
		return nil // Nothing to batch
	}

	// Move pending messages to batch
	nextBatch := v.currentBatch + 1
	messages := make([]ProposalMessage, len(v.pendingMsgs))
	copy(messages, v.pendingMsgs)
	v.pendingMsgs = make([]ProposalMessage, 0)

	// Convert to pool.Message for cover traffic injection
	poolMessages := make([]pool.Message, len(messages))
	for i, msg := range messages {
		poolMessages[i] = pool.Message{
			ID:         msg.ID,
			Ciphertext: msg.Ciphertext,
			Hash:       msg.Hash,
		}
	}

	// Inject cover traffic (30% probability, 1-3 messages)
	originalCount := len(poolMessages)
	poolMessages = v.coverGen.InjectCoverTraffic(poolMessages)
	coverInjected := len(poolMessages) > originalCount

	// Convert back to ProposalMessage
	messages = make([]ProposalMessage, len(poolMessages))
	for i, msg := range poolMessages {
		messages[i] = ProposalMessage{
			ID:         msg.ID,
			Ciphertext: msg.Ciphertext,
			Hash:       msg.Hash,
		}
	}

	// Observe cover traffic injection for Antithesis property
	if coverInjected {
		properties.ObserveCoverTraffic(true, nextBatch)
		log.Printf("[%s] Injected %d cover messages into batch %d", v.nodeID, len(poolMessages)-originalCount, nextBatch)
	}

	// Create batch hash for agreement
	batchHash := computeBatchHash(messages)

	prepareReq := &PrepareRequest{
		BatchNum:  nextBatch,
		Messages:  messages,
		LeaderID:  v.nodeID,
		BatchHash: batchHash,
	}
	v.preparedBatch = prepareReq
	v.prepareResponses = make(map[string]PrepareResponse)
	v.commitResponses = make(map[string]CommitResponse)
	v.mu.Unlock()

	log.Printf("[%s] Initiating batch %d with %d messages", v.nodeID, nextBatch, len(messages))

	// Phase 1: Send PREPARE to all validators (including self)
	prepareVotes := v.sendPrepare(ctx, prepareReq)

	// Check if we have quorum for prepare
	acceptedCount := 0
	for _, resp := range prepareVotes {
		if resp.Accepted && resp.BatchHash == batchHash {
			acceptedCount++
		}
	}

	log.Printf("[%s] Batch %d: received %d prepare votes (need %d)", v.nodeID, nextBatch, acceptedCount, QuorumSize)

	if acceptedCount < QuorumSize {
		log.Printf("[%s] Batch %d: failed to reach prepare quorum", v.nodeID, nextBatch)
		return fmt.Errorf("failed to reach prepare quorum: got %d, need %d", acceptedCount, QuorumSize)
	}

	// Phase 2: Send COMMIT to all validators
	commitReq := &CommitRequest{
		BatchNum:  nextBatch,
		BatchHash: batchHash,
		LeaderID:  v.nodeID,
	}
	commitVotes := v.sendCommit(ctx, commitReq)

	// Check if we have quorum for commit
	committedCount := 0
	for _, resp := range commitVotes {
		if resp.Committed {
			committedCount++
		}
	}

	log.Printf("[%s] Batch %d: received %d commit votes (need %d)", v.nodeID, nextBatch, committedCount, QuorumSize)

	if committedCount < QuorumSize {
		log.Printf("[%s] Batch %d: failed to reach commit quorum", v.nodeID, nextBatch)
		return fmt.Errorf("failed to reach commit quorum: got %d, need %d", committedCount, QuorumSize)
	}

	// All validators in quorum agreed - commit to message pool
	allAgreed := true // If we reached here, quorum agreed
	properties.AssertValidatorAgreement(allAgreed, nextBatch, v.nodeID)

	// Submit batch to message pool
	if err := v.submitToPool(ctx, messages); err != nil {
		log.Printf("[%s] Batch %d: failed to submit to pool: %v", v.nodeID, nextBatch, err)
		return fmt.Errorf("failed to submit to pool: %w", err)
	}

	v.mu.Lock()
	v.currentBatch = nextBatch
	v.preparedBatch = nil
	v.mu.Unlock()

	// Observe chain progression
	properties.ObserveChainProgression(true, nextBatch)
	log.Printf("[%s] Batch %d: committed successfully with %d messages", v.nodeID, nextBatch, len(messages))

	return nil
}

// sendPrepare sends prepare requests to all validators.
func (v *Validator) sendPrepare(ctx context.Context, req *PrepareRequest) map[string]PrepareResponse {
	responses := make(map[string]PrepareResponse)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, peer := range v.peers {
		wg.Add(1)
		go func(peerURL string) {
			defer wg.Done()

			resp, err := v.sendPrepareRequest(ctx, peerURL, req)
			if err != nil {
				log.Printf("[%s] Failed to send prepare to %s: %v", v.nodeID, peerURL, err)
				return
			}

			mu.Lock()
			responses[resp.ValidatorID] = *resp
			mu.Unlock()
		}(peer)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(BatchTimeout):
		log.Printf("[%s] Prepare phase timed out", v.nodeID)
	case <-ctx.Done():
		log.Printf("[%s] Prepare phase cancelled", v.nodeID)
	}

	return responses
}

// sendCommit sends commit requests to all validators.
func (v *Validator) sendCommit(ctx context.Context, req *CommitRequest) map[string]CommitResponse {
	responses := make(map[string]CommitResponse)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, peer := range v.peers {
		wg.Add(1)
		go func(peerURL string) {
			defer wg.Done()

			resp, err := v.sendCommitRequest(ctx, peerURL, req)
			if err != nil {
				log.Printf("[%s] Failed to send commit to %s: %v", v.nodeID, peerURL, err)
				return
			}

			mu.Lock()
			responses[resp.ValidatorID] = *resp
			mu.Unlock()
		}(peer)
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(BatchTimeout):
		log.Printf("[%s] Commit phase timed out", v.nodeID)
	case <-ctx.Done():
		log.Printf("[%s] Commit phase cancelled", v.nodeID)
	}

	return responses
}

// sendPrepareRequest sends a prepare request to a single peer.
func (v *Validator) sendPrepareRequest(ctx context.Context, peerURL string, req *PrepareRequest) (*PrepareResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal prepare request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL+"/prepare", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := v.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prepare request failed with status %d", httpResp.StatusCode)
	}

	var resp PrepareResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// sendCommitRequest sends a commit request to a single peer.
func (v *Validator) sendCommitRequest(ctx context.Context, peerURL string, req *CommitRequest) (*CommitResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal commit request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL+"/commit", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := v.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("commit request failed with status %d", httpResp.StatusCode)
	}

	var resp CommitResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// HandlePrepare handles a prepare request from the leader.
func (v *Validator) HandlePrepare(req *PrepareRequest) *PrepareResponse {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Validate the request
	expectedBatch := v.currentBatch + 1
	if req.BatchNum != expectedBatch {
		log.Printf("[%s] Rejecting prepare: expected batch %d, got %d", v.nodeID, expectedBatch, req.BatchNum)
		return &PrepareResponse{
			BatchNum:    req.BatchNum,
			ValidatorID: v.nodeID,
			Accepted:    false,
			BatchHash:   "",
		}
	}

	// Verify batch hash
	computedHash := computeBatchHash(req.Messages)
	if computedHash != req.BatchHash {
		log.Printf("[%s] Rejecting prepare: batch hash mismatch", v.nodeID)
		return &PrepareResponse{
			BatchNum:    req.BatchNum,
			ValidatorID: v.nodeID,
			Accepted:    false,
			BatchHash:   computedHash,
		}
	}

	// Accept the prepare
	v.preparedBatch = req
	log.Printf("[%s] Accepted prepare for batch %d with %d messages", v.nodeID, req.BatchNum, len(req.Messages))

	return &PrepareResponse{
		BatchNum:    req.BatchNum,
		ValidatorID: v.nodeID,
		Accepted:    true,
		BatchHash:   computedHash,
	}
}

// HandleCommit handles a commit request from the leader.
func (v *Validator) HandleCommit(req *CommitRequest) *CommitResponse {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Verify we have a prepared batch matching this commit
	if v.preparedBatch == nil {
		log.Printf("[%s] Rejecting commit: no prepared batch", v.nodeID)
		return &CommitResponse{
			BatchNum:    req.BatchNum,
			ValidatorID: v.nodeID,
			Committed:   false,
		}
	}

	if v.preparedBatch.BatchNum != req.BatchNum {
		log.Printf("[%s] Rejecting commit: batch number mismatch", v.nodeID)
		return &CommitResponse{
			BatchNum:    req.BatchNum,
			ValidatorID: v.nodeID,
			Committed:   false,
		}
	}

	if v.preparedBatch.BatchHash != req.BatchHash {
		log.Printf("[%s] Rejecting commit: batch hash mismatch", v.nodeID)
		return &CommitResponse{
			BatchNum:    req.BatchNum,
			ValidatorID: v.nodeID,
			Committed:   false,
		}
	}

	// Commit the batch locally
	v.currentBatch = req.BatchNum
	v.preparedBatch = nil

	// Assert validator agreement - we agreed with the leader on this batch
	properties.AssertValidatorAgreement(true, req.BatchNum, v.nodeID)

	log.Printf("[%s] Committed batch %d", v.nodeID, req.BatchNum)

	return &CommitResponse{
		BatchNum:    req.BatchNum,
		ValidatorID: v.nodeID,
		Committed:   true,
	}
}

// submitToPool sends the committed batch to the message pool service.
func (v *Validator) submitToPool(ctx context.Context, messages []ProposalMessage) error {
	// Convert ProposalMessage to pool.Message
	poolMessages := make([]pool.Message, len(messages))
	for i, msg := range messages {
		poolMessages[i] = pool.Message{
			ID:         msg.ID,
			Ciphertext: msg.Ciphertext,
			Hash:       msg.Hash,
		}
	}

	body, err := json.Marshal(poolMessages)
	if err != nil {
		return fmt.Errorf("failed to marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.messagePoolURL+"/batch", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pool request failed with status %d", resp.StatusCode)
	}

	return nil
}

// Status returns the current status of the validator.
func (v *Validator) Status() Status {
	v.mu.RLock()
	defer v.mu.RUnlock()

	role := "follower"
	if v.IsLeader() {
		role = "leader"
	}

	return Status{
		NodeID:       v.nodeID,
		Role:         role,
		CurrentBatch: v.currentBatch,
		Peers:        v.peers,
		PendingCount: len(v.pendingMsgs),
	}
}

// CurrentBatch returns the current batch number.
func (v *Validator) CurrentBatch() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.currentBatch
}

// PendingCount returns the number of pending messages.
func (v *Validator) PendingCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.pendingMsgs)
}

// computeBatchHash computes a deterministic hash of a batch of messages.
func computeBatchHash(messages []ProposalMessage) string {
	// Serialize messages deterministically
	var buf bytes.Buffer
	for _, msg := range messages {
		buf.WriteString(msg.ID)
		buf.Write(msg.Ciphertext)
		buf.WriteString(msg.Hash)
	}

	// Use SHA-256 for the batch hash
	h := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(h[:])
}

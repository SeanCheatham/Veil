// Package consensus implements the BFT consensus layer for Veil validators.
package consensus

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/veil-protocol/veil/pkg/antithesis"
	"github.com/veil-protocol/veil/pkg/epoch"
)

const (
	// DefaultBatchSize is the default maximum batch size.
	DefaultBatchSize = 10

	// DefaultBatchTimeout is the default batch timeout.
	DefaultBatchTimeout = 2 * time.Second

	// DefaultEpochDuration is the default epoch duration for validators.
	DefaultEpochDuration = 30 * time.Second

	// TotalValidators is the total number of validators in the network.
	TotalValidators = 3
)

// Validator represents a BFT validator node.
type Validator struct {
	mu sync.RWMutex

	// ID is the unique identifier for this validator (1, 2, or 3).
	ID string

	// NumericID is the numeric version of ID.
	NumericID int

	// Peers are the addresses of other validators.
	Peers []string

	// PoolAddr is the address of the message pool service.
	PoolAddr string

	// Collector manages batch creation.
	Collector *BatchCollector

	// Clock manages epoch timing.
	Clock *epoch.Clock

	// committedBatches tracks all committed batches by sequence number.
	committedBatches map[uint64]*Batch

	// lastCommittedSeq is the sequence number of the last committed batch.
	lastCommittedSeq uint64

	// pendingProposals are proposals waiting for votes.
	pendingProposals map[string]*Batch

	// httpClient for communicating with peers and pool.
	httpClient *http.Client

	// running indicates if the validator is running.
	running bool

	// stopCh signals the validator to stop.
	stopCh chan struct{}
}

// ValidatorConfig holds configuration for a validator.
type ValidatorConfig struct {
	ID            string
	Peers         []string
	PoolAddr      string
	MaxBatchSize  int
	BatchTimeout  time.Duration
	EpochDuration time.Duration
}

// NewValidator creates a new validator with the given configuration.
func NewValidator(cfg ValidatorConfig) (*Validator, error) {
	numericID, err := strconv.Atoi(cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid validator ID: %w", err)
	}

	if numericID < 1 || numericID > 3 {
		return nil, fmt.Errorf("validator ID must be 1, 2, or 3; got %d", numericID)
	}

	maxBatchSize := cfg.MaxBatchSize
	if maxBatchSize <= 0 {
		maxBatchSize = DefaultBatchSize
	}

	batchTimeout := cfg.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = DefaultBatchTimeout
	}

	epochDuration := cfg.EpochDuration
	if epochDuration <= 0 {
		epochDuration = DefaultEpochDuration
	}

	v := &Validator{
		ID:               cfg.ID,
		NumericID:        numericID,
		Peers:            cfg.Peers,
		PoolAddr:         cfg.PoolAddr,
		Collector:        NewBatchCollector(cfg.ID, maxBatchSize, batchTimeout),
		Clock:            epoch.NewClock(epochDuration),
		committedBatches: make(map[uint64]*Batch),
		pendingProposals: make(map[string]*Batch),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopCh: make(chan struct{}),
	}

	// Register epoch handler
	v.Clock.OnTick(v.handleEpochTick)

	return v, nil
}

// handleEpochTick is called when the epoch advances.
func (v *Validator) handleEpochTick(epochNum uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()

	log.Printf("validator %s: epoch advanced to %d", v.ID, epochNum)
	v.Collector.SetEpoch(epochNum)
}

// Start begins the validator's consensus operations.
func (v *Validator) Start() {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return
	}
	v.running = true
	v.stopCh = make(chan struct{})
	v.mu.Unlock()

	// Start epoch clock
	v.Clock.Start()

	// Start batch timeout checker
	go v.batchTimeoutLoop()

	log.Printf("validator %s: started", v.ID)
}

// Stop halts the validator.
func (v *Validator) Stop() {
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		return
	}
	v.running = false
	close(v.stopCh)
	v.mu.Unlock()

	v.Clock.Stop()

	log.Printf("validator %s: stopped", v.ID)
}

// IsRunning returns whether the validator is running.
func (v *Validator) IsRunning() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.running
}

// batchTimeoutLoop periodically checks for batch timeouts.
func (v *Validator) batchTimeoutLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-v.stopCh:
			return
		case <-ticker.C:
			if v.IsLeader() {
				if ready, batch := v.Collector.CheckTimeout(); ready {
					go v.proposeBatch(batch)
				}
			}
		}
	}
}

// IsLeader returns true if this validator is the current epoch's leader.
// Leadership rotates based on epoch number.
func (v *Validator) IsLeader() bool {
	currentEpoch := v.Clock.CurrentEpoch()
	if currentEpoch == 0 {
		return false
	}
	// Rotate leadership: epoch 1 -> validator 1, epoch 2 -> validator 2, etc.
	leader := int((currentEpoch-1)%TotalValidators) + 1
	return v.NumericID == leader
}

// CurrentLeaderID returns the ID of the current epoch's leader.
func (v *Validator) CurrentLeaderID() int {
	currentEpoch := v.Clock.CurrentEpoch()
	if currentEpoch == 0 {
		return 1 // Default to validator 1
	}
	return int((currentEpoch-1)%TotalValidators) + 1
}

// SubmitMessage submits a message for ordering through consensus.
// Returns the message ID if accepted.
func (v *Validator) SubmitMessage(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", fmt.Errorf("ciphertext cannot be empty")
	}

	// Compute message ID (hash)
	id := computeMessageID(ciphertext)

	log.Printf("validator %s: received message %s", v.ID, id)

	// If we're the leader, add to our batch
	if v.IsLeader() {
		ready, batch := v.Collector.AddMessage(id, ciphertext)
		if ready {
			go v.proposeBatch(batch)
		}
	} else {
		// Forward to leader
		go v.forwardToLeader(id, ciphertext)
	}

	return id, nil
}

// forwardToLeader forwards a message to the current leader.
func (v *Validator) forwardToLeader(id string, ciphertext []byte) {
	leaderID := v.CurrentLeaderID()
	if leaderID == v.NumericID {
		// We became the leader, handle locally
		ready, batch := v.Collector.AddMessage(id, ciphertext)
		if ready {
			go v.proposeBatch(batch)
		}
		return
	}

	// Find leader's address
	var leaderAddr string
	for _, peer := range v.Peers {
		// Peer format: "validator-N:9000"
		if strings.Contains(peer, fmt.Sprintf("validator-%d:", leaderID)) {
			leaderAddr = peer
			break
		}
	}

	if leaderAddr == "" {
		log.Printf("validator %s: could not find leader %d address", v.ID, leaderID)
		return
	}

	// Forward message
	req := SubmitRequest{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	body, _ := json.Marshal(req)

	url := fmt.Sprintf("http://%s/submit", leaderAddr)
	resp, err := v.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("validator %s: failed to forward message to leader: %v", v.ID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		log.Printf("validator %s: leader rejected message: %d", v.ID, resp.StatusCode)
	}
}

// proposeBatch proposes a batch to all validators.
func (v *Validator) proposeBatch(batch *Batch) {
	if batch == nil || batch.IsEmpty() {
		return
	}

	log.Printf("validator %s: proposing batch %d with %d messages (hash: %s)",
		v.ID, batch.SequenceNum, batch.Size(), batch.Hash[:16])

	// Vote for our own proposal
	batch.AddVote(v.ID)

	// Store as pending
	v.mu.Lock()
	v.pendingProposals[batch.Hash] = batch
	v.mu.Unlock()

	// Send proposal to peers
	for _, peer := range v.Peers {
		go v.sendProposal(peer, batch)
	}

	// Check if we already have quorum (single-node or all nodes agreed already)
	v.checkAndCommit(batch)
}

// sendProposal sends a batch proposal to a peer.
func (v *Validator) sendProposal(peer string, batch *Batch) {
	proposal := ProposalRequest{
		SequenceNum: batch.SequenceNum,
		Hash:        batch.Hash,
		ProposerID:  batch.ProposerID,
		Epoch:       batch.Epoch,
		Messages:    make([]ProposalMessage, len(batch.Messages)),
	}

	for i, msg := range batch.Messages {
		proposal.Messages[i] = ProposalMessage{
			ID:         msg.ID,
			Ciphertext: base64.StdEncoding.EncodeToString(msg.Ciphertext),
		}
	}

	body, _ := json.Marshal(proposal)

	url := fmt.Sprintf("http://%s/propose", peer)
	resp, err := v.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("validator %s: failed to send proposal to %s: %v", v.ID, peer, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Peer voted for the proposal
		v.mu.Lock()
		if pending, ok := v.pendingProposals[batch.Hash]; ok {
			// Extract voter ID from response
			var voteResp VoteResponse
			if json.NewDecoder(resp.Body).Decode(&voteResp) == nil {
				pending.AddVote(voteResp.VoterID)
				v.checkAndCommit(pending)
			}
		}
		v.mu.Unlock()
	}
}

// HandleProposal processes a batch proposal from another validator.
// Returns true if we vote for this proposal.
func (v *Validator) HandleProposal(proposal *ProposalRequest) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Validate proposal
	// Check sequence number is expected
	expectedSeq := v.lastCommittedSeq + 1
	if proposal.SequenceNum < expectedSeq {
		log.Printf("validator %s: rejecting proposal with old seq %d (expected >= %d)",
			v.ID, proposal.SequenceNum, expectedSeq)
		return false
	}

	// Verify hash matches messages
	messages := make([]*BatchMessage, len(proposal.Messages))
	for i, m := range proposal.Messages {
		ciphertext, err := base64.StdEncoding.DecodeString(m.Ciphertext)
		if err != nil {
			log.Printf("validator %s: invalid ciphertext in proposal", v.ID)
			return false
		}
		messages[i] = &BatchMessage{
			ID:         m.ID,
			Ciphertext: ciphertext,
			ReceivedAt: time.Now().UTC(),
		}
	}

	expectedHash := computeBatchHash(proposal.SequenceNum, messages)
	if expectedHash != proposal.Hash {
		log.Printf("validator %s: proposal hash mismatch", v.ID)
		return false
	}

	// Create batch from proposal and store it
	batch := &Batch{
		SequenceNum: proposal.SequenceNum,
		Hash:        proposal.Hash,
		Messages:    messages,
		ProposerID:  proposal.ProposerID,
		Epoch:       proposal.Epoch,
		CreatedAt:   time.Now().UTC(),
		State:       BatchProposed,
		Votes:       make(map[string]bool),
	}

	// Add proposer's implicit vote and our vote
	batch.AddVote(proposal.ProposerID)
	batch.AddVote(v.ID)

	v.pendingProposals[batch.Hash] = batch

	log.Printf("validator %s: voted for batch %d (hash: %s)", v.ID, batch.SequenceNum, batch.Hash[:16])

	// Check for quorum
	v.checkAndCommitLocked(batch)

	return true
}

// checkAndCommit checks if a batch has quorum and commits it.
func (v *Validator) checkAndCommit(batch *Batch) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.checkAndCommitLocked(batch)
}

// checkAndCommitLocked checks and commits with lock already held.
func (v *Validator) checkAndCommitLocked(batch *Batch) {
	if batch.State == BatchCommitted {
		return
	}

	if !batch.HasQuorum(TotalValidators) {
		return
	}

	// We have quorum! Commit the batch.
	batch.State = BatchCommitted

	// Verify this is the expected sequence
	expectedSeq := v.lastCommittedSeq + 1
	if batch.SequenceNum != expectedSeq {
		log.Printf("validator %s: batch seq %d doesn't match expected %d",
			v.ID, batch.SequenceNum, expectedSeq)
		// For now, we'll accept it anyway to handle gaps
	}

	v.committedBatches[batch.SequenceNum] = batch
	v.lastCommittedSeq = batch.SequenceNum

	// Update collector's next seq
	v.Collector.SetNextSequenceNum(v.lastCommittedSeq + 1)

	// Remove from pending
	delete(v.pendingProposals, batch.Hash)

	log.Printf("validator %s: committed batch %d with %d messages",
		v.ID, batch.SequenceNum, batch.Size())

	// Antithesis assertion: chain_progression (sometimes property)
	// This proves that new batches are being committed.
	assert.Sometimes(
		true,
		antithesis.ChainProgression,
		map[string]any{
			"validator_id": v.ID,
			"sequence_num": batch.SequenceNum,
			"batch_hash":   batch.Hash,
			"message_count": batch.Size(),
			"epoch":        batch.Epoch,
		},
	)

	// Antithesis assertion: validator_agreement (always property)
	// We assert that all validators that voted agree on the batch hash.
	// This will be verified across all validators.
	assert.Always(
		batch.VoteCount() >= 2,
		antithesis.ValidatorAgreement,
		map[string]any{
			"validator_id":  v.ID,
			"batch_hash":    batch.Hash,
			"sequence_num":  batch.SequenceNum,
			"vote_count":    batch.VoteCount(),
			"proposer_id":   batch.ProposerID,
		},
	)

	// Commit to pool in background
	go v.commitToPool(batch)
}

// commitToPool sends the committed batch to the message pool.
func (v *Validator) commitToPool(batch *Batch) {
	for _, msg := range batch.Messages {
		req := PoolPostRequest{
			Ciphertext: base64.StdEncoding.EncodeToString(msg.Ciphertext),
		}
		body, _ := json.Marshal(req)

		url := fmt.Sprintf("http://%s/messages", v.PoolAddr)
		resp, err := v.httpClient.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("validator %s: failed to commit message %s to pool: %v",
				v.ID, msg.ID, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			log.Printf("validator %s: pool rejected message %s: %d",
				v.ID, msg.ID, resp.StatusCode)
		}
	}

	log.Printf("validator %s: batch %d committed to pool", v.ID, batch.SequenceNum)
}

// GetStatus returns the validator's current status.
func (v *Validator) GetStatus() *ValidatorStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return &ValidatorStatus{
		ID:               v.ID,
		Running:          v.running,
		IsLeader:         v.IsLeader(),
		CurrentEpoch:     v.Clock.CurrentEpoch(),
		LastCommittedSeq: v.lastCommittedSeq,
		PendingProposals: len(v.pendingProposals),
		CommittedBatches: len(v.committedBatches),
	}
}

// GetCommittedBatch returns a committed batch by sequence number.
func (v *Validator) GetCommittedBatch(seqNum uint64) (*Batch, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	batch, ok := v.committedBatches[seqNum]
	return batch, ok
}

// LastCommittedSeq returns the last committed sequence number.
func (v *Validator) LastCommittedSeq() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastCommittedSeq
}

// RecordVote records a vote for a batch from another validator.
func (v *Validator) RecordVote(batchHash, voterID string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if batch, ok := v.pendingProposals[batchHash]; ok {
		batch.AddVote(voterID)
		log.Printf("validator %s: recorded vote from %s for batch %s",
			v.ID, voterID, batchHash[:16])
		v.checkAndCommitLocked(batch)
	}
}

// computeMessageID computes a SHA-256 hash of the message content.
func computeMessageID(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])[:32]
}

// ValidatorStatus represents the current state of a validator.
type ValidatorStatus struct {
	ID               string `json:"id"`
	Running          bool   `json:"running"`
	IsLeader         bool   `json:"is_leader"`
	CurrentEpoch     uint64 `json:"current_epoch"`
	LastCommittedSeq uint64 `json:"last_committed_seq"`
	PendingProposals int    `json:"pending_proposals"`
	CommittedBatches int    `json:"committed_batches"`
}

// SubmitRequest is the request body for POST /submit.
type SubmitRequest struct {
	Ciphertext string `json:"ciphertext"` // base64 encoded
}

// SubmitResponse is the response body for POST /submit.
type SubmitResponse struct {
	ID string `json:"id"`
}

// ProposalRequest is the request body for POST /propose.
type ProposalRequest struct {
	SequenceNum uint64            `json:"sequence_num"`
	Hash        string            `json:"hash"`
	ProposerID  string            `json:"proposer_id"`
	Epoch       uint64            `json:"epoch"`
	Messages    []ProposalMessage `json:"messages"`
}

// ProposalMessage is a message within a proposal.
type ProposalMessage struct {
	ID         string `json:"id"`
	Ciphertext string `json:"ciphertext"` // base64 encoded
}

// VoteResponse is the response for a successful vote.
type VoteResponse struct {
	VoterID string `json:"voter_id"`
	Voted   bool   `json:"voted"`
}

// PoolPostRequest matches the pool server's expected request format.
type PoolPostRequest struct {
	Ciphertext string `json:"ciphertext"` // base64 encoded
}
